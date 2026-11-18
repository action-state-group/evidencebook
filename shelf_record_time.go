// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// Record time (evidencebook#461, docs/design/461-shelf-facts.md §2.3 and
// §2.6): every receipt that verifies for a shelf checkpoint is kept in
// shelf_receipts, one per witness and key the receipt verified under
// (evidencebook#492: a directory row may list several keys, and an agreement
// names a witness by key), with the registration time the witness put in it
// (the iat of its protected CWT claims) or none. A witness is
// named by its receipt's URL (ts_url), the name a witness directory row is
// found by, so a witness an agreement names is that URL. Each row also keeps
// the directory key id the receipt verified under: a URL is a locator, and
// the key id lets a later naming by key read the rows already held.

// upsertReceipt keeps a verified receipt for the checkpoint whose SHA-256 is
// checkpointSHA256. Another receipt from the same witness under the same key
// replaces the held one only when it gives an earlier registration time, or
// the held one gives none: a witness's earliest registration is the one a
// record time reads. A receipt under another key of the same witness is kept
// beside it.
const upsertReceipt = `INSERT INTO shelf_receipts (checkpoint_sha256, witness_id, key_id, receipt, registered_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (checkpoint_sha256, witness_id, key_id) DO UPDATE SET receipt = excluded.receipt, registered_at = excluded.registered_at
WHERE excluded.registered_at IS NOT NULL AND (shelf_receipts.registered_at IS NULL OR excluded.registered_at < shelf_receipts.registered_at)`

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// keepReceipt stores a receipt that verified for statement.
func keepReceipt(ctx context.Context, db execer, statement []byte, receipt WitnessReceipt, encoded string, v verifiedReceipt) error {
	sum := sha256.Sum256(statement)
	var at any
	if v.registeredAt != nil {
		at = *v.registeredAt
	}
	_, err := db.ExecContext(ctx, upsertReceipt, hex.EncodeToString(sum[:]), receipt.TSURL, v.keyID, encoded, at)
	return err
}

