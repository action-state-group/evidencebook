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

	"github.com/action-state-group/checkpointed-local-log/go/mmr"
	evidencebook "github.com/action-state-group/evidencebook"
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
// query's time; and it refuses the handover with its forked checkpoint in
// place of a witnessed one.

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
// (after RecordTimePhase), which it leaves as it was. Its forked member is
// the witnessed checkpoint of the same step on a second copy that appends
// another record: the holder's key, the shelf log and the anchor's size,
// with another root, which a handover must refuse as a witnessed checkpoint.
func EarliestReceiptJSON(ctx context.Context, dir string) ([]byte, error) {
	scratch, err := os.MkdirTemp("", "shelf-earliest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	original, err := openDB(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	var envs []*env
	for _, name := range []string{"main", "forked"} {
		path := filepath.Join(scratch, name, FileName)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, errors.Join(err, original.Close())
		}
		if _, err := original.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
			return nil, errors.Join(err, original.Close())
		}
		e, err := open(ctx, path, EarliestStream, Start.Add(14600*time.Second))
		if err != nil {
			return nil, errors.Join(err, original.Close())
		}
		envs = append(envs, e)
	}
	release := func(err error) error {
		for _, e := range envs {
			err = errors.Join(err, e.release())
		}
		return errors.Join(err, original.Close())
	}
	forked, _, err := earliestStep(ctx, envs[1], "a9-forked")
	if err != nil {
		return nil, release(err)
	}
	out, err := earliest(ctx, envs[0], forked)
	return out, release(err)
}

// earliestStep appends payload to deal/a, stamps, and witnesses the stamp
// later than every receipt before it.
func earliestStep(ctx context.Context, e *env, payload string) (evidencebook.HandoverCheckpoint, time.Time, error) {
	if err := e.append(ctx, "deal/a", observation("a", payload)); err != nil {
		return evidencebook.HandoverCheckpoint{}, time.Time{}, err
	}
	at := Start.Add(14640 * time.Second)
	e.clock.set(at)
	if _, err := e.cadence.Stamp(ctx, at); err != nil {
		return evidencebook.HandoverCheckpoint{}, time.Time{}, err
	}
	tick, ok, err := e.cadence.Last(ctx)
	if err != nil || !ok {
		return evidencebook.HandoverCheckpoint{}, time.Time{}, errors.Join(err, errors.New("no tick to witness"))
	}
	anchorTime := Start.Add(14700 * time.Second)
	receipt, err := MintReceiptAt(tick.Checkpoint, anchorTime)
	if err != nil {
		return evidencebook.HandoverCheckpoint{}, time.Time{}, err
	}
	if _, err := e.cadence.Witnessed(ctx, tick.N, receipt); err != nil {
		return evidencebook.HandoverCheckpoint{}, time.Time{}, err
	}
	return evidencebook.HandoverCheckpoint{Checkpoint: tick.Checkpoint, Receipts: []evidencebook.WitnessReceipt{receipt}}, anchorTime, nil
}

func earliest(ctx context.Context, e *env, forked evidencebook.HandoverCheckpoint) ([]byte, error) {
	anchor, anchorTime, err := earliestStep(ctx, e, "a9")
	if err != nil {
		return nil, err
	}
	if string(anchor.Checkpoint) == string(forked.Checkpoint) {
		return nil, errors.New("the forked copy cut the same checkpoint")
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
		"description": "The earliest-receipt-not-anchor vector of evidencebook#461 (docs/design/461-deal-profile.md §8.1, invariant C), written by internal/shelfscript (EB_WRITE_SHELF_VECTORS=1 go test ./internal/shelfscript). Replay handover under pinned_key and the script's witness directory, then each query's record time (book_id, position, witnesses, which are receipt URLs) from the handover must equal time, the holder's RecordTime: most records' earliest receipt belongs to a witnessed checkpoint before the anchor. forked is a checkpoint by the same key, of the same shelf log and at the anchor's size, cut on a copy that appended another record: a handover with it in place of a witnessed checkpoint must be refused, since the handed-over entries do not recompute its root.",
		"pinned_key":  hex.EncodeToString(CheckpointKey.Public().(ed25519.PublicKey)),
		"handover":    h,
		"queries":     queries,
		"forked":      forked,
	})
}
