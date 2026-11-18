//! Record time (Go `shelf_record_time.go`; evidencebook#461,
//! `docs/design/461-shelf-facts.md` §2.3 and §2.6): every receipt that
//! verifies for a shelf checkpoint is kept in `shelf_receipts`, one per
//! witness and key it verified under (evidencebook#492), with the
//! registration time the witness put in it (the `iat` of its protected CWT
//! claims) or none. A witness is named by its receipt's URL
//! (`ts_url`), the name a witness directory row is found by, so a witness an
//! agreement names is that URL. Each row also keeps the directory key id
//! the receipt verified under: a URL is a locator, and the key id lets a
//! later naming by key read the rows already held.

use super::cadence::{
    mmr_size, verify_witness_receipt, VerifiedReceipt, WitnessReceipt, WITNESS_VERIFIED,
};
use super::store;
use super::time::Time;
use super::{Shelf, ShelfError};
use crate::shelf_facts::{FACT_KIND_BOOK, FACT_KIND_JOURNAL};
use rusqlite::{params, Connection};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::HashSet;

/// Keep a verified receipt for the checkpoint whose SHA-256 is the first
/// parameter (Go `upsertReceipt`). Another receipt from the same witness
/// replaces the held one only when it gives an earlier registration time, or
/// the held one gives none: a witness's earliest registration is the one a
/// record time reads.
const UPSERT_RECEIPT: &str = "INSERT INTO shelf_receipts (checkpoint_sha256, witness_id, key_id, receipt, registered_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (checkpoint_sha256, witness_id, key_id) DO UPDATE SET receipt = excluded.receipt, registered_at = excluded.registered_at
WHERE excluded.registered_at IS NOT NULL AND (shelf_receipts.registered_at IS NULL OR excluded.registered_at < shelf_receipts.registered_at)";

/// Store `receipt`, which verified for `statement`; `encoded` is its stored
/// text, as `cadence_ticks.receipt` holds it (Go `keepReceipt`).
pub(crate) fn keep_receipt(
    conn: &Connection,
    statement: &[u8],
    receipt: &WitnessReceipt,
    encoded: &str,
    verified: &VerifiedReceipt,
) -> Result<(), ShelfError> {
    conn.execute(
        UPSERT_RECEIPT,
        params![
            hex::encode(Sha256::digest(statement)),
            receipt.ts_url,
            verified.key_id,
            encoded,
            verified.registered_at
        ],
    )?;
    Ok(())
}

/// Keep, in `shelf_receipts`, the receipt of every witnessed tick that has
/// none there (Go `backfillReceiptsLocked`): a tick witnessed before the
/// table existed (a shelf written before #461 part 5) or imported
/// ([`super::Cadence::import_legacy`]). A receipt that no longer verifies
/// under `directory` is left out and tried again at the next open.
pub(crate) fn backfill_receipts(
    db: &mut Connection,
    directory: Option<&Value>,
) -> Result<(), ShelfError> {
    let ticks: Vec<(i64, Vec<u8>, String)> = {
        let mut stmt = db.prepare(
            "SELECT tick, checkpoint, receipt FROM cadence_ticks WHERE witnessed = 1 AND receipt IS NOT NULL ORDER BY tick",
        )?;
        let rows = stmt.query_map([], |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)))?;
        rows.collect::<Result<_, _>>()?
    };
    if ticks.is_empty() {
        return Ok(());
    }
    let tx = store::write_tx(db)?;
    for (n, statement, raw) in &ticks {
        let receipt: WitnessReceipt = serde_json::from_str(raw)
            .map_err(|e| ShelfError::Corrupt(format!("tick {n} receipt: {e}")))?;
        // The same receipt already held needs no second verification; a held
        // receipt of another key of the same witness does not cover this one.
        let present: i64 = tx.query_row(
            "SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ? AND witness_id = ? AND receipt = ?",
            params![hex::encode(Sha256::digest(statement)), receipt.ts_url, raw],
            |r| r.get(0),
        )?;
        if present > 0 {
            continue;
        }
        match verify_witness_receipt(statement, &receipt, directory) {
            Ok((check, verified)) if check.state == WITNESS_VERIFIED => {
                keep_receipt(&tx, statement, &receipt, raw, &verified)?;
            }
            _ => continue,
        }
    }
    tx.commit()?;
    Ok(())
}

/// How a record time names its witnesses: the `shelf_receipts` column it
/// matches (Go `byURL` and `byKey`).
#[derive(Clone, Copy)]
enum NamedBy {
    /// The receipt's URL (`witness_id`).
    Url,
    /// The directory key id the receipt verified under (`key_id`).
    Key,
}

