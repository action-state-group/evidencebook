// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const sqliteStoreSchema = `
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
-- The cadence (evidencebook#36 §2). cadence_logs is every log the profile
-- has ever appended to (H is its count, and rows are never deleted);
-- cadence_live the logs the next tick checkpoints; cadence_witnessed one row
-- per log checkpoint carried in a tick whose receipt verified, naming the
-- newest such tick.
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
-- capsule-cli's deal_cadence columns, plus whether the tick's receipt
-- verified.
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
-- The file's shelf version, and the kind of every log on the shelf but the
-- cadence log (evidencebook#254 §2.2): a book's row is written with its first
-- record, a host log's when it is opened as one.
CREATE TABLE IF NOT EXISTS shelf_meta (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS shelf_logs (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('book', 'host', 'journal')));
-- A shelf log of book facts (evidencebook#461, shelf_stamp.go): every entry
-- document after the format switch, by its position in the cadence log, and
-- the migration's progress. Empty in a version 1 file.
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
-- witness (its receipt's URL), with the registration time it gives in Unix
-- seconds or null (shelf_record_time.go).
CREATE TABLE IF NOT EXISTS shelf_receipts (
	checkpoint_sha256 TEXT    NOT NULL,
	witness_id        TEXT    NOT NULL,
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
`

// ShelfVersion is the shelf file version this implementation writes and the
// only one it opens (shelf_meta.version).
const ShelfVersion = 1

// The kinds of shelf_logs.
const (
	logKindBook    = "book"
	logKindHost    = "host"
	logKindJournal = "journal"
)

// sqliteStore is one book's Store in a database several books share, every
// row keyed by its book id. It has FileStore's semantics: a record is stored
// once (the same record again is a no-op, another one under its id is
// corrupt), records list in seq order, and checkpoints in the order kept.
// Every write is one SQLite transaction, durable when it returns.
type sqliteStore struct {
	db     *sql.DB
	bookID string
	// kind is the shelf_logs kind its first record claims: a book's, or the
	// journal's (evidencebook#461 §3).
	kind string
}

// initSQLiteStore creates the shelf's tables when absent, in one
// transaction, and applies the fill rules of evidencebook#254 §2.2: a file
// with no shelf_meta row (a fresh file, capsule-cli's deal.db before
// migration, or a shelf from v0.12.0 or earlier) gets version 1, and a file
// without shelf_logs gets it filled, each book_records id a book and each
// other cadence_logs id a host log. A version it does not know is refused
// before anything is written.
func initSQLiteStore(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	hasTable := func(name string) (bool, error) {
		var n int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
		return n > 0, err
	}
	hasMeta, err := hasTable("shelf_meta")
	if err != nil {
		return err
	}
	versioned := false
	if hasMeta {
		var version int64
		switch err := tx.QueryRowContext(ctx, `SELECT version FROM shelf_meta WHERE id = 1`).Scan(&version); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case version != ShelfVersion && version != ShelfVersionFacts:
			return fmt.Errorf("%w: shelf file version %d (this build opens versions %d and %d)", ErrUnsupported, version, ShelfVersion, ShelfVersionFacts)
		default:
			versioned = true
		}
	}
	hasLogs, err := hasTable("shelf_logs")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, sqliteStoreSchema); err != nil {
		return err
	}
	if !versioned {
		if _, err := tx.ExecContext(ctx, `INSERT INTO shelf_meta (id, version) VALUES (1, ?)`, ShelfVersion); err != nil {
			return err
		}
	}
	if !hasLogs {
		for _, statement := range []string{
			`INSERT INTO shelf_logs (log_id, kind) SELECT DISTINCT book_id, 'book' FROM book_records`,
			`INSERT INTO shelf_logs (log_id, kind) SELECT log_id, 'host' FROM cadence_logs WHERE log_id NOT IN (SELECT book_id FROM book_records)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// logKindOf is logID's kind as shelf_logs records it, empty when it has no
// row (a book with no record yet, or a log the shelf has not seen).
func logKindOf(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, logID string) (string, error) {
	var kind string
	err := q.QueryRowContext(ctx, `SELECT kind FROM shelf_logs WHERE log_id = ?`, logID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return kind, err
}

// claimLogKind records logID as kind in tx, refusing a log shelf_logs already
// holds as the other kind.
func claimLogKind(ctx context.Context, tx *sql.Tx, logID, kind string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO shelf_logs (log_id, kind) VALUES (?, ?) ON CONFLICT (log_id) DO NOTHING`, logID, kind); err != nil {
		return err
	}
	held, err := logKindOf(ctx, tx, logID)
	if err != nil {
		return err
	}
	if held != kind {
		return fmt.Errorf("%w: %s is a %s log, not a %s log", ErrInvalid, logID, held, kind)
	}
	return nil
}

func (s *sqliteStore) PutRecord(ctx context.Context, record StoredRecord) error {
	existing, err := s.GetRecord(ctx, record.RecordID)
	switch {
	case err == nil:
		if existing.Seq != record.Seq || !bytes.Equal(existing.Capsule, record.Capsule) {
			return fmt.Errorf("%w: record %s already stored with different content", ErrCorrupt, record.RecordID)
		}
		return nil
	case !errors.Is(err, ErrNotFound):
		return err
	}
	// The record and the book's cadence standing are one transaction: a
	// log that grows is live for the next tick, and its first record counts
	// it into the profile's lifetime log count.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES (?, ?, ?, ?, ?, ?)`,
		s.bookID, record.Seq, record.RecordID, []byte(record.Header), []byte(record.Capsule), record.Envelope); err != nil {
		return err
	}
	kind := s.kind
	if kind == "" {
		kind = logKindBook
	}
	if err := claimLogKind(ctx, tx, s.bookID, kind); err != nil {
		return err
	}
	if err := markLive(ctx, tx, s.bookID); err != nil {
		return err
	}
	return tx.Commit()
}

// markLive puts logID in the live set and counts it into the lifetime log
// count on its first append.
func markLive(ctx context.Context, tx *sql.Tx, logID string) error {
	for _, statement := range []string{
		`INSERT OR IGNORE INTO cadence_logs (log_id) VALUES (?)`,
		`INSERT OR IGNORE INTO cadence_live (log_id) VALUES (?)`,
	} {
		if _, err := tx.ExecContext(ctx, statement, logID); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteStore) GetRecord(ctx context.Context, recordID string) (StoredRecord, error) {
	record := StoredRecord{RecordID: recordID}
	var header, capsule []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT seq, header, capsule, envelope FROM book_records WHERE book_id = ? AND record_id = ?`,
		s.bookID, recordID).Scan(&record.Seq, &header, &capsule, &record.Envelope)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredRecord{}, fmt.Errorf("%w: record %s", ErrNotFound, recordID)
	}
	if err != nil {
		return StoredRecord{}, err
	}
	record.Header, record.Capsule = header, capsule
	return record, nil
}

func (s *sqliteStore) Records(ctx context.Context) ([]StoredRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, record_id, header, capsule, envelope FROM book_records WHERE book_id = ? ORDER BY seq`, s.bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredRecord
	for rows.Next() {
		var record StoredRecord
		var header, capsule []byte
		if err := rows.Scan(&record.Seq, &record.RecordID, &header, &capsule, &record.Envelope); err != nil {
			return nil, err
		}
		record.Header, record.Capsule = header, capsule
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *sqliteStore) PutCheckpoint(ctx context.Context, cp Checkpoint) error {
	encoded, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var held int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM book_checkpoints WHERE book_id = ? AND checkpoint_id = ?`, s.bookID, cp.ID).Scan(&held); err != nil {
		return err
	}
	if held > 0 {
		return nil
	}
	var n int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(n), 0) + 1 FROM book_checkpoints WHERE book_id = ?`, s.bookID).Scan(&n); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO book_checkpoints (book_id, n, checkpoint_id, checkpoint) VALUES (?, ?, ?, ?)`, s.bookID, n, cp.ID, encoded); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) Checkpoints(ctx context.Context) ([]Checkpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT checkpoint FROM book_checkpoints WHERE book_id = ? ORDER BY n`, s.bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var cp Checkpoint
		if err := json.Unmarshal(encoded, &cp); err != nil {
			return nil, fmt.Errorf("%w: a stored checkpoint of %s: %v", ErrCorrupt, s.bookID, err)
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}

// Release is a no-op: the shelf owns the shared database.
func (s *sqliteStore) Release() error { return nil }

// allowJournalKind lets shelf_logs hold the journal's kind in a file whose
// table was created before it existed (evidencebook#461 §3): SQLite cannot
// change a CHECK, so the table is rebuilt with the same rows, in tx. A file
// whose table already allows it is left alone.
func allowJournalKind(ctx context.Context, tx *sql.Tx) error {
	var create string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'shelf_logs'`).Scan(&create); err != nil {
		return err
	}
	if strings.Contains(create, "'journal'") {
		return nil
	}
	for _, statement := range []string{
		`CREATE TABLE shelf_logs_journal (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('book', 'host', 'journal')))`,
		`INSERT INTO shelf_logs_journal (log_id, kind) SELECT log_id, kind FROM shelf_logs`,
		`DROP TABLE shelf_logs`,
		`ALTER TABLE shelf_logs_journal RENAME TO shelf_logs`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
