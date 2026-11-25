// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"slices"
	"strings"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// disputeOf is a dispute record's body.
func disputeOf(r xdealv1.Record) (*xdealv1.DisputeBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.DisputeBody:
		return b, true
	case xdealv1.DisputeBody:
		return &b, true
	}
	return nil, false
}

// openDisputeOn reads the holder's disputes on row: the place of the open
// one (open), and the last dispute record on the row with its place.
func openDisputeOn(book []xdealv1.Record, row string) (seq int, open bool, last *xdealv1.DisputeBody, lastSeq int) {
	for i, r := range book {
		b, ok := disputeOf(r)
		if !ok || b.Row != row {
			continue
		}
		last, lastSeq = b, i+1
		switch b.Kind {
		case xdealv1.KindDispute:
			seq, open = i+1, true
		case xdealv1.KindWithdrawal:
			seq, open = 0, false
		}
	}
	return seq, open, last, lastSeq
}

// statementsOnRow are the references to the holder's own statement records
// on row that exist now: what a dispute cites (design §3.3). Only statement
// records count, not the statements an act carries.
func statementsOnRow(book []xdealv1.Record, row string) ([]xdealv1.Ref, error) {
	refs := []xdealv1.Ref{}
	for _, r := range book {
		var items []xdealv1.StatementItem
		switch b := r.Body.(type) {
		case *xdealv1.StatementBody:
			items = b.Statements
		case xdealv1.StatementBody:
			items = b.Statements
		default:
			continue
		}
		if !slices.ContainsFunc(items, func(it xdealv1.StatementItem) bool { return it.Row == row }) {
			continue
		}
		d, err := xdealv1.Digest(r)
		if err != nil {
			return nil, err
		}
		refs = append(refs, xdealv1.RefTo(d))
	}
	return refs, nil
}

// disputeDeal reads a deal for a dispute call: the holder's book, whose
// first record is its agreement, and that agreement.
func (s *Store) disputeDeal(ctx context.Context, dealID string) (*Deal, []xdealv1.Record, *xdealv1.AgreeBody, error) {
	d, book, err := s.agreedDeal(ctx, dealID)
	if err != nil {
		return nil, nil, nil, err
	}
	ag, ok := agreeOf(book[0])
	if !ok || ag.Kind != xdealv1.KindAgreement {
		return nil, nil, nil, inputError("the first record of your deal book is not your agreement")
	}
	return d, book, ag, nil
}

// DisputeIn is a dispute's input: the row disputed, as the offer names it,
// and the forum, one of the agreement's ("" when the agreement names only
// one).
type DisputeIn struct {
	Row, Forum string
	// Before is the caller's own check of the record (nil: none).
	Before BeforeSeal
}

// Dispute seals a dispute record in the holder's deal book of dealID: it
// names the adjudicator and the forum the holder's agreement accepts, and
// cites the holder's own statements on the row that exist now. The same
// row's open dispute is returned again, never sealed twice. An agreement
// that names no adjudicator allows no dispute.
func (s *Store) Dispute(ctx context.Context, dealID string, in DisputeIn) (Acted, error) {
	d, book, ag, err := s.disputeDeal(ctx, dealID)
	if err != nil {
		return Acted{}, err
	}
	if ag.Adjudicator.IsNull() || !ag.Adjudicator.Present {
		return Acted{}, inputError("the agreement of deal " + dealID + " names no adjudicator and no forum, so no dispute is possible")
	}
	if err := s.rowOfOffer(ctx, d, in.Row); err != nil {
		return Acted{}, err
	}
	seq, open, _, _ := openDisputeOn(book, in.Row)
	if open {
		prev, _ := disputeOf(book[seq-1])
		if in.Forum != "" && in.Forum != prev.Forum {
			return Acted{}, coded(CodeDisputeOpen, "", "you already have an open dispute on row "+in.Row+" in forum "+prev.Forum+": take it back with Store.WithdrawDispute first", in.Row, prev.Forum)
		}
		return s.again(ctx, d, seq, book[seq-1])
	}
	forum := in.Forum
	if forum == "" {
		if len(ag.Forums) != 1 {
			return Acted{}, fieldError("forum", " is required: the agreement accepts "+strings.Join(ag.Forums, ", "))
		}
		forum = ag.Forums[0]
	}
	stmts, err := statementsOnRow(book, in.Row)
	if err != nil {
		return Acted{}, err
	}
	body := xdealv1.DisputeBody{
		Kind: xdealv1.KindDispute, Row: in.Row, Statements: stmts,
		Adjudicator: ag.Adjudicator.V.KeyID, Forum: forum,
	}
	agreement := book[0]
	// The finished record is held to the holder's own agreement before
	// anything is stored: the adjudicator and forum it names are the
	// agreement's, and it is bound to that agreement.
	validate := func(rec xdealv1.Record) error {
		if err := xdealv1.CheckDispute(rec, agreement); err != nil {
			return inputError("the dispute does not pass the x-deal-v1 checks: " + err.Error())
		}
		return nil
	}
	return s.sealRecord(ctx, dealID, BookRecord{Body: body, Validate: validate}, in.Before)
}

