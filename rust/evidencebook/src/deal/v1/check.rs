// SPDX-License-Identifier: Apache-2.0

//! A v1 record's payload, digest and checks, as Go's `xdealv1` runs them
//! (`check.go`): the canonicalization, schema and record stages over one
//! record, the seal-time deal_id rule ([`check_sealed`]), and a record read
//! against another ([`check_agreement`], [`check_dispute`],
//! [`check_ruling`]).
//!
//! A refusal matches Go's in its stage, and in its text at the record and
//! agreement stages, which the profile's negative fixtures name.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::sync::OnceLock;

use boon::{Compiler, InstanceToken, SchemaIndex, Schemas, ValidationError};
use serde::de::{DeserializeSeed, MapAccess, SeqAccess, Visitor};
use serde_json::Value;

use super::names::*;
use super::record::{decode_value, Body, Record};
use super::types::*;
use crate::canonical::{jcs, json_digest, JcsError, MAX_SAFE_INTEGER};
use crate::deal::{Error, Ref, StageError, STAGE_CANONICALIZATION, STAGE_SCHEMA};

/// The record checks of design §3: where a record is sealed, deal_id
/// presence, and the rules within one record the schema does not state.
pub const STAGE_RECORD: &str = "record";
/// An agreement, dispute or ruling read against another record.
pub const STAGE_AGREEMENT: &str = "agreement";

pub(crate) fn stage_err(stage: &'static str, detail: String) -> Error {
    Error::Stage(StageError {
        stage,
        detail,
        causes: Vec::new(),
    })
}

fn bad(detail: String) -> Error {
    stage_err(STAGE_RECORD, detail)
}

fn bad_ag(detail: String) -> Error {
    stage_err(STAGE_AGREEMENT, detail)
}

/// A record's payload, the JCS bytes a record seals, and its record digest,
/// once the record passes [`check`]. `record` is the record's JSON text,
/// read as written.
pub fn encode(record: &[u8]) -> Result<(Vec<u8>, String), Error> {
    let doc = read(record)?;
    check_doc(&doc)?;
    Ok((jcs(&doc)?, json_digest(&doc)?))
}

/// Runs the stages over `record` (canonicalization, schema, record) and
/// returns the first refusal as [`Error::Stage`].
pub fn check(record: &[u8]) -> Result<(), Error> {
    check_doc(&read(record)?)
}

/// A record's record digest: lowercase hex SHA-256 over its JCS bytes.
pub fn digest(record: &[u8]) -> Result<String, Error> {
    Ok(json_digest(&read(record)?)?)
}

/// The record digest of a record value.
pub fn record_digest(r: &Record) -> Result<String, Error> {
    Ok(json_digest(&serde_json::to_value(r)?)?)
}

/// The rows digest of an offer's terms_rows: SHA-256 over the JCS bytes of
/// rows and named together (§4.2).
pub fn rows_digest(rows: &TermsRows) -> Result<String, Error> {
    Ok(json_digest(&serde_json::to_value(rows)?)?)
}

/// The digest reference to a record with digest `d`.
pub fn ref_to(d: &str) -> Ref {
    Ref {
        r#type: RECORD_REF.into(),
        digest_alg: DIGEST_ALG.into(),
        digest: d.into(),
    }
}

fn read(record: &[u8]) -> Result<Value, Error> {
    check_text(record)?;
    serde_json::from_slice(record)
        .map_err(|e| stage_err(STAGE_CANONICALIZATION, format!("not one JSON value: {e}")))
}

