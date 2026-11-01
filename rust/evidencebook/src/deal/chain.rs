//! The chain stage (PROFILE.md §6): one deal's records read in `seq` order,
//! each against the records before it. A port of the reference checker's
//! `check_chain` (`x-deal-v0/check_profile.py`) and of Go's `CheckChain`:
//! the same refusals in the same order with the same text, which the chain
//! vectors (`dealprofile/testdata/chain-vectors.json`) pin.

use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};

use serde_json::{json, Map, Value};

use super::check::{decode, is_typed, Error, StageError};
use super::names::*;
use crate::canonical::json_digest;

/// The stage the chain rules refuse at.
pub const STAGE_CHAIN: &str = "chain";

static NULL: Value = Value::Null;

type R<T> = Result<T, String>;

/// Runs the §6 rules over one deal's records in `seq` order. Each record is
/// its JSON text, and has already passed [`check`](super::check). A deal's
/// chain is the baseline and the records after it; a sale's own log opens
/// with its root. The refusal is [`Error::Stage`] at [`STAGE_CHAIN`], whose
/// `detail` begins `seq position N:`.
pub fn check_chain(records: &[&[u8]]) -> Result<(), Error> {
    let docs: Vec<Value> = records
        .iter()
        .map(|r| decode(r))
        .collect::<Result<_, _>>()?;
    check_values(&docs).map_err(refusal)
}

/// [`check_chain`] over records already decoded (numbers kept as written).
pub fn check_chain_values(records: &[Value]) -> Result<(), Error> {
    check_values(records).map_err(refusal)
}

fn refusal(detail: String) -> Error {
    Error::Stage(StageError {
        stage: STAGE_CHAIN,
        detail,
        causes: Vec::new(),
    })
}

fn check_values(docs: &[Value]) -> R<()> {
    if docs.is_empty() {
        return Err("a record the chain rules cannot read: no records".into());
    }
    if let Some(i) = docs.iter().position(|d| !d.is_object()) {
        return Err(msg(i, "a record is a JSON object"));
    }
    let digests: Vec<String> = docs.iter().map(digest_of).collect::<R<_>>()?;
    if !is_typed(docs[0].as_object().unwrap_or(&Map::new()))
        && s(g(g(&docs[0], PROFILE), "record_type")) == TYPE_SALE
    {
        return check_sale_chain(docs, &digests);
    }
    check_deal_chain(docs, digests)
}

fn msg(i: usize, m: impl AsRef<str>) -> String {
    format!("seq position {}: {}", i + 1, m.as_ref())
}

macro_rules! bail_at {
    ($i:expr, $($a:tt)*) => { return Err(msg($i, format!($($a)*))) };
}

// Accessors read generic JSON as the checker's dicts read it: an absent member
// and a null are alike, and a member of another type reads as absent.
fn g<'a>(v: &'a Value, k: &str) -> &'a Value {
    v.get(k).unwrap_or(&NULL)
}

fn gi(v: &Value, i: usize) -> &Value {
    v.get(i).unwrap_or(&NULL)
}

fn s(v: &Value) -> &str {
    v.as_str().unwrap_or("")
}

fn has(v: &Value, k: &str) -> bool {
    v.get(k).is_some()
}

fn list(v: &Value) -> &[Value] {
    v.as_array().map(Vec::as_slice).unwrap_or(&[])
}

fn truthy(v: &Value) -> bool {
    match v {
        Value::Null => false,
        Value::Bool(b) => *b,
        Value::String(x) => !x.is_empty(),
        Value::Array(a) => !a.is_empty(),
        Value::Object(o) => !o.is_empty(),
        Value::Number(n) => n.as_f64().is_some_and(|f| f != 0.0),
    }
}

fn num(v: &Value) -> R<i64> {
    v.as_i64()
        .ok_or_else(|| format!("a record the chain rules cannot read: {v} is not an integer"))
}

fn contains(l: &Value, v: &Value) -> bool {
    list(l).iter().any(|x| x == v)
}

fn any_not_in(l: &Value, within: &Value) -> bool {
    list(l).iter().any(|a| !contains(within, a))
}

fn string_is(v: &Value, want: &str) -> bool {
    v.as_str() == Some(want)
}

fn reference(d: &str) -> Value {
    json!({"type": "deal-record", "digest_alg": "SHA-256", "digest": d})
}

fn digest_of(v: &Value) -> R<String> {
    json_digest(v).map_err(|e| format!("a record the chain rules cannot read: {e}"))
}

fn py_repr(x: &str) -> String {
    let q = if x.contains('\'') && !x.contains('"') {
        '"'
    } else {
        '\''
    };
    format!("{q}{}{q}", x.replace(q, &format!("\\{q}")))
}

fn typed_type(t: &str) -> &'static str {
    match t {
        TYPE_TASK_AUTHORITY => "task_authority",
        TYPE_PROPOSED_ACTION => TYPE_CHECK,
        TYPE_ACTION_EVALUATION => TYPE_VERDICT,
        TYPE_ACTION_APPROVAL => TYPE_APPROVAL,
        TYPE_ACTION_RECORD => TYPE_ACTION,
        TYPE_ACTION_OUTCOME => TYPE_OUTCOME,
        _ => "report",
    }
}

fn typed(rec: &Value) -> bool {
    rec.as_object().is_some_and(is_typed)
}

/// A typed record as the §6 rules read it (the reference checker's
/// `typed_view`): its header as a block, and its body under the names those
/// rules use. Digests are always computed over the record itself.
fn view_of(rec: &Value) -> Value {
    let t = s(g(rec, "type"));
    let mut body = g(rec, "body").as_object().cloned().unwrap_or_default();
    let mut blk = Map::new();
    blk.insert("profile".into(), json!(t));
    blk.insert(
        "canonicalization".into(),
        g(rec, "canonicalization").clone(),
    );
    blk.insert("deal_id".into(), g(rec, "chain_id").clone());
    blk.insert("record_type".into(), json!(typed_type(t)));
    blk.insert("seq".into(), g(rec, "seq").clone());
    blk.insert("at".into(), g(rec, "at").clone());
    blk.insert("typed".into(), json!(t));
    if has(rec, "prev") {
        blk.insert("prev".into(), reference(s(g(g(rec, "prev"), "digest"))));
    }
    if has(rec, "chain_root") {
        blk.insert(
            "baseline_ref".into(),
            reference(s(g(g(rec, "chain_root"), "digest"))),
        );
    }
    if let Some(refs) = rec.get("refs") {
        let out: Vec<Value> = list(refs)
            .iter()
            .map(|r| {
                let mut x = reference(s(g(r, "digest")));
                x["rel"] = g(r, "rel").clone();
                x
            })
            .collect();
        blk.insert("refs".into(), Value::Array(out));
    }
    if let Some(cp) = body.remove("counterparty") {
        blk.insert("counterparty".into(), cp);
    }
    match t {
        TYPE_ACTION_EVALUATION => {
            let result = match s(g(&Value::Object(body.clone()), "disposition")) {
                "DO" => "pass",
                "ASK" => "pause",
                _ => "deny",
            };
            body.insert("result".into(), json!(result));
            body.insert(
                "differences".into(),
                body.get("findings").cloned().unwrap_or(Value::Null),
            );
            if let Some(v) = body.get("rendering_commitment").cloned() {
                body.insert("card_commitment".into(), v);
            }
        }
        TYPE_ACTION_APPROVAL => {
            let a = body.get("authority").and_then(Value::as_str).unwrap_or("");
            match a {
                "user_approval" => {
                    body.insert("approver".into(), json!("user"));
                }
                "card_answer" => {
                    body.insert("approver".into(), json!("agent_card"));
                }
                // platform_approval, policy_change, one_shot_override
                _ => {
                    blk.insert("record_type".into(), json!(a));
                }
            }
        }
        TYPE_ACTION_OUTCOME => {
            body.insert(
                "differences".into(),
                body.get("findings").cloned().unwrap_or(Value::Null),
            );
            if let Some(v) = body.get("attempted").cloned() {
                body.insert("unchecked".into(), v);
            }
        }
        _ => {}
    }
    let mut out = Map::new();
    out.insert(PROFILE.into(), Value::Object(blk));
    out.insert("body".into(), Value::Object(body));
    Value::Object(out)
}

fn readable(rec: &Value) -> Value {
    if typed(rec) {
        view_of(rec)
    } else {
        rec.clone()
    }
}

fn block(rec: &Value) -> &Value {
    g(rec, PROFILE)
}

fn disclosure_action(class: &str) -> &'static str {
    match class {
        "name" | "phone" | "email" | "home_address" | "address" | "pickup_location"
        | "pickup_area" | "other_contact" => "share_contact",
        "credential" | "verification_code" | "payment_card" | "id_document" => "share_credentials",
        // The checker's table has no other class: its lookup would raise.
        _ => "",
    }
}

