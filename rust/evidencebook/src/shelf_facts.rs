// SPDX-License-Identifier: Apache-2.0

//! The entry documents of a shelf log of book facts (evidencebook#461,
//! `docs/design/461-shelf-facts.md` §2), byte for byte with the Go package's
//! `shelf_facts.go`: every entry after the format-switch marker is the
//! SHA-256 of one document's RFC 8785 bytes; a fact commits to its link and
//! status values as `hex(SHA-256(salt ‖ JCS(value)))`, each under its own
//! 32-byte salt, and an opening `{salt, value}` shows one value.
//! `testdata/shelf/facts/vectors.json` holds the shared vectors.

use serde_json::{json, Value};
use sha2::{Digest, Sha256};

use crate::canonical::jcs;

/// An input or opening the construction cannot take.
#[derive(Debug, thiserror::Error)]
#[error("shelf facts: {0}")]
pub struct Invalid(String);

fn invalid(m: impl Into<String>) -> Invalid {
    Invalid(m.into())
}

/// Entry document kinds.
pub const FACT_KIND_BOOK: &str = "book_fact";
pub const FACT_KIND_JOURNAL: &str = "journal_fact";
pub const ENTRY_KIND_STAMP: &str = "stamp";
pub const ENTRY_KIND_FORMAT_SWITCH: &str = "format_switch";
/// The shelf log format the marker switches to.
pub const SHELF_FACTS_FORMAT: &str = "book_facts/1";
/// The profile of a book no profile describes.
pub const PROFILE_NONE: &str = "none";

/// An opening: the salt, in lowercase hex, and the committed value.
#[derive(Debug, Clone, PartialEq)]
pub struct Opening {
    pub salt: String,
    pub value: Value,
}

impl Opening {
    /// The opening as JSON, `{salt, value}`.
    pub fn to_json(&self) -> Value {
        json!({"salt": self.salt, "value": self.value})
    }

    /// Whether the opening recomputes `commitment`.
    pub fn verify(&self, commitment: &str) -> Result<(), Invalid> {
        let salt = hex::decode(&self.salt).map_err(|_| invalid("an opening's salt is hex"))?;
        if salt.len() != 32 || hex::encode(&salt) != self.salt {
            return Err(invalid("an opening's salt is 32 bytes in lowercase hex"));
        }
        if commit(&salt, &self.value)?.0 != commitment {
            return Err(invalid("the opening does not recompute the commitment"));
        }
        Ok(())
    }
}

/// `hex(SHA-256(salt ‖ JCS(value)))` and the opening.
pub fn commit(salt: &[u8], value: &Value) -> Result<(String, Opening), Invalid> {
    if salt.len() != 32 {
        return Err(invalid("a commitment's salt is 32 bytes"));
    }
    let bytes = jcs(value).map_err(|e| invalid(e.to_string()))?;
    let mut h = Sha256::new();
    h.update(salt);
    h.update(&bytes);
    let opening = Opening {
        salt: hex::encode(salt),
        value: serde_json::from_slice(&bytes).map_err(|e| invalid(e.to_string()))?,
    };
    Ok((hex::encode(h.finalize()), opening))
}

/// What a book's fact is made from at a stamp. `status` is the profile's
/// status object (`{}` for [`PROFILE_NONE`]); `link` is
/// `{last_received, checked_over}`.
pub struct FactInput<'a> {
    pub profile: &'a str,
    pub book_id: &'a str,
    pub log_id: &'a str,
    pub size: u64,
    pub checkpoint_sha256: &'a str,
    pub prev_fact_size: u64,
    pub last_received: Option<Value>,
    pub checked_over: Vec<String>,
    pub status: Option<Value>,
}

/// A fact document and its link and status openings.
pub struct Fact {
    pub doc: Value,
    pub link_opening: Opening,
    pub status_opening: Opening,
}

fn link_value(last: Option<Value>, mut over: Vec<String>) -> Result<Value, Invalid> {
    over.sort();
    if over.windows(2).any(|w| w[0] == w[1]) {
        return Err(invalid("a digest checked over twice"));
    }
    Ok(json!({"last_received": last.unwrap_or(Value::Null), "checked_over": over}))
}