impl NamedBy {
    /// The statement reading the receipts; the column is one of two
    /// constants, never input.
    fn receipts(self) -> &'static str {
        match self {
            NamedBy::Url => "SELECT checkpoint_sha256, witness_id, registered_at FROM shelf_receipts WHERE registered_at IS NOT NULL",
            NamedBy::Key => "SELECT checkpoint_sha256, key_id, registered_at FROM shelf_receipts WHERE registered_at IS NOT NULL",
        }
    }
}

fn position_from_one(position: u64) -> Result<(), ShelfError> {
    if position == 0 {
        return Err(ShelfError::Invalid(
            "a record position counts from 1".into(),
        ));
    }
    Ok(())
}

fn received_log_named(received_log_id: &str) -> Result<(), ShelfError> {
    if received_log_id.is_empty() {
        return Err(ShelfError::Invalid(
            "a received record names its log".into(),
        ));
    }
    Ok(())
}

impl Shelf {
    /// The record time of the record at `position` (from 1) of the book
    /// `book_id` (its book id, §2.2; the journal's included): the earliest
    /// registration time, among the receipts held from the `witnesses` named
    /// (their URLs), of a shelf checkpoint that covers the first fact of that
    /// book whose size covers the position (§2.6; Go `RecordTime`). `None`
    /// when there is none: no fact covers the position yet, no checkpoint
    /// covering that fact has a receipt from a named witness, or every such
    /// receipt gives no registration time. A status resting on a record with
    /// no time is local, never witnessed. Position 0 is
    /// [`ShelfError::Invalid`].
    ///
    /// It takes no shelf lock, so a [`super::BookStatus`] or a
    /// [`super::ReceiveProfile`] may call it, and reads in one transaction,
    /// so a stamp being finished is seen whole or not at all.
    pub fn record_time(
        &self,
        book_id: &str,
        position: u64,
        witnesses: &[String],
    ) -> Result<Option<Time>, ShelfError> {
        self.record_time_named(book_id, position, witnesses, NamedBy::Url)
    }

    /// [`Shelf::record_time`] with the witnesses named by their verified key
    /// ids (lowercase hex), as an x-deal-v1 agreement names them (Go
    /// `RecordTimeByKey`; Steven, #482 Q1: a witness is its key, its URL
    /// only a hint, so a witness that moved stays the same witness).
    pub fn record_time_by_key(
        &self,
        book_id: &str,
        position: u64,
        key_ids: &[String],
    ) -> Result<Option<Time>, ShelfError> {
        self.record_time_named(book_id, position, key_ids, NamedBy::Key)
    }

    fn record_time_named(
        &self,
        book_id: &str,
        position: u64,
        witnesses: &[String],
        by: NamedBy,
    ) -> Result<Option<Time>, ShelfError> {
        self.live()?;
        position_from_one(position)?;
        if witnesses.is_empty() {
            return Ok(None);
        }
        let db = self.shared().db();
        let tx = db.unchecked_transaction()?;
        let Some(seq) = first_covering_fact(&tx, book_id, position)? else {
            return Ok(None);
        };
        earliest_named_time(&tx, seq, witnesses, by)
    }

    /// The record time the holder's own stamps give a record it received
    /// (`docs/design/461-deal-profile.md` §4.3; Go `ReceivedRecordTime`): the
    /// record at `position` (from 1) of the other side's log
    /// `received_log_id`, as the holder's book `book_id` names it. Its time
    /// is the earliest registration time, among the receipts held from the
    /// `witnesses` named (their URLs), of a shelf checkpoint that covers the
    /// first fact of `book_id` whose link value's `last_received` names
    /// `received_log_id` at a size (a record count) that reaches the
    /// position. `None` when there is none, as for [`Shelf::record_time`].
    /// The other side's own stamps may give an earlier time; the profile
    /// takes the earlier of the two. An empty `received_log_id` is
    /// [`ShelfError::Invalid`].
    ///
    /// It takes no shelf lock and reads in one transaction, as
    /// [`Shelf::record_time`] does.
    pub fn received_record_time(
        &self,
        book_id: &str,
        received_log_id: &str,
        position: u64,
        witnesses: &[String],
    ) -> Result<Option<Time>, ShelfError> {
        self.received_record_time_named(book_id, received_log_id, position, witnesses, NamedBy::Url)
    }

