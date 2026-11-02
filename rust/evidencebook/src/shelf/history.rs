//! A cadence tick as the anchor of a shelf's `standard_history/1` card (Go
//! `cadence_standard_history.go`): the cadence checkpoint commits every
//! book's checkpoint and is the one checkpoint the witness sees, so a card
//! counting the deals of every book on the shelf is as of a tick.

use super::cadence::{Cadence, CadenceTick};
use super::log::Checkpoint;
use super::ShelfError;
use crate::answer::DERIVATION_HISTORY_CARD;
use crate::staple::Staple;
use evidence_request::cll_material::encode_consistency_proof;
use evidence_request::envelope::{sign_with_profile, Anchor, Binding, EnvelopeParts, PROFILE};
use evidence_request::request::{Derivation, Subject};
use rand_core::{OsRng, RngCore};
use rusqlite::OptionalExtension;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::time::Duration;

/// A cadence tick as the anchor of a shelf's card (Go
/// `StandardHistoryAnchor`).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StandardHistoryAnchor {
    pub tick: CadenceTick,
    /// The SHA-256 of the tick's signed cadence checkpoint.
    pub anchor: String,
    /// Its time, in the profile's whole-second form.
    pub anchor_time: String,
    /// The time of the cadence log's first checkpoint, in the same form.
    pub first_checkpoint_time: String,
    /// For every log a tick up to and including this one carried, how many
    /// of its records (seq 1 to the extent) the tick commits: the largest
    /// checkpoint of the log any of those ticks carries.
    pub extents: BTreeMap<String, u64>,
}

/// The profile's time form: whole seconds, UTC (Go `ProfileTime`).
fn profile_time(issued_at: &str) -> Result<String, ShelfError> {
    super::time::Time::parse(issued_at)
        .map(|t| t.truncate(Duration::from_secs(1)).rfc3339())
        .ok_or_else(|| ShelfError::Corrupt(format!("checkpoint time {issued_at:?}")))
}

impl Cadence {
    /// The anchor of tick `n`, or of the newest finished tick when `n` is 0.
    /// `NotFound` when the cadence has no such finished tick.
    pub fn standard_history_anchor(&self, n: u64) -> Result<StandardHistoryAnchor, ShelfError> {
        let state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let db = self.shelf.shared().db();
        let n = if n == 0 {
            let newest: i64 = db.query_row(
                "SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0",
                [],
                |r| r.get(0),
            )?;
            if newest == 0 {
                return Err(ShelfError::NotFound(
                    "the cadence has no finished tick".into(),
                ));
            }
            newest as u64
        } else {
            n
        };
        drop(db);
        let tick = self.tick_locked(n)?;
        if tick.checkpoint_size == 0 {
            return Err(ShelfError::NotFound(format!("tick {n} is not finished")));
        }
        let at = Checkpoint::from_statement(&tick.checkpoint)?;
        let db = self.shelf.shared().db();
        let first: Vec<u8> = db
            .query_row(
                "SELECT checkpoint FROM cadence_ticks WHERE checkpoint_size > 0 ORDER BY tick LIMIT 1",
                [],
                |r| r.get(0),
            )
            .optional()?
            .ok_or_else(|| ShelfError::Corrupt("the cadence has no finished tick".into()))?;
        let first = Checkpoint::from_statement(&first)?;
        let mut stmt = db.prepare(
            "SELECT j.key, MAX(json_extract(j.value, '$.size')) FROM cadence_ticks AS t, json_each(t.leaves, '$.deals') AS j
             WHERE t.tick <= ? AND t.checkpoint_size > 0 GROUP BY j.key",
        )?;
        let mut extents = BTreeMap::new();
        for row in stmt.query_map([n as i64], |r| {
            Ok((r.get::<_, String>(0)?, r.get::<_, i64>(1)?))
        })? {
            let (id, size) = row?;
            let leaves = cll::mmr::leaf_count(size as u64).map_err(|_| {
                ShelfError::Corrupt(format!(
                    "a tick carries {id} at size {size}, not a complete MMR"
                ))
            })?;
            extents.insert(id, leaves);
        }
        Ok(StandardHistoryAnchor {
            anchor: hex::encode(Sha256::digest(&tick.checkpoint)),
            anchor_time: profile_time(&at.issued_at)?,
            first_checkpoint_time: profile_time(&first.issued_at)?,
            tick,
            extents,
        })
    }
}

