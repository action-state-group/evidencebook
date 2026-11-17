// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	evidencebook "github.com/action-state-group/evidencebook"
)

// The record time vectors (evidencebook#461, docs/design/461-shelf-facts.md
// §2.6): RecordTimePhase runs on the scripted shelf after MigratePhase, and
// RecordTimeJSON is the phase with each step's outcome
// (testdata/shelf/record-time.json); the shelf's dump afterwards is
// after-record-time.json. An implementation agrees when it runs the steps on
// its own shelf after the migrate phase, gets every step's expect, and dumps
// the same bytes. Each witness step carries its receipt, so no
// implementation needs to mint one.

// UnnamedWitnessURL is a witness no record time step names beside the
// script's own: naming only it gives no time.
const UnnamedWitnessURL = "https://unnamed-witness.test"

// RecordTimeStream is the random stream of RecordTimePhase.
const RecordTimeStream = "evidencebook shelf vectors/record-time"

// RecordTimeStep is one step of the record time phase:
//
//   - "open", "release", "append", "stamp": as Step;
//   - "witness": report Receipt for the newest finished tick (Cadence.Witnessed);
//     Expect is {"tick": its number, "state": the check's state};
//   - "record_time": Shelf.RecordTime(BookID, Position, Witnesses) for the
//     book whose log is Book; Expect is {"time": RFC 3339 UTC or null}, or
//     {"error": "invalid"};
//   - "handover": hand the shelf over at its newest stamp and verify it under
//     the script's checkpoint key and directory; Expect is {"witnessed": bool}.
type RecordTimeStep struct {
	Op        string                       `json:"op"`
	Stream    string                       `json:"stream,omitempty"`
	At        int64                        `json:"at,omitempty"`
	Book      string                       `json:"book,omitempty"`
	BookID    string                       `json:"book_id,omitempty"`
	Subject   string                       `json:"subject,omitempty"`
	Payload   string                       `json:"payload,omitempty"`
	Position  uint64                       `json:"position,omitempty"`
	Witnesses []string                     `json:"witnesses,omitempty"`
	IAT       int64                        `json:"-"`
	Receipt   *evidencebook.WitnessReceipt `json:"receipt,omitempty"`
	Expect    map[string]any               `json:"expect,omitempty"`
}

// RecordTimePhase is the phase before its outcomes (positions are the book
// log's, from 1: deal/a holds 17 after MigratePhase and 20 after its append
// here, the journal 6): a record covered only by
// a receipt with no registration time; a new stamp witnessed with a time,
// which gives every record its checkpoint covers that time; a witness not
// named; a position no fact covers; position 0; the journal; the same
// witness again with an earlier time (kept) and with no time (ignored); and
// the handover, witnessed once its anchor's receipt has a time. Witness
// steps' IAT is seconds after Start, 0 for no registration time.
var RecordTimePhase = []RecordTimeStep{
	{Op: "open", Stream: RecordTimeStream, At: 14400},
	{Op: "record_time", Book: "deal/a", Position: 17, Witnesses: []string{WitnessURL}},
	{Op: "record_time", Book: evidencebook.JournalLogID, Position: 1, Witnesses: []string{WitnessURL}},
	{Op: "handover"},
	{Op: "append", Book: "deal/a", Subject: "a", Payload: "a8"},
	{Op: "stamp", At: 14460},
	{Op: "witness", IAT: 14520},
	{Op: "record_time", Book: "deal/a", Position: 20, Witnesses: []string{WitnessURL}},
	{Op: "record_time", Book: "deal/a", Position: 17, Witnesses: []string{UnnamedWitnessURL, WitnessURL}},
	{Op: "record_time", Book: "deal/a", Position: 20, Witnesses: []string{UnnamedWitnessURL}},
	{Op: "record_time", Book: "deal/a", Position: 21, Witnesses: []string{WitnessURL}},
	{Op: "record_time", Book: "deal/a", Position: 0, Witnesses: []string{WitnessURL}},
	{Op: "record_time", Book: evidencebook.JournalLogID, Position: 6, Witnesses: []string{WitnessURL}},
	{Op: "witness", IAT: 14490},
	{Op: "record_time", Book: "deal/a", Position: 20, Witnesses: []string{WitnessURL}},
	{Op: "witness"},
	{Op: "record_time", Book: "deal/a", Position: 20, Witnesses: []string{WitnessURL}},
	{Op: "handover"},
	{Op: "release"},
}

