//! A genesis fact's tie (Go `shelf_handover_tie.go`; evidencebook#461,
//! `docs/design/461-shelf-facts.md` §2.7): the old witnessed tick's leaf for
//! the fact's log, its path to that tick's tree root, that root's entry in
//! the shelf log before the format switch, the cadence checkpoint the tick
//! cut and the receipts held for it, the old book checkpoint the leaf
//! commits to, and the consistency proof from it to the genesis fact's
//! checkpoint. The tick is the one with the largest witnessed size of the
//! log not past the genesis fact's size. A genesis fact with no tie is
//! untied: nothing is claimed about the log before the coverage start.

use super::cadence::{verify_witness_receipt, Cadence, WitnessReceipt, WITNESS_VERIFIED};
use super::handover::{
    root_at, verify_cll_consistency, ConsistencyEvidence, Handover, CONSISTENCY_KIND_CLL,
};
use super::log::Checkpoint;
use super::stamp::bytes_b64;
use super::tree::{cadence_fold, cadence_leaf, CADENCE_DEPTH};
use super::{ShelfError, ShelfState};
use crate::shelf_facts::BookFact;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use cll::mmr;
use rusqlite::{params, OptionalExtension};
use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};

/// Ties one book's genesis fact to the old witnessed tree (Go
/// `HandoverTie`, member for member).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct HandoverTie {
    /// The genesis fact's book id.
    pub book_id: String,
    /// The old tick's leaf: the log, its size and checkpoint digest at the
    /// tick, the leaf's salt and position, and its path to the tick's root
    /// ([`CADENCE_DEPTH`] hashes, lowercase hex, from the leaf up).
    pub log_id: String,
    pub size: u64,
    pub checkpoint_sha256: String,
    pub salt: String,
    pub index: u64,
    #[serde(default, deserialize_with = "nullable")]
    pub path: Vec<String>,
    /// The position, from 1, of the tick's root in the shelf log, before
    /// the format switch.
    pub entry_seq: u64,
    /// The COSE statement the tick cut.
    #[serde(default, with = "bytes_b64")]
    pub cadence_checkpoint: Vec<u8>,
    /// The witness receipts held for it, one per witness and key, in URL
    /// order.
    #[serde(default, deserialize_with = "nullable")]
    pub receipts: Vec<WitnessReceipt>,
    /// The COSE statement of the book checkpoint the leaf commits to.
    #[serde(default, with = "bytes_b64")]
    pub old_checkpoint: Vec<u8>,
    /// Proves the genesis fact's checkpoint extends the old one; absent
    /// when the two are of one size (their roots are then equal).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub consistency: Option<ConsistencyEvidence>,
}

/// Go reads `null` (and a missing member) as the zero value.
fn nullable<'de, D: Deserializer<'de>, T: Deserialize<'de> + Default>(d: D) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

impl Cadence {
    /// The tie of the genesis fact of `log_id` (book `book_id`) whose
    /// checkpoint is `genesis` (Go `tieLocked`); `None` when the log has no
    /// witnessed tick at or below the genesis size, or its leaf keeps no
    /// checkpoint statement (a tick a released capsule-cli build stored),
    /// and the fact is then untied.
    pub(crate) fn tie_locked(
        &self,
        state: &mut ShelfState,
        book_id: &str,
        log_id: &str,
        genesis: &[u8],
    ) -> Result<Option<HandoverTie>, ShelfError> {
        let cp = Checkpoint::from_statement(genesis)?;
        let n: Option<i64> = self
            .shelf
            .shared()
            .db()
            .query_row(
                "SELECT tick FROM cadence_witnessed WHERE log_id = ? AND mmr_size <= ? ORDER BY mmr_size DESC, tick DESC LIMIT 1",
                params![log_id, cp.tree_size as i64],
                |r| r.get(0),
            )
            .optional()?;
        let Some(n) = n else {
            return Ok(None);
        };
        let n = n as u64;
        let tick = self.tick_locked(n)?;
        let leaf = match tick.leaves.deals.get(log_id) {
            Some(leaf) if tick.leaves.stamp.is_none() => leaf,
            _ => {
                return Err(ShelfError::Corrupt(format!(
                    "witnessed tick {n} holds no tree leaf of {log_id}"
                )))
            }
        };
        if leaf.statement.is_empty() {
            return Ok(None);
        }
        let old = URL_SAFE_NO_PAD
            .decode(&leaf.statement)
            .map_err(|e| ShelfError::Corrupt(format!("tick {n}'s statement of {log_id}: {e}")))?;
        let (_, path) = tick.leaves.tree(leaf.index)?;
        let mut tie = HandoverTie {
            book_id: book_id.into(),
            log_id: log_id.into(),
            size: leaf.size,
            checkpoint_sha256: leaf.checkpoint_sha256.clone(),
            salt: leaf.salt.clone(),
            index: leaf.index,
            path: path.iter().map(hex::encode).collect(),
            entry_seq: tick.entry_seq,
            cadence_checkpoint: tick.checkpoint.clone(),
            receipts: self.receipts_locked(&tick.checkpoint)?,
            old_checkpoint: old,
            consistency: None,
        };
        if leaf.size < cp.tree_size {
            let older = Checkpoint::from_statement(&tie.old_checkpoint)?;
            let log = self.shelf.cadence_log_locked(state, log_id)?;
            let proof = log.log().prove_consistency(&older, &cp)?;
            tie.consistency = Some(ConsistencyEvidence {
                kind: CONSISTENCY_KIND_CLL.into(),
                proof,
            });
        }
        Ok(Some(tie))
    }