const OFFER_TERMS: [&str; 4] = ["amount_minor", "currency", "terms", "recourse"];
const HANDOVERS: [&str; 2] = ["in_person", "shipped"];
const ADDRESS_CLASSES: [&str; 3] = ["home_address", "address", "pickup_location"];
const REPRESENTATION_GATED: [&str; 6] = [
    "offer",
    "commit",
    "sign",
    "pay",
    "share_contact",
    "share_credentials",
];
const IDENTIFIER_KINDS: [&str; 7] = [
    "payee",
    "name",
    "domain",
    "phone",
    "email",
    "relay_address",
    "profile_id",
];

fn allowed_rels(t: &str) -> &'static [&'static str] {
    match t {
        "evidence" => &["about", "confirms"],
        "detail_change" => &["source"],
        "verdict" => &["checks"],
        "approval" => &["approves"],
        "action" => &["authorized_by", "reverses", "source"],
        "outcome" => &["observes"],
        "close" => &["outcome"],
        "disclosure" | "claim" => &["authorized_by"],
        "task_authority" => &["source", "approves", "registration"],
        "check" => &["supersedes", "source"],
        "counterparty_profile" => &["about"],
        _ => &[],
    }
}

/// `terms.handover` (§6 rule 14) on one record: a member of the set, on a
/// deal where the user sells, and only in the terms of an offer or commit
/// check, a baseline or a sale. The refusal, or `None`.
fn check_handover(rec: &Value, party_role: &str) -> Option<&'static str> {
    let b = block(rec);
    let body = g(rec, "body");
    let t = s(g(b, "record_type"));
    let intent = g(body, "intent");
    let stated_here = t == TYPE_BASELINE
        || t == TYPE_SALE
        || (t == TYPE_CHECK
            && (string_is(g(body, "action"), "offer") || string_is(g(body, "action"), "commit")));
    let places: [(bool, &Value); 2] = match t {
        TYPE_INTENT => [(false, g(body, "asked")), (false, g(body, "terms"))],
        TYPE_DETAIL_CHANGE | TYPE_OUTCOME => {
            [(false, g(body, "terms")), (false, g(body, "delivered"))]
        }
        _ => [(stated_here, g(body, "terms")), (false, g(intent, "asked"))],
    };
    for (allowed_here, terms) in places {
        if !has(terms, "handover") {
            continue;
        }
        if !HANDOVERS.iter().any(|h| string_is(g(terms, "handover"), h)) {
            return Some("terms.handover is in_person or shipped");
        }
        if party_role != "seller" {
            return Some(
                "terms.handover is a seller's term: a deal where the user buys states none",
            );
        }
        if !allowed_here {
            return Some(
                "terms.handover goes on an offer or commit check or the opening, and nowhere else",
            );
        }
    }
    None
}

/// Whether `x` is a real `YYYY-MM-DDTHH:MM:SSZ` UTC time, as Python's
/// `strptime` reads the profile's pattern.
fn valid_utc(x: &str) -> bool {
    let b = x.as_bytes();
    if b.len() != 20
        || b[4] != b'-'
        || b[7] != b'-'
        || b[10] != b'T'
        || b[13] != b':'
        || b[16] != b':'
        || b[19] != b'Z'
    {
        return false;
    }
    let d = |a: usize, z: usize| -> Option<u32> {
        let t = &x[a..z];
        t.bytes()
            .all(|c| c.is_ascii_digit())
            .then(|| t.parse().ok())?
    };
    let (Some(y), Some(mo), Some(da), Some(h), Some(mi), Some(se)) =
        (d(0, 4), d(5, 7), d(8, 10), d(11, 13), d(14, 16), d(17, 19))
    else {
        return false;
    };
    let leap = (y % 4 == 0 && y % 100 != 0) || y % 400 == 0;
    let days = match mo {
        1 | 3 | 5 | 7 | 8 | 10 | 12 => 31,
        4 | 6 | 9 | 11 => 30,
        2 if leap => 29,
        2 => 28,
        _ => return false,
    };
    y >= 1 && (1..=days).contains(&da) && h < 24 && mi < 60 && se < 60
}

fn check_sale_chain(docs: &[Value], digests: &[String]) -> R<()> {
    if docs.len() < 2 {
        bail_at!(
            0,
            "a sale's log holds its root and its one task authority, then its thread registrations"
        );
    }
    let (root, ta) = (&docs[0], &docs[1]);
    let blk = block(root);
    if num(g(blk, "seq"))? != 1 {
        bail_at!(0, "a sale's root is the first record of its log");
    }
    let root_intent = g(g(root, "body"), "intent");
    if !string_is(g(root_intent, "party_role"), "seller") {
        bail_at!(0, "a sale is made by the seller (party_role seller)");
    }
    if let Some(why) = check_handover(root, "seller") {
        bail_at!(0, "{why}");
    }
    if !typed(ta) || !string_is(g(ta, "type"), TYPE_TASK_AUTHORITY) {
        bail_at!(
            1,
            "a sale's root is followed by its task authority (task-authority/v0)"
        );
    }
    let d0 = digests[0].clone();
    if num(g(ta, "seq")).unwrap_or(0) != 2
        || !string_is(g(g(ta, "prev"), "digest"), &d0)
        || !string_is(g(g(ta, "chain_root"), "digest"), &d0)
        || g(ta, "chain_id") != g(blk, "deal_id")
    {
        bail_at!(
            1,
            "the sale's task authority follows its root in the same log"
        );
    }
    let refs = list(g(ta, "refs"));
    if refs.len() != 1
        || !string_is(g(&refs[0], "rel"), "source")
        || !string_is(g(&refs[0], "digest"), &d0)
    {
        bail_at!(
            1,
            "the sale's task authority names the sale's root as its source, and nothing else"
        );
    }
    let ta_body = g(ta, "body");
    if has(ta_body, "sale_authority_commitment") {
        bail_at!(1, "a sale's own task authority names no other sale");
    }
    if !g(root_intent, "bounds_commitment").is_null() && !has(ta_body, "bounds_commitment") {
        bail_at!(
            1,
            "the sale's task authority carries the floor its root states"
        );
    }
    let sale_ta = digests[1].clone();
    let (mut prev, mut at) = (sale_ta.clone(), s(g(ta, "at")).to_string());
    let (mut registrations, mut opened) = (HashSet::new(), HashSet::new());
    let mut closed = false;
    // A listing post (rule 15): every post check asks, and only the user's own
    // approval, first and unused, of the latest post check authorizes the act.
    let mut kind_at: HashMap<usize, String> = HashMap::new();
    let mut first_answer: HashMap<usize, usize> = HashMap::new();
    let (mut used, mut stale) = (HashSet::new(), HashSet::new());
    let (mut latest_verdict, mut latest_check): (Option<usize>, Option<usize>) = (None, None);
    for i in 2..docs.len() {
        let r = &docs[i];
        let view = readable(r);
        let (b, body) = (block(&view), g(&view, "body"));
        let t = if typed(r) {
            format!("post_{}", s(g(b, "record_type")))
        } else {
            s(g(b, "record_type")).to_string()
        };
        const KINDS: [&str; 8] = [
            "thread",
            "thread_opened",
            "sale_cut",
            "sale_close",
            "post_check",
            "post_verdict",
            "post_approval",
            "post_action",
        ];
        if !KINDS.contains(&t.as_str()) {
            bail_at!(
                i,
                "after its task authority, a sale's log holds only thread registrations, their openings, cuts and its close, and a listing post's check, evaluation, approval and action"
            );
        }
        if closed && t != "sale_cut" {
            bail_at!(i, "after a sale's close, its log holds only cuts");
        }
        closed = closed || t == "sale_close";
        if num(g(b, "seq")).unwrap_or(0) != i as i64 + 1
            || g(b, "deal_id") != g(blk, "deal_id")
            || g(b, "prev") != &reference(&prev)
            || g(b, "baseline_ref") != &reference(&d0)
            || s(g(b, "at")) < at.as_str()
        {
            bail_at!(
                i,
                "a sale's record follows its previous record in the same log"
            );
        }
        kind_at.insert(i, t.clone());

        let ref_to = |rel: &str, kind: &str| -> R<usize> {
            let refs = list(g(b, "refs"));
            let js: Vec<&Value> = refs
                .iter()
                .filter(|x| string_is(g(x, "rel"), rel))
                .collect();
            let others = refs.iter().any(|x| !string_is(g(x, "rel"), rel));
            let j = if js.len() == 1 {
                digests[..i]
                    .iter()
                    .position(|d| string_is(g(js[0], "digest"), d))
            } else {
                None
            };
            match j {
                Some(j) if !others && kind_at.get(&j).map(String::as_str) == Some(kind) => Ok(j),
                _ => Err(msg(
                    i,
                    format!(
                        "a post's {} names exactly one earlier {} of this sale ({rel})",
                        &t[5..],
                        &kind[5..]
                    ),
                )),
            }
        };

        match t.as_str() {
            "post_check" => {
                if !string_is(g(body, "action"), "post") {
                    bail_at!(
                        i,
                        "a sale's own log checks only a listing post (action post)"
                    );
                }
                if !string_is(g(g(body, "task_authority_ref"), "digest"), &sale_ta) {
                    bail_at!(i, "a post check names the sale's one task authority");
                }
                // A newer proposal supersedes every earlier post check: no answer
                // sealed after it covers an earlier one (capsulectl's approvalChanged).
                latest_verdict = None;
                latest_check = Some(i);
            }
            "post_verdict" => {
                let j = ref_to("checks", "post_check")?;
                if Some(j) != latest_check {
                    bail_at!(i, "a post's evaluation evaluates the post proposal sealed right before it, never an earlier one");
                }
                if !string_is(g(body, "result"), "pause") {
                    bail_at!(i, "every post check asks: its evaluation is ASK");
                }
                if !string_is(g(body, "proposed_action_digest"), &digests[j])
                    || !string_is(g(g(body, "task_authority_ref"), "digest"), &sale_ta)
                {
                    bail_at!(
                        i,
                        "a post's evaluation names its check and the sale's one task authority"
                    );
                }
                if !string_is(g(body, "rules_status"), "not_applicable")
                    || has(body, "rules_checks")
                {
                    bail_at!(i, "no rules checker runs on a post: its evaluation says rules_status not_applicable");
                }
                latest_verdict = Some(i);
            }
            "post_approval" => {
                let j = ref_to("approves", "post_verdict")?;
                // What the user was shown is what the post check rendered, as on a
                // thread: the same rendering commitment, required on their own answer.
                let shown = g(g(r, "body"), "rendering_commitment");
                if (string_is(g(body, "approver"), "user") && shown.is_null())
                    || (!shown.is_null() && shown != g(g(&docs[j], "body"), "rendering_commitment"))
                {
                    bail_at!(i, "what was shown (rendering_commitment) is not the card the post check rendered");
                }
                if Some(j) != latest_verdict {
                    stale.insert(i);
                }
                first_answer.entry(j).or_insert(i);
            }
            "post_action" => {
                if !string_is(g(body, "action"), "post") {
                    bail_at!(i, "a sale's own log takes only the post act");
                }
                let ja = ref_to("authorized_by", "post_approval")?;
                let ab = readable(&docs[ja]);
                let ab = g(&ab, "body");
                let target = s(g(gi(g(&docs[ja], "refs"), 0), "digest"));
                let jv = digests.iter().position(|d| d == target).ok_or_else(|| {
                    "a record the chain rules cannot read: a ref names no record".to_string()
                })?;
                if !string_is(g(ab, "approver"), "user") || !truthy(g(ab, "proceed")) {
                    bail_at!(i, "only the user's own approval, in their words, authorizes a post: an answer without the user's words does not");
                }
                if stale.contains(&ja) || Some(jv) != latest_verdict {
                    bail_at!(i, "a newer post check superseded the one this post rests on: show the user the newer card and post only after their yes");
                }
                if first_answer.get(&jv) != Some(&ja) || used.contains(&ja) {
                    bail_at!(
                        i,
                        "a post rests on the first answer to its check, and an approval posts once"
                    );
                }
                used.insert(ja);
                let until = s(g(g(&docs[jv], "body"), "valid_until"));
                if s(g(b, "at")) > until {
                    bail_at!(i, "the post check was valid until {until}: a post after that needs a new check");
                }
                if !string_is(g(g(body, "evaluation_ref"), "digest"), &digests[jv]) {
                    bail_at!(i, "evaluation_ref is the post's evaluation");
                }
                let basis = list(g(body, "authority_basis"));
                let basis_ok = basis.len() == 2
                    && string_is(g(&basis[0], "type"), "task_authority")
                    && string_is(g(g(&basis[0], "ref"), "digest"), &sale_ta)
                    && string_is(g(&basis[1], "type"), "user_approval")
                    && string_is(g(g(&basis[1], "ref"), "digest"), &digests[ja]);
                if !basis_ok {
                    bail_at!(i, "a post's authority_basis is the sale's task authority, then the user's approval");
                }
            }
            "thread_opened" => {
                let reg = s(g(gi(g(b, "refs"), 0), "digest")).to_string();
                if !registrations.contains(&reg) || opened.contains(&reg) {
                    bail_at!(
                        i,
                        "a thread's opening names an earlier registration of this sale, once"
                    );
                }
                opened.insert(reg);
            }
            "sale_cut" => {
                let mut seen = HashSet::new();
                let (mut dup, mut unopened) = (false, false);
                for h in list(g(g(r, "body"), "thread_heads")) {
                    let reg = s(g(g(h, "registration"), "digest")).to_string();
                    dup = dup || !seen.insert(reg.clone());
                    unopened = unopened || !opened.contains(&reg);
                }
                if dup || unopened {
                    bail_at!(
                        i,
                        "a sale cut names each opened thread at most once, and only opened threads"
                    );
                }
            }
            _ => {}
        }
        prev = digests[i].clone();
        at = s(g(b, "at")).to_string();
        if t == "thread" {
            registrations.insert(prev.clone());
        }
    }
    Ok(())
}