/// A book fact; `salts` gives the link salt, then the status salt.
pub fn book_fact(input: FactInput, salts: [&[u8]; 2]) -> Result<Fact, Invalid> {
    if input.profile.is_empty()
        || input.book_id.is_empty()
        || input.log_id.is_empty()
        || input.checkpoint_sha256.is_empty()
    {
        return Err(invalid(
            "a fact needs its profile, book, log and checkpoint",
        ));
    }
    if input.size < input.prev_fact_size {
        return Err(invalid("a fact's size is below its previous fact's"));
    }
    let status = input.status.unwrap_or_else(|| json!({}));
    if !status.is_object() {
        return Err(invalid("a fact's status is a JSON object"));
    }
    let link = link_value(input.last_received, input.checked_over)?;
    let (lc, lo) = commit(salts[0], &link)?;
    let (sc, so) = commit(salts[1], &status)?;
    Ok(Fact {
        doc: json!({"v": 1, "kind": FACT_KIND_BOOK, "profile": input.profile, "book_id": input.book_id,
            "log_id": input.log_id, "size": input.size, "checkpoint_sha256": input.checkpoint_sha256,
            "prev_fact_size": input.prev_fact_size, "link_commitment": lc, "status_commitment": sc}),
        link_opening: lo,
        status_opening: so,
    })
}

/// The journal's fact: its book id is the SHA-256 of its log id, and its
/// size, checkpoint digest and previous size are in its status only.
pub fn journal_fact(
    log_id: &str,
    size: u64,
    checkpoint_sha256: &str,
    prev_fact_size: u64,
    salts: [&[u8]; 2],
) -> Result<Fact, Invalid> {
    if log_id.is_empty() || checkpoint_sha256.is_empty() || size < prev_fact_size {
        return Err(invalid(
            "a journal fact needs its log, checkpoint and sizes",
        ));
    }
    let link = link_value(None, Vec::new())?;
    let status = json!({"size": size, "checkpoint_sha256": checkpoint_sha256, "prev_fact_size": prev_fact_size});
    let (lc, lo) = commit(salts[0], &link)?;
    let (sc, so) = commit(salts[1], &status)?;
    Ok(Fact {
        doc: json!({"v": 1, "kind": FACT_KIND_JOURNAL, "profile": PROFILE_NONE,
            "book_id": hex::encode(Sha256::digest(log_id.as_bytes())), "log_id": log_id, "size": 0,
            "checkpoint_sha256": "", "prev_fact_size": 0, "link_commitment": lc, "status_commitment": sc}),
        link_opening: lo,
        status_opening: so,
    })
}

/// The entry that ends every stamp.
pub fn stamp_entry(stamp: u64, facts: u64) -> Value {
    json!({"v": 1, "kind": ENTRY_KIND_STAMP, "stamp": stamp, "facts": facts})
}

/// The migration's marker; `logs` is `(log_id, size)`, sorted here by log id.
pub fn format_switch(last_tick: u64, mut logs: Vec<(String, u64)>) -> Value {
    logs.sort_by(|a, b| a.0.cmp(&b.0));
    let logs: Vec<Value> = logs
        .into_iter()
        .map(|(id, size)| json!({"log_id": id, "size": size}))
        .collect();
    json!({"v": 1, "kind": ENTRY_KIND_FORMAT_SWITCH, "format": SHELF_FACTS_FORMAT, "last_tick": last_tick, "logs": logs})
}

/// A shelf log entry: SHA-256 of the document's RFC 8785 bytes, and those
/// bytes.
pub fn entry_id(doc: &Value) -> Result<([u8; 32], Vec<u8>), Invalid> {
    let bytes = jcs(doc).map_err(|e| invalid(e.to_string()))?;
    Ok((Sha256::digest(&bytes).into(), bytes))
}

/// Which of a fact's openings an audience may have: `(link, status)`; see
/// the Go `OpeningsFor`.
pub fn openings_for(kind: &str, audience: &str) -> Result<(bool, bool), Invalid> {
    match (kind, audience) {
        (FACT_KIND_BOOK, "counterparty") | (FACT_KIND_BOOK, "owner") => Ok((true, true)),
        (FACT_KIND_BOOK, "adjudicator") | (FACT_KIND_BOOK, "asker") => Ok((true, false)),
        (FACT_KIND_BOOK, "chosen_reader") => Ok((false, true)),
        (FACT_KIND_BOOK, "auditor") => Ok((false, false)),
        (FACT_KIND_JOURNAL, "owner") | (FACT_KIND_JOURNAL, "auditor") => Ok((true, true)),
        (FACT_KIND_JOURNAL, "counterparty" | "adjudicator" | "asker" | "chosen_reader") => {
            Ok((false, false))
        }
        (FACT_KIND_BOOK | FACT_KIND_JOURNAL, _) => {
            Err(invalid(format!("unknown audience {audience:?}")))
        }
        _ => Err(invalid(format!("no openings for {kind:?}"))),
    }
}
