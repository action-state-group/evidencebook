//! Sealed reports (PROFILE.md §1, "Sealed reports are not steps"), as Go's
//! `dealprofile` lays them out (`report.go`).
//!
//! A sealed report seals one copy's readable deal text as one more Capsule:
//! action_id [`REPORT_ACTION_ID`], fyi, not chained, with the payload
//! `{"type": "deal_report", "profile", "deal_id", "audience", "nonce",
//! "report"}`. A standalone report, appended to no log, adds
//! `"deal_checkpoint"`, the deal checkpoint the copy is made at. What the
//! report's lines say is the producer's.

use serde_json::{json, Map, Value};

use super::capsule::{rfc3339_seconds, Capsule, ACTION_TYPE_FYI};
use super::check::Error;
use super::names::*;
use crate::canonical::jcs;

/// The deal checkpoint a standalone report binds.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Checkpoint {
    pub log_id: String,
    pub mmr_size: u64,
    pub root: String,
}

/// A sealed report's payload before it is encoded.
#[derive(Clone, Debug, PartialEq)]
pub struct Report {
    pub deal_id: String,
    pub audience: String,
    /// A fresh 256-bit random value (64 lowercase hex), so the Capsule's
    /// digest confirms no guess at the text.
    pub nonce: String,
    /// The copy's deal text, already cut for its audience.
    pub body: Map<String, Value>,
    /// Set for a standalone report only.
    pub checkpoint: Option<Checkpoint>,
}

fn is_hex64(s: &str) -> bool {
    s.len() == 64 && s.bytes().all(|b| matches!(b, b'0'..=b'9' | b'a'..=b'f'))
}

/// A report's payload: its JCS bytes.
pub fn report_payload(r: &Report) -> Result<Vec<u8>, Error> {
    if ![AUDIENCE_KEEP, AUDIENCE_COUNTERPARTY, AUDIENCE_ADJUDICATOR].contains(&r.audience.as_str())
    {
        return Err(Error::Invalid(format!(
            "report audience {} is not a copy audience",
            super::check::go_q(&r.audience)
        )));
    }
    if !is_hex64(&r.nonce) {
        return Err(Error::Invalid(
            "report nonce must be 64 lowercase hex".into(),
        ));
    }
    let mut body = Value::Object(r.body.clone());
    integral_floats_as_integers(&mut body);
    let mut m = json!({
        "type": REPORT_TYPE, "profile": PROFILE, "deal_id": r.deal_id, "audience": r.audience,
        "nonce": r.nonce, "report": body,
    });
    if let Some(c) = &r.checkpoint {
        m["deal_checkpoint"] = json!({"log_id": c.log_id, "mmr_size": c.mmr_size, "root": c.root});
    }
    Ok(jcs(&m)?)
}

/// Reads a whole-number float in the report's lines as the integer it is,
/// as Go's `ReportPayload` does (it writes the body as JSON and reads its
/// numbers back): a body decoded from JSON text may hold `1.0` for 1. A
/// fraction, and a whole number beyond ±2^53, stay floats, which JCS
/// refuses.
fn integral_floats_as_integers(v: &mut Value) {
    const MAX_SAFE: f64 = 9_007_199_254_740_991.0;
    match v {
        Value::Number(n) if n.is_f64() => {
            if let Some(f) = n
                .as_f64()
                .filter(|f| f.fract() == 0.0 && f.abs() <= MAX_SAFE)
            {
                *v = Value::from(f as i64);
            }
        }
        Value::Object(m) => m.values_mut().for_each(integral_floats_as_integers),
        Value::Array(a) => a.iter_mut().for_each(integral_floats_as_integers),
        _ => {}
    }
}

/// A report's Capsule members, sealed at `at` (seconds since the Unix epoch)
/// by `operator`, naming the producer's own `developer` (the capsulectl deal
/// plugin names "capsulectl-deal"). The producer seals it with the payload
/// as JSON and its own key; on a book, the record header it carries is
/// [`REPORT_RECORD_TYPE`], with no subject_ref, link or field commitment. An
/// empty developer is refused.
pub fn report_capsule(operator: &str, developer: &str, at: i64) -> Result<Capsule, Error> {
    if developer.is_empty() {
        return Err(Error::Invalid(
            "a report needs the producer's developer name".into(),
        ));
    }
    Ok(Capsule {
        action_id: REPORT_ACTION_ID.into(),
        action_type: ACTION_TYPE_FYI.into(),
        operator: operator.into(),
        developer: developer.into(),
        timestamp: rfc3339_seconds(at),
        disposition: None,
        effect: None,
        chain: None,
        extensions: Vec::new(),
    })
}
