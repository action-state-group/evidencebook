//! The `standard_history/1` deal entry (agent-action-capsule
//! `spec/standard-history-v0.md` §5.2, SH-2) as the profile fills it from a
//! deal's sealed records: a port of Go's `dealprofile/standard_history.go`,
//! as the Python package's `evidencebook.deal.standard_history`. The mapping
//! is here, with the profile, so every producer that counts x-deal-v0 deals
//! counts them the same way. A member with no sealed record behind it is
//! never guessed:
//!
//! * A deal is a deal's own log holding an `action` (or typed
//!   `action-record/v0`) `commit` or `pay`. A sale's log is not a deal; a deal
//!   that never reached such a step was never agreed.
//! * `agreed_at` is that first step's `committed_at`, to the minute.
//! * `role` is the baseline intent's `party_role`: seller is `provided`,
//!   buyer or absent `bought`.
//! * `amount` is the `amount_minor` and currency of the check (typed
//!   `proposed-action/v0`) the step rests on, scaled by the currency's ISO
//!   4217 minor units. A deal without one is [`Error::Unpriced`]: the host
//!   refuses the card, it never drops the deal.
//! * `counterparty_class` is the member of that name a pay check carries at
//!   the top of its record (SH §7), when it is one of SH's three values; a
//!   deal whose records carry it on two or more records, on a record that is
//!   no pay check, or with another value, lacks it.
//! * `outcome` is the newest verified `ec:deal-reconcile@1` Result's claims.
//! * `happened` is `stamped_by_both` only when the counterparty's half is
//!   received and verifies; else `receipt_only` when the holder's own final
//!   close observed a `received` outcome; else `no_record`.
//! * `disputes` is empty: the profile seals no IP §11.2 disputes statement.
//!
//! The entry is the JSON object `derive_card` takes in `deals`. Records are
//! read with serde_json: a member name is matched exactly, where Go's
//! `encoding/json` also matches it in another case.

use std::collections::HashMap;

use serde_json::{json, Map, Value};

use super::check::{digest, Error};
use super::names::*;

/// One sealed record of a deal's own log.
#[derive(Clone, Debug)]
pub struct HistoryRecord {
    /// The record's JSON text, the Capsule's payload.
    pub payload: Vec<u8>,
    /// When the holder's book committed it, RFC 3339.
    pub committed_at: String,
}

/// One Result the deal's results book sealed.
#[derive(Clone, Debug)]
pub struct HistoryResult {
    /// The `evidence-result-v0` document.
    pub result: Vec<u8>,
    /// The host's check that the Result's record signature and seal verify.
    /// A Result that does not is no Result.
    pub verified: bool,
}

/// What the host holds for one deal under the card's anchor.
#[derive(Clone, Debug, Default)]
pub struct HistoryInput {
    /// The deal log's records the anchor commits, in log order.
    pub records: Vec<HistoryRecord>,
    /// The deal's sealed Results the anchor commits, oldest first.
    pub results: Vec<HistoryResult>,
    /// Whether the counterparty's half of the deal is received and its
    /// signature verifies.
    pub counterparty_half_verified: bool,
}

const UNPRICED: &str = "dealprofile: a deal has no recorded amount";

struct Rec {
    committed_at: String,
    kind: String,
    deal_id: String,
    refs: HashMap<String, Vec<String>>,
    body: Map<String, Value>,
    has_class: bool,
    class: String,
}

impl Rec {
    fn str(&self, member: &str) -> &str {
        self.body.get(member).and_then(Value::as_str).unwrap_or("")
    }

    /// The one digest the record names under `rel`, or "".
    fn reference(&self, rel: &str) -> &str {
        match self.refs.get(rel).map(Vec::as_slice) {
            Some([d]) => d,
            _ => "",
        }
    }
}

fn not_json(why: &str) -> Error {
    Error::Invalid(format!("dealprofile: a deal record is not JSON: {why}"))
}

fn string_member(obj: &Map<String, Value>, name: &str) -> Result<String, Error> {
    match obj.get(name) {
        None | Some(Value::Null) => Ok(String::new()),
        Some(Value::String(s)) => Ok(s.clone()),
        Some(_) => Err(not_json(&format!("member {name} is not a string"))),
    }
}