    /// [`Shelf::received_record_time`] with the witnesses named by their
    /// verified key ids, as [`Shelf::record_time_by_key`] (Go
    /// `ReceivedRecordTimeByKey`).
    pub fn received_record_time_by_key(
        &self,
        book_id: &str,
        received_log_id: &str,
        position: u64,
        key_ids: &[String],
    ) -> Result<Option<Time>, ShelfError> {
        self.received_record_time_named(book_id, received_log_id, position, key_ids, NamedBy::Key)
    }

    fn received_record_time_named(
        &self,
        book_id: &str,
        received_log_id: &str,
        position: u64,
        witnesses: &[String],
        by: NamedBy,
    ) -> Result<Option<Time>, ShelfError> {
        self.live()?;
        position_from_one(position)?;
        received_log_named(received_log_id)?;
        if witnesses.is_empty() {
            return Ok(None);
        }
        let db = self.shared().db();
        let tx = db.unchecked_transaction()?;
        let Some(seq) = first_linking_fact(&tx, book_id, received_log_id, position)? else {
            return Ok(None);
        };
        earliest_named_time(&tx, seq, witnesses, by)
    }

    /// The stamp (its tick, from 1) whose fact is the first to cover the
    /// record at `position` (from 1) of the book `book_id`, as
    /// [`Shelf::record_time`] finds that fact: a record's time can come from
    /// no earlier stamp (Go `RecordStamp`). `None` when no fact covers the
    /// position yet, so the next stamp will be the first
    /// (`docs/design/461-deal-profile.md` §8.1, "Times"). It takes no shelf
    /// lock, as [`Shelf::record_time`].
    pub fn record_stamp(&self, book_id: &str, position: u64) -> Result<Option<u64>, ShelfError> {
        self.record_stamp_of(position, |tx| first_covering_fact(tx, book_id, position))
    }

    /// [`Shelf::record_stamp`] for a record the holder received: the stamp
    /// whose fact of `book_id` is the first whose link value's
    /// `last_received` names `received_log_id` at a size that reaches the
    /// position, as [`Shelf::received_record_time`] finds it (Go
    /// `ReceivedRecordStamp`).
    pub fn received_record_stamp(
        &self,
        book_id: &str,
        received_log_id: &str,
        position: u64,
    ) -> Result<Option<u64>, ShelfError> {
        received_log_named(received_log_id)?;
        self.record_stamp_of(position, |tx| {
            first_linking_fact(tx, book_id, received_log_id, position)
        })
    }

    fn record_stamp_of(
        &self,
        position: u64,
        first: impl FnOnce(&Connection) -> Result<Option<u64>, ShelfError>,
    ) -> Result<Option<u64>, ShelfError> {
        self.live()?;
        position_from_one(position)?;
        let db = self.shared().db();
        let tx = db.unchecked_transaction()?;
        let Some(seq) = first(&tx)? else {
            return Ok(None);
        };
        let tick: i64 = tx.query_row(
            "SELECT tick FROM shelf_entries WHERE entry_seq = ?",
            [seq as i64],
            |r| r.get(0),
        )?;
        Ok(Some(tick as u64))
    }
}

/// The earliest registration time, among the receipts from the `witnesses`
/// named (by `by`: their URL or their key id), of a shelf checkpoint whose
/// tree holds the entry at `seq` (Go `earliestNamedTime`).
fn earliest_named_time(
    conn: &Connection,
    seq: u64,
    witnesses: &[String],
    by: NamedBy,
) -> Result<Option<Time>, ShelfError> {
    // A shelf checkpoint covers the fact's entry when its tree holds that
    // entry: its MMR size is at least the size of `seq` leaves.
    let covering: HashSet<String> = {
        let mut stmt = conn.prepare(
            "SELECT checkpoint FROM cadence_ticks WHERE checkpoint_size >= ? ORDER BY tick",
        )?;
        let rows = stmt.query_map([mmr_size(seq) as i64], |r| r.get::<_, Vec<u8>>(0))?;
        rows.map(|r| r.map(|statement| hex::encode(Sha256::digest(statement))))
            .collect::<Result<_, _>>()?
    };
    let named: HashSet<&str> = witnesses.iter().map(String::as_str).collect();
    let mut earliest: Option<i64> = None;
    let mut stmt = conn.prepare(by.receipts())?;
    let rows = stmt.query_map([], |r| {
        Ok((
            r.get::<_, String>(0)?,
            r.get::<_, String>(1)?,
            r.get::<_, i64>(2)?,
        ))
    })?;
    for row in rows {
        let (cp, witness, at) = row?;
        if covering.contains(&cp)
            && named.contains(witness.as_str())
            && earliest.is_none_or(|e| at < e)
        {
            earliest = Some(at);
        }
    }
    Ok(earliest.map(Time::from_unix_seconds))
}

