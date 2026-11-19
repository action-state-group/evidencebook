//! A sealed v1 report read against its deal's first record (Go
//! `VerifyReport`, design §9 "Verifying a report").

use serde_json::Value;
use sha2::{Digest as _, Sha256};

use super::book::verify_book;
use super::check::{check_text, stage_err};
use super::names::*;
use super::report::Report;
use super::sent::{pair, verify_capsule};
use crate::canonical::jcs;
use crate::deal::names::{REPORT_ACTION_ID, REPORT_TYPE};
use crate::deal::Error;

/// A sealed report read against its deal's first record (Go `StageReport`).
pub const STAGE_REPORT: &str = "report";

fn bad(m: String) -> Error {
    stage_err(STAGE_REPORT, m)
}

/// A sealed report as it travels (Go `ReportPair`): the report Capsule with
/// its signature and key_id, and the payload as its agent_input, the form of
/// a sent pair.
pub fn report_pair(capsule: &[u8], envelope: &[u8], payload: &[u8]) -> Result<Vec<u8>, Error> {
    pair(capsule, envelope, payload)
}

/// What a verified report shows (Go `ReportRead`): the report, its Capsule
/// id and the key that signed it, the key that signed its deal's first
/// record.
#[derive(Clone, Debug, PartialEq)]
pub struct ReportRead {
    pub report: Report,
    pub capsule_id: String,
    pub key_id: String,
}

/// Checks a sealed v1 report against its deal's first record (Go
/// `VerifyReport`): `first` is the book pair of the record at seq 1 of the
/// holder's deal book. The report Capsule verifies under its key_id, is a
/// report Capsule and commits its payload; the payload is a `deal_report`
/// of x-deal-v1 that reads as [`Report`] and would encode to the same bytes,
/// with none of the values [`build_report`](super::build_report) never
/// writes; each row's ruling is against buyer, seller or null; the first
/// record is the agreement at seq 1 of the report's deal, in the log the
/// report rests on; and the report is signed by the key that signed it.
pub fn verify_report(report: &[u8], first: &[u8]) -> Result<ReportRead, Error> {
    check_text(report)?;
    let p: Value = serde_json::from_slice(report)
        .map_err(|e| bad(format!("not a report pair {{record, agent_input}}: {e}")))?;
    super::exact::exact_object(&p, &super::exact::SENT_PAIR, false, "the pair")
        .map_err(|e| bad(format!("not a report pair {{record, agent_input}}: {e}")))?;
    let ok_shape = p.as_object().is_some_and(|o| {
        o.get("record").is_some_and(Value::is_object) && o.contains_key("agent_input")
    });
    if !ok_shape {
        return Err(bad("not a report pair {record, agent_input}".into()));
    }
    let record = p["record"].as_object().cloned().unwrap_or_default();
    let (capsule, _, capsule_id, key_id) = verify_capsule(STAGE_REPORT, &record)?;
    let action_id = capsule
        .get("action_id")
        .and_then(Value::as_str)
        .unwrap_or("");
    let action_type = capsule
        .get("action_type")
        .and_then(Value::as_str)
        .unwrap_or("");
    if action_id != REPORT_ACTION_ID || action_type != "fyi" {
        return Err(bad(format!(
            "not a report Capsule: action_id {action_id:?}, action_type {action_type:?}"
        )));
    }
    let payload = jcs(&p["agent_input"]).map_err(|e| bad(format!("the payload: {e}")))?;
    let attestation = capsule.get("model_attestation"); // vocabulary-gate: AAC capsule member
    let committed = attestation
        .and_then(|a| a.pointer("/compute_attestation/agent_input_digest"))
        .and_then(Value::as_str);
    if committed != Some(hex::encode(Sha256::digest(&payload)).as_str()) {
        return Err(bad(
            "the payload is not the one the Capsule commits (agent_input_digest)".into(),
        ));
    }
    let r = read_report(&payload)?;
    if r.r#type != REPORT_TYPE || r.profile != PROFILE {
        return Err(bad(format!("not a deal_report of {PROFILE}")));
    }
    for row in &r.rows {
        if let Some(a) = row.ruling.as_ref().and_then(|ru| ru.against.as_deref()) {
            if a != ROLE_BUYER && a != ROLE_SELLER {
                return Err(bad(format!(
                    "row {:?}: a ruling is against buyer, seller or null, not {a:?}",
                    row.id
                )));
            }
        }
    }
    let b = verify_book(first)?;
    let agreement = b.record.agree().is_some_and(|a| a.kind == KIND_AGREEMENT);
    if !agreement || b.seq != 1 {
        return Err(bad(
            "the first record is not the agreement at seq 1 of a deal book".into(),
        ));
    }
    if b.record.block.deal_id != r.deal_id || b.log_id != r.rests_on.own.log_id {
        return Err(bad(
            "the first record is not of the book the report rests on".into(),
        ));
    }
    if key_id != b.key_id {
        return Err(bad(
            "the report is signed by another key than its deal's first record".into(),
        ));
    }
    Ok(ReportRead {
        report: r,
        capsule_id,
        key_id,
    })
}

/// The payload read as [`Report`] (Go `strictUnmarshal`, `sameRecord` and
/// `reportNulls`): a member the type does not name, a value of another
/// kind, a null where the type holds a value, a list or an object, and text
/// that would encode to other bytes are refused. So are what the Go round
/// trip keeps but `BuildReport` never writes: an owed that is not an
/// object, and a relied_on record whose witnessed is not whether it has a
/// time.
fn read_report(payload: &[u8]) -> Result<Report, Error> {
    let not_a_report = |e: String| bad(format!("the payload does not read as a report: {e}"));
    let doc: Value = serde_json::from_slice(payload).map_err(|e| not_a_report(e.to_string()))?;
    let r: Report = serde_json::from_value(doc.clone()).map_err(|e| not_a_report(e.to_string()))?;
    let back = serde_json::to_value(&r).map_err(|e| not_a_report(e.to_string()))?;
    if jcs(&back).map_err(|e| not_a_report(e.to_string()))? != payload {
        return Err(not_a_report(
            "the record's text does not read as its type: it would encode to other bytes".into(),
        ));
    }
    for row in &r.rows {
        if !row.owed.is_object() {
            return Err(not_a_report(format!(
                "row {:?}: owed is not an object",
                row.id
            )));
        }
        for src in &row.relied_on {
            if src.witnessed != src.time.is_some() {
                return Err(not_a_report(format!(
                    "row {:?}: relied_on {:?} is witnessed exactly when it has a time",
                    row.id, src.record
                )));
            }
        }
    }
    Ok(r)
}