// backfillReceiptsLocked keeps, in shelf_receipts, the receipt of every
// witnessed tick that has none there: a tick witnessed before the table
// existed (a shelf written by evidencebook before #461 part 5) or imported
// (ImportLegacy). A receipt that no longer verifies under the configured
// directory is left out and tried again at the next open.
func (c *Cadence) backfillReceiptsLocked(ctx context.Context) error {
	s := c.shelf
	rows, err := s.db.QueryContext(ctx, `SELECT tick, checkpoint, receipt FROM cadence_ticks WHERE witnessed = 1 AND receipt IS NOT NULL ORDER BY tick`)
	if err != nil {
		return err
	}
	type held struct {
		n         uint64
		statement []byte
		receipt   string
	}
	var ticks []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.n, &h.statement, &h.receipt); err != nil {
			rows.Close()
			return err
		}
		ticks = append(ticks, h)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(ticks) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, h := range ticks {
			var receipt WitnessReceipt
			if err := json.Unmarshal([]byte(h.receipt), &receipt); err != nil {
				return fmt.Errorf("%w: tick %d receipt: %v", ErrCorrupt, h.n, err)
			}
			// The same receipt already held needs no second verification; a
			// held receipt of another key of the same witness does not cover
			// this one.
			sum := sha256.Sum256(h.statement)
			var present int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ? AND witness_id = ? AND receipt = ?`,
				hex.EncodeToString(sum[:]), receipt.TSURL, h.receipt).Scan(&present); err != nil {
				return err
			}
			if present > 0 {
				continue
			}
			check, verified, err := verifyWitnessReceipt(h.statement, receipt, c.cfg.Witnesses)
			if err != nil || check.State != WitnessVerified {
				continue
			}
			if err := keepReceipt(ctx, tx, h.statement, receipt, h.receipt, verified); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordTime is the record time of the record at position (from 1) of the
// book bookID (its book id, §2.2; the journal's included): the earliest
// registration time, among the receipts held from the witnesses named (their
// URLs), of a shelf checkpoint that covers the first fact of that book whose
// size covers the position (§2.6). ok is false when there is none: no fact
// covers the position yet, no checkpoint covering that fact has a receipt
// from a named witness, or every such receipt gives no registration time. A
// status resting on a record with no time is local, never witnessed.
//
// It takes no lock, so a StatusProvider or ReceiveProfile may call it, and
// reads in one transaction, so a stamp being finished is seen whole or not
// at all.
func (s *Shelf) RecordTime(ctx context.Context, bookID string, position uint64, witnesses []string) (t time.Time, ok bool, err error) {
	return s.recordTime(ctx, bookID, position, witnesses, byURL)
}

// RecordTimeByKey is RecordTime with the witnesses named by their verified
// key ids (lowercase hex), as an x-deal-v1 agreement names them (Steven,
// #482 Q1, u604: a witness is its key, its URL only a hint, so a witness
// that moved stays the same witness).
func (s *Shelf) RecordTimeByKey(ctx context.Context, bookID string, position uint64, keyIDs []string) (t time.Time, ok bool, err error) {
	return s.recordTime(ctx, bookID, position, keyIDs, byKey)
}

// How a record time names its witnesses: shelf_receipts' column it matches.
const (
	byURL = "witness_id"
	byKey = "key_id"
)

func (s *Shelf) recordTime(ctx context.Context, bookID string, position uint64, witnesses []string, by string) (t time.Time, ok bool, err error) {
	if err := s.live(); err != nil {
		return time.Time{}, false, err
	}
	if position == 0 {
		return time.Time{}, false, fmt.Errorf("%w: a record position counts from 1", ErrInvalid)
	}
	if len(witnesses) == 0 {
		return time.Time{}, false, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return time.Time{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	seq, found, err := firstCoveringFact(ctx, tx, bookID, position)
	if err != nil || !found {
		return time.Time{}, false, err
	}
	return earliestNamedTime(ctx, tx, seq, witnesses, by)
}

// ReceivedRecordTime is the record time the holder's own stamps give a
// record it received (docs/design/461-deal-profile.md §4.3): the record at
// position (from 1) of the other side's log receivedLogID, as the holder's
// book bookID names it. Its time is the earliest registration time, among
// the receipts held from the witnesses named, of a shelf checkpoint that
// covers the first fact of bookID whose link value's last_received names
// receivedLogID at a size (a record count) that reaches the position. ok is
// false when there is none, as for RecordTime. The other side's own stamps
// may give an earlier time; the profile takes the earlier of the two.
//
// It takes no lock and reads in one transaction, as RecordTime does.
func (s *Shelf) ReceivedRecordTime(ctx context.Context, bookID, receivedLogID string, position uint64, witnesses []string) (t time.Time, ok bool, err error) {
	return s.receivedRecordTime(ctx, bookID, receivedLogID, position, witnesses, byURL)
}

// ReceivedRecordTimeByKey is ReceivedRecordTime with the witnesses named by
// their verified key ids, as RecordTimeByKey.
func (s *Shelf) ReceivedRecordTimeByKey(ctx context.Context, bookID, receivedLogID string, position uint64, keyIDs []string) (t time.Time, ok bool, err error) {
	return s.receivedRecordTime(ctx, bookID, receivedLogID, position, keyIDs, byKey)
}

func (s *Shelf) receivedRecordTime(ctx context.Context, bookID, receivedLogID string, position uint64, witnesses []string, by string) (t time.Time, ok bool, err error) {
	if err := s.live(); err != nil {
		return time.Time{}, false, err
	}
	if position == 0 {
		return time.Time{}, false, fmt.Errorf("%w: a record position counts from 1", ErrInvalid)
	}
	if receivedLogID == "" {
		return time.Time{}, false, fmt.Errorf("%w: a received record names its log", ErrInvalid)
	}
	if len(witnesses) == 0 {
		return time.Time{}, false, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return time.Time{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	seq, found, err := firstLinkingFact(ctx, tx, bookID, receivedLogID, position)
	if err != nil || !found {
		return time.Time{}, false, err
	}
	return earliestNamedTime(ctx, tx, seq, witnesses, by)
}

// RecordStamp is the stamp (its tick, from 1) whose fact is the first to
// cover the record at position (from 1) of the book bookID, as RecordTime
// finds that fact: a record's time can come from no earlier stamp. ok is
// false when no fact covers the position yet, so the next stamp will be the
// first (docs/design/461-deal-profile.md §8.1, "Times"). It takes no lock,
// as RecordTime.
func (s *Shelf) RecordStamp(ctx context.Context, bookID string, position uint64) (n uint64, ok bool, err error) {
	return s.recordStamp(ctx, position, func(tx *sql.Tx) (uint64, bool, error) {
		return firstCoveringFact(ctx, tx, bookID, position)
	})
}

// ReceivedRecordStamp is RecordStamp for a record the holder received: the
// stamp whose fact of bookID is the first whose link value's last_received
// names receivedLogID at a size that reaches the position, as
// ReceivedRecordTime finds it.
func (s *Shelf) ReceivedRecordStamp(ctx context.Context, bookID, receivedLogID string, position uint64) (n uint64, ok bool, err error) {
	if receivedLogID == "" {
		return 0, false, fmt.Errorf("%w: a received record names its log", ErrInvalid)
	}
	return s.recordStamp(ctx, position, func(tx *sql.Tx) (uint64, bool, error) {
		return firstLinkingFact(ctx, tx, bookID, receivedLogID, position)
	})
}

func (s *Shelf) recordStamp(ctx context.Context, position uint64, first func(*sql.Tx) (uint64, bool, error)) (n uint64, ok bool, err error) {
	if err := s.live(); err != nil {
		return 0, false, err
	}
	if position == 0 {
		return 0, false, fmt.Errorf("%w: a record position counts from 1", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	seq, found, err := first(tx)
	if err != nil || !found {
		return 0, false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT tick FROM shelf_entries WHERE entry_seq = ?`, seq).Scan(&n); err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// earliestNamedTime is the earliest registration time, among the receipts