struct Chain {
    docs: Vec<Value>, // the records as the rules read them
    digests: Vec<String>,
    by_dig: HashMap<String, usize>,
    i: usize,
    typed_chain: bool,
    task_idx: usize,
    task_at_verdict: HashMap<usize, usize>,
    platform_for: HashMap<usize, Vec<usize>>,
    party_role: String,
    allowed: Value,
    max_total: Value,
    delegated: Vec<Value>,
    bounds: Value,
    bounds_known: bool,
    ordered: Value,
    rep_rules: bool,
    stated: HashSet<String>,
    confirmed: HashSet<usize>,
    used_approvals: HashSet<usize>,
    verdict_for_check: HashSet<usize>,
    profile_for_check: HashSet<usize>,
    reversed: HashSet<usize>,
    first_answer: HashMap<usize, usize>,
    last_outcome: Option<usize>,
    unchecked: i64,
    latest_offer: Option<usize>,
    accepted: Option<usize>,
    closed_final: Option<usize>,
    t: String,
    by_rel: BTreeMap<String, Vec<usize>>,
}

macro_rules! bail {
    ($c:expr, $($a:tt)*) => { return Err(msg($c.i, format!($($a)*))) };
}

impl Chain {
    fn rt(&self, k: usize) -> &str {
        s(g(block(&self.docs[k]), "record_type"))
    }

    fn body_of(&self, k: usize) -> &Value {
        g(&self.docs[k], "body")
    }

    fn first_ref_index(&self, k: usize) -> usize {
        let d = s(g(gi(g(block(&self.docs[k]), "refs"), 0), "digest"));
        self.by_dig.get(d).copied().unwrap_or(usize::MAX)
    }

    /// The checker's `one`: the index of the record the rel names, at most (or
    /// exactly) one, of one of the types.
    fn one(&self, rel: &str, types: &[&str], required: bool) -> R<Option<usize>> {
        let js = self.by_rel.get(rel).map(Vec::as_slice).unwrap_or(&[]);
        if js.len() > 1 || (required && js.is_empty()) {
            if required {
                bail!(
                    self,
                    "a {} record carries exactly one {} ref",
                    self.t,
                    py_repr(rel)
                );
            }
            bail!(
                self,
                "a {} record carries at most one {} ref",
                self.t,
                py_repr(rel)
            );
        }
        if let Some(&j) = js.first() {
            if !types.contains(&self.rt(j)) {
                bail!(
                    self,
                    "{} must point at a {} record",
                    py_repr(rel),
                    types.join(" or ")
                );
            }
        }
        Ok(js.first().copied())
    }

    fn offer_check_of(&self, mut k: usize) -> usize {
        if self.rt(k) == TYPE_APPROVAL {
            k = self.first_ref_index(k);
        }
        self.first_ref_index(k)
    }

    /// A record after which no earlier check covers a step (§6 rules 5 and 13).
    fn changes_details(&self, k: usize) -> bool {
        let (kb, kbody) = (block(&self.docs[k]), self.body_of(k));
        let tk = s(g(kb, "record_type"));
        let identity = (tk == TYPE_MESSAGE || tk == TYPE_EVIDENCE)
            && (has(kb, "counterparty") || has(kbody, "counterparty_facts"));
        let leg = tk == TYPE_INTENT && has(kb, "counterparty");
        let pick = (tk == TYPE_INTENT && has(kbody, "move") && has(g(kbody, "asked"), "item"))
            || (tk == TYPE_AGENT_MOVE && string_is(g(kbody, "move"), "chose"));
        tk == TYPE_DETAIL_CHANGE || identity || leg || pick
    }

    fn seller_needs_acceptance(&self, what: &str) -> R<()> {
        let Some(_) = self.latest_offer else {
            bail!(self, "no offer is on record: a seller's {what} rests on an offer the other party accepted");
        };
        let Some(accepted) = self.accepted else {
            bail!(
                self,
                "the latest offer has no recorded acceptance: the {what} needs one"
            );
        };
        for k in accepted + 1..self.i {
            if self.changes_details(k) {
                bail!(self, "details changed after the other party accepted; the {what} needs the offer made and accepted again");
            }
        }
        if self.by_rel.get("source").map(Vec::as_slice) != Some(&[accepted][..]) {
            bail!(
                self,
                "a seller's {what} cites the acceptance it rests on (source)"
            );
        }
        Ok(())
    }