/// Refuses JSON text a decoder would read other than as written: invalid
/// UTF-8, an escaped surrogate outside a pair, and an object member given
/// twice (Go's `checkText`).
pub(crate) fn check_text(raw: &[u8]) -> Result<(), Error> {
    let canon = |d: &str| stage_err(STAGE_CANONICALIZATION, d.into());
    if std::str::from_utf8(raw).is_err() {
        return Err(canon("invalid UTF-8"));
    }
    let hex4 = |at: usize| -> Option<u16> {
        let s = std::str::from_utf8(raw.get(at..at + 4)?).ok()?;
        u16::from_str_radix(s, 16).ok()
    };
    let mut in_string = false;
    let mut i = 0;
    while i < raw.len() {
        let c = raw[i];
        if !in_string {
            in_string = c == b'"';
            i += 1;
            continue;
        }
        match c {
            b'"' => in_string = false,
            b'\\' if i + 1 < raw.len() => {
                if raw[i + 1] == b'u' && i + 5 < raw.len() {
                    if let Some(u) = hex4(i + 2).filter(|u| (0xD800..=0xDFFF).contains(u)) {
                        let paired = u < 0xDC00
                            && i + 11 < raw.len()
                            && raw[i + 6] == b'\\'
                            && raw[i + 7] == b'u'
                            && hex4(i + 8).is_some_and(|l| (0xDC00..=0xDFFF).contains(&l));
                        if !paired {
                            return Err(canon("an escaped surrogate outside a pair"));
                        }
                        i += 12;
                        continue;
                    }
                    i += 6;
                    continue;
                }
                i += 2;
                continue;
            }
            _ => {}
        }
        i += 1;
    }
    let mut de = serde_json::Deserializer::from_slice(raw);
    NoRepeats
        .deserialize(&mut de)
        .map_err(|e| stage_err(STAGE_CANONICALIZATION, e.to_string()))?;
    Ok(())
}

/// Walks one JSON value and refuses an object member given twice.
struct NoRepeats;

impl<'de> DeserializeSeed<'de> for NoRepeats {
    type Value = ();
    fn deserialize<D: serde::Deserializer<'de>>(self, d: D) -> Result<(), D::Error> {
        d.deserialize_any(NoRepeatsVisitor)
    }
}

struct NoRepeatsVisitor;

impl<'de> Visitor<'de> for NoRepeatsVisitor {
    type Value = ();
    fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str("a JSON value")
    }
    fn visit_bool<E>(self, _: bool) -> Result<(), E> {
        Ok(())
    }
    fn visit_i64<E>(self, _: i64) -> Result<(), E> {
        Ok(())
    }
    fn visit_u64<E>(self, _: u64) -> Result<(), E> {
        Ok(())
    }
    fn visit_f64<E>(self, _: f64) -> Result<(), E> {
        Ok(())
    }
    fn visit_str<E>(self, _: &str) -> Result<(), E> {
        Ok(())
    }
    fn visit_unit<E>(self) -> Result<(), E> {
        Ok(())
    }
    fn visit_seq<A: SeqAccess<'de>>(self, mut a: A) -> Result<(), A::Error> {
        while a.next_element_seed(NoRepeats)?.is_some() {}
        Ok(())
    }
    fn visit_map<A: MapAccess<'de>>(self, mut a: A) -> Result<(), A::Error> {
        let mut seen = BTreeSet::new();
        while let Some(k) = a.next_key::<String>()? {
            if !seen.insert(k.clone()) {
                return Err(serde::de::Error::custom(format!(
                    "member {} given twice",
                    super::super::check::go_q(&k)
                )));
            }
            a.next_value_seed(NoRepeats)?;
        }
        Ok(())
    }
}

fn check_doc(doc: &Value) -> Result<(), Error> {
    let blk = doc
        .as_object()
        .and_then(|r| r.get(PROFILE))
        .and_then(Value::as_object);
    let Some(blk) = blk else {
        return Err(stage_err(
            STAGE_CANONICALIZATION,
            "no x-deal-v1 block".into(),
        ));
    };
    let c = blk.get("canonicalization");
    if c.and_then(Value::as_str) != Some(CANONICALIZATION) {
        let got = match c {
            None | Some(Value::Null) => "<nil>".to_string(),
            Some(Value::String(s)) => s.clone(),
            Some(v) => v.to_string(),
        };
        return Err(stage_err(
            STAGE_CANONICALIZATION,
            format!("canonicalization must be exactly \"jcs\" (got {got}); fail closed"),
        ));
    }
    no_floats(doc, "$")?;
    match jcs(doc) {
        Ok(_) => {}
        Err(JcsError::UnsafeInteger(_) | JcsError::FloatInDigest) => {
            return Err(stage_err(
                STAGE_CANONICALIZATION,
                format!("not JCS-serializable: unsafe integer outside ±{MAX_SAFE_INTEGER}"),
            ))
        }
        Err(e) => {
            return Err(stage_err(
                STAGE_CANONICALIZATION,
                format!("not JCS-serializable: {e}"),
            ))
        }
    }
    stage_schema(doc)?;
    let r = decode_value(doc).map_err(|e| stage_err(STAGE_SCHEMA, e.to_string()))?;
    check_record(&r)
}

