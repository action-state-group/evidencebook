// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// BeforeSeal is the caller's own check of a deal-book record, run on the
// finished record after this package's checks (for an act: the first-act
// gate and its authorization, which auth then holds; nil for a record that
// is no act) and before anything is stored. An error refuses the record and
// is returned as it is. It runs only for a record about to be sealed, never
// for one an earlier call already sealed.
type BeforeSeal func(rec xdealv1.Record, auth *xdealv1.ActAuthorization) error

// Acted is a deal-book record a call sealed, or found already sealed by an
// earlier call with the same input (Again; nothing new is sealed then). Gate
// is the first-act gate's answer when it ran (an act sealed now), and
// Authorization the act's authorization (any act, sealed now or found).
type Acted struct {
	Sealed
	Again         bool
	Gate          *xdealv1.ActGate
	Authorization *xdealv1.ActAuthorization
}

// actDeal is what a call on an agreed deal reads first: the deal, the
// holder's book (its agreement first) and the offer's body.
type actDeal struct {
	d     *Deal
	book  []xdealv1.Record
	offer *xdealv1.ProposeBody
}

// agreedDeal reads a deal the holder has agreed, repairing an agreement a
// stopped run appended but did not record.
func (s *Store) agreedDeal(ctx context.Context, dealID string) (*Deal, []xdealv1.Record, error) {
	d, err := s.Deal(ctx, dealID)
	if err != nil {
		return nil, nil, err
	}
	if d == nil {
		return nil, nil, inputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
	}
	if d.OwnLogID != "" {
		if err := s.repairIfStopped(ctx, d); err != nil {
			return nil, nil, err
		}
	}
	if d.OwnAgreementID == "" {
		return nil, nil, coded(CodeNoAgreement, "", "deal "+dealID+" has no agreement of yours yet: Store.Agree opens your deal book", dealID)
	}
	book, err := s.OwnBook(ctx, d)
	if err != nil {
		return nil, nil, err
	}
	return d, book, nil
}

func (s *Store) loadActDeal(ctx context.Context, dealID string) (*actDeal, error) {
	d, book, err := s.agreedDeal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	held, err := s.HeldOffer(ctx, d.OfferDigest)
	if err != nil {
		return nil, err
	}
	if held == nil {
		return nil, inputError("this profile no longer holds the offer of deal " + dealID)
	}
	prop, ok := proposeOf(held.Record)
	if !ok {
		return nil, conflictf("the offer of deal %s is not an offer", dealID)
	}
	return &actDeal{d: d, book: book, offer: prop}, nil
}

func (a *actDeal) row(id string) (*xdealv1.Row, error) {
	for i := range a.offer.TermsRows.Rows {
		if a.offer.TermsRows.Rows[i].ID == id {
			return &a.offer.TermsRows.Rows[i], nil
		}
	}
	return nil, inputError("deal " + a.d.DealID + " has no row " + id + ": the rows are the ones its offer names")
}

// checkStatements holds statements to the offer's rows (PROFILE §3.3): the
// value is met, not_met or cannot_tell, a remedy is one the row allows, and
// a row is stated once in a record.
func (a *actDeal) checkStatements(items []xdealv1.StatementItem) ([]xdealv1.StatementItem, error) {
	out := []xdealv1.StatementItem{}
	seen := map[string]bool{}
	for _, it := range items {
		name := it.Row + "=" + it.Value
		if it.Remedy != "" {
			name += ":" + it.Remedy
		}
		switch it.Value {
		case "met", "not_met", "cannot_tell":
		default:
			return nil, fieldError("statement", " "+name+": the value is met, not_met or cannot_tell")
		}
		r, err := a.row(it.Row)
		if err != nil {
			return nil, err
		}
		if it.Remedy != "" && !slices.ContainsFunc(r.Remedies, func(rr xdealv1.RowRemedy) bool { return rr.ID == it.Remedy }) {
			return nil, fieldError("statement", " "+name+": row "+it.Row+" allows no remedy "+it.Remedy)
		}
		if seen[it.Row] {
			return nil, fieldError("statement", " names row "+it.Row+" twice: one statement per row in a record")
		}
		seen[it.Row] = true
		out = append(out, xdealv1.StatementItem{Row: it.Row, Value: it.Value, Remedy: it.Remedy})
	}
	return out, nil
}

