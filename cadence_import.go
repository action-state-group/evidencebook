// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// LegacyCadence is a profile's cadence as a pre-shelf writer kept it:
// capsule-cli's deal_cadence rows, on the same cadence log, and the logs
// its deal index holds (evidencebook#36 §4, capsule-cli#242 §3.2).
type LegacyCadence struct {
	// Logs are the profile's pre-shelf logs, each a log of the shelf's file
	// that is not a book (capsule-cli: "deal/" + every deal_id of
	// deal_steps, sales included). Each counts into H.
	Logs []string
	// Ticks are the pre-shelf writer's finished ticks (CheckpointSize > 0),
	// in cadence-log order. N is ignored: a tick's identity is its place in
	// the cadence log (EntrySeq) and its checkpoint, and ImportLegacy
	// numbers it. Witnessed and Receipt are set on each tick whose receipt
	// the host has verified; evidencebook verifies no receipt. Every log a
	// tick carries is one of Logs.
	Ticks []CadenceTick
}

// ImportLegacy carries a pre-shelf cadence into this one, in one
// transaction, under the shelf's lock:
//
//   - every log in legacy.Logs is registered: it counts into H;
//   - each tick not already held is stored, numbered after the cadence's
//     last tick, with its due time, leaves, checkpoint and receipt, so the
//     cadence continues from it and Chain rebuilds a bundle anchored on it;
//   - each witnessed tick's leaves get cadence_witnessed rows;
//   - each registered log is live unless it has ended and has not grown
//     past its largest witnessed checkpoint (the #36 rule).
//
// It is re-runnable: a tick already held (the same cadence-log position and
// checkpoint) keeps its number and content, and is only promoted to
// witnessed when it now carries a receipt it did not (a pre-shelf writer
// delivered it since); a later run imports only ticks a pre-shelf writer
// added since, after the cadence's own. A tick at a held position
// with another checkpoint, or older than the newest tick held, is refused:
// it would rewrite what bundles may already name. So is any import while
// the cadence's own last tick is unfinished (a crash after its row): Tick
// resumes only the last tick, so a later tick would strand it; the host
// ticks first, which finishes it. Ended is evaluated before the
// transaction, as for Witnessed, and must not call the shelf.
func (c *Cadence) ImportLegacy(ctx context.Context, legacy LegacyCadence) error {
	logs := map[string]bool{}
	for _, id := range legacy.Logs {
		if id == "" || id == c.cfg.LogID {
			return fmt.Errorf("%w: legacy log %q", ErrInvalid, id)
		}
		logs[id] = true
	}
	ticks := slices.Clone(legacy.Ticks)
	receipts := make([]sql.NullString, len(ticks))
	for i := range ticks {
		tick := &ticks[i]
		if tick.CheckpointSize == 0 || tick.EntrySeq == 0 || len(tick.Checkpoint) == 0 {
			return fmt.Errorf("%w: legacy tick at entry %d is not finished", ErrInvalid, tick.EntrySeq)
		}
		if i > 0 && tick.EntrySeq <= ticks[i-1].EntrySeq {
			return fmt.Errorf("%w: legacy ticks are not in cadence-log order (entry %d after %d)", ErrInvalid, tick.EntrySeq, ticks[i-1].EntrySeq)
		}
		for id := range tick.Leaves.Deals {
			if !logs[id] {
				return fmt.Errorf("%w: legacy tick at entry %d carries %s, which is not a legacy log", ErrInvalid, tick.EntrySeq, id)
			}
		}
		if tick.Witnessed {
			if tick.Receipt == nil {
				return fmt.Errorf("%w: witnessed legacy tick at entry %d has no receipt", ErrInvalid, tick.EntrySeq)
			}
			receipt := *tick.Receipt
			for len(receipt.TSURL) > 0 && receipt.TSURL[len(receipt.TSURL)-1] == '/' {
				receipt.TSURL = receipt.TSURL[:len(receipt.TSURL)-1]
			}
			if receipt.TSURL == "" || receipt.EntryHash == "" || len(receipt.Receipt) == 0 {
				return fmt.Errorf("%w: witnessed legacy tick at entry %d needs its receipt, its entry hash and its witness's URL", ErrInvalid, tick.EntrySeq)
			}
			encoded, err := json.Marshal(receipt)
			if err != nil {
				return err
			}
			receipts[i] = sql.NullString{String: string(encoded), Valid: true}
		}
	}

	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return ErrReleased
	}
	for id := range logs {
		var records int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM book_records WHERE book_id = ?`, id).Scan(&records); err != nil {
			return err
		}
		if records > 0 {
			return fmt.Errorf("%w: %s is a book, not a legacy log", ErrInvalid, id)
		}
	}
	ended := map[string]bool{}
	for id := range logs {
		var err error
		if ended[id], err = c.cfg.Ended(ctx, id); err != nil {
			return err
		}
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var last, lastEntry uint64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(tick), 0), COALESCE(MAX(entry_seq), 0) FROM cadence_ticks`).Scan(&last, &lastEntry); err != nil {
			return err
		}
		var unfinished int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cadence_ticks WHERE checkpoint_size = 0`).Scan(&unfinished); err != nil {
			return err
		}
		if unfinished > 0 {
			return fmt.Errorf("%w: the cadence's last tick is unfinished; tick first, which finishes it, then import", ErrInvalid)
		}
		for id := range logs {
			if err := markLive(ctx, tx, id); err != nil {
				return err
			}
		}
		witness := func(n uint64, tick CadenceTick) error {
			for id, leaf := range tick.Leaves.Deals {
				if _, err := tx.ExecContext(ctx,
					`INSERT OR IGNORE INTO cadence_witnessed (log_id, checkpoint_sha256, mmr_size, tick) VALUES (?, ?, ?, ?)`,
					id, leaf.CheckpointSHA256, leaf.Size, n); err != nil {
					return err
				}
			}
			return nil
		}
		for i, tick := range ticks {
			var held []byte
			var heldN uint64
			var heldWitnessed int
			var heldLeaves string
			err := tx.QueryRowContext(ctx, `SELECT tick, checkpoint, witnessed, leaves FROM cadence_ticks WHERE entry_seq = ? AND checkpoint_size > 0`, tick.EntrySeq).Scan(&heldN, &held, &heldWitnessed, &heldLeaves)
			switch {
			case err == nil:
				if !bytes.Equal(held, tick.Checkpoint) {
					return fmt.Errorf("%w: the cadence holds another tick at entry %d", ErrInvalid, tick.EntrySeq)
				}
				if tick.Witnessed && heldWitnessed == 0 {
					// Delivered since it was imported: witnessed from now on,
					// as Witnessed would record it. A held receipt is kept.
					if _, err := tx.ExecContext(ctx, `UPDATE cadence_ticks SET witnessed = 1, receipt = COALESCE(receipt, ?) WHERE tick = ?`, receipts[i], heldN); err != nil {
						return err
					}
					// The held tick's own leaves are what is indexed: the
					// incoming copy only says it was delivered.
					var stored CadenceTick
					if err := json.Unmarshal([]byte(heldLeaves), &stored.Leaves); err != nil {
						return fmt.Errorf("%w: tick %d leaves: %v", ErrCorrupt, heldN, err)
					}
					if err := witness(heldN, stored); err != nil {
						return err
					}
				}
				continue
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
			if tick.EntrySeq <= lastEntry {
				return fmt.Errorf("%w: legacy tick at entry %d is older than the cadence's newest tick (entry %d)", ErrInvalid, tick.EntrySeq, lastEntry)
			}
			encoded, err := json.Marshal(tick.Leaves)
			if err != nil {
				return err
			}
			last++
			witnessed := 0
			if tick.Witnessed {
				witnessed = 1
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed, receipt) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				last, tick.At.UTC().Format(time.RFC3339), tick.DueNext.UTC().Format(time.RFC3339), tick.EntrySeq, tick.CheckpointSize, tick.Checkpoint,
				string(encoded), witnessed, receipts[i]); err != nil {
				return err
			}
			lastEntry = tick.EntrySeq
			if tick.Witnessed {
				if err := witness(last, tick); err != nil {
					return err
				}
			}
		}
		for id := range logs {
			if !ended[id] {
				continue
			}
			var witnessedSize uint64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(mmr_size), 0) FROM cadence_witnessed WHERE log_id = ?`, id).Scan(&witnessedSize); err != nil {
				return err
			}
			size, err := heldSizeTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if witnessedSize > 0 && size <= witnessedSize {
				if _, err := tx.ExecContext(ctx, `DELETE FROM cadence_live WHERE log_id = ?`, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