fn no_floats(v: &Value, path: &str) -> Result<(), Error> {
    match v {
        Value::Number(n) if n.to_string().contains(['.', 'e', 'E']) => Err(stage_err(
            STAGE_CANONICALIZATION,
            format!("a float at {path}; numbers are integers"),
        )),
        Value::Object(m) => {
            for (k, e) in m {
                no_floats(e, &format!("{path}.{k}"))?;
            }
            Ok(())
        }
        Value::Array(a) => {
            for (i, e) in a.iter().enumerate() {
                no_floats(e, &format!("{path}[{i}]"))?;
            }
            Ok(())
        }
        _ => Ok(()),
    }
}

/// Where a record is sealed: in a deal book, a case book, or the journal.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Placement {
    DealBook,
    CaseBook,
    Journal,
}

impl Placement {
    /// The placement's name, as the fixtures and Go write it.
    pub fn as_str(self) -> &'static str {
        match self {
            Placement::DealBook => "deal_book",
            Placement::CaseBook => "case_book",
            Placement::Journal => "journal",
        }
    }
}

impl fmt::Display for Placement {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

const DEAL_BOOK_TYPES: &[&str] = &[
    TYPE_PERFORM,
    TYPE_SETTLE,
    TYPE_STATEMENT,
    TYPE_DISPUTE,
    TYPE_RULES_VERDICT,
];
const NO_DEAL_ID: &[&str] = &[
    TYPE_INTENT,
    TYPE_ADMIT,
    TYPE_SALE,
    TYPE_THREAD,
    TYPE_THREAD_OPENED,
    TYPE_SALE_UNPOST,
    TYPE_COUNTERPARTY_PROFILE,
    TYPE_DISCLOSURE,
];
const JOURNEY_TYPES: &[&str] = &[
    TYPE_MESSAGE,
    TYPE_CLAIM,
    TYPE_EVIDENCE,
    TYPE_DETAIL_CHANGE,
    TYPE_AGENT_MOVE,
];
const ASK_TYPES: &[&str] = &[TYPE_ASK, TYPE_ANSWER];

/// Where a record of this type and block is sealed (§3.2).
pub fn placement_of(r: &Record) -> Placement {
    let t = r.block.record_type.as_str();
    if t == TYPE_RULING {
        return Placement::CaseBook;
    }
    if DEAL_BOOK_TYPES.contains(&t) {
        return Placement::DealBook;
    }
    if t == TYPE_AGREE {
        return match r.agree() {
            Some(b) if b.kind == KIND_AGREEMENT => Placement::DealBook,
            _ => Placement::Journal,
        };
    }
    if (t == TYPE_ASK || t == TYPE_ANSWER) && r.block.seq > 0 {
        return Placement::DealBook;
    }
    Placement::Journal
}

fn check_record(r: &Record) -> Result<(), Error> {
    let b = &r.block;
    let t = b.record_type.as_str();
    let place = placement_of(r);
    match place {
        Placement::DealBook | Placement::CaseBook => {
            if b.seq < 1 {
                return Err(bad(format!("a {t} record in a {place} carries its seq")));
            }
            if b.prev.is_some() != (b.seq > 1) {
                return Err(bad(
                    "a book record carries prev exactly when its seq is past 1".into(),
                ));
            }
            if place == Placement::CaseBook {
                if b.seen.is_present() || b.agreement_ref.is_some() {
                    return Err(bad("a ruling carries no seen and no agreement_ref".into()));
                }
            } else {
                if !b.seen.is_present() {
                    return Err(bad("a deal-book record carries seen (null when nothing is held from the other side)".into()));
                }
                if b.refs.is_some() {
                    return Err(bad("a deal-book record carries no refs: it cites only through its type's own members".into()));
                }
                if b.agreement_ref.is_some() != (b.seq > 1) {
                    return Err(bad(
                        "a deal-book record carries agreement_ref exactly when its seq is past 1"
                            .into(),
                    ));
                }
                if r.agree().is_some_and(|a| a.kind == KIND_AGREEMENT) && b.seq != 1 {
                    return Err(bad("an agreement opens its deal book: seq 1".into()));
                }
                if t != TYPE_AGREE && b.seq == 1 {
                    return Err(bad(format!(
                        "a deal book opens with its agreement, not a {t}"
                    )));
                }
            }
        }
        Placement::Journal => {
            if b.seq != 0 || b.prev.is_some() || b.agreement_ref.is_some() || b.seen.is_present() {
                return Err(bad("a journal record carries no seq, prev, agreement_ref or seen, so a record sent whole shows no place in the journal".into()));
            }
            if t == TYPE_PROPOSE && b.refs.is_some() {
                return Err(bad(
                    "an offer, sent whole, carries no refs: it cites only through counter_to"
                        .into(),
                ));
            }
            if let Some(ag) = r.agree() {
                if b.refs.is_some() || b.marks.is_some() || b.counterparty.is_some() {
                    return Err(bad(format!("a {} carries no refs, marks or counterparty fingerprint: it names nothing beyond the offer it answers", ag.kind)));
                }
            }
        }
    }
    check_dates(r)?;
    check_deal_id(r, place)?;
    check_body(r)
}

fn check_deal_id(r: &Record, place: Placement) -> Result<(), Error> {
    let t = r.block.record_type.as_str();
    let has = !r.block.deal_id.is_empty();
    let (mut required, mut absent) = (false, false);
    if place != Placement::Journal {
        required = true;
    } else if JOURNEY_TYPES.contains(&t) || ASK_TYPES.contains(&t) {
    } else if NO_DEAL_ID.contains(&t) {
        absent = true;
    } else if t == TYPE_APPROVAL {
        match &r.body {
            Body::Approval(a) if a.v0.choice == CHOICE_CONFIRM => absent = true,
            _ => required = true,
        }
    } else {
        required = true;
    }
    if required && !has {
        return Err(bad(format!("a {t} record carries deal_id")));
    }
    if absent && has {
        return Err(bad(format!(
            "a {t} record carries no deal_id: it predates or spans deals"
        )));
    }
    Ok(())
}

fn check_body(r: &Record) -> Result<(), Error> {
    match &r.body {
        Body::Propose(body) => {
            check_witnesses(&body.witnesses)?;
            check_forums(&body.adjudicator, &body.forums)?;
            check_rows(&body.terms_rows)
        }
        Body::Agree(body) => {
            check_witnesses(body.witness_list())?;
            if body.kind == KIND_AGREEMENT {
                check_forums(&body.adjudicator, body.forum_list())?;
                let layer = body.layer();
                if !layer && (!r.block.seen.is_null() || !body.counterparty_key.is_null()) {
                    return Err(bad("an agreement with a counterparty that runs no layer has seen null and counterparty_key null".into()));
                }
                if layer && body.counterparty_key.is_null() {
                    return Err(bad("an agreement with a counterparty that runs the layer names its record key in counterparty_key".into()));
                }
                if body.side == SIDE_ACCEPTOR && !r.block.seen.is_null() {
                    return Err(bad(
                        "the acceptor's agreement comes first: its seen is null".into(),
                    ));
                }
            }
            Ok(())
        }
        Body::Check(body) => check_v0_body(&serde_json::to_value(&body.v0)?, "body_check"),
        Body::Verdict(body) => check_v0_body(&serde_json::to_value(&body.v0)?, "body_verdict"),
        Body::Approval(body) => check_v0_body(&serde_json::to_value(&body.v0)?, "body_approval"),
        _ => Ok(()),
    }
}

/// Refuses a time the schema admits that names no instant (Go's
/// `checkDates`): each must be RFC 3339 UTC to the second and exist.
fn check_dates(r: &Record) -> Result<(), Error> {
    let mut times: Vec<&str> = vec![&r.block.at];
    match &r.body {
        Body::Propose(b) => {
            times.push(&b.stated_at);
            times.push(&b.agree_by);
            for row in &b.terms_rows.rows {
                times.push(&row.due.at);
                times.push(&row.window_end.at);
            }
            for n in b.terms_rows.named.values() {
                times.push(&n.latest);
            }
        }
        Body::Ruling(b) => {
            if let Some(rem) = &b.remedy {
                times.push(&rem.due);
            }
        }
        _ => {}
    }
    for s in times {
        if s.is_empty() {
            continue;
        }
        if crate::row_status::parse_time(s).is_err() {
            return Err(bad(format!(
                "{} is not a UTC time to the second that exists",
                super::super::check::go_q(s)
            )));
        }
    }
    Ok(())
}

/// What the holder knows when it seals a journal record that the record
/// alone does not show (§3.1).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, serde::Deserialize, serde::Serialize)]
pub struct SealContext {
    /// The offer of the deal the record serves exists when it is sealed.
    pub offer_exists: bool,
    /// An ask or answer is of an admission decision.
    pub admission: bool,
}

/// [`check`], and then §3.1's deal_id rule for the journal records whose
/// presence depends on when they are sealed (Go `CheckSealed`).
pub fn check_sealed(record: &[u8], ctx: SealContext) -> Result<(), Error> {
    let doc = read(record)?;
    check_doc(&doc)?;
    let r = decode_value(&doc)?;
    if placement_of(&r) != Placement::Journal {
        return Ok(());
    }
    let t = r.block.record_type.as_str();
    let has = !r.block.deal_id.is_empty();
    let journey = JOURNEY_TYPES.contains(&t);
    let want = if journey {
        ctx.offer_exists
    } else if ASK_TYPES.contains(&t) {
        !ctx.admission
    } else {
        return Ok(());
    };
    if has != want {
        let when = if journey {
            "when its offer exists"
        } else {
            "between the two parties of a deal"
        };
        return Err(bad(format!(
            "a {t} record sealed here carries deal_id only {when}"
        )));
    }
    Ok(())
}

fn check_forums(adj: &Null<Adjudicator>, forums: &[String]) -> Result<(), Error> {
    let none = adj.is_null();
    if none && !forums.is_empty() {
        return Err(bad(
            "an offer or agreement with no adjudicator names no forum".into(),
        ));
    }
    if !none && forums.is_empty() {
        return Err(bad(
            "an offer or agreement with an adjudicator names the forums both sides accept".into(),
        ));
    }
    Ok(())
}

fn check_witnesses(ws: &[Witness]) -> Result<(), Error> {
    let mut seen = BTreeSet::new();
    for w in ws {
        if !seen.insert(&w.key_id) {
            return Err(bad(format!("witness {} is named twice", w.key_id)));
        }
    }
    Ok(())
}

/// The key ids a witness list names, sorted.
pub(crate) fn witness_keys(ws: &[Witness]) -> Vec<&str> {
    let mut out: Vec<&str> = ws.iter().map(|w| w.key_id.as_str()).collect();
    out.sort_unstable();
    out
}

fn check_rows(tr: &TermsRows) -> Result<(), Error> {
    let mut seen = BTreeSet::new();
    for row in &tr.rows {
        if !seen.insert(&row.id) {
            return Err(bad(format!("row {} is given twice", row.id)));
        }
        for d in [&row.due, &row.window_end] {
            if !d.after.is_empty() && !tr.named.contains_key(&d.after) {
                return Err(bad(format!(
                    "row {} hangs on {}, which is not named",
                    row.id, d.after
                )));
            }
        }
    }
    Ok(())
}

/// Reads a dispute of kind dispute against the agreement of its deal book
/// (Go `CheckDispute`).
pub fn check_dispute(dispute: &Record, agreement: &Record) -> Result<(), Error> {
    let Body::Dispute(d) = &dispute.body else {
        return Err(bad_ag("not a dispute of kind dispute".into()));
    };
    if d.kind != KIND_DISPUTE {
        return Err(bad_ag("not a dispute of kind dispute".into()));
    }
    let ag = match agreement.agree() {
        Some(a) if a.kind == KIND_AGREEMENT => a,
        _ => return Err(bad_ag("not an agreement".into())),
    };
    if dispute.block.deal_id != agreement.block.deal_id {
        return Err(bad_ag("the dispute is of another deal".into()));
    }
    let ad = record_digest(agreement)?;
    if dispute
        .block
        .agreement_ref
        .as_ref()
        .map(|r| r.digest.as_str())
        != Some(ad.as_str())
    {
        return Err(bad_ag(
            "the dispute is bound to another agreement (agreement_ref)".into(),
        ));
    }
    let Some(adj) = ag.adjudicator.value() else {
        return Err(bad_ag(
            "the agreement names no adjudicator: no dispute is possible".into(),
        ));
    };
    if d.adjudicator != adj.key_id {
        return Err(bad_ag(
            "the dispute goes to another adjudicator than the agreement names".into(),
        ));
    }
    if !ag.forum_list().contains(&d.forum) {
        return Err(bad_ag(format!(
            "the dispute's forum {} is not one the agreement accepts",
            d.forum
        )));
    }
    Ok(())
}

/// Reads a ruling against the agreement of the deal (Go `CheckRuling`).
pub fn check_ruling(ruling: &Record, agreement: &Record) -> Result<(), Error> {
    let Body::Ruling(r) = &ruling.body else {
        return Err(bad_ag("not a ruling".into()));
    };
    let ag = match agreement.agree() {
        Some(a) if a.kind == KIND_AGREEMENT => a,
        _ => return Err(bad_ag("not an agreement".into())),
    };
    if ruling.block.deal_id != agreement.block.deal_id {
        return Err(bad_ag("the ruling is of another deal".into()));
    }
    if !ag.forum_list().contains(&r.forum) {
        return Err(bad_ag(format!(
            "the ruling's forum {} is not one the agreement accepts",
            r.forum
        )));
    }
    Ok(())
}

fn sorted(v: &[String]) -> Vec<&str> {
    let mut out: Vec<&str> = v.iter().map(String::as_str).collect();
    out.sort_unstable();
    out
}

/// Reads an agreement against the offer it cites (Go `CheckAgreement`).
/// `held_acceptor_agreement` is the record id of the acceptor's agreement
/// the holder holds, which an offerer's agreement with a layered
/// counterparty names in seen. It runs [`check`] on both records first.
pub fn check_agreement(
    agreement: &Record,
    offer: &Record,
    held_acceptor_agreement: &str,
) -> Result<(), Error> {
    for r in [agreement, offer] {
        check(&serde_json::to_vec(r)?)?;
    }
    let ag = match agreement.agree() {
        Some(a) if a.kind == KIND_AGREEMENT => a,
        _ => return Err(bad_ag("not an agreement".into())),
    };
    let Some(of) = offer.propose() else {
        return Err(bad_ag(format!(
            "the agreement cites a {}, not an offer",
            offer.block.record_type
        )));
    };
    let d = record_digest(offer)?;
    if ag.offer != ref_to(&d) {
        return Err(bad_ag(
            "the agreement does not cite this offer's record digest".into(),
        ));
    }
    if agreement.block.deal_id != offer.block.deal_id {
        return Err(bad_ag("the agreement's deal_id is not the offer's".into()));
    }
    if ag.rows_digest != rows_digest(&of.terms_rows)? {
        return Err(bad_ag("rows_digest is not the offer's rows digest".into()));
    }
    if witness_keys(ag.witness_list()) != witness_keys(&of.witnesses) {
        return Err(bad_ag(
            "the agreement's witnesses are not the offer's".into(),
        ));
    }
    if ag.adjudicator != of.adjudicator {
        return Err(bad_ag(
            "the agreement's adjudicator is not the offer's".into(),
        ));
    }
    if sorted(ag.forum_list()) != sorted(&of.forums) {
        return Err(bad_ag("the agreement's forums are not the offer's".into()));
    }
    match ag.side.as_str() {
        SIDE_OFFERER => {
            if ag.role != of.role {
                return Err(bad_ag("the offerer's role is the offer's".into()));
            }
            if ag.layer()
                && agreement.block.seen.value().map(String::as_str) != Some(held_acceptor_agreement)
            {
                return Err(bad_ag(
                    "the offerer's agreement names the acceptor's agreement it holds in seen"
                        .into(),
                ));
            }
        }
        SIDE_ACCEPTOR if ag.role == of.role => {
            return Err(bad_ag(
                "the acceptor's role is the other side of the offer's".into(),
            ));
        }
        _ => {}
    }
    Ok(())
}

// --- schema ---------------------------------------------------------------

const V0_SCHEMA: &[u8] = include_bytes!("../../../schemas/vendor/x-deal-v0/x-deal-v0.schema.json");
const V0_SCHEMA_ID: &str = "https://agentactioncapsule.org/profiles/x-deal-v0/record.schema.json";
const V1_SCHEMA: &[u8] = include_bytes!("../../../schemas/vendor/x-deal-v1/x-deal-v1.schema.json");
const V1_SCHEMA_ID: &str = "https://agentactioncapsule.org/profiles/x-deal-v1/record.schema.json";
const V0_BODIES: &[&str] = &["body_check", "body_verdict", "body_approval"];

/// The raw x-deal-v1 record schema.
pub fn profile_schema() -> &'static [u8] {
    V1_SCHEMA
}

