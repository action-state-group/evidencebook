// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// RecordSource yields every committed record in log order. Index rebuilds
// read only from a RecordSource, so an index can always be reproduced from
// the committed history alone.
type RecordSource func(ctx context.Context, yield func(Record) error) error

// Filter selects records from the operational index. Zero-valued fields do
// not constrain the result.
type Filter struct {
	RecordType      string
	SubjectRef      string
	CounterpartyRef string
	ExchangeID      string
	RequestDigest   string
	LinkType        LinkType
	LinkTarget      string
	FromSeq         uint64
	ToSeq           uint64
}

// OperationalIndex is fast local lookup with no independent trust value: its
// answers are only as trustworthy as the store producing them, and nothing
// that produces a proof reads from it.
type OperationalIndex interface {
	Add(ctx context.Context, record Record) error
	// Find returns matching record ids in log order.
	Find(ctx context.Context, filter Filter) ([]string, error)
	// Rebuild discards all state and re-derives it from source.
	Rebuild(ctx context.Context, source RecordSource) error
	// StateDigest is a deterministic digest of the full index contents.
	StateDigest(ctx context.Context) (string, error)
	Release() error
}

// SQLiteIndex is the reference OperationalIndex.
type SQLiteIndex struct{ db *sql.DB }

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS records (
	seq INTEGER PRIMARY KEY,
	record_id TEXT NOT NULL UNIQUE,
	record_type TEXT NOT NULL,
	epistemic_type TEXT NOT NULL,
	subject_ref TEXT NOT NULL,
	counterparty_ref TEXT NOT NULL,
	exchange_id TEXT NOT NULL,
	request_digest TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS links (
	source_seq INTEGER NOT NULL,
	ordinal INTEGER NOT NULL,
	link_type TEXT NOT NULL,
	target TEXT NOT NULL,
	PRIMARY KEY (source_seq, ordinal)
);
CREATE INDEX IF NOT EXISTS links_target ON links (target, link_type);
CREATE INDEX IF NOT EXISTS records_subject ON records (subject_ref);`

// OpenSQLiteIndex opens the reference operational index. An empty path keeps
// it in memory, which is safe because it is always rebuildable.
func OpenSQLiteIndex(path string) (*SQLiteIndex, error) {
	dsn := "file::memory:"
	if path != "" {
		dsn = "file:" + path
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(sqliteSchema); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &SQLiteIndex{db: db}, nil
}

func (x *SQLiteIndex) Add(ctx context.Context, record Record) error {
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := insertRecord(ctx, tx, record); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

func insertRecord(ctx context.Context, tx *sql.Tx, record Record) error {
	h := record.Header
	var correlation Correlation
	if h.Correlation != nil {
		correlation = *h.Correlation
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO records VALUES (?,?,?,?,?,?,?,?)`,
		int64(record.Seq), record.RecordID, h.RecordType, string(h.EpistemicType), h.SubjectRef, h.CounterpartyRef, correlation.ExchangeID, correlation.RequestDigest); err != nil {
		return err
	}
	for i, link := range h.Links {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO links VALUES (?,?,?,?)`, int64(record.Seq), i, string(link.Type), link.Target); err != nil {
			return err
		}
	}
	return nil
}

func (x *SQLiteIndex) Find(ctx context.Context, f Filter) ([]string, error) {
	var where []string
	var args []any
	// CONTRACT: database/sql DB.QueryContext takes its arguments as ...any.
	add := func(clause string, value any) {
		where = append(where, clause)
		args = append(args, value)
	}
	if f.RecordType != "" {
		add("r.record_type = ?", f.RecordType)
	}
	if f.SubjectRef != "" {
		add("r.subject_ref = ?", f.SubjectRef)
	}
	if f.CounterpartyRef != "" {
		add("r.counterparty_ref = ?", f.CounterpartyRef)
	}
	if f.ExchangeID != "" {
		add("r.exchange_id = ?", f.ExchangeID)
	}
	if f.RequestDigest != "" {
		add("r.request_digest = ?", f.RequestDigest)
	}
	if f.FromSeq != 0 {
		add("r.seq >= ?", int64(f.FromSeq))
	}
	if f.ToSeq != 0 {
		add("r.seq <= ?", int64(f.ToSeq))
	}
	if f.LinkType != "" || f.LinkTarget != "" {
		clause := "EXISTS (SELECT 1 FROM links l WHERE l.source_seq = r.seq"
		if f.LinkType != "" {
			clause += " AND l.link_type = ?"
			args = append(args, string(f.LinkType))
		}
		if f.LinkTarget != "" {
			clause += " AND l.target = ?"
			args = append(args, f.LinkTarget)
		}
		where = append(where, clause+")")
	}
	query := "SELECT r.record_id FROM records r"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := x.db.QueryContext(ctx, query+" ORDER BY r.seq", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (x *SQLiteIndex) Rebuild(ctx context.Context, source RecordSource) error {
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rebuild := func() error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM links; DELETE FROM records;"); err != nil {
			return err
		}
		return source(ctx, func(record Record) error { return insertRecord(ctx, tx, record) })
	}
	if err := rebuild(); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

func (x *SQLiteIndex) StateDigest(ctx context.Context) (string, error) {
	hash := sha256.New()
	for _, query := range []string{
		"SELECT seq, record_id, record_type, epistemic_type, subject_ref, counterparty_ref, exchange_id, request_digest FROM records ORDER BY seq",
		"SELECT source_seq, ordinal, link_type, target, '', '', '', '' FROM links ORDER BY source_seq, ordinal",
	} {
		rows, err := x.db.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var a, b, c, d, e, f, g, h string
			if err := rows.Scan(&a, &b, &c, &d, &e, &f, &g, &h); err != nil {
				return "", errors.Join(err, rows.Close())
			}
			fmt.Fprintf(hash, "%q|%q|%q|%q|%q|%q|%q|%q\n", a, b, c, d, e, f, g, h)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (x *SQLiteIndex) Release() error { return x.db.Close() }
