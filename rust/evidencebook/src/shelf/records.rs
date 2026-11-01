//! Listing a shelf's committed records without opening a book (Go
//! `shelf_records.go`), with each record's retention state (Go
//! `retention.go`, `retentionOf`).

use super::book::links_to;
use super::time::Time;
use super::{Shelf, ShelfError};
use crate::record::{record_types, LinkType};
use evidence_request::request::{subject_from_value, Subject};
use serde_json::{Map, Value};
use std::collections::{BTreeMap, HashMap};

/// The record type of a retention commitment a book has issued (Go
/// `RecordTypeRetentionCommitment`).
const RETENTION_COMMITMENT: &str = "retention_commitment";

/// A committed record of one of a shelf's books, as [`Shelf::records`]
/// lists it.
#[derive(Clone, Debug, PartialEq)]
pub struct ShelfRecord {
    pub book_id: String,
    pub seq: u64,
    pub record_id: String,
    /// The header's members, as stored.
    pub header: Map<String, Value>,
    /// The record's retention state now (`AVAILABLE`, `LEGAL_HOLD`, ...);
    /// empty for a record that commits no payload or field, which has none.
    pub retention: String,
}

/// What [`Shelf::records`] reads: the committed records, and how many
/// records each book holds beyond them.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ShelfRecords {
    pub records: Vec<ShelfRecord>,
    /// By book, the records stored above the book's committed entry count:
    /// a crash after the record's row and before its log append, which the
    /// book's next open recovers. They are not listed.
    pub pending: BTreeMap<String, u64>,
}

struct Stored {
    book: String,
    seq: u64,
    record_id: String,
    header: Map<String, Value>,
}

impl Shelf {
    /// The committed records of the shelf's books, in book id then seq
    /// order, without opening a book: nothing is recovered, verified or
    /// written (Go `Records`). A record is committed when `1 <= seq <=` its
    /// book's log entry count. `book_ids` narrows the listing to those
    /// books; none lists every book. The headers are as stored.
    pub fn records(&self, book_ids: &[&str]) -> Result<ShelfRecords, ShelfError> {
        let state = self.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let shared = self.shared();
        let db = shared.db();
        let mut query = String::from("SELECT book_id, seq, record_id, header FROM book_records");
        if !book_ids.is_empty() {
            query.push_str(" WHERE book_id IN (?");
            query.push_str(&", ?".repeat(book_ids.len() - 1));
            query.push(')');
        }
        query.push_str(" ORDER BY book_id, seq");
        let mut stmt = db.prepare(&query)?;
        let rows = stmt.query_map(rusqlite::params_from_iter(book_ids.iter()), |r| {
            Ok((
                r.get::<_, String>(0)?,
                r.get::<_, i64>(1)? as u64,
                r.get::<_, String>(2)?,
                r.get::<_, Vec<u8>>(3)?,
            ))
        })?;
        let mut all = Vec::new();
        for row in rows {
            let (book, seq, record_id, header) = row?;
            let header = match serde_json::from_slice(&header) {
                Ok(Value::Object(m)) => m,
                _ => {
                    return Err(ShelfError::Corrupt(format!(
                        "header of {record_id} in {book}"
                    )))
                }
            };
            all.push(Stored {
                book,
                seq,
                record_id,
                header,
            });
        }
        drop(stmt);
        let mut entries: HashMap<String, u64> = HashMap::new();
        let mut counts = db.prepare("SELECT log_id, COUNT(*) FROM cll_entries GROUP BY log_id")?;
        for row in counts.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, i64>(1)?)))? {
            let (id, n) = row?;
            entries.insert(id, n as u64);
        }
        drop(counts);
        drop(db);
        drop(state);

        let now = shared.now();
        let mut out = ShelfRecords::default();
        let mut i = 0;
        while i < all.len() {
            let book = all[i].book.clone();
            let committed = entries.get(&book).copied().unwrap_or(0);
            let mut listed: Vec<&Stored> = Vec::new();
            while i < all.len() && all[i].book == book {
                let record = &all[i];
                i += 1;
                if record.seq < 1 || record.seq > committed {
                    *out.pending.entry(book.clone()).or_default() += 1;
                    continue;
                }
                if record.seq != listed.len() as u64 + 1 {
                    return Err(ShelfError::Corrupt(format!(
                        "{book} holds record {} at seq {} after {} records",
                        record.record_id,
                        record.seq,
                        listed.len()
                    )));
                }
                listed.push(record);
            }
            for (at, record) in listed.iter().enumerate() {
                let retention = if commits_originals(&record.header) {
                    retention_of(&listed, at, now)?
                } else {
                    String::new()
                };
                out.records.push(ShelfRecord {
                    book_id: book.clone(),
                    seq: record.seq,
                    record_id: record.record_id.clone(),
                    header: record.header.clone(),
                    retention,
                });
            }
        }
        Ok(out)
    }
}

