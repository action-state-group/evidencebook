//! A cadence tick as the anchor of a shelf's `standard_history/1` card (Go
//! `cadence_standard_history.go`): the cadence checkpoint commits every
//! book's checkpoint and is the one checkpoint the witness sees, so a card
//! counting the deals of every book on the shelf is as of a tick.

use super::cadence::{Cadence, CadenceTick};
use super::log::Checkpoint;
use super::ShelfError;
use rusqlite::OptionalExtension;
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