// checkMarks holds marks to the offer's named records: a record marks only
// a name the holder's own party writes (the named record's side).
func (a *actDeal) checkMarks(marks []string) ([]string, error) {
	var out []string
	for _, m := range marks {
		n, ok := a.offer.TermsRows.Named[m]
		if !ok {
			return nil, fieldError("mark", " "+m+": the offer of deal "+a.d.DealID+" names no record "+m)
		}
		if n.Side != a.d.Role {
			return nil, fieldError("mark", " "+m+": the offer says the "+n.Side+" writes it, and you are the "+a.d.Role)
		}
		if slices.Contains(out, m) {
			return nil, fieldError("mark", " names "+m+" twice")
		}
		out = append(out, m)
	}
	return out, nil
}

// remedyFor is the remedy_for member of an act that is a remedy for row, nil
// for none.
func (a *actDeal) remedyFor(row string) (*xdealv1.RemedyFor, error) {
	if row == "" {
		return nil, nil
	}
	if _, err := a.row(row); err != nil {
		return nil, err
	}
	return &xdealv1.RemedyFor{Row: row}, nil
}

// sealedLike finds the record of the holder's book an earlier call with the
// same input already sealed: one of the same type whose body and marks are
// the call's, byte for byte. Its place (seq) is 0 when there is none. With
// restated, a match counts only when no later record of the book states any
// of the same rows: a row reads the side's latest statement, so repeating an
// older one after another is a new statement. A perform needs no such rule:
// its check's approval authorizes one act only.
func sealedLike(book []xdealv1.Record, recordType string, body xdealv1.Body, marks []string, restated []xdealv1.StatementItem) (int, error) {
	want, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	for i, r := range book {
		if r.Block.RecordType != recordType || !slices.Equal(r.Block.Marks, marks) || statedAfter(book, i, restated) {
			continue
		}
		got, err := json.Marshal(r.Body)
		if err != nil {
			return 0, err
		}
		if bytes.Equal(got, want) {
			return i + 1, nil
		}
	}
	return 0, nil
}

// statedAfter says a record of the book after place i states any of the rows.
func statedAfter(book []xdealv1.Record, i int, items []xdealv1.StatementItem) bool {
	rows := map[string]bool{}
	for _, it := range items {
		rows[it.Row] = true
	}
	if len(rows) == 0 {
		return false
	}
	for _, r := range book[i+1:] {
		for _, it := range statementsOf(r) {
			if rows[it.Row] {
				return true
			}
		}
	}
	return false
}

// statementsOf are the statements a record of the holder's book carries.
func statementsOf(r xdealv1.Record) []xdealv1.StatementItem {
	switch b := r.Body.(type) {
	case *xdealv1.StatementBody:
		return b.Statements
	case xdealv1.StatementBody:
		return b.Statements
	case *xdealv1.PerformBody:
		return b.Statements
	case xdealv1.PerformBody:
		return b.Statements
	case *xdealv1.SettleBody:
		return b.Statements
	case xdealv1.SettleBody:
		return b.Statements
	}
	return nil
}

// acts are the holder's own acts of a deal, in book order: its perform and
// settle records.
func acts(book []xdealv1.Record) []xdealv1.Record {
	var out []xdealv1.Record
	for _, r := range book {
		if r.Block.RecordType == xdealv1.TypePerform || r.Block.RecordType == xdealv1.TypeSettle {
			out = append(out, r)
		}
	}
	return out
}

// NotAuthorizedError is an act that x-deal-v1's authorization (PROFILE §8,
// with x-deal-v0's rule 5 on a settled amount) refuses over the deal's
// journal. It is an [InputError] too, so a caller that only tells input
// errors apart needs nothing more.
type NotAuthorizedError struct{ Err error }

func (e *NotAuthorizedError) Error() string { return "the act is not authorized: " + e.Err.Error() }

// Unwrap gives the refusal as an InputError, and the authorization's own
// error.
func (e *NotAuthorizedError) Unwrap() []error {
	return []error{&InputError{Reason: e.Error()}, e.Err}
}