    /// §6 rule 5: the approval proceeds, is unused, is the verdict's first
    /// answer, and its check is of this action with no change of details
    /// since. Returns the check.
    fn authorized_check(&mut self, ja: usize, action: &str, what: &str) -> R<usize> {
        let appr = self.body_of(ja);
        if string_is(g(appr, "choice"), "confirm_limits") {
            bail!(self, "a limits confirmation authorizes no action or disclosure; the step needs its own check");
        }
        if !truthy(g(appr, "proceed")) {
            bail!(self, "the referenced approval did not approve proceeding");
        }
        if !self.used_approvals.insert(ja) {
            bail!(
                self,
                "an approval authorizes at most one action or disclosure"
            );
        }
        let jv = self.first_ref_index(ja);
        if self.first_answer.get(&jv) != Some(&ja) {
            let n = if what == "action" { "n" } else { "" };
            bail!(self, "a{n} {what} is held to the verdict's first answer; a later answer needs a new check");
        }
        let jc = self.first_ref_index(jv);
        if !string_is(g(self.body_of(jc), "action"), action) {
            bail!(self, "the {what} differs from the one checked");
        }
        for k in jc + 1..self.i {
            if self.changes_details(k) {
                bail!(
                    self,
                    "details changed after the check; the {what} needs a new check"
                );
            }
        }
        Ok(jc)
    }

    /// Typed chains (§9): a DO evaluation authorizes the step on the task
    /// authority alone; an ASK is answered only by the user's own approval.
    /// Returns the evaluation, the approval and the check.
    fn authorize_typed(
        &mut self,
        jt: usize,
        action: &str,
        what: &str,
    ) -> R<(usize, Option<usize>, usize)> {
        if self.rt(jt) == TYPE_VERDICT {
            if !string_is(g(self.body_of(jt), "result"), "pass") {
                bail!(self, "only a DO evaluation authorizes on its own; an ASK needs the user's own approval");
            }
            if !self.used_approvals.insert(jt) {
                bail!(
                    self,
                    "an evaluation authorizes at most one action or disclosure"
                );
            }
            let jc = self.first_ref_index(jt);
            if !string_is(g(self.body_of(jc), "action"), action) {
                bail!(self, "the {what} differs from the one checked");
            }
            if !self.allowed.is_null()
                && !contains(&self.allowed, &json!(action))
                && action != "represent"
            {
                bail!(self, "the task authority does not allow this action");
            }
            for k in jc + 1..self.i {
                if self.changes_details(k) {
                    bail!(
                        self,
                        "details changed after the check; the {what} needs a new check"
                    );
                }
            }
            return Ok((jt, None, jc));
        }
        if !string_is(g(self.body_of(jt), "approver"), "user") {
            bail!(self, "the deal check asked (it paused); an ask needs the user's own approval, and a card answer does not satisfy it");
        }
        let chk = self.authorized_check(jt, action, what)?;
        Ok((self.first_ref_index(jt), Some(jt), chk))
    }

    /// The executed phase of the check contract: the evaluation relied on
    /// still valid, and every authority layer in order, each a ref to its
    /// record.
    fn verify_basis(&self, jv: usize, ja: Option<usize>, amount: &Value) -> R<()> {
        let v = self.body_of(jv);
        let body = g(&self.docs[self.i], "body");
        let b = block(&self.docs[self.i]);
        let until = s(g(v, "valid_until"));
        if s(g(b, "at")) > until {
            bail!(
                self,
                "the evaluation was valid until {until}: a step after that needs a new check"
            );
        }
        if !string_is(g(g(body, "evaluation_ref"), "digest"), &self.digests[jv]) {
            bail!(
                self,
                "evaluation_ref is not the evaluation the step relied on"
            );
        }
        let basis = list(g(body, "authority_basis"));
        let types: Vec<&str> = basis.iter().map(|e| s(g(e, "type"))).collect();
        if types.contains(&"one_shot_override") {
            bail!(
                self,
                "one_shot_override is reserved: no rule in this profile defines an override"
            );
        }
        if types.first() != Some(&"task_authority")
            || types.iter().filter(|x| **x == "task_authority").count() != 1
        {
            bail!(
                self,
                "authority_basis starts with exactly one task_authority"
            );
        }
        if g(g(&basis[0], "ref"), "digest") != g(g(v, "task_authority_ref"), "digest") {
            bail!(
                self,
                "task_authority is not the evaluation's task_authority_ref"
            );
        }
        let asa: Vec<&Value> = basis
            .iter()
            .filter(|e| string_is(g(e, "type"), "user_approval"))
            .collect();
        match ja {
            Some(ja) => {
                if asa.len() != 1 || !string_is(g(g(asa[0], "ref"), "digest"), &self.digests[ja]) {
                    bail!(self, "the user_approval entry is the user's answer the step cites (authorized_by)");
                }
            }
            None if !asa.is_empty() => {
                bail!(
                    self,
                    "a DO evaluation needs no approval: the step rests on the task authority"
                );
            }
            None => {}
        }
        let mut seen = Vec::new();
        for e in basis {
            if !string_is(g(e, "type"), "platform_approval") {
                continue;
            }
            let jp = self.by_dig.get(s(g(g(e, "ref"), "digest"))).copied();
            let Some(jp) = jp.filter(|&jp| jp < self.i && self.rt(jp) == "platform_approval")
            else {
                bail!(
                    self,
                    "a platform_approval entry names an earlier platform approval"
                );
            };
            let pb = self.body_of(jp);
            let mismatch =
                !amount.is_null() && has(pb, "amount_minor") && g(pb, "amount_minor") != amount;
            if mismatch != string_is(g(e, "scope"), "mismatch") {
                bail!(self, "scope mismatch is stated exactly when the platform's approval stated another amount");
            }
            seen.push(jp);
        }
        let mut want = self.platform_for.get(&jv).cloned().unwrap_or_default();
        seen.sort_unstable();
        want.sort_unstable();
        if seen != want {
            bail!(self, "authority_basis lists every platform approval observed for the evaluation, and only those");
        }
        let mut want_types = vec!["task_authority"];
        if ja.is_some() {
            want_types.push("user_approval");
        }
        want_types.extend(std::iter::repeat_n("platform_approval", seen.len()));
        if types != want_types {
            bail!(self, "authority_basis lists task_authority, then the user's answer, then platform approvals");
        }
        Ok(())
    }

    fn limits_in_force(&self) -> Map<String, Value> {
        let mut out = Map::new();
        if !self.max_total.is_null() {
            out.insert("max_total_minor".into(), self.max_total.clone());
        }
        if !self.allowed.is_null() {
            out.insert("allowed".into(), self.allowed.clone());
        }
        if !self.delegated.is_empty() {
            out.insert("delegated".into(), Value::Array(self.delegated.clone()));
        }
        if self.bounds_known && !self.bounds.is_null() {
            out.insert("bounds_commitment".into(), self.bounds.clone());
        }
        out
    }

    fn without_unknown_bounds(&self, d: &Value) -> Value {
        let m = d.as_object().cloned().unwrap_or_default();
        if self.bounds_known {
            return Value::Object(m);
        }
        Value::Object(
            m.into_iter()
                .filter(|(k, _)| k != "bounds_commitment")
                .collect(),
        )
    }

    fn intent_between(&self, j: usize) -> bool {
        (j + 1..self.i).any(|k| self.rt(k) == TYPE_INTENT)
    }
}

