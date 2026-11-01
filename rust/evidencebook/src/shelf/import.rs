//! Carrying a pre-shelf cadence into a shelf's (Go `cadence_import.go`).

use super::cadence::{
    check_witness_receipt, held_size, Cadence, CadenceTick, INDEX_WITNESSED, WITNESS_VERIFIED,
};
use super::store::{self, KIND_HOST};
use super::tree::TickLeaves;
use super::ShelfError;
use rusqlite::{params, OptionalExtension, Transaction};
use std::collections::{BTreeMap, BTreeSet};

/// A profile's cadence as a pre-shelf writer kept it (Go `LegacyCadence`):
/// capsule-cli's `deal_cadence` rows, on the same cadence log, and the logs
/// its deal index holds.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct LegacyCadence {
    /// The profile's pre-shelf logs, each a log of the shelf's file that is
    /// not a book. Each counts into the lifetime log count.
    pub logs: Vec<String>,
    /// The pre-shelf writer's finished ticks, in cadence-log order. `n` is
    /// ignored: a tick's identity is its place in the cadence log
    /// (`entry_seq`) and its checkpoint. A tick the writer recorded as
    /// witnessed carries its receipt, which is verified. Every log a tick
    /// carries is one of `logs`.
    pub ticks: Vec<CadenceTick>,
}