/// The entry position of the first book fact of `book_id` whose link value's
/// `last_received` names `log_id` at a size that reaches `position` (Go
/// `firstLinkingFact`).
fn first_linking_fact(
    conn: &Connection,
    book_id: &str,
    log_id: &str,
    position: u64,
) -> Result<Option<u64>, ShelfError> {
    let mut stmt = conn.prepare(
        "SELECT entry_seq, link_opening FROM shelf_entries WHERE book_id = ? AND kind = ? ORDER BY entry_seq",
    )?;
    let mut rows = stmt.query(params![book_id, FACT_KIND_BOOK])?;
    while let Some(r) = rows.next()? {
        let seq: i64 = r.get(0)?;
        let Some(lo) = r.get::<_, Option<Vec<u8>>>(1)? else {
            continue;
        };
        let corrupt = || ShelfError::Corrupt(format!("stored fact {seq}'s link opening"));
        let opening: Value = serde_json::from_slice(&lo).map_err(|_| corrupt())?;
        // Go reads the opening's value into a LinkValue: an absent value is
        // no JSON and refused; a null one, or a null last_received, is a
        // fact that names no received record.
        let value = opening
            .as_object()
            .ok_or_else(corrupt)?
            .get("value")
            .ok_or_else(corrupt)?;
        let last = match value {
            Value::Null => continue,
            Value::Object(link) => match link.get("last_received") {
                None | Some(Value::Null) => continue,
                Some(Value::Object(last)) => last,
                Some(_) => return Err(corrupt()),
            },
            _ => return Err(corrupt()),
        };
        let named = match last.get("log_id") {
            None | Some(Value::Null) => "",
            Some(Value::String(s)) => s.as_str(),
            Some(_) => return Err(corrupt()),
        };
        let size = match last.get("size") {
            None | Some(Value::Null) => 0,
            Some(v) => v.as_u64().ok_or_else(corrupt)?,
        };
        if named == log_id && size >= position {
            return Ok(Some(seq as u64));
        }
    }
    Ok(None)
}

/// The entry position of the first fact of `book_id` whose size covers
/// `position`; a journal fact's size is its status opening's (Go
/// `firstCoveringFact`).
fn first_covering_fact(
    conn: &Connection,
    book_id: &str,
    position: u64,
) -> Result<Option<u64>, ShelfError> {
    let mut stmt = conn.prepare(
        "SELECT entry_seq, kind, doc, status_opening FROM shelf_entries WHERE book_id = ? AND kind IN (?, ?) ORDER BY entry_seq",
    )?;
    let mut rows = stmt.query(params![book_id, FACT_KIND_BOOK, FACT_KIND_JOURNAL])?;
    while let Some(r) = rows.next()? {
        let seq: i64 = r.get(0)?;
        let kind: String = r.get(1)?;
        let doc: Vec<u8> = r.get(2)?;
        let so: Option<Vec<u8>> = r.get(3)?;
        let corrupt = |what: &str| ShelfError::Corrupt(format!("{what} {seq}"));
        let fact: Value = serde_json::from_slice(&doc).map_err(|_| corrupt("stored fact"))?;
        let mut size = size_of(&fact).ok_or_else(|| corrupt("stored fact"))?;
        if kind == FACT_KIND_JOURNAL {
            let opening: Value = serde_json::from_slice(so.as_deref().unwrap_or_default())
                .map_err(|_| corrupt("stored journal fact"))?;
            size = opening
                .get("value")
                .and_then(size_of)
                .ok_or_else(|| corrupt("stored journal fact"))?;
        }
        if cll::mmr::leaf_count(size).is_ok_and(|leaves| leaves >= position) {
            return Ok(Some(seq as u64));
        }
    }
    Ok(None)
}

/// The `size` member of a fact or journal status object, 0 when it has none
/// or when it is JSON null (as Go decodes one into its struct); `None` when
/// it is not an object or the size is not an unsigned integer.
fn size_of(v: &Value) -> Option<u64> {
    if v.is_null() {
        return Some(0);
    }
    match v.as_object()?.get("size") {
        None | Some(Value::Null) => Some(0),
        Some(size) => size.as_u64(),
    }
}