fn check_deal_chain(raw: &[Value], digests: Vec<String>) -> R<()> {
    let mut c = Chain {
        docs: raw.iter().map(readable).collect(),
        digests,
        by_dig: HashMap::new(),
        i: 0,
        typed_chain: false,
        task_idx: 0,
        task_at_verdict: HashMap::new(),
        platform_for: HashMap::new(),
        party_role: "buyer".into(),
        allowed: Value::Null,
        max_total: Value::Null,
        delegated: Vec::new(),
        bounds: Value::Null,
        bounds_known: true,
        ordered: Value::Null,
        rep_rules: false,
        stated: HashSet::new(),
        confirmed: HashSet::new(),
        used_approvals: HashSet::new(),
        verdict_for_check: HashSet::new(),
        profile_for_check: HashSet::new(),
        reversed: HashSet::new(),
        first_answer: HashMap::new(),
        last_outcome: None,
        unchecked: 0,
        latest_offer: None,
        accepted: None,
        closed_final: None,
        t: String::new(),
        by_rel: BTreeMap::new(),
    };
    for (i, d) in c.digests.iter().enumerate() {
        c.by_dig.insert(d.clone(), i);
    }
    if c.docs.is_empty() {
        return Ok(());
    }
    // A chain sealed in the typed action records opens with the baseline and then
    // the user's task authority; its authority steps are typed records.
    c.typed_chain =
        c.docs.len() > 1 && string_is(g(block(&c.docs[1]), "typed"), TYPE_TASK_AUTHORITY);
    c.task_idx = usize::from(c.typed_chain);
    let blk0 = block(&c.docs[0]).clone();
    if !string_is(g(&blk0, "record_type"), TYPE_BASELINE) {
        bail_at!(0, "the first record of a deal must be the baseline");
    }
    let intent0 = g(g(&c.docs[0], "body"), "intent").clone();
    c.allowed = g(&intent0, "allowed").clone();
    c.max_total = g(&intent0, "max_total_minor").clone();
    c.delegated = list(g(&intent0, "delegated")).to_vec();
    if let Some(pr) = intent0.get("party_role") {
        c.party_role = s(pr).to_string();
    }
    if c.party_role == "seller" && !c.typed_chain {
        bail_at!(
            0,
            "a deal where the user sells is sealed in the typed action records"
        );
    }
    c.bounds = g(&intent0, "bounds_commitment").clone();
    if c.party_role == "seller" {
        c.ordered = g(&intent0, "ordered_representations").clone();
    }
    c.rep_rules = !c.ordered.is_null();

    for i in 0..c.docs.len() {
        c.i = i;
        let rec = c.docs[i].clone();
        let (b, body) = (block(&rec), g(&rec, "body"));
        let t = s(g(b, "record_type")).to_string();
        c.t = t.clone();
        let is_typed_rec = has(b, "typed");
        if is_typed_rec && !c.typed_chain {
            bail!(
                c,
                "a typed record in a chain that did not open with a task authority"
            );
        }
        if t == TYPE_CHECK
            && has(body, "listing_classes")
            && !has(c.body_of(c.task_idx), "sale_authority_commitment")
        {
            bail!(c, "listing_classes is a rule input of a sale's thread only");
        }
        if (t == TYPE_CHECK || t == TYPE_ACTION) && string_is(g(body, "action"), "post") {
            bail!(c, "a post is a step of a sale's own log (section 6, rule 15), never of a deal's chain");
        }
        if t == TYPE_VERDICT && has(body, "rules_status") {
            bail!(c, "rules_status not_applicable is a sale's post evaluation only: a deal's checks state their rules_checks");
        }
        let late_answer = t == TYPE_APPROVAL
            && string_is(g(body, "choice"), "confirm_limits")
            && !truthy(g(body, "proceed"));
        if c.typed_chain
            && !is_typed_rec
            && [
                TYPE_CHECK,
                TYPE_VERDICT,
                TYPE_APPROVAL,
                TYPE_ACTION,
                TYPE_OUTCOME,
            ]
            .contains(&t.as_str())
            && !late_answer
        {
            bail!(
                c,
                "in a chain of typed records a {t} step is a typed record"
            );
        }
        // A check that states its rule inputs version (section 8) carries the count that version
        // defines; one without it re-derives and verifies as before.
        if t == TYPE_CHECK
            && has(body, "rule_inputs_version")
            && !(has(body, "material_fields_changed") && has(body, "material_fields_basis"))
        {
            bail!(c, "a check that states rule_inputs_version carries material_fields_changed and material_fields_basis");
        }
        if c.typed_chain && t == TYPE_DISCLOSURE && string_is(g(body, "authority"), "approval") {
            bail!(
                c,
                "in a chain of typed records an approved disclosure is an action-record/v0"
            );
        }
        if t == "one_shot_override" {
            bail!(
                c,
                "one_shot_override is reserved: no rule in this profile defines an override"
            );
        }
        let confirms: Vec<&Value> = list(g(b, "refs"))
            .iter()
            .filter(|r| string_is(g(r, "rel"), "confirms"))
            .collect();
        if let Some(cf) = c.closed_final {
            // Close is terminal: after it, only later evidence that confirms
            // that close (and commits to its digest) may follow.
            if t != TYPE_EVIDENCE
                || confirms.len() != 1
                || !string_is(g(confirms[0], "digest"), &c.digests[cf])
            {
                bail!(c, "record after a close whose outcome is final (completed, mismatch, not_selected or called_off; close is terminal): only an evidence record that confirms that close may follow");
            }
        } else if !confirms.is_empty() {
            bail!(
                c,
                "a confirms ref names the deal's final close; this deal is not closed"
            );
        }
        if g(b, "deal_id") != g(&blk0, "deal_id") {
            bail!(c, "deal_id differs from the baseline's");
        }
        if let Some(why) = check_handover(&rec, &c.party_role) {
            bail!(c, "{why}");
        }
        let seq = num(g(b, "seq"))?;
        if seq != i as i64 + 1 {
            let kind = if seq <= i as i64 { "regression" } else { "gap" };
            bail!(c, "seq {kind}: expected {}, got {seq}", i + 1);
        }
        if i > 0 {
            if t == TYPE_BASELINE {
                bail!(c, "a deal has exactly one baseline");
            }
            if g(b, "prev") != &reference(&c.digests[i - 1]) {
                bail!(c, "prev is not the digest of the previous record");
            }
            if g(b, "baseline_ref") != &reference(&c.digests[0]) {
                bail!(c, "baseline_ref is not the digest of the baseline");
            }
            if s(g(b, "at")) < s(g(block(&c.docs[i - 1]), "at")) {
                bail!(c, "at is earlier than the previous record's");
            }
        }
        c.by_rel = BTreeMap::new();
        let mut registrations = 0;
        for r in list(g(b, "refs")) {
            let rel = s(g(r, "rel"));
            if !allowed_rels(&t).contains(&rel) {
                bail!(c, "rel {} is not allowed on a {t} record", py_repr(rel));
            }
            if rel == "registration" {
                // A sale's thread names its registration, a record on the sale's
                // own log: checked with the sale's bundle, never in this chain.
                registrations += 1;
                if registrations > 1 || !has(body, "sale_authority_commitment") {
                    bail!(
                        c,
                        "only a sale's thread names a registration, and exactly one"
                    );
                }
                continue;
            }
            match c.by_dig.get(s(g(r, "digest"))) {
                Some(&j) if j < i => c.by_rel.entry(rel.to_string()).or_default().push(j),
                _ => bail!(c, "ref {rel} does not name an earlier record of this deal"),
            }
        }
        if t == TYPE_MESSAGE && has(body, "sent_at") {
            // The platform's own time for the message: a real UTC time, never
            // later than when the message was noted.
            let sent = s(g(body, "sent_at"));
            if !valid_utc(sent) {
                bail!(c, "a message's sent_at is not a valid UTC time");
            }
            let noted = s(g(b, "at"));
            if !valid_utc(noted) {
                return Err("a record the chain rules cannot read: at is not a UTC time".into());
            }
            if sent > noted {
                bail!(
                    c,
                    "a message's sent_at is later than when it was noted (at)"
                );
            }
        }
        c.step(&rec, is_typed_rec)?;
    }
    Ok(())
}

fn lt(a: &Value, b: &Value) -> R<bool> {
    Ok(num(a)? < num(b)?)
}

