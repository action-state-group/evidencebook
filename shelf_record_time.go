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
// shelf_receipts, one per witness, with the registration time the witness
// put in it (the iat of its protected CWT claims) or none. A witness is
// named by its receipt's URL (ts_url), the name a witness directory row is
// found by, so a witness an agreement names is that URL. Each row also keeps
// the directory key id the receipt verified under: a URL is a locator, and
// the key id lets a later naming by key read the rows already held.

// upsertReceipt keeps a verified receipt for the checkpoint whose SHA-256 is
// checkpointSHA256. Another receipt from the same witness replaces the held
// one only when it gives an earlier registration time, or the held one gives
// none: a witness's earliest registration is the one a record time reads.
const upsertReceipt = `INSERT INTO shelf_receipts (checkpoint_sha256, witness_id, key_id, receipt, registered_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (checkpoint_sha256, witness_id) DO UPDATE SET key_id = excluded.key_id, receipt = excluded.receipt, registered_at = excluded.registered_at
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
			sum := sha256.Sum256(h.statement)
			var present int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ? AND witness_id = ?`,
				hex.EncodeToString(sum[:]), receipt.TSURL).Scan(&present); err != nil {
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
	rows, err := tx.QueryContext(ctx, `SELECT checkpoint_sha256, witness_id, registered_at FROM shelf_receipts WHERE registered_at IS NOT NULL`)
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