fn refs_of(obj: &Map<String, Value>) -> Result<Vec<(String, String)>, Error> {
    let items = match obj.get("refs") {
        None | Some(Value::Null) => return Ok(Vec::new()),
        Some(Value::Array(a)) => a,
        Some(_) => return Err(not_json("refs is not an array")),
    };
    let mut out = Vec::new();
    for r in items {
        match r {
            Value::Null => out.push((String::new(), String::new())),
            Value::Object(o) => {
                // Go reads each into RelRef, whose members are all strings.
                string_member(o, "type")?;
                string_member(o, "digest_alg")?;
                out.push((string_member(o, "rel")?, string_member(o, "digest")?));
            }
            _ => return Err(not_json("a ref is not an object")),
        }
    }
    Ok(out)
}

/// A step record of either kind (with its digest); `None` for a record that
/// is no step, such as a sealed report.
fn read_record(r: &HistoryRecord) -> Result<Option<(String, Rec)>, Error> {
    if r.payload.starts_with(&[0xEF, 0xBB, 0xBF]) {
        return Err(not_json("a byte order mark"));
    }
    let top: Value = serde_json::from_slice(&r.payload).map_err(|e| not_json(&e.to_string()))?;
    let Value::Object(top) = top else {
        return Err(not_json("not an object"));
    };
    let block = match top.get("x-deal-v0") {
        None | Some(Value::Null) => None,
        Some(Value::Object(b)) => Some(b),
        Some(_) => return Err(not_json("the x-deal-v0 block is not an object")),
    };
    let typ = string_member(&top, "type")?;
    let chain_id = string_member(&top, "chain_id")?;
    let body = match top.get("body") {
        None | Some(Value::Null) => Map::new(),
        Some(Value::Object(b)) => b.clone(),
        Some(_) => return Err(not_json("body is not an object")),
    };
    let top_refs = refs_of(&top)?;
    let (kind, deal_id, refs) = if let Some(b) = block {
        (
            string_member(b, "record_type")?,
            string_member(b, "deal_id")?,
            refs_of(b)?,
        )
    } else if typ.ends_with("/v0") {
        (typ, chain_id, top_refs)
    } else {
        return Ok(None);
    };
    let mut by_rel: HashMap<String, Vec<String>> = HashMap::new();
    for (rel, d) in refs {
        by_rel.entry(rel).or_default().push(d);
    }
    let class = top.get("counterparty_class");
    let d = digest(&r.payload)?;
    Ok(Some((
        d,
        Rec {
            committed_at: r.committed_at.clone(),
            kind,
            deal_id,
            refs: by_rel,
            body,
            has_class: class.is_some(),
            class: class.and_then(Value::as_str).unwrap_or("").to_string(),
        },
    )))
}

const CLASSES: [&str; 3] = ["person_agent", "business_agent", "shop_preauth"];

fn counterparty_class(records: &[Rec]) -> Option<&str> {
    let mut carriers = records.iter().filter(|r| r.has_class);
    let (c, None) = (carriers.next()?, carriers.next()) else {
        return None;
    };
    if c.kind != TYPE_CHECK || c.str("action") != "pay" || !CLASSES.contains(&c.class.as_str()) {
        return None;
    }
    Some(&c.class)
}

/// One deal's SH §5.2 entry, or `None` for a log that is not an agreed deal
/// (it is not on the card). [`Error::Unpriced`], or any other error, means
/// the card cannot be built. The entry's members are in Go's struct order.
pub fn standard_history_deal(input: &HistoryInput) -> Result<Option<Value>, Error> {
    let mut records: Vec<Rec> = Vec::new();
    let mut by_digest: HashMap<String, usize> = HashMap::new();
    for r in &input.records {
        if let Some((d, rec)) = read_record(r)? {
            by_digest.insert(d, records.len());
            records.push(rec);
        }
    }
    let Some(first) = records.first() else {
        return Ok(None);
    };
    if first.deal_id.starts_with("sale-") || first.kind == TYPE_SALE {
        return Ok(None);
    }
    let Some(agreed) = records.iter().find(|r| {
        (r.kind == TYPE_ACTION || r.kind == TYPE_ACTION_RECORD)
            && matches!(r.str("action"), "commit" | "pay")
    }) else {
        return Ok(None);
    };
    let deal_id = first.deal_id.clone();
    let amount = agreed_amount(agreed, &records, &by_digest).map_err(|e| match e {
        Error::Unpriced(m) => Error::Unpriced(format!("deal {deal_id}: {m}")),
        e => e,
    })?;
    let mut entry = Map::new();
    entry.insert("deal".into(), deal_id.into());
    entry.insert("agreed_at".into(), minute(&agreed.committed_at)?.into());
    if let Some(class) = counterparty_class(&records) {
        entry.insert("counterparty_class".into(), class.into());
    }
    entry.insert("role".into(), deal_role(&records).into());
    entry.insert("amount".into(), amount);
    entry.insert("outcome".into(), reconcile_outcome(&input.results).into());
    entry.insert(
        "happened".into(),
        happened(&records, &by_digest, input.counterparty_half_verified).into(),
    );
    entry.insert("disputes".into(), json!([]));
    Ok(Some(Value::Object(entry)))
}

