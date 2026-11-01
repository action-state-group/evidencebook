//! The shelf's own tables and one book's record store over them (Go
//! `store_sqlite.go`, column for column; evidencebook#254 §2.2).

use super::ShelfError;
use rusqlite::{params, Connection, OptionalExtension, Transaction};

/// The shelf's tables, exactly Go's `sqliteStoreSchema`.
pub(crate) const SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS book_records (
	book_id   TEXT    NOT NULL,
	seq       INTEGER NOT NULL,
	record_id TEXT    NOT NULL,
	header    BLOB    NOT NULL,
	capsule   BLOB    NOT NULL,
	envelope  BLOB    NOT NULL,
	PRIMARY KEY (book_id, record_id)
);
CREATE INDEX IF NOT EXISTS book_records_seq ON book_records (book_id, seq);
CREATE TABLE IF NOT EXISTS cadence_logs (log_id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS cadence_live (log_id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS cadence_witnessed (
	log_id            TEXT    NOT NULL,
	checkpoint_sha256 TEXT    NOT NULL,
	mmr_size          INTEGER NOT NULL,
	tick              INTEGER NOT NULL,
	PRIMARY KEY (log_id, checkpoint_sha256)
);
CREATE INDEX IF NOT EXISTS cadence_witnessed_size ON cadence_witnessed (log_id, mmr_size);
CREATE TABLE IF NOT EXISTS cadence_ticks (
	tick            INTEGER PRIMARY KEY,
	at              TEXT    NOT NULL,
	due_next        TEXT    NOT NULL,
	entry_seq       INTEGER NOT NULL,
	checkpoint_size INTEGER NOT NULL,
	checkpoint      BLOB    NOT NULL,
	leaves          TEXT    NOT NULL,
	witnessed       INTEGER NOT NULL DEFAULT 0,
	receipt         TEXT
);
CREATE TABLE IF NOT EXISTS book_checkpoints (
	book_id       TEXT    NOT NULL,
	n             INTEGER NOT NULL,
	checkpoint_id TEXT    NOT NULL,
	checkpoint    BLOB    NOT NULL,
	PRIMARY KEY (book_id, n),
	UNIQUE (book_id, checkpoint_id)
);
CREATE TABLE IF NOT EXISTS shelf_meta (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS shelf_logs (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('book', 'host')));
";

/// The shelf file version this implementation writes and the only one it
/// opens (`shelf_meta.version`).
pub const SHELF_VERSION: i64 = 1;

/// The kinds of `shelf_logs`.
pub(crate) const KIND_BOOK: &str = "book";
pub(crate) const KIND_HOST: &str = "host";

/// Create the shelf's tables when absent, in one transaction, and apply the
/// fill rules of evidencebook#254 §2.2 (Go `initSQLiteStore`): a file with
/// no `shelf_meta` row gets version 1, and a file without `shelf_logs` gets
/// it filled, each `book_records` id a book and each other `cadence_logs` id
/// a host log. A version it does not know is refused before anything is
/// written.
pub(crate) fn init(conn: &mut Connection) -> Result<(), ShelfError> {
    let tx = conn.transaction()?;
    let has_table = |name: &str| -> Result<bool, ShelfError> {
        let n: i64 = tx.query_row(
            "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
            [name],
            |r| r.get(0),
        )?;
        Ok(n > 0)
    };
    let mut versioned = false;
    if has_table("shelf_meta")? {
        let version: Option<i64> = tx
            .query_row("SELECT version FROM shelf_meta WHERE id = 1", [], |r| {
                r.get(0)
            })
            .optional()?;
        match version {
            None => {}
            Some(v) if v != SHELF_VERSION => {
                return Err(ShelfError::Unsupported(format!(
                    "shelf file version {v} (this build opens version {SHELF_VERSION})"
                )))
            }
            Some(_) => versioned = true,
        }
    }
    let has_logs = has_table("shelf_logs")?;
    tx.execute_batch(SCHEMA)?;
    if !versioned {
        tx.execute(
            "INSERT INTO shelf_meta (id, version) VALUES (1, ?)",
            [SHELF_VERSION],
        )?;
    }
    if !has_logs {
        tx.execute_batch(
            "INSERT INTO shelf_logs (log_id, kind) SELECT DISTINCT book_id, 'book' FROM book_records;
             INSERT INTO shelf_logs (log_id, kind) SELECT log_id, 'host' FROM cadence_logs WHERE log_id NOT IN (SELECT book_id FROM book_records);",
        )?;
    }
    tx.commit()?;
    Ok(())
}

/// `log_id`'s kind as `shelf_logs` records it, `None` when it has no row (a
/// book with no record yet, or a log the shelf has not seen).
pub(crate) fn log_kind(conn: &Connection, log_id: &str) -> Result<Option<String>, ShelfError> {
    Ok(conn
        .query_row(
            "SELECT kind FROM shelf_logs WHERE log_id = ?",
            [log_id],
            |r| r.get(0),
        )
        .optional()?)
}

/// Record `log_id` as `kind` in `tx`, refusing a log `shelf_logs` already
/// holds as the other kind.
pub(crate) fn claim_kind(tx: &Transaction<'_>, log_id: &str, kind: &str) -> Result<(), ShelfError> {
    tx.execute(
        "INSERT INTO shelf_logs (log_id, kind) VALUES (?, ?) ON CONFLICT (log_id) DO NOTHING",
        params![log_id, kind],
    )?;
    let held = log_kind(tx, log_id)?.unwrap_or_default();
    if held != kind {
        return Err(ShelfError::Invalid(format!(
            "{log_id} is a {held} log, not a {kind} log"
        )));
    }
    Ok(())
}

/// Put `log_id` in the live set and count it into the lifetime log count on
/// its first append.
pub(crate) fn mark_live(tx: &Transaction<'_>, log_id: &str) -> Result<(), ShelfError> {
    tx.execute(
        "INSERT OR IGNORE INTO cadence_logs (log_id) VALUES (?)",
        [log_id],
    )?;
    tx.execute(
        "INSERT OR IGNORE INTO cadence_live (log_id) VALUES (?)",
        [log_id],
    )?;
    Ok(())
}

/// How many records `book_records` holds for `log_id`.
pub(crate) fn record_count(conn: &Connection, log_id: &str) -> Result<u64, ShelfError> {
    let n: i64 = conn.query_row(
        "SELECT COUNT(*) FROM book_records WHERE book_id = ?",
        [log_id],
        |r| r.get(0),
    )?;
    Ok(n as u64)
}

/// The durable form of one record (Go `StoredRecord`): the canonical header
/// bytes, the sealed capsule, and its Producer Envelope.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StoredRecord {
    pub seq: u64,
    pub record_id: String,
    pub header: Vec<u8>,
    pub capsule: Vec<u8>,
    pub envelope: Vec<u8>,
}

/// Store `record` in `book_id` (Go `sqliteStore.PutRecord`): stored once,
/// the same record again a no-op, another one under its id corrupt. The
/// record and the book's cadence standing are one transaction.
pub(crate) fn put_record(
    conn: &mut Connection,
    book_id: &str,
    record: &StoredRecord,
) -> Result<(), ShelfError> {
    if let Some(existing) = get_record(conn, book_id, &record.record_id)? {
        if existing.seq != record.seq || existing.capsule != record.capsule {
            return Err(ShelfError::Corrupt(format!(
                "record {} already stored with different content",
                record.record_id
            )));
        }
        return Ok(());
    }
    let tx = conn.transaction()?;
    tx.execute(
        "INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES (?, ?, ?, ?, ?, ?)",
        params![book_id, record.seq as i64, record.record_id, record.header, record.capsule, record.envelope],
    )?;
    claim_kind(&tx, book_id, KIND_BOOK)?;
    mark_live(&tx, book_id)?;
    tx.commit()?;
    Ok(())
}

pub(crate) fn get_record(
    conn: &Connection,
    book_id: &str,
    record_id: &str,
) -> Result<Option<StoredRecord>, ShelfError> {
    Ok(conn
        .query_row(
            "SELECT seq, header, capsule, envelope FROM book_records WHERE book_id = ? AND record_id = ?",
            params![book_id, record_id],
            |r| {
                Ok(StoredRecord {
                    seq: r.get::<_, i64>(0)? as u64,
                    record_id: record_id.to_string(),
                    header: r.get(1)?,
                    capsule: r.get(2)?,
                    envelope: r.get(3)?,
                })
            },
        )
        .optional()?)
}

/// Every record of `book_id`, in seq order.
pub(crate) fn records(conn: &Connection, book_id: &str) -> Result<Vec<StoredRecord>, ShelfError> {
    let mut stmt = conn.prepare(
        "SELECT seq, record_id, header, capsule, envelope FROM book_records WHERE book_id = ? ORDER BY seq",
    )?;
    let rows = stmt.query_map([book_id], |r| {
        Ok(StoredRecord {
            seq: r.get::<_, i64>(0)? as u64,
            record_id: r.get(1)?,
            header: r.get(2)?,
            capsule: r.get(3)?,
            envelope: r.get(4)?,
        })
    })?;
    Ok(rows.collect::<Result<_, _>>()?)
}

/// Keep `checkpoint` (its JSON, Go's `Checkpoint` encoding) as `book_id`'s
/// next checkpoint, unless it already holds one with this id.
pub(crate) fn put_checkpoint(
    conn: &mut Connection,
    book_id: &str,
    checkpoint_id: &str,
    encoded: &[u8],
) -> Result<(), ShelfError> {
    let tx = conn.transaction()?;
    let held: i64 = tx.query_row(
        "SELECT COUNT(*) FROM book_checkpoints WHERE book_id = ? AND checkpoint_id = ?",
        params![book_id, checkpoint_id],
        |r| r.get(0),
    )?;
    if held > 0 {
        return Ok(());
    }
    let n: i64 = tx.query_row(
        "SELECT COALESCE(MAX(n), 0) + 1 FROM book_checkpoints WHERE book_id = ?",
        [book_id],
        |r| r.get(0),
    )?;
    tx.execute(
        "INSERT INTO book_checkpoints (book_id, n, checkpoint_id, checkpoint) VALUES (?, ?, ?, ?)",
        params![book_id, n, checkpoint_id, encoded],
    )?;
    tx.commit()?;
    Ok(())
}

/// Every checkpoint `book_id` has kept, in the order kept, as stored.
pub(crate) fn checkpoints(conn: &Connection, book_id: &str) -> Result<Vec<Vec<u8>>, ShelfError> {
    let mut stmt =
        conn.prepare("SELECT checkpoint FROM book_checkpoints WHERE book_id = ? ORDER BY n")?;
    let rows = stmt.query_map([book_id], |r| r.get(0))?;
    Ok(rows.collect::<Result<_, _>>()?)
}