fn non_empty_array(header: &Map<String, Value>, member: &str) -> bool {
    header
        .get(member)
        .and_then(Value::as_array)
        .is_some_and(|a| !a.is_empty())
}

fn commits_originals(header: &Map<String, Value>) -> bool {
    non_empty_array(header, "payload_commitments") || non_empty_array(header, "field_commitments")
}

fn record_type(header: &Map<String, Value>) -> &str {
    header
        .get("record_type")
        .and_then(Value::as_str)
        .unwrap_or("")
}

/// The retention state of `records[at]` at `now` (Go `retentionOf`): the
/// state its header committed, folded forward through every later
/// lifecycle record that cites it; an `AVAILABLE` or `PARTIAL` record a
/// live hold covers reads `LEGAL_HOLD`.
fn retention_of(records: &[&Stored], at: usize, now: Time) -> Result<String, ShelfError> {
    let record = records[at];
    let mut state = record
        .header
        .get("retention_state")
        .and_then(Value::as_str)
        .unwrap_or("")
        .to_string();
    for later in &records[at + 1..] {
        if record_type(&later.header) != record_types::LIFECYCLE
            || !links_to(&later.header, LinkType::Cites).contains(&record.record_id.as_str())
        {
            continue;
        }
        state = later
            .header
            .get("statement")
            .and_then(|s| s.get("retention_state"))
            .and_then(Value::as_str)
            .ok_or_else(|| ShelfError::Corrupt(format!("lifecycle record {}", later.record_id)))?
            .to_string();
    }
    if state == "DELETED" {
        return Ok(state);
    }
    let held = state == "LEGAL_HOLD" || held_by(records, record, now);
    if held && (state == "AVAILABLE" || state == "PARTIAL") {
        state = "LEGAL_HOLD".into();
    }
    Ok(state)
}

/// Whether an unexpired retention commitment covers `record` (Go `heldBy`):
/// one naming it by id, a range of positions holding it, or an exchange it
/// cites.
fn held_by(records: &[&Stored], record: &Stored, now: Time) -> bool {
    records.iter().any(|r| {
        if record_type(&r.header) != RETENTION_COMMITMENT {
            return false;
        }
        let Some(statement) = r.header.get("statement") else {
            return false;
        };
        let until = statement
            .get("until")
            .and_then(Value::as_str)
            .and_then(Time::parse);
        let subject = statement.get("subject").map(subject_from_value);
        let (Some(until), Some(Ok(subject))) = (until, subject) else {
            return false;
        };
        if now > until {
            return false;
        }
        match subject {
            Subject::Record(digest) => digest == record.record_id,
            // A range names leaf indexes, from 0.
            Subject::Range(a, b) => (a..=b).contains(&(record.seq - 1)),
            Subject::Exchange(digest) => {
                links_to(&record.header, LinkType::Cites).contains(&digest.as_str())
            }
            _ => false,
        }
    })
}
