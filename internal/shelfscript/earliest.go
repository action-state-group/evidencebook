// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	evidencebook "github.com/action-state-group/evidencebook"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// The earliest-receipt-not-anchor vector (evidencebook#461,
// docs/design/461-deal-profile.md §8.1, invariant C): on a copy of the
// scripted shelf after RecordTimePhase, one more record and stamp, witnessed
// later than every receipt before it, become the anchor of a handover. The
// earliest named receipt of most records then belongs to a checkpoint before
// the anchor, and a reader of the handover (VerifyHandoverTimes) must give
// each book position the time the holder's RecordTime gives. Every language
// reads testdata/shelf/handover/earliest-receipt-not-anchor.json, replays its
// handover under the script's checkpoint key and directory, and gets every
// query's time.

// EarliestStream is the random stream of the earliest-receipt step.
const EarliestStream = "evidencebook shelf vectors/earliest-receipt"

// EarliestQuery is one record time query and the holder's answer.
type EarliestQuery struct {
	BookID    string   `json:"book_id"`
	LogID     string   `json:"log_id"`
	Position  uint64   `json:"position"`
	Witnesses []string `json:"witnesses"`
	// Time is RFC 3339 UTC, or nil for none.
	Time *string `json:"time"`
}

// EarliestReceiptJSON builds the vector from the shelf at dir/shelf.db
// (after RecordTimePhase), which it leaves as it was.
func EarliestReceiptJSON(ctx context.Context, dir string) ([]byte, error) {
	scratch, err := os.MkdirTemp("", "shelf-earliest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	path := filepath.Join(scratch, FileName)
	original, err := openDB(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	_, err = original.ExecContext(ctx, `VACUUM INTO ?`, path)
	if err := errors.Join(err, original.Close()); err != nil {
		return nil, err
	}
	e, err := open(ctx, path, EarliestStream, Start.Add(14600*time.Second))
	if err != nil {
		return nil, err
	}
	out, err := earliest(ctx, e)
	return out, errors.Join(err, e.release())
}

func earliest(ctx context.Context, e *env) ([]byte, error) {
	if err := e.append(ctx, "deal/a", observation("a", "a9")); err != nil {
		return nil, err
	}
	at := Start.Add(14640 * time.Second)
	e.clock.set(at)
	if _, err := e.cadence.Stamp(ctx, at); err != nil {
		return nil, err
	}
	tick, ok, err := e.cadence.Last(ctx)
	if err != nil || !ok {
		return nil, errors.Join(err, errors.New("no tick to witness"))
	}
	anchorTime := Start.Add(14700 * time.Second)
	receipt, err := MintReceiptAt(tick.Checkpoint, anchorTime)
	if err != nil {
		return nil, err
	}
	if _, err := e.cadence.Witnessed(ctx, tick.N, receipt); err != nil {
		return nil, err
	}
	h, err := e.cadence.Handover(ctx, 0)
	if err != nil {
		return nil, err
	}
	if len(h.Witnessed) == 0 {
		return nil, errors.New("the handover carries no witnessed checkpoint before its anchor")
	}
	// Every book's positions, one past its last, by the holder's RecordTime.
	rows, err := e.db.QueryContext(ctx, `SELECT book_id, log_id, MAX(json_extract(doc, '$.size')) FROM shelf_entries WHERE kind = ? GROUP BY book_id, log_id ORDER BY log_id, book_id`, evidencebook.FactKindBook)
	if err != nil {
		return nil, err
	}
	type book struct {
		id, log string
		size    uint64
	}
	var books []book
	for rows.Next() {
		var b book
		if err := rows.Scan(&b.id, &b.log, &b.size); err != nil {
			rows.Close()
			return nil, err
		}
		books = append(books, b)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	var queries []EarliestQuery
	beforeAnchor := false
	for _, b := range books {
		leaves, ok := mmr.LeafCount(b.size)
		if !ok {
			return nil, fmt.Errorf("book %s's size %d is not an MMR size", b.log, b.size)
		}
		for p := uint64(1); p <= leaves+1; p++ {
			for _, w := range [][]string{{WitnessURL}, {UnnamedWitnessURL}} {
				q := EarliestQuery{BookID: b.id, LogID: b.log, Position: p, Witnesses: w}
				t, ok, err := e.shelf.RecordTime(ctx, b.id, p, w)
				if err != nil {
					return nil, err
				}
				if ok {
					s := t.Format(time.RFC3339)
					q.Time = &s
					beforeAnchor = beforeAnchor || t.Before(anchorTime)
				}
				queries = append(queries, q)
			}
		}
	}
	if !beforeAnchor {
		return nil, errors.New("no record's earliest receipt is before the anchor's")
	}
	return marshal(map[string]any{
		"description": "The earliest-receipt-not-anchor vector of evidencebook#461 (docs/design/461-deal-profile.md §8.1, invariant C), written by internal/shelfscript (EB_WRITE_SHELF_VECTORS=1 go test ./internal/shelfscript). Replay handover under pinned_key and the script's witness directory, then each query's record time (book_id, position, witnesses, which are receipt URLs) from the handover must equal time, the holder's RecordTime: most records' earliest receipt belongs to a witnessed checkpoint before the anchor.",
		"pinned_key":  hex.EncodeToString(CheckpointKey.Public().(ed25519.PublicKey)),
		"handover":    h,
		"queries":     queries,
	})
}