/// The shelf's signer as an envelope signer.
struct EnvelopeSigner<'a>(&'a dyn crate::substrate::Signer);

impl evidence_request::envelope::Signer for EnvelopeSigner<'_> {
    fn key_id(&self) -> String {
        self.0.key_id()
    }

    fn sign(&self, message: &[u8]) -> Result<[u8; 64], String> {
        self.0.sign(message).map_err(|e| e.to_string())
    }
}

impl Cadence {
    /// The profile's `history_card/1` over the cadence log, as a staple (Go
    /// `Cadence.HistoryCard`): the cadence checkpoint of every finished tick
    /// from the first through tick `n` (the newest finished tick when `n` is
    /// 0), each with the consistency proof from the one before, as of tick
    /// `n`'s checkpoint and signed by the shelf's signer. `check_staple`
    /// accepts it for the signer's key id. `NotFound` when the cadence has no
    /// such finished tick.
    pub fn history_card(&self, n: u64) -> Result<Staple, ShelfError> {
        let state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let n = if n == 0 {
            let newest: i64 = self.shelf.shared().db().query_row(
                "SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0",
                [],
                |r| r.get(0),
            )?;
            if newest == 0 {
                return Err(ShelfError::NotFound(
                    "the cadence has no finished tick".into(),
                ));
            }
            newest as u64
        } else {
            n
        };
        let mut chain = Vec::new();
        for i in 1..=n {
            let tick = self.tick_locked(i)?;
            if tick.checkpoint_size == 0 {
                if i == n {
                    return Err(ShelfError::NotFound(format!("tick {n} is not finished")));
                }
                continue;
            }
            chain.push(Checkpoint::from_statement(&tick.checkpoint)?);
        }
        let anchor = chain.last().expect("n >= 1 and tick n is finished");
        let anchor_digest = hex::encode(Sha256::digest(&anchor.statement));
        let mut history = Vec::with_capacity(chain.len());
        for (i, cp) in chain.iter().enumerate() {
            let consistency = match i.checked_sub(1).map(|p| &chain[p]) {
                Some(prev) if prev.tree_size < cp.tree_size => {
                    let (old_peaks, paths, new_peaks) =
                        self.log.consistency_parts(prev.tree_size, cp.tree_size)?;
                    let proof = encode_consistency_proof(
                        prev.tree_size,
                        cp.tree_size,
                        &old_peaks,
                        &paths,
                        &new_peaks,
                    )
                    .ok_or_else(|| {
                        ShelfError::Corrupt(format!(
                            "the cadence log's consistency proof from {} to {} does not reach its own peaks",
                            prev.tree_size, cp.tree_size
                        ))
                    })?;
                    json!(hex::encode(proof))
                }
                _ => Value::Null,
            };
            history.push(json!({
                "checkpoint": hex::encode(&cp.statement),
                "consistency_from_previous": consistency,
            }));
        }
        let invalid = |e: &dyn std::fmt::Display| ShelfError::Invalid(e.to_string());
        let artifact = crate::canonical::jcs(&json!({
            "anchor": anchor_digest,
            "derivation": DERIVATION_HISTORY_CARD,
            "evidence_stream": anchor.log_id,
            "history": history,
            "subject": {"checkpoints": null},
        }))
        .map_err(|e| invalid(&e))?;
        let mut nonce = [0u8; 16];
        OsRng.fill_bytes(&mut nonce);
        let request = crate::canonical::jcs(&json!({
            "coverage": {"expected_pin": anchor_digest},
            "derivation": DERIVATION_HISTORY_CARD,
            "nonce": hex::encode(nonce),
            "profile": PROFILE,
            "subject": {"checkpoints": null},
        }))
        .map_err(|e| invalid(&e))?;
        let shared = self.shelf.shared();
        let issued_at = shared.now().truncate(Duration::from_secs(1)).rfc3339();
        let envelope = sign_with_profile(
            PROFILE,
            &request,
            EnvelopeParts {
                subject: Subject::Checkpoints,
                derivation: Some(Derivation::Token(DERIVATION_HISTORY_CARD.into())),
                anchor: Anchor {
                    digest: anchor_digest,
                    freshness: None,
                },
                artifact,
                verification: Vec::new(),
                issued_at,
            },
            &EnvelopeSigner(shared.signer.as_ref()),
        )
        .map_err(|e| invalid(&e))?;
        Ok(Staple {
            request,
            reply: envelope.encode(Binding::Json),
        })
    }
}