impl Cadence {
    /// Carry a pre-shelf cadence into this one, in one transaction, under
    /// the shelf's lock (Go `ImportLegacy`): every legacy log is registered
    /// as a host log in the cadence; each tick not already held is stored,
    /// numbered after the cadence's last; each witnessed tick it stores or
    /// promotes has its receipt verified (an import with a receipt that is
    /// not `VERIFIED` imports nothing); each witnessed tick's leaves are
    /// indexed; and each registered log is live unless it has ended and has
    /// not grown past its largest witnessed checkpoint.
    ///
    /// It is re-runnable: a tick already held (the same cadence-log position
    /// and checkpoint) keeps its number and content, and is only promoted to
    /// witnessed when it now carries a receipt it did not. A tick at a held
    /// position with another checkpoint, or older than the newest tick held,
    /// is refused, and so is any import while the cadence's last tick is
    /// unfinished (tick first, which finishes it).
    pub fn import_legacy(&self, legacy: &LegacyCadence) -> Result<(), ShelfError> {
        let invalid = |why: String| Err(ShelfError::Invalid(why));
        let mut logs = BTreeSet::new();
        for id in &legacy.logs {
            if id.is_empty() || *id == self.cfg.log_id {
                return invalid(format!("legacy log {id:?}"));
            }
            logs.insert(id.clone());
        }
        let mut ticks = legacy.ticks.clone();
        let mut receipts: Vec<Option<String>> = vec![None; ticks.len()];
        for i in 0..ticks.len() {
            let tick = &mut ticks[i];
            if tick.checkpoint_size == 0 || tick.entry_seq == 0 || tick.checkpoint.is_empty() {
                return invalid(format!(
                    "legacy tick at entry {} is not finished",
                    tick.entry_seq
                ));
            }
            if i > 0 && tick.entry_seq <= legacy.ticks[i - 1].entry_seq {
                return invalid(format!(
                    "legacy ticks are not in cadence-log order (entry {} after {})",
                    tick.entry_seq,
                    legacy.ticks[i - 1].entry_seq
                ));
            }
            if let Some(id) = tick.leaves.deals.keys().find(|id| !logs.contains(*id)) {
                return invalid(format!(
                    "legacy tick at entry {} carries {id}, which is not a legacy log",
                    tick.entry_seq
                ));
            }
            if tick.witnessed {
                let Some(receipt) = tick.receipt.as_mut() else {
                    return invalid(format!(
                        "witnessed legacy tick at entry {} has no receipt",
                        tick.entry_seq
                    ));
                };
                while receipt.ts_url.ends_with('/') {
                    receipt.ts_url.pop();
                }
                if receipt.ts_url.is_empty()
                    || receipt.entry_hash.is_empty()
                    || receipt.receipt.is_empty()
                {
                    return invalid(format!(
                        "witnessed legacy tick at entry {} needs its receipt, its entry hash and its witness's URL",
                        tick.entry_seq
                    ));
                }
                receipts[i] = Some(receipt.to_json());
            }
        }

        let state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        {
            let db = self.shelf.shared().db();
            for id in &logs {
                if store::record_count(&db, id)? > 0 || state.books.contains_key(id) {
                    return invalid(format!("{id} is a book, not a legacy log"));
                }
                if state.hosts.get(id).is_some_and(|h| !h.in_cadence) {
                    return invalid(format!("{id} is a host log outside the cadence"));
                }
                let known: i64 = db.query_row(
                    "SELECT COUNT(*) FROM cadence_logs WHERE log_id = ?",
                    [id],
                    |r| r.get(0),
                )?;
                if store::log_kind(&db, id)?.as_deref() == Some(KIND_HOST) && known == 0 {
                    return invalid(format!("{id} is a host log outside the cadence"));
                }
            }
        }
        let mut ended = BTreeMap::new();
        for id in &logs {
            ended.insert(
                id.clone(),
                (self.cfg.ended)(id).map_err(ShelfError::Invalid)?,
            );
        }
        let mut db = self.shelf.shared().db();
        let tx = db.transaction()?;
        let (mut last, mut last_entry): (i64, i64) = tx.query_row(
            "SELECT COALESCE(MAX(tick), 0), COALESCE(MAX(entry_seq), 0) FROM cadence_ticks",
            [],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )?;
        let unfinished: i64 = tx.query_row(
            "SELECT COUNT(*) FROM cadence_ticks WHERE checkpoint_size = 0",
            [],
            |r| r.get(0),
        )?;
        if unfinished > 0 {
            return invalid(
                "the cadence's last tick is unfinished; tick first, which finishes it, then import"
                    .into(),
            );
        }
        for id in &logs {
            // A legacy log is a host log (shelf_logs), so open refuses it.
            store::claim_kind(&tx, id, KIND_HOST)?;
            store::mark_live(&tx, id)?;
        }
        // A tick the import stores or promotes has its receipt verified; a
        // held witnessed tick is never re-checked.
        let verify = |tick: &CadenceTick| -> Result<(), ShelfError> {
            let receipt = tick.receipt.as_ref().expect("checked above");
            let check =
                check_witness_receipt(&tick.checkpoint, receipt, self.cfg.witnesses.as_ref())
                    .map_err(|e| {
                        ShelfError::Invalid(format!(
                            "witnessed legacy tick at entry {}: {e}",
                            tick.entry_seq
                        ))
                    })?;
            if check.state != WITNESS_VERIFIED {
                return Err(ShelfError::Invalid(format!(
                    "witnessed legacy tick at entry {}: its receipt is {}: {}",
                    tick.entry_seq, check.state, check.reason
                )));
            }
            Ok(())
        };
        let witness =
            |tx: &Transaction<'_>, n: i64, leaves: &TickLeaves| -> Result<(), ShelfError> {
                for (id, leaf) in &leaves.deals {
                    tx.execute(
                        INDEX_WITNESSED,
                        params![id, leaf.checkpoint_sha256, leaf.size as i64, n],
                    )?;
                }
                Ok(())
            };
        for (i, tick) in ticks.iter().enumerate() {
            let held: Option<(i64, Vec<u8>, i64, String)> = tx
                .query_row(
                    "SELECT tick, checkpoint, witnessed, leaves FROM cadence_ticks WHERE entry_seq = ? AND checkpoint_size > 0",
                    [tick.entry_seq as i64],
                    |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?)),
                )
                .optional()?;
            if let Some((held_n, held_checkpoint, held_witnessed, held_leaves)) = held {
                if held_checkpoint != tick.checkpoint {
                    return invalid(format!(
                        "the cadence holds another tick at entry {}",
                        tick.entry_seq
                    ));
                }
                if tick.witnessed && held_witnessed == 0 {
                    // Delivered since it was imported: witnessed from now on.
                    // The held tick's own leaves are what is indexed.
                    verify(tick)?;
                    tx.execute(
                        "UPDATE cadence_ticks SET witnessed = 1, receipt = COALESCE(receipt, ?) WHERE tick = ?",
                        params![receipts[i], held_n],
                    )?;
                    let stored: TickLeaves = serde_json::from_str(&held_leaves)
                        .map_err(|e| ShelfError::Corrupt(format!("tick {held_n} leaves: {e}")))?;
                    witness(&tx, held_n, &stored)?;
                }
                continue;
            }
            if tick.entry_seq as i64 <= last_entry {
                return invalid(format!(
                    "legacy tick at entry {} is older than the cadence's newest tick (entry {last_entry})",
                    tick.entry_seq
                ));
            }
            last += 1;
            if tick.witnessed {
                verify(tick)?;
            }
            tx.execute(
                "INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed, receipt) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                params![
                    last,
                    tick.at.rfc3339(),
                    tick.due_next.rfc3339(),
                    tick.entry_seq as i64,
                    tick.checkpoint_size as i64,
                    tick.checkpoint,
                    tick.leaves.to_json(),
                    i64::from(tick.witnessed),
                    receipts[i]
                ],
            )?;
            last_entry = tick.entry_seq as i64;
            if tick.witnessed {
                witness(&tx, last, &tick.leaves)?;
            }
        }
        for id in &logs {
            if !ended[id] {
                continue;
            }
            let witnessed: i64 = tx.query_row(
                "SELECT COALESCE(MAX(mmr_size), 0) FROM cadence_witnessed WHERE log_id = ?",
                [id],
                |r| r.get(0),
            )?;
            if witnessed > 0 && held_size(&tx, id)? <= witnessed as u64 {
                tx.execute("DELETE FROM cadence_live WHERE log_id = ?", [id])?;
            }
        }
        tx.commit()?;
        Ok(())
    }
}