impl Chain {
    /// The rule of one record's type, the second half of the checker's loop
    /// body (§6): `self.i` is the record, `rec` its reading, `by_rel` the
    /// indexes its refs name.
    fn step(&mut self, rec: &Value, is_typed_rec: bool) -> R<()> {
        let i = self.i;
        let (b, body) = (block(rec), g(rec, "body"));
        let t = self.t.clone();
        let by_rel_has = |c: &Chain, rel: &str| c.by_rel.contains_key(rel);
        let on_offer = t == TYPE_CHECK && string_is(g(body, "action"), "offer");

        if t == "task_authority" {
            // The user's task authority: as asked at the baseline, or a new version
            // the user confirmed in their words (previous_ref names the one replaced).
            if self.one("source", &[TYPE_BASELINE], false)?.is_some() {
                if i != 1 {
                    bail!(
                        self,
                        "the opening task authority directly follows the baseline"
                    );
                }
                let intent0 = g(self.body_of(0), "intent");
                for k in ["max_total_minor", "allowed", "ordered_representations"] {
                    if g(body, k) != g(intent0, k) {
                        bail!(
                            self,
                            "the task authority's {k} is not the one asked at the baseline"
                        );
                    }
                }
                if has(body, "bounds_commitment") != has(intent0, "bounds_commitment") {
                    bail!(
                        self,
                        "the task authority states a floor exactly when the baseline does"
                    );
                }
            } else {
                let j = self
                    .one("approves", &[TYPE_INTENT], true)?
                    .unwrap_or_default();
                let prop = self.body_of(j).clone();
                if self.confirmed.contains(&j) || self.intent_between(j) {
                    bail!(self, "a proposal replaced by a later intent, or already confirmed, gives no new task authority");
                }
                if !string_is(
                    g(g(body, "previous_ref"), "digest"),
                    &self.digests[self.task_idx],
                ) {
                    bail!(self, "previous_ref is not the task authority in force");
                }
                let keys = ["max_total_minor", "allowed", "ordered_representations"];
                let mut next = Map::new();
                for (k, v) in [
                    ("max_total_minor", &self.max_total),
                    ("allowed", &self.allowed),
                    ("ordered_representations", &self.ordered),
                ] {
                    if !v.is_null() {
                        next.insert(k.into(), v.clone());
                    }
                }
                for k in keys {
                    if !g(&prop, k).is_null()
                        && (k != "ordered_representations" || !self.ordered.is_null())
                    {
                        next.insert(k.into(), g(&prop, k).clone());
                    }
                }
                let got: Map<String, Value> = keys
                    .iter()
                    .filter_map(|k| body.get(*k).map(|v| (k.to_string(), v.clone())))
                    .collect();
                if Value::Object(got) != Value::Object(next.clone()) {
                    bail!(
                        self,
                        "the new task authority is not what the proposal asked for"
                    );
                }
                if has(&prop, "bounds_commitment")
                    && g(body, "bounds_commitment") != g(&prop, "bounds_commitment")
                {
                    bail!(
                        self,
                        "the new task authority's floor is not the one the proposal stated"
                    );
                }
                let more = (!g(&prop, "max_total_minor").is_null()
                    && !self.max_total.is_null()
                    && lt(&self.max_total, g(&prop, "max_total_minor"))?)
                    || (!g(&prop, "allowed").is_null()
                        && !self.allowed.is_null()
                        && any_not_in(g(&prop, "allowed"), &self.allowed))
                    || (!g(&prop, "ordered_representations").is_null()
                        && !self.ordered.is_null()
                        && g(&prop, "ordered_representations") != &self.ordered);
                if !more {
                    bail!(self, "the proposal asks for no higher limit and no new action: nothing to confirm");
                }
                self.confirmed.insert(j);
                self.max_total = next.get("max_total_minor").cloned().unwrap_or(Value::Null);
                self.allowed = next.get("allowed").cloned().unwrap_or(Value::Null);
                if !self.ordered.is_null() {
                    self.ordered = next
                        .get("ordered_representations")
                        .cloned()
                        .unwrap_or(Value::Null);
                }
            }
            self.task_idx = i;
        } else if t == "platform_approval" {
            // An observation of another platform's approval interaction for a proposed
            // action: listed beside that action's evaluation, never as its answer.
            let j = self
                .by_dig
                .get(s(g(g(body, "proposed_action_ref"), "digest")))
                .copied()
                .filter(|&j| j < i && self.rt(j) == TYPE_CHECK);
            let Some(j) = j else {
                bail!(
                    self,
                    "proposed_action_ref names no earlier proposed action of this chain"
                );
            };
            let jv = (j + 1..i).find(|&k| {
                self.rt(k) == TYPE_VERDICT
                    && string_is(
                        g(gi(g(block(&self.docs[k]), "refs"), 0), "digest"),
                        &self.digests[j],
                    )
            });
            let Some(jv) = jv else {
                bail!(
                    self,
                    "a platform approval observation follows the evaluation of its proposed action"
                );
            };
            if s(g(body, "observed_at")) > s(g(b, "at")) {
                bail!(self, "observed_at is later than the record");
            }
            self.platform_for.entry(jv).or_default().push(i);
        } else if on_offer {
            if self.party_role != "seller" {
                bail!(
                    self,
                    "an offer is made on a deal where the user sells (party_role seller)"
                );
            }
            let j = self.one("supersedes", &[TYPE_CHECK], self.latest_offer.is_some())?;
            if j != self.latest_offer {
                bail!(self, "an offer supersedes exactly the latest earlier offer");
            }
            // The proposal it answers: one message the counterparty sent, or,
            // when the counterparty sent none before it, no_proposal; never both.
            let j = self.one("source", &[TYPE_MESSAGE], false)?;
            if let Some(j) = j {
                if !string_is(g(self.body_of(j), "from"), "counterparty") {
                    bail!(
                        self,
                        "an offer answers a message from the counterparty (source)"
                    );
                }
            }
            if g(body, "no_proposal") == &Value::Bool(true) {
                if j.is_some() {
                    bail!(
                        self,
                        "an offer carrying no_proposal answers no message: it has no source ref"
                    );
                }
                if (0..i).any(|k| {
                    self.rt(k) == TYPE_MESSAGE
                        && string_is(g(self.body_of(k), "from"), "counterparty")
                }) {
                    bail!(
                        self,
                        "no_proposal on an offer after a message from the counterparty"
                    );
                }
            }
            self.latest_offer = Some(i);
            self.accepted = None;
        } else if t == TYPE_CHECK {
            if by_rel_has(self, "supersedes") {
                bail!(self, "only an offer supersedes an earlier offer");
            }
            if by_rel_has(self, "source") || has(body, "no_proposal") {
                bail!(
                    self,
                    "only an offer answers a message (source) or carries no_proposal"
                );
            }
            if string_is(g(body, "action"), "represent") && !self.rep_rules {
                bail!(self, "a represent check is made on a deal where the user sells, opened with ordered_representations");
            }
        } else if t == "counterparty_acceptance" {
            // An observation that the counterparty accepted one exact offer. It
            // authorizes nothing by itself; a seller's commit rests on it.
            let j = self
                .by_dig
                .get(s(g(g(body, "proposed_action_ref"), "digest")))
                .copied()
                .filter(|&j| {
                    j < i
                        && self.rt(j) == TYPE_CHECK
                        && string_is(g(self.body_of(j), "action"), "offer")
                });
            let Some(j) = j else {
                bail!(
                    self,
                    "proposed_action_ref names no earlier offer of this chain"
                );
            };
            if Some(j) != self.latest_offer {
                bail!(
                    self,
                    "a later offer superseded that one: only the latest offer can be accepted"
                );
            }
            let made = (j + 1..i).any(|k| {
                self.rt(k) == TYPE_ACTION
                    && string_is(g(self.body_of(k), "action"), "offer")
                    && list(g(block(&self.docs[k]), "refs")).iter().any(|r| {
                        string_is(g(r, "rel"), "authorized_by")
                            && self
                                .by_dig
                                .get(s(g(r, "digest")))
                                .is_some_and(|&a| self.offer_check_of(a) == j)
                    })
            });
            if !made {
                bail!(self, "that offer was checked but never made: the offer action comes before its acceptance");
            }
            if s(g(body, "observed_at")) > s(g(b, "at")) {
                bail!(self, "observed_at is later than the record");
            }
            self.accepted = Some(i);
        } else if t == "policy_change" {
            // its bindings are structural (the schema); no chain step depends on it yet
        } else if t == TYPE_INTENT {
            if let Some(pr) = body.get("party_role") {
                if !string_is(pr, &self.party_role) {
                    bail!(
                        self,
                        "an intent cannot change the deal's party_role ({}, set when it opened)",
                        self.party_role
                    );
                }
            }
            // An intent narrows the limits in force; asking for more is a
            // proposal that applies only once the user confirms it.
            let mt = g(body, "max_total_minor");
            if !mt.is_null() && (self.max_total.is_null() || lt(mt, &self.max_total)?) {
                self.max_total = mt.clone();
            }
            if !g(body, "bounds_commitment").is_null()
                && g(body, "bounds_commitment") != &self.bounds
            {
                self.bounds_known = false;
            }
            if !g(body, "allowed").is_null() {
                self.allowed = Value::Array(
                    list(g(body, "allowed"))
                        .iter()
                        .filter(|a| self.allowed.is_null() || contains(&self.allowed, a))
                        .cloned()
                        .collect(),
                );
            }
            // Naming the item, or picking an option, takes the item back
            // from the agent's delegation.
            if !g(g(body, "asked"), "item").is_null() || string_is(g(body, "move"), "picked") {
                self.delegated.retain(|d| !string_is(d, "item"));
            }
        } else if t == TYPE_AGENT_MOVE {
            // The agent chooses an item only where the user's words handed
            // it the choice.
            if string_is(g(body, "move"), "chose")
                && !self.delegated.iter().any(|d| string_is(d, "item"))
            {
                bail!(
                    self,
                    "an agent's chose needs the user's delegation of the item in force"
                );
            }
        } else if t == TYPE_COUNTERPARTY_PROFILE {
            // The payee keyed per profile, sealed right after the check it is
            // about: one per check.
            let j = self.one("about", &[TYPE_CHECK], true)?.unwrap_or_default();
            if j + 1 != i {
                bail!(
                    self,
                    "a counterparty_profile record follows the check it is about"
                );
            }
            if !self.profile_for_check.insert(j) {
                bail!(self, "a check has at most one counterparty_profile record");
            }
        } else if t == TYPE_EVIDENCE {
            self.one("about", &[TYPE_CLAIM, TYPE_BASELINE], true)?;
            self.one("confirms", &[TYPE_CLOSE], false)?;
            if has(body, "resolves_obligation") {
                let ok = self
                    .by_dig
                    .get(s(g(g(body, "resolves_obligation"), "digest")))
                    .is_some_and(|&j| j < i && has(self.body_of(j), "obligation"));
                if !ok {
                    bail!(
                        self,
                        "resolves_obligation must name an earlier record holding an obligation"
                    );
                }
            }
        } else if t == TYPE_CLAIM {
            if self.rep_rules
                && string_is(g(body, "source_kind"), "agent")
                && has(body, "text_commitment")
            {
                if !has(body, "class") {
                    bail!(self, "a seller's representation names its class");
                }
                let unchecked = g(body, "unchecked") == &Value::Bool(true);
                if has(body, "authority_basis") == unchecked {
                    bail!(self, "a seller's representation carries exactly one of authority_basis or unchecked");
                }
                if unchecked {
                    if by_rel_has(self, "authorized_by") {
                        bail!(
                            self,
                            "an unchecked representation names nothing that authorized it"
                        );
                    }
                } else {
                    let jt = self
                        .one("authorized_by", &[TYPE_APPROVAL, TYPE_VERDICT], true)?
                        .unwrap_or_default();
                    let (jv, ja, chk) = self.authorize_typed(jt, "represent", "representation")?;
                    self.verify_basis(jv, ja, &Value::Null)?;
                    let rep = g(self.body_of(chk), "representing").clone();
                    if g(&rep, "class") != g(body, "class") {
                        bail!(
                            self,
                            "the representation's class differs from the one checked"
                        );
                    }
                    // Sealed under the check's own nonces: equal commitments
                    // mean the words said are the words checked.
                    if g(&rep, "text_commitment") != g(body, "text_commitment")
                        || g(&rep, "source_ref_commitment") != g(body, "source_ref_commitment")
                    {
                        bail!(
                            self,
                            "the representation's words differ from the ones checked"
                        );
                    }
                    if self.task_at_verdict.get(&jv) != Some(&self.task_idx) {
                        bail!(self, "the task authority changed after the check: the representation needs a new check");
                    }
                    if ja.is_none()
                        && !contains(
                            g(self.body_of(self.task_idx), "ordered_representations"),
                            g(body, "class"),
                        )
                    {
                        bail!(self, "a representation the task authority does not order needs the user's own approval");
                    }
                    // The user's own claim (capsule-engine#189): whatever the
                    // task authority orders, only the user's own approval, in
                    // their words, says the words are theirs.
                    if string_is(g(&rep, "attributed_to"), "user") && ja.is_none() {
                        bail!(
                            self,
                            "a representation attributed to the user needs the user's own approval"
                        );
                    }
                    self.stated.insert(s(g(body, "class")).to_string());
                }
            } else if has(body, "authority_basis")
                || has(body, "unchecked")
                || by_rel_has(self, "authorized_by")
            {
                bail!(self, "only a seller's representation under the representation rules carries an authority");
            }
        } else if t == TYPE_DETAIL_CHANGE {
            self.one("source", &[TYPE_MESSAGE, TYPE_EVIDENCE], false)?;
            let kinds: BTreeSet<&str> = g(g(b, "counterparty"), "ids")
                .as_object()
                .map(|o| o.keys().map(String::as_str).collect())
                .unwrap_or_default();
            let want: BTreeSet<&str> = list(g(body, "changed"))
                .iter()
                .map(s)
                .filter(|x| IDENTIFIER_KINDS.contains(x))
                .collect();
            if kinds != want {
                bail!(
                    self,
                    "counterparty.ids kinds must equal the identifier kinds listed in changed"
                );
            }
        } else if t == TYPE_VERDICT {
            let j = self.one("checks", &[TYPE_CHECK], true)?.unwrap_or_default();
            if !self.verdict_for_check.insert(j) {
                bail!(self, "a check has at most one verdict");
            }
            // Every check of a claim attributed to the user asks.
            if string_is(
                g(g(self.body_of(j), "representing"), "attributed_to"),
                "user",
            ) && string_is(g(body, "result"), "pass")
            {
                bail!(
                    self,
                    "a check of a claim attributed to the user asks; it never passes on its own"
                );
            }
            // pack_id is optional (records sealed before v0.1.0-rc7 carry it); when
            // either side names one, the two must agree.
            if g(body, "pack_id") != g(self.body_of(j), "pack_id") {
                bail!(self, "verdict pack_id differs from the check's");
            }
            if is_typed_rec {
                // The check contract on the evaluation: the ProposedAction it
                // evaluated and the task authority in force; its proposed-phase
                // basis is that authority.
                self.task_at_verdict.insert(i, self.task_idx);
                if !string_is(g(body, "proposed_action_digest"), &self.digests[j]) {
                    bail!(self, "proposed_action_digest is not the digest of the proposed action this evaluation evaluates");
                }
                if !string_is(
                    g(g(body, "task_authority_ref"), "digest"),
                    &self.digests[self.task_idx],
                ) {
                    bail!(
                        self,
                        "task_authority_ref is not the task authority in force at the evaluation"
                    );
                }
                if g(body, "authority_basis")
                    != &json!([{"type": "task_authority", "ref": g(body, "task_authority_ref")}])
                {
                    bail!(
                        self,
                        "the evaluation's authority_basis is its task_authority_ref"
                    );
                }
            }
        } else if t == TYPE_APPROVAL && string_is(g(body, "choice"), "confirm_limits") {
            let j = self
                .one("approves", &[TYPE_INTENT], true)?
                .unwrap_or_default();
            self.confirm_limits(j, body)?;
        } else if t == TYPE_APPROVAL {
            let j = self
                .one("approves", &[TYPE_VERDICT], true)?
                .unwrap_or_default();
            self.first_answer.entry(j).or_insert(i);
            let v = self.body_of(j).clone();
            if string_is(g(body, "approver"), "standing_intent") {
                let chk = self.body_of(self.first_ref_index(j));
                if !string_is(g(&v, "result"), "pass") {
                    bail!(self, "a standing-intent approval needs a passing verdict; a pause needs the user's answer");
                }
                if !self.allowed.is_null() && !contains(&self.allowed, g(chk, "action")) {
                    bail!(
                        self,
                        "standing-intent approval for an action the intent does not allow"
                    );
                }
            } else if (string_is(g(&v, "result"), "pause") || string_is(g(&v, "result"), "deny"))
                && !contains(g(&v, "options"), g(body, "choice"))
            {
                bail!(
                    self,
                    "the user's choice is not one of the verdict's options"
                );
            }
            // The card the answer was given on is committed under the verdict's own card nonce,
            // so equal commitments mean the card shown is the card checked.
            if has(body, "card_commitment") {
                if !has(&v, "card_commitment") {
                    bail!(self, "the approval commits to a shown card, but the verdict it answers rendered none");
                } else if g(body, "card_commitment") != g(&v, "card_commitment") {
                    bail!(self, "the card shown is not the card checked: the approval's card_commitment differs from the verdict's");
                }
            }
            if is_typed_rec
                && has(body, "rendering_commitment")
                && g(body, "rendering_commitment") != g(&v, "card_commitment")
            {
                bail!(
                    self,
                    "what was shown (rendering_commitment) is not what the evaluation rendered"
                );
            }
        } else if t == TYPE_DISCLOSURE {
            let actions: BTreeSet<&str> = list(g(body, "fields"))
                .iter()
                .map(|f| disclosure_action(s(g(f, "class"))))
                .collect();
            if actions.len() != 1 {
                bail!(self, "contact details and credentials are separate disclosures, each covered by its own check");
            }
            if string_is(g(body, "authority"), "none") {
                if by_rel_has(self, "authorized_by") {
                    bail!(
                        self,
                        "a disclosure with authority none carries no authorized_by"
                    );
                }
            } else {
                if !by_rel_has(self, "authorized_by") {
                    bail!(self, "a disclosure with authority approval references that approval (authorized_by)");
                }
                let ja = self
                    .one("authorized_by", &[TYPE_APPROVAL], true)?
                    .unwrap_or_default();
                let action = *actions.iter().next().unwrap_or(&"");
                self.authorized_check(ja, action, "disclosure")?;
            }
        } else if t == TYPE_ACTION {
            self.action(b, body, is_typed_rec)?;
        } else if t == TYPE_OUTCOME {
            self.one("observes", &[TYPE_ACTION], false)?;
            if string_is(g(body, "status"), "unchecked_action") {
                self.unchecked += 1;
            }
            let o = s(g(body, "outcome"));
            if (o == "not_selected" || o == "called_off")
                && (0..i).any(|k| self.rt(k) == TYPE_ACTION)
            {
                bail!(
                    self,
                    "{o} means nothing was done on the deal, and an action is on record"
                );
            }
            self.last_outcome = Some(i);
        } else if t == TYPE_CLOSE {
            match self.last_outcome {
                None => {
                    if by_rel_has(self, "outcome") {
                        bail!(self, "close references an outcome but none exists");
                    }
                    if !string_is(g(body, "outcome"), "open") {
                        bail!(self, "without an outcome record a close can only be open");
                    }
                }
                Some(last) => {
                    let j = self.one("outcome", &[TYPE_OUTCOME], true)?;
                    if j != Some(last) {
                        bail!(self, "close must reference the latest outcome record");
                    }
                    if g(body, "outcome") != g(self.body_of(last), "outcome") {
                        bail!(self, "close outcome differs from the referenced outcome");
                    }
                    for k in ["actor", "reason"] {
                        if g(body, k) != g(self.body_of(last), k) {
                            bail!(self, "close {k} differs from the referenced outcome's");
                        }
                    }
                }
            }
            if num(g(body, "unchecked_actions"))? != self.unchecked {
                bail!(
                    self,
                    "unchecked_actions must count unchecked_action outcomes ({})",
                    self.unchecked
                );
            }
            if !string_is(g(body, "outcome"), "open") {
                self.closed_final = Some(i);
            }
            for ob in list(g(body, "carried_obligations")) {
                let j = self
                    .by_dig
                    .get(s(g(g(ob, "obligation"), "digest")))
                    .copied();
                let date = if has(ob, "due_by") {
                    "due_by"
                } else {
                    "cancel_by"
                };
                let ok = j.is_some_and(|j| {
                    j < i && g(g(self.body_of(j), "obligation"), date) == g(ob, date)
                });
                if !ok {
                    bail!(
                        self,
                        "carried_obligations must name earlier records holding that date"
                    );
                }
            }
        }
        Ok(())
    }