struct Compiled {
    schemas: Schemas,
    profile: SchemaIndex,
    v0_bodies: BTreeMap<&'static str, SchemaIndex>,
}

fn compile() -> Result<Compiled, String> {
    let mut schemas = Schemas::new();
    let mut c = Compiler::new();
    for (id, raw) in [(V0_SCHEMA_ID, V0_SCHEMA), (V1_SCHEMA_ID, V1_SCHEMA)] {
        let doc: Value = serde_json::from_slice(raw).map_err(|e| e.to_string())?;
        c.add_resource(id, doc).map_err(|e| e.to_string())?;
    }
    let profile = c
        .compile(V1_SCHEMA_ID, &mut schemas)
        .map_err(|e| e.to_string())?;
    let mut v0_bodies = BTreeMap::new();
    for def in V0_BODIES {
        let idx = c
            .compile(&format!("{V0_SCHEMA_ID}#/$defs/{def}"), &mut schemas)
            .map_err(|e| e.to_string())?;
        v0_bodies.insert(*def, idx);
    }
    Ok(Compiled {
        schemas,
        profile,
        v0_bodies,
    })
}

fn compiled() -> Result<&'static Compiled, Error> {
    static C: OnceLock<Result<Compiled, String>> = OnceLock::new();
    C.get_or_init(compile).as_ref().map_err(|e| {
        Error::Invalid(format!(
            "the vendored x-deal-v1 schema does not compile: {e}"
        ))
    })
}