// Authorize runs x-deal-v1's authorization over the deal's journal
// (DealJournal) and its acts, act last, and returns act's: the check,
// verdict and approval it rests on and the digests the person signs. earlier
// are the holder's acts before it, in book order; settlement is the act's
// settlement member, which rule 5 holds to the checked amount (nil for
// none). A refusal is a [NotAuthorizedError].
func (s *Store) Authorize(ctx context.Context, dealID string, earlier []xdealv1.Record, act xdealv1.Record, settlement json.RawMessage) (*xdealv1.ActAuthorization, error) {
	journal, err := s.DealJournal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	ins := make([]xdealv1.ActIn, 0, len(earlier)+1)
	for _, r := range earlier {
		ins = append(ins, xdealv1.ActIn{Record: r})
	}
	ins = append(ins, xdealv1.ActIn{Record: act, Settlement: settlement})
	auths, err := xdealv1.CheckActs(journal, ins)
	if err != nil {
		return nil, &NotAuthorizedError{Err: err}
	}
	for i := range auths {
		if auths[i].Act.Block.Seq == act.Block.Seq {
			return &auths[i], nil
		}
	}
	return nil, nil
}

// gate runs the first-act gate on a candidate act (u610, u611): the holder's
// first act waits until the counterparty's stamp proof covers its
// agreement. The counterparty's published evidence commitment is not held
// here, so its shelf log is "" and the gate skips that comparison.
func (s *Store) gate(ctx context.Context, d *Deal, book []xdealv1.Record, act xdealv1.Record) (xdealv1.ActGate, error) {
	if s.cfg.ReceiveProfile == nil {
		return xdealv1.ActGate{}, errors.New("deal: an act needs Config.ReceiveProfile, the shelf's x-deal-v1 receive profile, for its first-act gate")
	}
	act.Block.DealID = d.DealID
	return s.cfg.ReceiveProfile.GateAct(ctx, xdealv1.ActGateInput{Own: book, Act: act, CounterpartAgreement: d.CounterpartyAgreement})
}

// sealAct seals an act through the deal-book writer: the first-act gate,
// the authorization over the earlier acts and the caller's check run on the
// finished record, again on every rebuild, since the act's own time is the
// one they read.
func (s *Store) sealAct(ctx context.Context, a *actDeal, in BookRecord, settlement json.RawMessage, before BeforeSeal) (Acted, error) {
	earlier := acts(a.book)
	var out Acted
	in.Validate = func(rec xdealv1.Record) error {
		g, err := s.gate(ctx, a.d, a.book, rec)
		if err != nil {
			return err
		}
		if g.Gated && !g.Allowed {
			return inputError("your first act on deal " + a.d.DealID + " waits on the other side's stamp proof: " + g.Reason)
		}
		auth, err := s.Authorize(ctx, a.d.DealID, earlier, rec, settlement)
		if err != nil {
			return err
		}
		if before != nil {
			if err := before(rec, auth); err != nil {
				return err
			}
		}
		out.Gate, out.Authorization = &g, auth
		return nil
	}
	sealed, err := s.SealInBook(ctx, a.d.DealID, in)
	if err != nil {
		return Acted{}, err
	}
	out.Sealed = sealed
	return out, nil
}

// sealRecord seals a deal-book record that is no act: the caller's check
// runs on the finished record.
func (s *Store) sealRecord(ctx context.Context, dealID string, in BookRecord, before BeforeSeal) (Acted, error) {
	validate := in.Validate
	in.Validate = func(rec xdealv1.Record) error {
		if validate != nil {
			if err := validate(rec); err != nil {
				return err
			}
		}
		if before != nil {
			return before(rec, nil)
		}
		return nil
	}
	sealed, err := s.SealInBook(ctx, dealID, in)
	if err != nil {
		return Acted{}, err
	}
	return Acted{Sealed: sealed}, nil
}

// sealedAt is the record at place seq of the holder's book as an earlier
// call sealed it: its record id, digest and the pair that travels.
func (s *Store) sealedAt(ctx context.Context, d *Deal, seq int, rec xdealv1.Record) (Sealed, error) {
	book, err := s.cfg.Shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return Sealed{}, err
	}
	rows, err := book.Query(ctx, evidencebook.Filter{FromSeq: uint64(seq), ToSeq: uint64(seq)})
	if err != nil {
		return Sealed{}, err
	}
	if len(rows) != 1 || len(rows[0].Header.PayloadCommitments) != 1 {
		return Sealed{}, inputError("the deal book has no record at that place")
	}
	stored, err := book.Sealed(ctx, rows[0].RecordID)
	if err != nil {
		return Sealed{}, err
	}
	payload, err := book.ResolvePayload(ctx, rows[0].RecordID, rows[0].Header.PayloadCommitments[0])
	if err != nil {
		return Sealed{}, err
	}
	pair, err := xdealv1.BookPair(stored, payload)
	if err != nil {
		return Sealed{}, err
	}
	digest, err := xdealv1.Digest(rec)
	if err != nil {
		return Sealed{}, err
	}
	return Sealed{Seq: uint64(seq), RecordID: rows[0].RecordID, Digest: digest, Record: rec, Pair: pair}, nil
}