/// `at` (RFC 3339) in UTC, floored to the minute.
fn minute(at: &str) -> Result<String, Error> {
    let bad = || Error::Invalid(format!("dealprofile: committed_at {at:?} is not RFC 3339"));
    let b = at.as_bytes();
    let num = |from: usize, to: usize| -> Result<i64, Error> {
        at.get(from..to)
            .filter(|s| s.bytes().all(|c| c.is_ascii_digit()))
            .and_then(|s| s.parse().ok())
            .ok_or_else(bad)
    };
    if b.len() < 20
        || b[4] != b'-'
        || b[7] != b'-'
        || !matches!(b[10], b'T' | b't')
        || b[13] != b':'
        || b[16] != b':'
    {
        return Err(bad());
    }
    let (y, mo, d, h, mi) = (
        num(0, 4)?,
        num(5, 7)?,
        num(8, 10)?,
        num(11, 13)?,
        num(14, 16)?,
    );
    let mut i = 19;
    if b[i] == b'.' {
        i += 1;
        while i < b.len() && b[i].is_ascii_digit() {
            i += 1;
        }
    }
    let offset = match b.get(i) {
        Some(b'Z' | b'z') if i + 1 == b.len() => 0,
        Some(sign @ (b'+' | b'-')) if i + 6 == b.len() && b[i + 3] == b':' => {
            let m = num(i + 1, i + 3)? * 60 + num(i + 4, i + 6)?;
            if *sign == b'+' {
                m
            } else {
                -m
            }
        }
        _ => return Err(bad()),
    };
    // Days from civil (Howard Hinnant), to subtract the offset exactly.
    let (yy, mm) = if mo <= 2 {
        (y - 1, mo + 9)
    } else {
        (y, mo - 3)
    };
    let era = yy.div_euclid(400);
    let yoe = yy - era * 400;
    let doy = (153 * mm + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146097 + doe - 719468;
    let minutes = days * 1440 + h * 60 + mi - offset;
    let (days, rem) = (minutes.div_euclid(1440), minutes.rem_euclid(1440));
    let z = days + 719468;
    let era = z.div_euclid(146097);
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    Ok(format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:00Z",
        rem / 60,
        rem % 60
    ))
}

fn agreed_amount(
    step: &Rec,
    records: &[Rec],
    by_digest: &HashMap<String, usize>,
) -> Result<Value, Error> {
    let get = |d: &str| by_digest.get(d).map(|&i| &records[i]);
    let unpriced = |why: String| Error::Unpriced(format!("{UNPRICED}: {why}"));
    let mut check = None;
    if step.kind == TYPE_ACTION_RECORD {
        let d = step
            .body
            .get("evaluation_ref")
            .and_then(|r| r.get("digest"))
            .and_then(Value::as_str)
            .unwrap_or("");
        if let Some(ev) = get(d).filter(|e| e.kind == TYPE_ACTION_EVALUATION) {
            check =
                get(ev.str("proposed_action_digest")).filter(|c| c.kind == TYPE_PROPOSED_ACTION);
        }
    } else if let Some(ap) =
        get(step.reference("authorized_by")).filter(|a| a.kind == TYPE_APPROVAL)
    {
        if let Some(v) = get(ap.reference("approves")).filter(|v| v.kind == TYPE_VERDICT) {
            check = get(v.reference("checks")).filter(|c| c.kind == TYPE_CHECK);
        }
    }
    let Some(check) = check else {
        return Err(unpriced(format!(
            "the {} step's check is not on its log",
            step.str("action")
        )));
    };
    // Go reads *int64: a whole number only, and within int64.
    let minor = check
        .body
        .get("amount_minor")
        .and_then(Value::as_number)
        .and_then(|n| n.as_i64())
        .filter(|m| *m >= 0);
    let currency = check.str("currency");
    let Some(minor) = minor.filter(|_| !currency.is_empty()) else {
        return Err(unpriced("its check states no amount and currency".into()));
    };
    let Some(scale) = minor_units(currency) else {
        return Err(unpriced(format!(
            "currency {currency:?} has no ISO 4217 minor units"
        )));
    };
    Ok(json!({"value": minor.to_string(), "assetCode": currency, "assetScale": scale}))
}