// from the witnesses named (by shelf_receipts' column by: their URL or
// their key id), of a shelf checkpoint whose tree holds the entry at seq.
func earliestNamedTime(ctx context.Context, tx *sql.Tx, seq uint64, witnesses []string, by string) (t time.Time, ok bool, err error) {
	// A shelf checkpoint covers the fact's entry when its tree holds that
	// entry: its MMR size is at least the size of seq leaves.
	cps, err := tx.QueryContext(ctx, `SELECT checkpoint FROM cadence_ticks WHERE checkpoint_size >= ? ORDER BY tick`, mmrSize(seq))
	if err != nil {
		return time.Time{}, false, err
	}
	covering := map[string]bool{}
	for cps.Next() {
		var statement []byte
		if err := cps.Scan(&statement); err != nil {
			cps.Close()
			return time.Time{}, false, err
		}
		sum := sha256.Sum256(statement)
		covering[hex.EncodeToString(sum[:])] = true
	}
	if err := errors.Join(cps.Err(), cps.Close()); err != nil {
		return time.Time{}, false, err
	}
	named := map[string]bool{}
	for _, w := range witnesses {
		named[w] = true
	}
	// by is one of two constants, never input.
	rows, err := tx.QueryContext(ctx, `SELECT checkpoint_sha256, `+by+`, registered_at FROM shelf_receipts WHERE registered_at IS NOT NULL`)
	if err != nil {
		return time.Time{}, false, err
	}
	defer rows.Close()
	var earliest int64
	for rows.Next() {
		var cp, witness string
		var at int64
		if err := rows.Scan(&cp, &witness, &at); err != nil {
			return time.Time{}, false, err
		}
		if covering[cp] && named[witness] && (!ok || at < earliest) {
			earliest, ok = at, true
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, false, err
	}
	if !ok {
		return time.Time{}, false, nil
	}
	return time.Unix(earliest, 0).UTC(), true, nil
}

// firstLinkingFact is the entry position of the first book fact of bookID
// whose link value's last_received names logID at a size that reaches
// position.
func firstLinkingFact(ctx context.Context, tx *sql.Tx, bookID, logID string, position uint64) (seq uint64, found bool, err error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT entry_seq, link_opening FROM shelf_entries WHERE book_id = ? AND kind = ? ORDER BY entry_seq`, bookID, FactKindBook)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var lo []byte
		if err := rows.Scan(&seq, &lo); err != nil {
			return 0, false, err
		}
		if lo == nil {
			continue
		}
		var opening FactOpening
		var link LinkValue
		if err := errors.Join(json.Unmarshal(lo, &opening), json.Unmarshal(opening.Value, &link)); err != nil {
			return 0, false, fmt.Errorf("%w: stored fact %d's link opening: %v", ErrCorrupt, seq, err)
		}
		if r := link.LastReceived; r != nil && r.LogID == logID && r.Size >= position {
			return seq, true, nil
		}
	}
	return 0, false, rows.Err()
}

// firstCoveringFact is the entry position of the first fact of bookID whose
// size covers position; a journal fact's size is its status opening's.
func firstCoveringFact(ctx context.Context, tx *sql.Tx, bookID string, position uint64) (seq uint64, found bool, err error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT entry_seq, kind, doc, status_opening FROM shelf_entries WHERE book_id = ? AND kind IN (?, ?) ORDER BY entry_seq`,
		bookID, FactKindBook, FactKindJournal)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var doc, so []byte
		if err := rows.Scan(&seq, &kind, &doc, &so); err != nil {
			return 0, false, err
		}
		var fact BookFact
		if err := json.Unmarshal(doc, &fact); err != nil {
			return 0, false, fmt.Errorf("%w: stored fact %d: %v", ErrCorrupt, seq, err)
		}
		size := fact.Size
		if kind == FactKindJournal {
			var opening FactOpening
			var js JournalStatus
			if err := errors.Join(json.Unmarshal(so, &opening), json.Unmarshal(opening.Value, &js)); err != nil {
				return 0, false, fmt.Errorf("%w: stored journal fact %d: %v", ErrCorrupt, seq, err)
			}
			size = js.Size
		}
		if leaves, ok := mmr.LeafCount(size); ok && leaves >= position {
			return seq, true, nil
		}
	}
	return 0, false, rows.Err()
}