// again is Acted for a record an earlier call sealed.
func (s *Store) again(ctx context.Context, d *Deal, seq int, rec xdealv1.Record) (Acted, error) {
	sealed, err := s.sealedAt(ctx, d, seq, rec)
	if err != nil {
		return Acted{}, err
	}
	return Acted{Sealed: sealed, Again: true}, nil
}

// PerformIn is a perform record's input: delivery or another act the holder
// did. Check is the digest of the holder's passing commit check the act
// rests on; Act and Observed are tokens (deliver, carrier_scan, ...).
// Statements say how rows went; Marks name the offer's named records this
// act is; RemedyRow makes the act a remedy for that row.
type PerformIn struct {
	Check         string
	Act, Observed string
	Statements    []xdealv1.StatementItem
	Marks         []string
	RemedyRow     string
	// Before is the caller's own check of the act (nil: none).
	Before BeforeSeal
}

// Perform seals a perform record in the holder's deal book of dealID
// (xdealv1-write-path §13): an act, which rests on the holder's passing
// commit check. It is sealed only when x-deal-v1's authorization accepts it
// over the deal's journal (the check's verdict has its approval, and the
// approval authorized no other act), and the holder's first act only once
// the counterparty's stamp proof covers its agreement (u610, u611); a
// counterparty that runs no stamping gates nothing. Called again with the
// same input, it seals nothing and returns the record already sealed.
//
// The result's Authorization says which of the person's signatures the act
// still waits on (x-deal-v1 §8): no one should rely on the act until they
// are held.
func (s *Store) Perform(ctx context.Context, dealID string, in PerformIn) (Acted, error) {
	a, err := s.loadActDeal(ctx, dealID)
	if err != nil {
		return Acted{}, err
	}
	stmts, err := a.checkStatements(in.Statements)
	if err != nil {
		return Acted{}, err
	}
	marks, err := a.checkMarks(in.Marks)
	if err != nil {
		return Acted{}, err
	}
	ref := xdealv1.RefTo(in.Check)
	body := xdealv1.PerformBody{Act: in.Act, Observed: in.Observed, Statements: stmts, Check: &ref}
	if body.RemedyFor, err = a.remedyFor(in.RemedyRow); err != nil {
		return Acted{}, err
	}
	if seq, err := sealedLike(a.book, xdealv1.TypePerform, body, marks, nil); err != nil {
		return Acted{}, err
	} else if seq > 0 {
		rec := a.book[seq-1]
		var before []xdealv1.Record
		for _, r := range acts(a.book) {
			if r.Block.Seq < rec.Block.Seq {
				before = append(before, r)
			}
		}
		auth, err := s.Authorize(ctx, dealID, before, rec, nil)
		if err != nil {
			return Acted{}, err
		}
		out, err := s.again(ctx, a.d, seq, rec)
		out.Authorization = auth
		return out, err
	}
	return s.sealAct(ctx, a, BookRecord{Body: body, Marks: marks}, nil, in.Before)
}

// StatementIn is a statement record's input: how one or more rows went.
type StatementIn struct {
	Statements []xdealv1.StatementItem
	// Before is the caller's own check of the record (nil: none).
	Before BeforeSeal
}

// Statement seals a statement record in the holder's deal book of dealID:
// how rows went, which a row's status reads. A statement is never gated and
// rests on no check. Called again with the same input, it seals nothing and
// returns the record already sealed, unless a later record states one of
// the same rows: then the statement is new.
func (s *Store) Statement(ctx context.Context, dealID string, in StatementIn) (Acted, error) {
	a, err := s.loadActDeal(ctx, dealID)
	if err != nil {
		return Acted{}, err
	}
	stmts, err := a.checkStatements(in.Statements)
	if err != nil {
		return Acted{}, err
	}
	if len(stmts) == 0 {
		return Acted{}, fieldError("statement", " is required at least once: a statement says how a row went")
	}
	body := xdealv1.StatementBody{Statements: stmts}
	if seq, err := sealedLike(a.book, xdealv1.TypeStatement, body, nil, stmts); err != nil {
		return Acted{}, err
	} else if seq > 0 {
		return s.again(ctx, a.d, seq, a.book[seq-1])
	}
	return s.sealRecord(ctx, dealID, BookRecord{Body: body}, in.Before)
}