    fn confirm_limits(&mut self, j: usize, body: &Value) -> R<()> {
        let prop = self.body_of(j).clone();
        // A late answer (the proposal was replaced by a later intent, or is
        // already confirmed) is sealed as said but changes nothing.
        if self.confirmed.contains(&j) || self.intent_between(j) {
            if truthy(g(body, "proceed")) {
                bail!(self, "a proposal replaced by a later intent, or already confirmed, cannot be confirmed (proceed must be false)");
            }
            return Ok(());
        }
        if !truthy(g(body, "proceed")) {
            bail!(self, "a confirmation of a standing proposal proceeds");
        }
        let in_force = self.limits_in_force();
        let limits = g(body, "limits");
        if self.without_unknown_bounds(g(limits, "previous")) != Value::Object(in_force.clone()) {
            bail!(self, "limits.previous is not the limits in force");
        }
        let mut next = in_force;
        for k in ["max_total_minor", "allowed", "bounds_commitment"] {
            if !g(&prop, k).is_null() {
                next.insert(k.into(), g(&prop, k).clone());
            }
        }
        let added: Vec<Value> = list(g(&prop, "delegated"))
            .iter()
            .filter(|d| !self.delegated.contains(d))
            .cloned()
            .collect();
        if !added.is_empty() {
            let mut all = self.delegated.clone();
            all.extend(added.iter().cloned());
            next.insert("delegated".into(), Value::Array(all));
        }
        let got = if has(&prop, "bounds_commitment") {
            g(limits, "new").clone()
        } else {
            self.without_unknown_bounds(g(limits, "new"))
        };
        if got != Value::Object(next.clone()) {
            bail!(self, "limits.new is not what the intent proposed");
        }
        // A floor cannot be compared (only its commitment is sealed): a proposal that states a
        // floor other than the one in force may be asking for a lower one, so it needs confirming.
        let more = (!g(&prop, "max_total_minor").is_null()
            && !self.max_total.is_null()
            && lt(&self.max_total, g(&prop, "max_total_minor"))?)
            || (!g(&prop, "allowed").is_null()
                && !self.allowed.is_null()
                && any_not_in(g(&prop, "allowed"), &self.allowed))
            || (!g(&prop, "bounds_commitment").is_null()
                && g(&prop, "bounds_commitment") != &self.bounds)
            || !added.is_empty();
        if !more {
            bail!(
                self,
                "the intent asks for no higher limit and no new action: nothing to confirm"
            );
        }
        self.confirmed.insert(j);
        self.max_total = next.get("max_total_minor").cloned().unwrap_or(Value::Null);
        self.allowed = next.get("allowed").cloned().unwrap_or(Value::Null);
        self.delegated = next
            .get("delegated")
            .map(|d| list(d).to_vec())
            .unwrap_or_default();
        self.bounds = g(g(limits, "new"), "bounds_commitment").clone();
        self.bounds_known = true;
        Ok(())
    }

