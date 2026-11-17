// SPDX-License-Identifier: Apache-2.0

//! Record times from a handover (evidencebook#461,
//! `docs/design/461-deal-profile.md` §8.1, invariant C; Go
//! `shelf_handover_times.go`): a reader handed the whole shelf resolves the
//! record time of a book's position as the holder's
//! [`super::Shelf::record_time`] does, from the anchor and the earlier
//! witnessed checkpoints the handover carries with their receipts. A
//! checkpoint after the anchor is not handed over, so a time only a later
//! checkpoint gives is not resolved. The journal's sizes are never handed
//! over, so its records get no time here.

use super::cadence::{verify_witness_receipt, WITNESS_VERIFIED};
use super::handover::{verify_handover, Handover, HandoverCheckpoint};
use super::log::Checkpoint;
use super::time::Time;
use super::ShelfError;
use crate::shelf_facts::{BookFact, FACT_KIND_BOOK};
use serde_json::Value;
use std::collections::HashMap;

/// Record times from a verified handover (Go `HandoverTimes`).
#[derive(Clone, Debug, Default)]
pub struct HandoverTimes {
    /// The book facts after the format switch, in entry order: entry
    /// position, book id, size.
    facts: Vec<(u64, String, u64)>,
    /// The anchor and the earlier witnessed checkpoints: each one's entries
    /// and the earliest registration time of each witness whose receipt
    /// verifies under the reader's directory.
    checkpoints: Vec<(u64, HashMap<String, i64>)>,
}

/// Replay `h` with [`verify_handover`], then read the registration times its
/// receipts give under the reader's directory (Go `VerifyHandoverTimes`): a
/// receipt that does not verify, or gives no registration time, gives none.
pub fn verify_handover_times(
    h: &Handover,
    pinned_key: &str,
    witnesses: Option<&Value>,
) -> Result<HandoverTimes, ShelfError> {
    verify_handover(h, pinned_key, witnesses)?;
    let mut out = HandoverTimes::default();
    for d in &h.documents {
        if d.status_opening.is_none() {
            continue;
        }
        let f: BookFact = serde_json::from_slice(&d.doc).map_err(|e| {
            ShelfError::Invalid(format!("handover: the document at {}: {e}", d.seq))
        })?;
        if f.kind == FACT_KIND_BOOK {
            out.facts.push((d.seq, f.book_id, f.size));
        }
    }
    out.facts.sort_by_key(|f| f.0);
    let anchor = HandoverCheckpoint {
        checkpoint: h.anchor.clone(),
        receipts: h.receipts.clone(),
    };
    for w in std::iter::once(&anchor).chain(h.witnessed.iter()) {
        let cp = Checkpoint::from_statement(&w.checkpoint)?;
        let mut at: HashMap<String, i64> = HashMap::new();
        for r in &w.receipts {
            let Ok((check, verified)) = verify_witness_receipt(&w.checkpoint, r, witnesses) else {
                continue;
            };
            let Some(t) = verified
                .registered_at
                .filter(|_| check.state == WITNESS_VERIFIED)
            else {
                continue;
            };
            at.entry(r.ts_url.clone())
                .and_modify(|held| *held = (*held).min(t))
                .or_insert(t);
        }
        out.checkpoints.push((cp.entries, at));
    }
    Ok(out)
}

impl HandoverTimes {
    /// The record time of the record at `position` (from 1) of the book
    /// `book_id`, from the witnesses named (their URLs), as
    /// [`super::Shelf::record_time`] computes it (Go
    /// `HandoverTimes.RecordTime`). Position 0 is [`ShelfError::Invalid`].
    pub fn record_time(
        &self,
        book_id: &str,
        position: u64,
        witnesses: &[String],
    ) -> Result<Option<Time>, ShelfError> {
        if position == 0 {
            return Err(ShelfError::Invalid(
                "a record position counts from 1".into(),
            ));
        }
        let Some(seq) = self
            .facts
            .iter()
            .find(|(_, book, size)| {
                book == book_id && cll::mmr::leaf_count(*size).is_ok_and(|l| l >= position)
            })
            .map(|f| f.0)
        else {
            return Ok(None);
        };
        let earliest = self
            .checkpoints
            .iter()
            .filter(|(entries, _)| *entries >= seq)
            .flat_map(|(_, at)| witnesses.iter().filter_map(|w| at.get(w).copied()))
            .min();
        Ok(earliest.map(Time::from_unix_seconds))
    }
}