// rowOfOffer refuses a row the deal's offer does not name.
func (s *Store) rowOfOffer(ctx context.Context, d *Deal, row string) error {
	held, err := s.HeldOffer(ctx, d.OfferDigest)
	if err != nil {
		return err
	}
	if held == nil {
		return inputError("this profile no longer holds the offer of deal " + d.DealID)
	}
	prop, _ := proposeOf(held.Record)
	if prop == nil || !slices.ContainsFunc(prop.TermsRows.Rows, func(r xdealv1.Row) bool { return r.ID == row }) {
		return inputError("deal " + d.DealID + " has no row " + row + ": the rows are the ones its offer names")
	}
	return nil
}

// WithdrawIn is a withdrawal's input: the row whose open dispute the holder
// takes back.
type WithdrawIn struct {
	Row string
	// Before is the caller's own check of the record (nil: none).
	Before BeforeSeal
}

// WithdrawDispute seals the withdrawal of the holder's open dispute on a row
// of dealID. A withdrawal already sealed is returned again, never sealed
// twice.
func (s *Store) WithdrawDispute(ctx context.Context, dealID string, in WithdrawIn) (Acted, error) {
	d, book, ag, err := s.disputeDeal(ctx, dealID)
	if err != nil {
		return Acted{}, err
	}
	if ag.Adjudicator.IsNull() || !ag.Adjudicator.Present {
		return Acted{}, inputError("the agreement of deal " + dealID + " names no adjudicator and no forum, so no dispute is possible")
	}
	if err := s.rowOfOffer(ctx, d, in.Row); err != nil {
		return Acted{}, err
	}
	_, open, last, lastSeq := openDisputeOn(book, in.Row)
	if !open {
		if last != nil && last.Kind == xdealv1.KindWithdrawal {
			return s.again(ctx, d, lastSeq, book[lastSeq-1])
		}
		return Acted{}, inputError("you have no open dispute on row " + in.Row + " of deal " + dealID + " to take back")
	}
	return s.sealRecord(ctx, dealID, BookRecord{Body: xdealv1.DisputeBody{Kind: xdealv1.KindWithdrawal, Row: in.Row}}, in.Before)
}

// ChallengeIn is a challenge's input: the digest of a ruling of the deal the
// holder holds, and the SHA-256 (64 lowercase hex) of the holder's grounds,
// which stay with the holder.
type ChallengeIn struct {
	Ruling, GroundsCommitment string
	// Before is the caller's own check of the record (nil: none).
	Before BeforeSeal
}

// Challenge seals a dispute of kind challenge in the holder's deal book of
// dealID (evidencebook#773): it names a ruling of the deal's adjudicator the
// holder holds and the SHA-256 of its grounds. A challenge changes no row's
// status: the first final ruling stays effective and is never replaced. The
// same challenge again is returned, never sealed twice.
func (s *Store) Challenge(ctx context.Context, dealID string, in ChallengeIn) (Acted, error) {
	d, book, ag, err := s.disputeDeal(ctx, dealID)
	if err != nil {
		return Acted{}, err
	}
	if ag.Adjudicator.V == nil {
		return Acted{}, inputError("the agreement of deal " + dealID + " names no adjudicator, so it has no ruling to challenge")
	}
	_, found, err := xdealv1.HeldRuling(ctx, s.cfg.Shelf, dealID, ag.Adjudicator.V.KeyID, in.Ruling)
	if err != nil {
		return Acted{}, err
	}
	if !found {
		return Acted{}, coded(CodeChallengeUnknownRuling, "", "ruling "+in.Ruling+" is unavailable: this profile holds no such ruling of deal "+dealID+
			" from its adjudicator; receive the adjudicator's record (Shelf.Receive) first", in.Ruling, dealID)
	}
	for i, r := range book {
		if b, ok := disputeOf(r); ok && b.Kind == xdealv1.KindChallenge && b.Ruling != nil &&
			b.Ruling.Digest == in.Ruling && b.GroundsCommitment == in.GroundsCommitment {
			return s.again(ctx, d, i+1, r)
		}
	}
	ref := xdealv1.RefTo(in.Ruling)
	body := xdealv1.DisputeBody{Kind: xdealv1.KindChallenge, Ruling: &ref, GroundsCommitment: in.GroundsCommitment}
	return s.sealRecord(ctx, dealID, BookRecord{Body: body}, in.Before)
}
