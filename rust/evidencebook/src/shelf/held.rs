// SPDX-License-Identifier: Apache-2.0

//! Lock-free readers a profile calls from a stamp or a Receive
//! (evidencebook#461, design §7.2 and §8.3), as Go's `shelf_held.go` gives
//! them: the received records held at a log's first position, the received
//! records held for a deal, and the stamp proofs held from a sender, each
//! set against the sender's counted prefix now. The store keeps a log id
//! per sender, so these keep the sender dimension and leave every choice
//! between candidates to the profile.

use rusqlite::params;

use super::receive::{
    parse_received_time, received_prefix, HeldRecord, ReceivedRow, PROOF_CONTRADICTED,
    PROOF_RECOMPUTED, PROOF_UNCHECKED,
};
use super::stamp_proof::{verify_stamp_proof, StampProof, VerifiedStampProof};
use super::time::Time;
use super::{Shelf, ShelfError};

/// A stamp proof the store holds from a sender, as it stands against that
/// sender's counted prefix now (Go `HeldProof`).
#[derive(Clone, Debug, PartialEq)]
pub struct HeldProof {
    /// The proof as received.
    pub proof: StampProof,
    /// What it shows: the fact (its book id), both checkpoints and the key
    /// that signed them.
    pub verified: VerifiedStampProof,
    /// The records held recomputed the book checkpoint's root.
    pub recomputed: bool,
    /// The records held did not recompute it.
    pub contradicted: bool,
    /// How many of the sender's currently counted records the proof covers:
    /// its book checkpoint's entries, capped to the counted prefix now, once
    /// recomputed; 0 otherwise.
    pub covers: u64,
    pub received_at: Time,
}

impl Shelf {
    /// Every received record held at position 1 of a log with id `log_id`,
    /// across senders and both records of a fork there, ordered by sender
    /// key, then digest (Go `HeldFirst`). It takes no shelf lock.
    pub fn held_first(&self, log_id: &str) -> Result<Vec<HeldRecord>, ShelfError> {
        self.held(
            "log_id = ? AND position = 1 ORDER BY sender_key, digest",
            log_id,
        )
    }

    /// Every received record held for the deal `deal_id`, across senders
    /// and logs, forks included, ordered by sender key, log id, position and
    /// digest (Go `HeldByDeal`). It takes no shelf lock.
    pub fn held_by_deal(&self, deal_id: &str) -> Result<Vec<HeldRecord>, ShelfError> {
        self.held(
            "deal_id = ? ORDER BY sender_key, log_id, position, digest",
            deal_id,
        )
    }

    /// The received records `clause` selects; `clause` is one of the
    /// constant clauses above, never input.
    fn held(&self, clause: &str, arg: &str) -> Result<Vec<HeldRecord>, ShelfError> {
        self.live()?;
        let db = self.shared().db();
        let mut stmt = db.prepare(&format!(
            "SELECT digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at FROM shelf_received WHERE {clause}"
        ))?;
        let rows = stmt.query_map([arg], |r| {
            Ok((
                ReceivedRow::from_row(r)?,
                r.get::<_, Vec<u8>>(7)?,
                r.get::<_, String>(8)?,
            ))
        })?;
        let mut out = Vec::new();
        for row in rows {
            let (row, record, at) = row?;
            let received = row.record();
            let received_at =
                parse_received_time(&at, &format!("received record {}", received.digest))?;
            out.push(HeldRecord {
                received,
                record,
                received_at,
            });
        }
        Ok(out)
    }

    /// Every stamp proof held from `sender_key` for `log_id`, by size and
    /// then shelf checkpoint, each re-verified from the bytes kept and set
    /// against the sender's counted prefix in one read (Go `HeldProofs`). It
    /// takes no shelf lock.
    pub fn held_proofs(
        &self,
        sender_key: &str,
        log_id: &str,
    ) -> Result<Vec<HeldProof>, ShelfError> {
        self.live()?;
        let mut db = self.shared().db();
        let tx = db.transaction()?;
        let prefix = received_prefix(&tx, &(sender_key.to_string(), log_id.to_string()))?;
        let corrupt = |what: &str, e: String| {
            ShelfError::Corrupt(format!(
                "a held stamp proof of {log_id} from {sender_key}: {what}: {e}"
            ))
        };
        let mut stmt = tx.prepare(
            "SELECT book_checkpoint, fact, status_opening, inclusion_proof, shelf_checkpoint, received_at, verified FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? ORDER BY size, shelf_checkpoint_sha256",
        )?;
        let rows = stmt.query_map(params![sender_key, log_id], |r| {
            Ok((
                r.get::<_, Vec<u8>>(0)?,
                r.get::<_, Vec<u8>>(1)?,
                r.get::<_, Option<Vec<u8>>>(2)?,
                r.get::<_, Vec<u8>>(3)?,
                r.get::<_, Vec<u8>>(4)?,
                r.get::<_, String>(5)?,
                r.get::<_, i64>(6)?,
            ))
        })?;
        let mut out = Vec::new();
        for row in rows {
            let (book_checkpoint, fact, opening, inclusion, shelf_checkpoint, at, state) = row?;
            let status_opening = match opening {
                Some(o) => Some(
                    serde_json::from_slice(&o)
                        .map_err(|e| corrupt("its status opening", e.to_string()))?,
                ),
                None => None,
            };
            let inclusion = serde_json::from_slice(&inclusion)
                .map_err(|e| corrupt("its inclusion proof", e.to_string()))?;
            let proof = StampProof {
                fact,
                status_opening,
                book_checkpoint,
                inclusion,
                shelf_checkpoint,
            };
            let verified = verify_stamp_proof(&proof)
                .map_err(|e| corrupt("it no longer verifies", e.to_string()))?;
            let received_at = parse_received_time(&at, "a held stamp proof")?;
            let (mut recomputed, mut contradicted, mut covers) = (false, false, 0);
            match state {
                PROOF_UNCHECKED => {}
                PROOF_RECOMPUTED => {
                    recomputed = true;
                    covers = verified
                        .book_checkpoint
                        .entries
                        .min(prefix.counted.len() as u64);
                }
                PROOF_CONTRADICTED => contradicted = true,
                other => return Err(corrupt("its state", other.to_string())),
            }
            out.push(HeldProof {
                proof,
                verified,
                recomputed,
                contradicted,
                covers,
                received_at,
            });
        }
        Ok(out)
    }
}