fn leaves(v: &ValidationError, out: &mut Vec<String>) {
    if v.causes.is_empty() {
        let at: Vec<String> = v
            .instance_location
            .tokens
            .iter()
            .map(|t| match t {
                InstanceToken::Prop(p) => p.to_string(),
                InstanceToken::Item(i) => i.to_string(),
            })
            .collect();
        out.push(format!("/{}: {}", at.join("/"), v.kind));
        return;
    }
    for c in &v.causes {
        leaves(c, out);
    }
}

fn stage_schema(doc: &Value) -> Result<(), Error> {
    let c = compiled()?;
    if let Err(e) = c.schemas.validate(doc, c.profile) {
        let mut out = Vec::new();
        leaves(&e, &mut out);
        return Err(Error::Stage(StageError {
            stage: STAGE_SCHEMA,
            detail: out[0].clone(),
            causes: out,
        }));
    }
    Ok(())
}

/// Validates the v0 part of a v1 overlay body against v0's closed body
/// schema.
fn check_v0_body(v0: &Value, def: &'static str) -> Result<(), Error> {
    let c = compiled()?;
    if let Err(e) = c.schemas.validate(v0, c.v0_bodies[def]) {
        return Err(stage_err(
            STAGE_SCHEMA,
            format!(
                "the {} body is not v0's {def}: {e}",
                def.trim_start_matches("body_")
            ),
        ));
    }
    Ok(())
}
