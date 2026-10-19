// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
-- per log checkpoint carried in a tick whose receipt verified, inserted only.
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
`

// sqliteStore is one book's Store in a database several books share, every
// row keyed by its book id. It has FileStore's semantics: a record is stored
// once (the same record again is a no-op, another one under its id is
// corrupt), records list in seq order, and checkpoints in the order kept.
// Every write is one SQLite transaction, durable when it returns.
type sqliteStore struct {
	db     *sql.DB
	bookID string
}

func initSQLiteStore(db *sql.DB) error {
	_, err := db.Exec(sqliteStoreSchema)
	return err
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
