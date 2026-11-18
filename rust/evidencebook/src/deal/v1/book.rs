// SPDX-License-Identifier: Apache-2.0

//! A book pair (design §8.3; Go `receive.go`'s `BookPair` and `VerifyBook`):
//! a deal-book or case-book record travels as its Capsule with its
//! signature and key_id, the record header the Capsule commits as its
//! agent_input_digest, and the x-deal-v1 record the header commits as its
//! one payload.

use serde_json::{json, Map, Value};
use sha2::{Digest as _, Sha256};

use super::check::{check, check_text, digest, placement_of, stage_err, Placement};
use super::record::{decode, Record};
use super::sent::verify_capsule;
use crate::canonical::jcs;
use crate::deal::Error;

/// The stage a book pair is read at on its own ([`verify_book`]).
pub const STAGE_BOOK: &str = "book";

/// The top-level Capsule member a caller-sealed record carries its header
/// in; a book pair's Capsule commits its header instead (the shelf's
/// `RECORD_HEADER_MEMBER`).
const INLINE_HEADER: &str = "evidence_record_header";

fn bad(m: impl Into<String>) -> Error {
    stage_err(STAGE_BOOK, m.into())
}

/// What a verified book pair shows (Go `BookRead`): the record, its record
/// digest, the Capsule id (the record's entry in its book log), the key
/// that signed it, and its place in its book: the header's book log id,
/// its seq, and the record id it cites before it (`""` at seq 1).
#[derive(Clone, Debug, PartialEq)]
pub struct BookRead {
    pub record: Record,
    pub digest: String,
    pub record_id: String,
    pub key_id: String,
    pub log_id: String,
    pub seq: u64,
    pub prev: String,
}

/// A book record as it is sent (Go `BookPair`): `header`, `capsule` and
/// `envelope` as its book sealed them (the shelf's `StoredRecord`), and
/// `payload` the x-deal-v1 record's JCS bytes, its one payload. The pair's
/// members are in name order, as Go marshals a map.
pub fn book_pair(
    header: &[u8],
    capsule: &[u8],
    envelope: &[u8],
    payload: &[u8],
) -> Result<Vec<u8>, Error> {
    let mut members: Map<String, Value> = match serde_json::from_slice(capsule)? {
        Value::Object(m) => m,
        _ => {
            return Err(Error::Invalid(
                "xdealv1: the Capsule is not a JSON object".into(),
            ))
        }
    };
    let id = members
        .get("capsule_id")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string();
    let verified = agent_action_capsule::envelope::verify(&id, envelope);
    if !verified.ok {
        return Err(Error::Invalid(
            "xdealv1: the envelope does not verify".into(),
        ));
    }
    members.insert("signature".into(), json!(hex::encode(envelope)));
    members.insert("key_id".into(), json!(hex::encode(&verified.public_key)));
    let mut sorted: Vec<(String, Value)> = members.into_iter().collect();
    sorted.sort_by(|a, b| a.0.cmp(&b.0));
    let record: Map<String, Value> = sorted.into_iter().collect();
    let mut out = Map::new();
    out.insert("header".into(), serde_json::from_slice(header)?);
    out.insert("payload".into(), serde_json::from_slice(payload)?);
    out.insert("record".into(), Value::Object(record));
    Ok(serde_json::to_vec(&Value::Object(out))?)
}

/// Checks a book pair on its own (Go `VerifyBook`): the Capsule verifies
/// under its key_id and commits the header as its agent_input_digest; the
/// header commits the record's digest as its one payload and names its book
/// log, seq and, past seq 1, exactly one record it cites; and the record
/// passes [`check`], is sealed in a deal book or a case book, and is at the
/// header's seq.
pub fn verify_book(pair: &[u8]) -> Result<BookRead, Error> {
    check_text(pair)?;
    let p: Value = serde_json::from_slice(pair)
        .map_err(|e| bad(format!("not a book pair {{record, header, payload}}: {e}")))?;
    let shape = p.as_object().filter(|o| {
        o.keys()
            .all(|k| matches!(k.as_str(), "record" | "header" | "payload"))
            && o.get("record").is_some_and(Value::is_object)
            && o.contains_key("header")
            && o.contains_key("payload")
    });
    let Some(o) = shape else {
        return Err(bad("not a book pair {record, header, payload}"));
    };
    let record = o["record"].as_object().cloned().unwrap_or_default();
    if record.contains_key(INLINE_HEADER) {
        return Err(bad(
            "the Capsule carries its header inline; a book record commits it",
        ));
    }
    let (capsule, _, record_id, key_id) = verify_capsule(STAGE_BOOK, &record)?;
    let header_bytes = jcs(&o["header"]).map_err(|e| bad(format!("the header: {e}")))?;
    let committed = capsule
        .get("model_attestation") // vocabulary-gate: AAC capsule member
        .and_then(|a| a.pointer("/compute_attestation/agent_input_digest"))
        .and_then(Value::as_str);
    if committed != Some(hex::encode(Sha256::digest(&header_bytes)).as_str()) {
        return Err(bad("the Capsule does not commit this header"));
    }
    let header = &o["header"];
    let payload_text = serde_json::to_vec(&o["payload"])?;
    let d = digest(&payload_text)?;
    // One payload, this record: a header committing two would let one
    // record id be read as either.
    let commitments: Vec<&str> = header
        .get("payload_commitments")
        .and_then(Value::as_array)
        .map(|a| a.iter().filter_map(Value::as_str).collect())
        .unwrap_or_default();
    if commitments != [d.as_str()] {
        return Err(bad(
            "the header does not commit this record as its one payload",
        ));
    }
    check(&payload_text)?;
    let r = decode(&payload_text)?;
    if placement_of(&r) == Placement::Journal {
        return Err(bad(format!(
            "a {} is sealed in the journal, not sent from a book",
            r.block.record_type
        )));
    }
    let log_id = header
        .get("book_id")
        .and_then(Value::as_str)
        .unwrap_or("")
        .to_string();
    let seq = header.get("seq").and_then(Value::as_u64).unwrap_or(0);
    if log_id.is_empty() || seq == 0 || u64::try_from(r.block.seq).ok() != Some(seq) {
        return Err(bad(format!(
            "the record's seq {} is not its header's {seq}",
            r.block.seq
        )));
    }
    let cites: Vec<&str> = header
        .get("links")
        .and_then(Value::as_array)
        .map(|links| {
            links
                .iter()
                .filter(|l| l.get("type").and_then(Value::as_str) == Some("cites"))
                .filter_map(|l| l.get("target").and_then(Value::as_str))
                .collect()
        })
        .unwrap_or_default();
    let prev = match (seq, cites.as_slice()) {
        (1, []) => String::new(),
        (1, _) => return Err(bad("the first record of a book cites none before it")),
        (_, [one]) => one.to_string(),
        _ => return Err(bad("a book record cites exactly the record before it")),
    };
    Ok(BookRead {
        record: r,
        digest: d,
        record_id,
        key_id,
        log_id,
        seq,
        prev,
    })
}
