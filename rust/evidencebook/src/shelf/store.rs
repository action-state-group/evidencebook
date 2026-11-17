//! The shelf's own tables and one book's record store over them (Go
//! `store_sqlite.go`, column for column; evidencebook#254 §2.2).

use super::ShelfError;
use rusqlite::{params, Connection, OptionalExtension, Transaction};

/// The shelf's tables, exactly Go's `sqliteStoreSchema`. `shelf_entries`
/// (every entry document after the format switch, by its position in the
/// cadence log) and `shelf_migration` (the migration's progress) are empty
/// in a version 1 file.
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
CREATE TABLE IF NOT EXISTS shelf_logs (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('book', 'host', 'journal')));
CREATE TABLE IF NOT EXISTS shelf_entries (
	entry_seq      INTEGER PRIMARY KEY,
	kind           TEXT    NOT NULL,
	book_id        TEXT    NOT NULL,
	log_id         TEXT    NOT NULL,
	doc            BLOB    NOT NULL,
	link_opening   BLOB,
	status_opening BLOB,
	tick           INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS shelf_entries_log ON shelf_entries (log_id, entry_seq);
-- Every witness receipt that verified for a shelf checkpoint, one per
-- witness (its receipt's URL), with the directory key id it verified under
-- and the registration time it gives in Unix seconds or null
-- (record_time.rs).
CREATE TABLE IF NOT EXISTS shelf_receipts (
	checkpoint_sha256 TEXT    NOT NULL,
	witness_id        TEXT    NOT NULL,
	key_id            TEXT    NOT NULL,
	receipt           TEXT    NOT NULL,
	registered_at     INTEGER,
	PRIMARY KEY (checkpoint_sha256, witness_id)
);
CREATE TABLE IF NOT EXISTS shelf_migration (id INTEGER PRIMARY KEY CHECK (id = 1), state TEXT NOT NULL, backup_sha256 TEXT NOT NULL);
-- The store of what the other side pushes (evidencebook#461 §3,
-- shelf_receive.go): its records, by digest, with the predecessor each names
-- and whether it is an unanswered unsolicited offer; every stamp proof
-- accepted, and whether the records held recomputed its root (0 not yet, 1
-- recomputed, 2 contradicted); the tombstone a deleted record leaves; the
-- evidence of a fork, a contradicting checkpoint or a checkpoint the records
-- do not recompute; each conflict found, once per position (a fork) or
-- size (a checkpoint), which no deletion undoes; and the position and digest
-- of every deleted record, so another record arriving there is a fork. A
-- row with offer = 1 (a dropped offer's position, a conflict in a log of
-- offers alone) counts under the offer bounds and is not kept past them.
-- Nothing here is appended to a book.
CREATE TABLE IF NOT EXISTS shelf_received (
	digest      TEXT    PRIMARY KEY,
	sender_key  TEXT    NOT NULL,
	log_id      TEXT    NOT NULL,
	position    INTEGER NOT NULL,
	prev        TEXT    NOT NULL,
	deal_id     TEXT    NOT NULL,
	offer       INTEGER NOT NULL DEFAULT 0,
	record      BLOB    NOT NULL,
	received_at TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS shelf_received_position ON shelf_received (sender_key, log_id, position);
CREATE INDEX IF NOT EXISTS shelf_received_deal ON shelf_received (deal_id);
CREATE TABLE IF NOT EXISTS shelf_received_proofs (
	sender_key              TEXT    NOT NULL,
	log_id                  TEXT    NOT NULL,
	size                    INTEGER NOT NULL,
	shelf_checkpoint_sha256 TEXT    NOT NULL,
	book_checkpoint         BLOB    NOT NULL,
	fact                    BLOB    NOT NULL,
	status_opening          BLOB,
	inclusion_proof         BLOB    NOT NULL,
	shelf_checkpoint        BLOB    NOT NULL,
	received_at             TEXT    NOT NULL,
	verified                INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (sender_key, log_id, size, shelf_checkpoint_sha256)
);
CREATE TABLE IF NOT EXISTS shelf_received_tombstones (
	digest      TEXT PRIMARY KEY,
	sender_key  TEXT NOT NULL,
	received_at TEXT NOT NULL,
	deleted_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS shelf_evidence (
	id       INTEGER PRIMARY KEY,
	kind     TEXT    NOT NULL,
	a        BLOB    NOT NULL,
	b        BLOB    NOT NULL,
	noted_at TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS shelf_received_conflicts (
	sender_key  TEXT    NOT NULL,
	log_id      TEXT    NOT NULL,
	kind        TEXT    NOT NULL,
	at          INTEGER NOT NULL,
	evidence_id INTEGER NOT NULL,
	offer       INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (sender_key, log_id, kind, at)
);
CREATE TABLE IF NOT EXISTS shelf_received_vacated (
	sender_key TEXT    NOT NULL,
	log_id     TEXT    NOT NULL,
	position   INTEGER NOT NULL,
	digest     TEXT    NOT NULL,
	deal_id    TEXT    NOT NULL,
	offer      INTEGER NOT NULL,
	PRIMARY KEY (sender_key, log_id, position, digest)
);
";

/// A write transaction, begun IMMEDIATE: it takes the file's write lock
/// first, so a transaction that reads before it writes waits on another
/// writer (the busy timeout) rather than failing when its read snapshot is
/// stale (`SQLITE_BUSY_SNAPSHOT` under WAL). Each book's CLL store, the
/// artifact store and a host's backend write the file on connections of
/// their own.
pub(crate) fn write_tx(conn: &mut Connection) -> Result<Transaction<'_>, ShelfError> {
    Ok(conn.transaction_with_behavior(rusqlite::TransactionBehavior::Immediate)?)
}

/// The shelf file version this implementation writes (`shelf_meta.version`).
pub const SHELF_VERSION: i64 = 1;

/// The shelf file version once [`super::Cadence::migrate`] has switched the
/// shelf to a log of book facts (evidencebook#461). This build opens both
/// versions; an older build refuses this one.
pub const SHELF_VERSION_FACTS: i64 = 2;

/// The kinds of `shelf_logs`.
pub(crate) const KIND_BOOK: &str = "book";
pub(crate) const KIND_HOST: &str = "host";
/// The shelf's journal (evidencebook#461 §3).
pub(crate) const KIND_JOURNAL: &str = "journal";

/// Let `shelf_logs` hold the journal's kind in a file whose table was
/// created before it existed (evidencebook#461 §3; Go `allowJournalKind`):
/// SQLite cannot change a CHECK, so the table is rebuilt with the same rows,
/// in `tx`. A file whose table already allows it is left alone.
pub(crate) fn allow_journal_kind(tx: &Transaction<'_>) -> Result<(), ShelfError> {
    let create: String = tx.query_row(
        "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'shelf_logs'",
        [],
        |r| r.get(0),
    )?;
    if create.contains("'journal'") {
        return Ok(());
    }
    tx.execute_batch(
        "CREATE TABLE shelf_logs_journal (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('book', 'host', 'journal')));
         INSERT INTO shelf_logs_journal (log_id, kind) SELECT log_id, kind FROM shelf_logs;
         DROP TABLE shelf_logs;
         ALTER TABLE shelf_logs_journal RENAME TO shelf_logs;",
    )?;
    Ok(())
}

/// Create the shelf's tables when absent, in one transaction, and apply the
/// fill rules of evidencebook#254 §2.2 (Go `initSQLiteStore`): a file with
/// no `shelf_meta` row gets version 1, and a file without `shelf_logs` gets
/// it filled, each `book_records` id a book and each other `cadence_logs` id
/// a host log. A version it does not know is refused before anything is
/// written.
pub(crate) fn init(conn: &mut Connection) -> Result<(), ShelfError> {
    let tx = write_tx(conn)?;
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
            Some(v) if v != SHELF_VERSION && v != SHELF_VERSION_FACTS => {
                return Err(ShelfError::Unsupported(format!(
                    "shelf file version {v} (this build opens versions {SHELF_VERSION} and {SHELF_VERSION_FACTS})"
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
/// record, the `shelf_logs` kind its first record claims (`kind`: a book's,
/// or the journal's) and the book's cadence standing are one transaction.
pub(crate) fn put_record(
    conn: &mut Connection,
    book_id: &str,
    kind: &str,
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
    let tx = write_tx(conn)?;
    tx.execute(
        "INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES (?, ?, ?, ?, ?, ?)",
        params![book_id, record.seq as i64, record.record_id, record.header, record.capsule, record.envelope],
    )?;
    claim_kind(&tx, book_id, kind)?;
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
    let tx = write_tx(conn)?;
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