// RecordTimeJSON runs RecordTimePhase on dir/shelf.db (after MigratePhase)
// and returns the vector: the phase with each step's receipt and outcome.
func RecordTimeJSON(ctx context.Context, dir string) ([]byte, error) {
	path := filepath.Join(dir, FileName)
	steps := make([]RecordTimeStep, len(RecordTimePhase))
	copy(steps, RecordTimePhase)
	var e *env
	for i := range steps {
		step := &steps[i]
		err := func() error {
			if (e == nil) != (step.Op == "open") {
				return fmt.Errorf("%s with the shelf %s", step.Op, map[bool]string{true: "released", false: "open"}[e == nil])
			}
			switch step.Op {
			case "open":
				var err error
				e, err = open(ctx, path, step.Stream, Start.Add(time.Duration(step.At)*time.Second))
				return err
			case "release":
				err := e.release()
				e = nil
				return err
			case "append":
				return e.append(ctx, step.Book, observation(step.Subject, step.Payload))
			case "stamp":
				at := Start.Add(time.Duration(step.At) * time.Second)
				e.clock.set(at)
				_, err := e.cadence.Stamp(ctx, at)
				return err
			case "witness":
				tick, ok, err := e.cadence.Last(ctx)
				if err != nil || !ok {
					return errors.Join(err, errors.New("no tick to witness"))
				}
				var iat time.Time
				if step.IAT != 0 {
					iat = Start.Add(time.Duration(step.IAT) * time.Second)
				}
				receipt, err := MintReceiptAt(tick.Checkpoint, iat)
				if err != nil {
					return err
				}
				check, err := e.cadence.Witnessed(ctx, tick.N, receipt)
				if err != nil {
					return err
				}
				step.Receipt = &receipt
				step.Expect = map[string]any{"tick": tick.N, "state": check.State}
				return nil
			case "record_time":
				if err := e.db.QueryRowContext(ctx, `SELECT book_id FROM shelf_entries WHERE log_id = ? ORDER BY entry_seq LIMIT 1`, step.Book).Scan(&step.BookID); err != nil {
					return fmt.Errorf("the book id of %s: %w", step.Book, err)
				}
				t, ok, err := e.shelf.RecordTime(ctx, step.BookID, step.Position, step.Witnesses)
				switch {
				case errors.Is(err, evidencebook.ErrInvalid):
					step.Expect = map[string]any{"error": "invalid"}
				case err != nil:
					return err
				case ok:
					step.Expect = map[string]any{"time": t.Format(time.RFC3339)}
				default:
					step.Expect = map[string]any{"time": nil}
				}
				return nil
			case "handover":
				h, err := e.cadence.Handover(ctx, 0)
				if err != nil {
					return err
				}
				directory, err := Directory()
				if err != nil {
					return err
				}
				result, err := evidencebook.VerifyHandover(h, hex.EncodeToString(CheckpointKey.Public().(ed25519.PublicKey)), directory)
				if err != nil {
					return err
				}
				step.Expect = map[string]any{"witnessed": result.Witnessed}
				return nil
			}
			return fmt.Errorf("unknown step %q", step.Op)
		}()
		if err != nil {
			if e != nil {
				err = errors.Join(err, e.release())
			}
			return nil, fmt.Errorf("record time step %d (%s): %w", i+1, step.Op, err)
		}
	}
	if e != nil {
		return nil, errors.Join(errors.New("the phase left the shelf open"), e.release())
	}
	return marshal(map[string]any{
		"description": "The record time vectors of evidencebook#461 (docs/design/461-shelf-facts.md §2.6), written by internal/shelfscript (EB_WRITE_SHELF_VECTORS=1 go test ./internal/shelfscript). Run the steps on the scripted shelf after migrate_phase (script.json); every step's expect must match, and the dump afterwards is after-record-time.json. A witness step reports its receipt for the newest finished tick; a record_time step calls RecordTime(book_id, position, witnesses), witnesses being receipt URLs; a handover step hands the shelf over at its newest stamp and verifies it.",
		"steps":       steps,
	})
}