fn deal_role(records: &[Rec]) -> &'static str {
    for r in records {
        if r.kind == TYPE_BASELINE {
            let seller = r
                .body
                .get("intent")
                .and_then(|i| i.get("party_role"))
                .and_then(Value::as_str)
                == Some("seller");
            return if seller { "provided" } else { "bought" };
        }
    }
    "bought"
}

/// The newest verified Result's claims.
fn reconcile_outcome(results: &[HistoryResult]) -> &'static str {
    let Some(res) = results.iter().rev().find(|r| r.verified) else {
        return "undecided";
    };
    let Ok(Value::Object(doc)) = serde_json::from_slice::<Value>(&res.result) else {
        return "undecided";
    };
    let Some(Value::Array(claims)) = doc.get("claims") else {
        return "undecided";
    };
    let mut verdicts = Vec::new();
    for c in claims {
        match c {
            Value::Null => verdicts.push(""),
            Value::Object(o) => match o.get("verdict") {
                None | Some(Value::Null) => verdicts.push(""),
                Some(Value::String(s)) => verdicts.push(s.as_str()),
                Some(_) => return "undecided",
            },
            _ => return "undecided",
        }
    }
    if verdicts.is_empty() {
        return "undecided";
    }
    if verdicts.contains(&"not_met") {
        return "did_not_hold";
    }
    if verdicts.iter().all(|v| *v == "met") {
        "held"
    } else {
        "undecided"
    }
}

/// SH §5.5 over the holder's records: the counterparty's half, else the
/// holder's own final close of a received outcome.
fn happened(
    records: &[Rec],
    by_digest: &HashMap<String, usize>,
    counterparty_half: bool,
) -> &'static str {
    if counterparty_half {
        return "stamped_by_both";
    }
    let last_close = records
        .iter()
        .rfind(|r| r.kind == TYPE_CLOSE && r.str("outcome") != "open");
    if let Some(fin) = last_close {
        if let Some(&i) = by_digest.get(fin.reference("outcome")) {
            let o = &records[i];
            if (o.kind == TYPE_OUTCOME || o.kind == TYPE_ACTION_OUTCOME)
                && o.str("status") == "received"
            {
                return "receipt_only";
            }
        }
    }
    "no_record"
}

/// ISO 4217 codes with minor units other than two.
const MINOR_UNIT_EXCEPTIONS: [(&str, u8); 26] = [
    ("BIF", 0),
    ("CLP", 0),
    ("DJF", 0),
    ("GNF", 0),
    ("ISK", 0),
    ("JPY", 0),
    ("KMF", 0),
    ("KRW", 0),
    ("PYG", 0),
    ("RWF", 0),
    ("UGX", 0),
    ("UYI", 0),
    ("VND", 0),
    ("VUV", 0),
    ("XAF", 0),
    ("XOF", 0),
    ("XPF", 0),
    ("BHD", 3),
    ("IQD", 3),
    ("JOD", 3),
    ("KWD", 3),
    ("LYD", 3),
    ("OMR", 3),
    ("TND", 3),
    ("CLF", 4),
    ("UYW", 4),
];

/// ISO 4217 codes with two minor units, including ANG and BGN, withdrawn for
/// XCG and EUR but still on older deals.
const TWO_DECIMAL_CURRENCIES: &str = " AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BMD BND BOB BOV BRL BSD BTN BWP BYN BZD \
 CAD CDF CHE CHF CHW CNY COP COU CRC CUP CVE CZK DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GTQ GYD \
 HKD HNL HTG HUF IDR ILS INR IRR JMD KES KGS KHR KPW KYD KZT LAK LBP LKR LRD LSL MAD MDL MGA MKD MMK MNT MOP MRU \
 MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD PAB PEN PGK PHP PKR PLN QAR RON RSD RUB SAR SBD SCR SDG SEK \
 SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TOP TRY TTD TWD TZS UAH USD USN UYU UZS VED VES WST XCD XCG \
 YER ZAR ZMW ZWG ";

/// A currency's ISO 4217 minor units: the `assetScale` of an amount in minor
/// units. `None` for a code with none (a metal, a fund code without them) or
/// an unknown one.
pub fn minor_units(currency: &str) -> Option<u8> {
    if let Some((_, s)) = MINOR_UNIT_EXCEPTIONS.iter().find(|(c, _)| *c == currency) {
        return Some(*s);
    }
    (currency.len() == 3 && TWO_DECIMAL_CURRENCIES.contains(&format!(" {currency} "))).then_some(2)
}