    fn action(&mut self, b: &Value, body: &Value, is_typed_rec: bool) -> R<()> {
        if !self.by_rel.contains_key("authorized_by") {
            bail!(
                self,
                "an action must reference the sealed approval that authorized it (authorized_by)"
            );
        }
        if !is_typed_rec && self.by_rel.contains_key("source") {
            bail!(
                self,
                "only a seller's commit or address cites an acceptance (source)"
            );
        }
        let action = s(g(body, "action")).to_string();
        let chk;
        if is_typed_rec {
            let jt = self
                .one("authorized_by", &[TYPE_APPROVAL, TYPE_VERDICT], true)?
                .unwrap_or_default();
            let (jv, ja, c) = self.authorize_typed(jt, &action, "action")?;
            chk = c;
            self.verify_basis(jv, ja, g(body, "amount_minor"))?;
            if self.rep_rules && REPRESENTATION_GATED.contains(&action.as_str()) {
                let missing: Vec<&str> = list(&self.ordered)
                    .iter()
                    .map(s)
                    .filter(|c| !self.stated.contains(*c))
                    .collect();
                if !missing.is_empty() {
                    bail!(self, "the user ordered the agent to tell the buyer about {} first: no offer, commit, sign, pay or share goes ahead before it", missing.join(", "));
                }
            }
            if has(body, "disclosed") {
                let actions: BTreeSet<&str> = list(g(g(body, "disclosed"), "fields"))
                    .iter()
                    .map(|f| disclosure_action(s(g(f, "class"))))
                    .collect();
                if actions != BTreeSet::from([action.as_str()]) {
                    bail!(
                        self,
                        "the disclosed classes are not the ones this action covers"
                    );
                }
            }
            let addr = list(g(g(body, "disclosed"), "fields"))
                .iter()
                .any(|f| ADDRESS_CLASSES.contains(&s(g(f, "class"))));
            if self.party_role == "seller" && action == "commit" {
                self.seller_needs_acceptance("commit")?;
                // On exactly the accepted offer's terms: its amount and currency,
                // terms and recourse, as the two checks seal them.
                let offered = self.body_of(self.latest_offer.unwrap_or_default());
                let committed = self.body_of(chk);
                if OFFER_TERMS.iter().any(|k| g(offered, k) != g(committed, k)) {
                    bail!(self, "the commit's terms differ from the offer the other party accepted (changed_after_acceptance)");
                }
            } else if self.party_role == "seller" && addr {
                self.seller_needs_acceptance("address")?;
            } else if self.by_rel.contains_key("source") {
                bail!(
                    self,
                    "only a seller's commit or address cites an acceptance (source)"
                );
            }
        } else {
            let ja = self
                .one("authorized_by", &[TYPE_APPROVAL], true)?
                .unwrap_or_default();
            chk = self.authorized_check(ja, &action, "action")?;
        }
        let cb = self.body_of(chk);
        if has(body, "amount_minor")
            && has(cb, "amount_minor")
            && g(body, "amount_minor") != g(cb, "amount_minor")
        {
            bail!(self, "the amount differs from the one checked");
        }
        if has(body, "cancelled_amount_minor")
            && has(cb, "cancelled_amount_minor")
            && g(body, "cancelled_amount_minor") != g(cb, "cancelled_amount_minor")
        {
            bail!(self, "the cancelled amount differs from the one checked");
        }
        if has(body, "currency") && has(cb, "currency") && g(body, "currency") != g(cb, "currency")
        {
            bail!(self, "the currency differs from the one checked");
        }
        let crail = g(g(cb, "recourse"), "rail");
        if has(body, "rail") && truthy(crail) && g(body, "rail") != crail {
            bail!(self, "the payment rail differs from the one checked");
        }
        let p_act = g(g(g(b, "counterparty"), "ids"), "payee");
        let p_chk = g(g(g(block(&self.docs[chk]), "counterparty"), "ids"), "payee");
        if truthy(p_act) && truthy(p_chk) && p_act != p_chk {
            bail!(self, "the payee fingerprint differs from the one checked");
        }
        // Section 6, rule 8: an action that returns money names the pay it
        // reverses, once, with the same amount and currency.
        if let Some(j) = self.one("reverses", &[TYPE_ACTION], false)? {
            let undone = self.body_of(j);
            let dir = undone.get("direction").unwrap_or(&Value::Null);
            let out = !has(undone, "direction") || string_is(dir, "out");
            if !string_is(g(undone, "action"), "pay") || !out {
                bail!(self, "reverses must name an earlier pay action");
            }
            if !string_is(g(body, "direction"), "in") {
                bail!(
                    self,
                    "an action that reverses a payment carries direction in"
                );
            }
            if g(body, "amount_minor") != g(undone, "amount_minor")
                || g(body, "currency") != g(undone, "currency")
            {
                bail!(
                    self,
                    "a reversal returns the amount and currency of the pay it reverses"
                );
            }
            if !self.reversed.insert(j) {
                bail!(self, "a payment is reversed at most once");
            }
        } else if string_is(g(body, "direction"), "in") && has(body, "amount_minor") {
            // A cancel that returns no sealed payment carries direction in
            // with its amount as cancelled_amount_minor: nothing returned,
            // nothing to name.
            bail!(
                self,
                "an action with direction in names the pay it reverses (reverses)"
            );
        }
        Ok(())
    }
}
