//! The witnessed cadence chain a bundle of a log's checkpoint carries (Go
//! `cadence_chain.go`; capsule-cli's `cadenceChain`, member for member, so
//! its released verifier checks it unchanged).

use super::cadence::Cadence;
use super::log::Checkpoint;
use super::ShelfError;
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use base64::Engine as _;
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};

/// The bundle extension a cadence chain travels in (capsule-cli's name).
pub const CADENCE_EXTENSION: &str = "x-cadence-witness/v0";

impl Cadence {
    /// The witnessed cadence chain for a log's checkpoint, given as its COSE
    /// statement (Go `Chain`):
    ///
    /// - `"all"`: a witnessed tick carries this very checkpoint;
    /// - `"part"`: the newest witnessed tick carrying an earlier checkpoint
    ///   of the log, plus that checkpoint and a consistency proof from it to
    ///   this one (`earlier`).
    ///
    /// `NotFound` when no witnessed tick carries the log up to this
    /// checkpoint. Object members are in byte order and each proof's in
    /// Go's struct order, as Go's encoding of the chain writes them.
    pub fn chain(&self, statement: &[u8]) -> Result<Value, ShelfError> {
        let current = Checkpoint::from_statement(statement)?;
        let mut state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let sum = hex::encode(Sha256::digest(statement));
        let (extent, tick, leaf) =
            self.witnessed_tick_locked(&current.log_id, &sum, current.tree_size)?;
        let receipt = tick.receipt.clone().ok_or_else(|| {
            ShelfError::Corrupt(format!("witnessed tick {} holds no receipt", tick.n))
        })?;
        let (_, path) = tick.leaves.tree(leaf.index)?;
        let cadence_checkpoint = Checkpoint::from_statement(&tick.checkpoint)?;
        let inclusion = self
            .log
            .prove_inclusion(tick.entry_seq, &cadence_checkpoint)?;
        let path: Vec<Value> = path.iter().map(|p| json!(hex::encode(p))).collect();
        let mut out = sorted([
            ("state", json!("witnessed")),
            ("extent", json!(extent)),
            ("checkpoint_at", json!(tick.at.rfc3339())),
            ("log_id", json!(leaf.log_id)),
            ("size", json!(leaf.size)),
            ("salt", json!(leaf.salt)),
            ("index", json!(leaf.index)),
            ("path", Value::Array(path)),
            (
                "cadence",
                Value::Object(sorted([
                    ("log_id", json!(self.cfg.log_id)),
                    ("entry_index", json!(tick.entry_seq - 1)),
                    (
                        "checkpoint",
                        Value::Object(sorted([(
                            "cose",
                            json!(URL_SAFE_NO_PAD.encode(&tick.checkpoint)),
                        )])),
                    ),
                    ("inclusion_proof", inclusion),
                    (
                        "witnesses",
                        json!([Value::Object(sorted([
                            ("ts_url", json!(receipt.ts_url)),
                            ("entry_hash", json!(receipt.entry_hash)),
                            ("receipt_b64", json!(STANDARD.encode(&receipt.receipt))),
                            ("leaf_index", json!(receipt.leaf_index)),
                            ("tree_size", json!(receipt.tree_size)),
                        ]))]),
                    ),
                ])),
            ),
        ]);
        if extent == "all" {
            return Ok(Value::Object(out));
        }
        let earlier_statement = URL_SAFE_NO_PAD.decode(&leaf.statement).map_err(|e| {
            ShelfError::Corrupt(format!(
                "tick {}'s statement of {}: {e}",
                tick.n, leaf.log_id
            ))
        })?;
        let earlier = Checkpoint::from_statement(&earlier_statement)
            .ok()
            .filter(|e| {
                hex::encode(Sha256::digest(&earlier_statement)) == leaf.checkpoint_sha256
                    && e.log_id == current.log_id
                    && e.tree_size == leaf.size
            });
        let earlier = earlier.ok_or_else(|| {
            ShelfError::Corrupt(format!(
                "tick {}'s statement of {} is not its leaf's checkpoint",
                tick.n, leaf.log_id
            ))
        })?;
        let log = self.shelf.cadence_log_locked(&mut state, &current.log_id)?;
        let consistency = log.log().prove_consistency(&earlier, &current)?;
        out.insert(
            "earlier".into(),
            Value::Object(sorted([
                (
                    "checkpoint",
                    Value::Object(sorted([("cose", json!(leaf.statement))])),
                ),
                ("consistency_proof", consistency),
            ])),
        );
        let mut sorted_out: Vec<(String, Value)> = out.into_iter().collect();
        sorted_out.sort_by(|a, b| a.0.cmp(&b.0));
        Ok(Value::Object(sorted_out.into_iter().collect()))
    }
}

/// An object of `members` with its keys in byte order, as Go encodes a map.
pub(crate) fn sorted<const N: usize>(members: [(&str, Value); N]) -> Map<String, Value> {
    let mut members: Vec<(&str, Value)> = members.into();
    members.sort_by(|a, b| a.0.cmp(b.0));
    members
        .into_iter()
        .map(|(k, v)| (k.to_string(), v))
        .collect()
}