    /// Every receipt `shelf_receipts` holds for the checkpoint `statement`,
    /// in witness URL and then key id order (Go `receiptsLocked`).
    pub(crate) fn receipts_locked(
        &self,
        statement: &[u8],
    ) -> Result<Vec<WitnessReceipt>, ShelfError> {
        let sum = hex::encode(Sha256::digest(statement));
        let db = self.shelf.shared().db();
        let mut stmt = db.prepare(
            "SELECT receipt FROM shelf_receipts WHERE checkpoint_sha256 = ? ORDER BY witness_id, key_id",
        )?;
        let rows = stmt.query_map([&sum], |r| r.get::<_, String>(0))?;
        let mut out = Vec::new();
        for raw in rows {
            out.push(serde_json::from_str(&raw?).map_err(|e| {
                ShelfError::Corrupt(format!("a stored receipt of checkpoint {sum}: {e}"))
            })?);
        }
        Ok(out)
    }
}

/// Check a genesis fact's tie (§2.7; Go `verifyTie`) against the
/// handover's entries (`tree`, the tree of them) before the format switch at
/// `marker`, under the holder's key and the reader's witness directory.
#[allow(clippy::too_many_arguments)]
pub(crate) fn verify_tie(
    t: &HandoverTie,
    f: &BookFact,
    genesis: &Checkpoint,
    h: &Handover,
    tree: &mmr::MemoryNodeStore,
    marker: u64,
    pinned_key: &str,
    witnesses: Option<&Value>,
) -> Result<(), String> {
    if t.log_id != f.log_id || t.size > f.size {
        return Err(format!(
            "the tie names {} at {}, the genesis fact {} at {}",
            t.log_id, t.size, f.log_id, f.size
        ));
    }
    // The old book checkpoint: the holder's, of the log at the leaf's size,
    // hashing to the leaf's digest, and a prefix of the genesis checkpoint.
    let old = Checkpoint::from_statement(&t.old_checkpoint)
        .map_err(|e| format!("the old checkpoint does not verify: {e}"))?;
    if old.key_id != pinned_key
        || old.log_id != t.log_id
        || old.tree_size != t.size
        || hex::encode(Sha256::digest(&t.old_checkpoint)) != t.checkpoint_sha256
    {
        return Err("the old checkpoint is not the leaf's".into());
    }
    match &t.consistency {
        _ if t.size == f.size => {
            if t.consistency.is_some() || old.root != genesis.root {
                return Err("the old checkpoint is not the genesis checkpoint's root".into());
            }
        }
        None => return Err("no consistency proof from the old checkpoint".into()),
        Some(proof) => verify_cll_consistency(&old, genesis, proof)
            .map_err(|e| format!("the genesis checkpoint does not extend the old one: {e}"))?,
    }
    // The leaf and its path recompute the tick's root.
    if t.index >= 1 << CADENCE_DEPTH || t.salt.is_empty() || t.path.len() != CADENCE_DEPTH {
        return Err("a malformed leaf".into());
    }
    let mut path = Vec::with_capacity(t.path.len());
    for p in &t.path {
        let sibling: [u8; 32] = hex::decode(p)
            .ok()
            .and_then(|b| b.try_into().ok())
            .filter(|b: &[u8; 32]| hex::encode(b) == *p)
            .ok_or("a malformed path")?;
        path.push(sibling);
    }
    let leaf = cadence_leaf(&t.log_id, t.size, &t.checkpoint_sha256, &t.salt);
    let root = cadence_fold(&leaf, t.index, &path);
    // The root is a handed-over entry before the format switch, and the
    // cadence checkpoint is the holder's checkpoint of the shelf log the
    // handed-over entries recompute at its size, so the receipts over it
    // witnessed that root.
    if t.entry_seq < 1
        || t.entry_seq >= marker
        || h.entries[(t.entry_seq - 1) as usize] != hex::encode(root)
    {
        return Err(format!(
            "the tick's root is not the shelf log's entry at {}",
            t.entry_seq
        ));
    }
    let cadence = Checkpoint::from_statement(&t.cadence_checkpoint)
        .map_err(|e| format!("the cadence checkpoint does not verify: {e}"))?;
    if cadence.key_id != pinned_key || cadence.log_id != h.log_id || cadence.entries < t.entry_seq {
        return Err(
            "the cadence checkpoint is not the holder's shelf log at the tick's root".into(),
        );
    }
    if root_at(tree, cadence.tree_size).as_deref() != Some(cadence.root.as_str()) {
        return Err("the handed-over entries do not recompute the cadence checkpoint".into());
    }
    let verified = t.receipts.iter().any(|r| {
        verify_witness_receipt(&t.cadence_checkpoint, r, witnesses)
            .is_ok_and(|(check, _)| check.state == WITNESS_VERIFIED)
    });
    if !verified {
        return Err("no receipt from a named witness verifies for the tick's checkpoint".into());
    }
    Ok(())
}
