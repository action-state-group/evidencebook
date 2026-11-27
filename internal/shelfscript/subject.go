// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	evidencebook "github.com/action-state-group/evidencebook"
)

// The subject vectors (evidencebook#116 design note section 4; the Rust and
// Python ports, #1012 and #1013): SubjectPhase runs on the scripted shelf
// after ContinuePhase, and SubjectJSON is the phase with each step's outcome
// (testdata/shelf/subject.json); the shelf's dump afterwards is
// after-subject.json. An implementation agrees when it runs the steps on its
// own shelf after the continue phase, gets every step's expect, and dumps the
// same bytes: the same sealed subject records, the same refusals, and the
// same index_root records, the subject_id index's among them once it holds a
// leaf and never in a book with no subject.

// SubjectStream is the random stream of SubjectPhase.
const SubjectStream = "evidencebook shelf vectors/subject"

// SubjectStep is one step of the subject phase:
//
//   - "open", "release", "append": as Step;
//   - "subject_root": seal a subject_root in Book (SealSubjectRoot) declaring
//     {SubjectKind, SubjectID, Membership}, with no further header members;
//   - "member": append to Book an observation with the one payload Payload,
//     correlation.exchange_id Exchange, citing the subject_root this phase
//     sealed for SubjectID;
//   - "close": close the subject this phase rooted for SubjectID
//     (CloseSubject);
//   - "checkpoint": checkpoint Book (Book.Checkpoint), which first commits
//     each changed index root.
//
// Expect is "ok", or "invalid" when the book refuses the step (ErrInvalid)
// and nothing is written.
type SubjectStep struct {
	Op          string `json:"op"`
	Stream      string `json:"stream,omitempty"`
	At          int64  `json:"at,omitempty"`
	Book        string `json:"book,omitempty"`
	Subject     string `json:"subject,omitempty"`
	Payload     string `json:"payload,omitempty"`
	SubjectKind string `json:"subject_kind,omitempty"`
	SubjectID   string `json:"subject_id,omitempty"`
	Membership  string `json:"membership,omitempty"`
	Exchange    string `json:"exchange,omitempty"`
	Expect      string `json:"expect,omitempty"`
}

// SubjectPhase is the phase before its outcomes: a field subject in a book
// that already holds a record (deal/s), with a refused member, a checkpoint,
// a second root refused, a close, a member after the close refused; a
// per-subject book (deal/l, a log root at seq 1) and its close; a checkpoint
// of a book with no subject (deal/a), which commits no subject_id root; and,
// after a reopen, the close still holding.
var SubjectPhase = []SubjectStep{
	{Op: "open", Stream: SubjectStream, At: 9000},
	{Op: "append", Book: "deal/s", Subject: "s", Payload: "s1"},
	{Op: "subject_root", Book: "deal/s", SubjectKind: "exchange", SubjectID: "x-1", Membership: evidencebook.MembershipExchangeID},
	{Op: "member", Book: "deal/s", SubjectID: "x-1", Exchange: "x-1", Payload: "m1"},
	{Op: "member", Book: "deal/s", SubjectID: "x-1", Exchange: "x-2", Payload: "bad"},
	{Op: "checkpoint", Book: "deal/s"},
	{Op: "member", Book: "deal/s", SubjectID: "x-1", Exchange: "x-1", Payload: "m2"},
	{Op: "subject_root", Book: "deal/s", SubjectKind: "exchange", SubjectID: "x-1", Membership: evidencebook.MembershipExchangeID},
	{Op: "subject_root", Book: "deal/s", SubjectKind: "deal", SubjectID: "l-2", Membership: evidencebook.MembershipLog},
	{Op: "close", Book: "deal/s", SubjectID: "x-1"},
	{Op: "member", Book: "deal/s", SubjectID: "x-1", Exchange: "x-1", Payload: "late"},
	{Op: "close", Book: "deal/s", SubjectID: "x-1"},
	{Op: "checkpoint", Book: "deal/s"},
	{Op: "subject_root", Book: "deal/l", SubjectKind: "deal", SubjectID: "l-1", Membership: evidencebook.MembershipLog},
	{Op: "append", Book: "deal/l", Subject: "l", Payload: "l1"},
	{Op: "subject_root", Book: "deal/l", SubjectKind: "exchange", SubjectID: "x-3", Membership: evidencebook.MembershipExchangeID},
	{Op: "close", Book: "deal/l", SubjectID: "l-1"},
	{Op: "append", Book: "deal/l", Subject: "l", Payload: "l2"},
	{Op: "checkpoint", Book: "deal/l"},
	{Op: "checkpoint", Book: "deal/a"},
	{Op: "release"},
	{Op: "open", Stream: SubjectStream + "/reopen", At: 9600},
	{Op: "member", Book: "deal/s", SubjectID: "x-1", Exchange: "x-1", Payload: "after reopen"},
	{Op: "append", Book: "deal/s", Subject: "s", Payload: "s2"},
	{Op: "checkpoint", Book: "deal/s"},
	{Op: "release"},
}

