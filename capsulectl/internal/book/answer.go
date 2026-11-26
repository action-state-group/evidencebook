// SPDX-License-Identifier: Apache-2.0

package book

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// answerResult is what `acknowledge` and `rebut` print: the record that
// answers the counterparty's Close, and whether an earlier run had already
// written it.
type answerResult struct {
	RecordID     string `json:"record_id"`
	Seq          uint64 `json:"seq"`
	RecordType   string `json:"record_type"`
	Period       string `json:"period"`
	Counterparty string `json:"counterparty"`
	PeerClose    string `json:"peer_close"`
	// Book is the id of the book holding the record: on a windowed store,
	// the window's log id, which the counterparty reads it under.
	Book string `json:"book"`
	// Window is the window whose book holds the record, on a windowed store.
	Window          string `json:"window,omitempty"`
	AlreadyRecorded bool   `json:"already_recorded"`
}

// rebuttalStatement is a rebuttal record's statement: why this book
// disputes the counterparty's Close.
type rebuttalStatement struct {
	Reason string `json:"reason"`
}

func acknowledgeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "acknowledge", Short: "Acknowledge the counterparty's Close for one period: this book has seen it and agrees, which makes it AGREED (signs; a repeat adds nothing)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		return answerPeerClose(c, evidencebook.RecordTypeAcknowledgement, nil)
	}}
	periodFlags(cmd)
	return cmd
}

func rebutCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "rebut", Short: "Rebut the counterparty's Close for one period: this book disputes it, which makes it CONTESTED (signs; a repeat adds nothing)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		reason, _ := c.Flags().GetString("reason")
		if strings.TrimSpace(reason) == "" {
			return cli.InputError("--reason is required: why this book disputes the counterparty's Close")
		}
		raw, err := json.Marshal(rebuttalStatement{Reason: reason})
		if err != nil {
			return err
		}
		return answerPeerClose(c, evidencebook.RecordTypeRebuttal, raw)
	}}
	periodFlags(cmd)
	cmd.Flags().String("reason", "", "Why this book disputes the Close, in your own words (required); sealed in the rebuttal, which the counterparty reads")
	return cmd
}

// answerPeerClose writes this book's acknowledgement or rebuttal of the
// counterparty's Close for a period: the Close `book close` would cite,
// read from a peer bundle under the pinned key. The record goes in the
// current book (on a windowed store, the current window, which takes one at
// any time), and a checkpoint covers it, so a bundle can carry it to the
// counterparty.
func answerPeerClose(c *cobra.Command, recordType string, reason json.RawMessage) (err error) {
	p, err := cli.Selected(c)
	if err != nil {
		return err
	}
	a, err := periodAndPeer(c)
	if err != nil {
		return err
	}
	if !a.hasPeer {
		return cli.InputError("--peer is required: a held Evidence Bundle from the counterparty's book that discloses its Close")
	}
	opened, err := openBook(c.Context(), p, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, opened.release()) }()
	ownBook, err := periodBookID(opened, a.period)
	if err != nil {
		return err
	}
	cited, err := currentPeerClose(a.peer, ownBook, a.period)
	if err != nil {
		return err
	}
	if cited == nil {
		return cli.InputError(fmt.Sprintf("the peer bundle discloses no Close of %s's for %s naming this book (%s) as its counterparty", a.counterparty, a.period.key, ownBook))
	}
	peerClose := cited.record.RecordID
	book := opened.book
	result := answerResult{RecordType: recordType, Period: a.period.key, Counterparty: a.counterparty, PeerClose: peerClose}
	prior, err := priorAnswer(c.Context(), opened, a.counterparty, peerClose, recordType)
	if err != nil {
		return err
	}
	var record evidencebook.Record
	switch {
	case prior != nil && recordType == evidencebook.RecordTypeRebuttal && !sameReason(prior.Header.Statement, reason):
		return cli.InputError(fmt.Sprintf("this book already rebutted Close %s at position %d with: %s. That rebuttal is sealed and is not replaced; repeat it with the same --reason to read it back", peerClose, prior.Seq, reasonOf(prior.Header.Statement)))
	case prior != nil:
		record, result.AlreadyRecorded = *prior, true
	case recordType == evidencebook.RecordTypeAcknowledgement:
		rebutted, err := priorAnswer(c.Context(), opened, a.counterparty, peerClose, evidencebook.RecordTypeRebuttal)
		if err != nil {
			return err
		}
		if rebutted != nil {
			return cli.InputError(fmt.Sprintf("this book already rebutted Close %s at position %d with: %s. That rebuttal is sealed and outranks an acknowledgement, so the Close stays CONTESTED; if the counterparty seals a new Close that supersedes this one, acknowledge that one instead", peerClose, rebutted.Seq, reasonOf(rebutted.Header.Statement)))
		}
		if record, err = book.Acknowledge(c.Context(), peerClose, a.counterparty); err != nil {
			return bookError(err)
		}
	default:
		if record, err = book.Rebut(c.Context(), peerClose, a.counterparty, reason); err != nil {
			return bookError(err)
		}
	}
	// A repeat after a run that wrote the record but failed before its
	// checkpoint checkpoints it now: the counterparty counts only a record
	// a checkpoint covers. A record in an earlier window is under that
	// window's boundary checkpoint already.
	current, _ := book.Info()
	if record.Header.BookID == current {
		if covered, err := book.CheckpointCovers(c.Context(), record.Seq); err != nil {
			return bookError(err)
		} else if !covered {
			if _, err = book.Checkpoint(c.Context()); err != nil {
				return bookError(err)
			}
		}
	}
	result.RecordID, result.Seq, result.Book = record.RecordID, record.Seq, record.Header.BookID
	if opened.shelf != nil {
		result.Window = strings.TrimPrefix(record.Header.BookID, opened.shelf.WindowPolicy().Base+"/")
	}
	return cli.Output(c, result)
}

// periodBookID is the book id a counterparty's Close for p names as its
// counterparty: on a JSONL book, the book's; on a windowed store, the log
// id of the window holding p's start, or of the predecessor book for a
// period before the conversion.
func periodBookID(o openedBook, p period) (string, error) {
	if o.shelf == nil {
		id, _ := o.book.Info()
		return id, nil
	}
	policy := o.shelf.WindowPolicy()
	if policy.Predecessor != nil && p.start.Before(policy.Since) {
		return policy.Predecessor.LogID, nil
	}
	return policy.Period.WindowAt(p.start).LogID(policy.Base), nil
}

// priorAnswer returns this book's record of type recordType that links to
// peerClose for counterparty, or nil; on a windowed store, from any
// window's book, so a repeat after a roll adds nothing.
func priorAnswer(ctx context.Context, opened openedBook, counterparty, peerClose, recordType string) (*evidencebook.Record, error) {
	link := evidencebook.Acknowledges
	if recordType == evidencebook.RecordTypeRebuttal {
		link = evidencebook.Rebuts
	}
	records, err := everyWindowQuery(ctx, opened, evidencebook.Filter{RecordType: recordType, CounterpartyRef: counterparty})
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if slices.Contains(r.Header.LinksTo(link), peerClose) {
			return &r, nil
		}
	}
	return nil, nil
}

// sameReason reports whether two rebuttal statements state the same reason.
func sameReason(a, b json.RawMessage) bool {
	var x, y rebuttalStatement
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && x == y
}

// reasonOf is the reason a rebuttal statement states, quoted.
func reasonOf(statement json.RawMessage) string {
	var r rebuttalStatement
	if json.Unmarshal(statement, &r) != nil {
		return "(no readable reason)"
	}
	return fmt.Sprintf("%q", r.Reason)
}