// SubjectJSON runs SubjectPhase on dir/shelf.db (after ContinuePhase) and
// returns the vector: the phase with each step's outcome.
func SubjectJSON(ctx context.Context, dir string) ([]byte, error) {
	path := filepath.Join(dir, FileName)
	steps := make([]SubjectStep, len(SubjectPhase))
	copy(steps, SubjectPhase)
	roots := map[string]string{}
	var e *env
	for i := range steps {
		step := &steps[i]
		err := func() error {
			if (e == nil) != (step.Op == "open") {
				return fmt.Errorf("%s with the shelf %s", step.Op, map[bool]string{true: "released", false: "open"}[e == nil])
			}
			if step.Op == "open" {
				var err error
				e, err = open(ctx, path, step.Stream, Start.Add(time.Duration(step.At)*time.Second))
				return err
			}
			if step.Op == "release" {
				err := e.release()
				e = nil
				return err
			}
			book, err := e.shelf.Open(ctx, step.Book)
			if err != nil {
				return err
			}
			switch step.Op {
			case "append":
				_, err = book.Append(ctx, observation(step.Subject, step.Payload))
			case "subject_root":
				var root evidencebook.Record
				root, err = book.SealSubjectRoot(ctx, evidencebook.SubjectRootStatement{SubjectKind: step.SubjectKind, SubjectID: step.SubjectID, Membership: step.Membership}, evidencebook.Entry{})
				if err == nil {
					roots[step.SubjectID] = root.RecordID
				}
			case "member":
				_, err = book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent,
					Correlation: evidencebook.Correlation{ExchangeID: step.Exchange}, Links: []evidencebook.Link{{Type: evidencebook.Cites, Target: roots[step.SubjectID]}},
					Payloads: [][]byte{[]byte(step.Payload)}})
			case "close":
				_, err = book.CloseSubject(ctx, roots[step.SubjectID])
			case "checkpoint":
				_, err = book.Checkpoint(ctx)
			default:
				return fmt.Errorf("unknown step %q", step.Op)
			}
			switch {
			case errors.Is(err, evidencebook.ErrInvalid):
				step.Expect = "invalid"
			case err != nil:
				return err
			default:
				step.Expect = "ok"
			}
			return nil
		}()
		if err != nil {
			if e != nil {
				err = errors.Join(err, e.release())
			}
			return nil, fmt.Errorf("subject step %d (%s): %w", i+1, step.Op, err)
		}
	}
	if e != nil {
		return nil, errors.Join(errors.New("the phase left the shelf open"), e.release())
	}
	return marshal(map[string]any{
		"description": "The subject vectors (evidencebook#116 design note section 4; #1012, #1013), written by internal/shelfscript (EB_WRITE_SHELF_VECTORS=1 go test ./internal/shelfscript). Run the steps on the scripted shelf after continue_phase (script.json); every step's expect must match (invalid: the book refuses the step and writes nothing), and the dump afterwards is after-subject.json. subject_root seals a subject_root record declaring {subject_kind, subject_id, membership}; member appends an observation (epistemic_type observed_event) with the one payload, correlation.exchange_id the step's exchange, and one cites link to the subject_root this phase sealed for subject_id; close closes that subject; checkpoint checkpoints the book, committing each changed index root first.",
		"steps":       steps,
	})
}
