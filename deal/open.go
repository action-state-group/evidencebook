// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// Offer seals an offer, or a counter-offer when body.CounterTo is set, in the
// journal under a new deal id and returns it with the pair to send. The
// offer's time is taken from the clock (body.StatedAt is overwritten). An
// offer whose To is null is to a party that keeps no signed records of its
// own: there is nothing to send, and the holder agrees to it alone (Agree).
//
// request is the caller's key for this call, such as a digest of its inputs:
// a call with the same request after a failure returns the offer the first
// one sealed or began, and never seals the same offer twice; once the offer
// is delivered (MarkWritten), the same request makes a new offer.
func (s *Store) Offer(ctx context.Context, body xdealv1.ProposeBody, request string) (Sent, error) {
	var (
		dealID  string
		payload []byte
		digest  string
		resumed bool
	)
	if op, err := s.findSentOp(ctx, request); err != nil {
		return Sent{}, err
	} else if op != nil && op.state != sentWritten {
		// A run that stopped: its record is fixed, whatever the clock says now.
		dealID, payload, digest, resumed = op.dealID, op.payload, op.recordDigest, true
	} else {
		if dealID, err = s.newDealID(); err != nil {
			return Sent{}, err
		}
		now := s.nowText()
		body.StatedAt = now
		rec := xdealv1.Record{
			Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypePropose, At: now},
			Body:  body,
		}
		if err = xdealv1.CheckSealed(rec, xdealv1.SealContext{}); err != nil {
			return Sent{}, inputError("the offer does not pass the x-deal-v1 checks: " + err.Error())
		}
		if payload, digest, err = xdealv1.Encode(rec); err != nil {
			return Sent{}, err
		}
	}
	if _, err := s.cfg.DB.ExecContext(ctx, `INSERT INTO deal_versions (deal_id, version, own_log_id, counterparty_log_id, state) VALUES (?, ?, '', '', 'offered') ON CONFLICT(deal_id) DO NOTHING`, dealID, version); err != nil {
		return Sent{}, err
	}
	if _, err := s.cfg.DB.ExecContext(ctx, `INSERT INTO deal_v1 (deal_id, side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest) VALUES (?, ?, ?, ?, '', '', '', '') ON CONFLICT(deal_id) DO NOTHING`,
		dealID, xdealv1.SideOfferer, body.Role, digest); err != nil {
		return Sent{}, err
	}
	pair, err := s.send(ctx, request, dealID, xdealv1.TypePropose, payload, digest)
	if err != nil {
		return Sent{}, err
	}
	return Sent{DealID: dealID, Digest: digest, Payload: payload, Pair: pair, Request: request, Resumed: resumed}, nil
}

// supersede sets aside a sent row whose record the journal holds but whose
// pair was never made: the pair cannot be rebuilt, so the record is sealed
// again as a new operation under the same request.
func (s *Store) supersede(ctx context.Context, op *sentOp) (bool, error) {
	held, err := s.journalHolds(ctx, op.recordDigest)
	if err != nil || !held {
		return false, err
	}
	_, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_sent SET request_digest=request_digest||':superseded:'||id WHERE record_digest=? AND request_digest=?`, op.recordDigest, op.requestDigest)
	return err == nil, err
}

// CancelOffer seals the cancellation of an offer the holder made
// (offerDigest is the offer's record digest), before the deal starts, and
// returns it with the pair to send. An offer is cancelled once. request is
// the caller's key for the call, as for Offer.
func (s *Store) CancelOffer(ctx context.Context, offerDigest, request string) (Sent, error) {
	var dealID string
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT deal_id FROM deal_sent WHERE record_digest=? AND kind=?`, offerDigest, xdealv1.TypePropose).Scan(&dealID)
	if errors.Is(err, sql.ErrNoRows) {
		return Sent{}, inputError("this profile wrote no offer with the digest " + offerDigest)
	}
	if err != nil {
		return Sent{}, err
	}
	op, err := s.findSentOp(ctx, request)
	if err != nil {
		return Sent{}, err
	}
	if op != nil && op.state == sentWritten {
		return Sent{}, coded(CodeCancelAgain, "", "this offer is already cancelled: Store.CancelOffer writes one cancellation per offer")
	}
	if op != nil && op.pair == nil {
		if gone, err := s.supersede(ctx, op); err != nil {
			return Sent{}, err
		} else if gone {
			op = nil
		}
	}
	var (
		payload []byte
		digest  string
	)
	if op != nil {
		payload, digest = op.payload, op.recordDigest
	} else {
		rec := xdealv1.Record{
			Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypeAgree, At: s.nowText()},
			Body:  xdealv1.AgreeBody{Kind: xdealv1.KindCancellation, Offer: xdealv1.RefTo(offerDigest)},
		}
		if err = xdealv1.CheckSealed(rec, xdealv1.SealContext{OfferExists: true}); err != nil {
			return Sent{}, inputError("the cancellation does not pass the x-deal-v1 checks: " + err.Error())
		}
		if payload, digest, err = xdealv1.Encode(rec); err != nil {
			return Sent{}, err
		}
	}
	pair, err := s.send(ctx, request, dealID, xdealv1.KindCancellation, payload, digest)
	if err != nil {
		return Sent{}, err
	}
	// The deal's state before the pair is delivered: a run that stops before
	// then leaves the operation unwritten, and its re-run delivers it.
	if _, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_versions SET state='cancelled' WHERE deal_id=?`, dealID); err != nil {
		return Sent{}, err
	}
	return Sent{DealID: dealID, Digest: digest, Payload: payload, Pair: pair, Request: request, Resumed: op != nil}, nil
}

// DeclineOffer seals the decline of an offer sent to the holder's key
// (offerDigest is the offer's record digest), before the holder agreed to
// it, and returns it with the pair to send back. reason is
// xdealv1.ReasonNotSel, xdealv1.ReasonOther or empty. An offer is declined
// once; a decline a stopped call sealed is returned as it was sealed,
// whatever reason says now (Resumed). request is the caller's key for the
// call, as for Offer.
func (s *Store) DeclineOffer(ctx context.Context, offerDigest, reason, request string) (Sent, error) {
	if reason != "" && reason != xdealv1.ReasonNotSel && reason != xdealv1.ReasonOther {
		return Sent{}, fieldError("reason", " must be not_selected or other")
	}
	held, err := s.HeldOffer(ctx, offerDigest)
	if err != nil {
		return Sent{}, err
	}
	if held == nil {
		return Sent{}, coded(CodeDeclineUnknownOffer, "", "this profile holds no offer with the digest "+offerDigest+": receive it first (Store.NoteReceived)", offerDigest)
	}
	if held.Own {
		return Sent{}, coded(CodeDeclineOwnOffer, "", "you wrote this offer: withdraw it with Store.CancelOffer")
	}
	prop, ok := proposeOf(held.Record)
	if !ok {
		return Sent{}, inputError("the record " + offerDigest + " is not an offer")
	}
	if prop.To.V == nil {
		return Sent{}, inputError("this offer names no key (to is null): it is to a party that keeps no signed records, and only its offerer holds it, so there is nothing to decline")
	}
	if *prop.To.V != s.keyID() {
		return Sent{}, inputError("this offer was sent to another key, so you cannot decline it: only the key it was sent to can sign its decline")
	}
	dealID := held.Record.Block.DealID
	d, err := s.Deal(ctx, dealID)
	if err != nil {
		return Sent{}, err
	}
	if d == nil {
		return Sent{}, inputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
	}
	// An agreement of the holder's and a decline exclude each other: once it
	// agreed, no decline is sealed or delivered, not even a stopped one
	// resumed. An agreeing run that stopped before its book holds a record
	// sealed nothing: it is no agreement.
	open, err := s.declinableState(ctx, d)
	if err != nil {
		return Sent{}, err
	}
	op, err := s.findSentOp(ctx, request)
	if err != nil {
		return Sent{}, err
	}
	if op != nil && op.state == sentWritten {
		return Sent{}, coded(CodeDeclineAgain, "", "this offer is already declined: Store.DeclineOffer writes one decline per offer")
	}
	if op != nil && op.pair == nil && d.State != open {
		// Nothing of it left the shelf, and the deal moved on (a cancellation
		// received): it is not sealed now.
		inJournal, err := s.journalHolds(ctx, op.recordDigest)
		if err != nil {
			return Sent{}, err
		}
		if !inJournal {
			return Sent{}, inputError("this offer is " + d.State + ": there is nothing to decline")
		}
	}
	if op != nil && op.pair == nil {
		if gone, err := s.supersede(ctx, op); err != nil {
			return Sent{}, err
		} else if gone {
			op = nil
		}
	}
	var (
		payload []byte
		digest  string
	)
	if op != nil {
		payload, digest = op.payload, op.recordDigest
	} else {
		if d.State != open {
			return Sent{}, inputError("this offer is " + d.State + ": there is nothing to decline")
		}
		rec := xdealv1.Record{
			Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypeAgree, At: s.nowText()},
			Body:  xdealv1.AgreeBody{Kind: xdealv1.KindDecline, Offer: xdealv1.RefTo(offerDigest), Reason: reason},
		}
		if err = xdealv1.CheckSealed(rec, xdealv1.SealContext{OfferExists: true}); err != nil {
			return Sent{}, inputError("the decline does not pass the x-deal-v1 checks: " + err.Error())
		}
		if payload, digest, err = xdealv1.Encode(rec); err != nil {
			return Sent{}, err
		}
	}
	pair, err := s.send(ctx, request, dealID, xdealv1.KindDecline, payload, digest)
	if err != nil {
		return Sent{}, err
	}
	// The deal's state before the pair is delivered, as CancelOffer does.
	if _, err = s.cfg.DB.ExecContext(ctx, `UPDATE deal_versions SET state='declined' WHERE deal_id=? AND state IN ('offer_received','agreeing','declined')`, dealID); err != nil {
		return Sent{}, err
	}
	return Sent{DealID: dealID, Digest: digest, Payload: payload, Pair: pair, Request: request, Resumed: op != nil}, nil
}

// pendingDecline says a decline of the deal was begun: any decline row of
// it, under its request key or one superseded (a run can stop between
// superseding a row and storing its repeat), which an agreement must not
// overtake.
func (s *Store) pendingDecline(ctx context.Context, dealID string) (bool, error) {
	var n int
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM deal_sent WHERE deal_id=? AND kind=?`, dealID, xdealv1.KindDecline).Scan(&n)
	return n > 0, err
}

// declinableState is the state a deal can be declined from: offer_received,
// or agreeing when that run stopped before the book held a record (it sealed
// nothing). An agreement of the holder's, or a book that holds one, refuses.
func (s *Store) declinableState(ctx context.Context, d *Deal) (string, error) {
	agreed := inputError("you agreed to this offer, so you cannot decline it")
	switch {
	case d.OwnAgreementID != "" || d.State == "agreed":
		return "", agreed
	case d.State != "agreeing":
		return "offer_received", nil
	}
	if d.OwnLogID != "" {
		books, err := s.cfg.Shelf.Books(ctx)
		if err != nil {
			return "", err
		}
		if slices.Contains(books, d.OwnLogID) {
			return "", agreed
		}
	}
	return "agreeing", nil
}

// AgreeOptions are the journal records an agreement cites.
type AgreeOptions struct {
	// Admit is the digest of the holder's admission decision for the deal.
	// Empty is the one Admit sealed for it.
	Admit string
	// Intent is the digest of the holder's intent for the task, when it
	// sealed one.
	Intent string
}

// Agreement is the holder's agreement: the first record of its deal book,
// with what the other side needs of it.
type Agreement struct {
	DealID string
	// Digest is the agreement's record digest; Side is offerer or acceptor.
	Digest, Side string
	// Pair is the agreement as it travels.
	Pair []byte
	// StampProof covers the agreement: the other side's first act waits on
	// it.
	StampProof evidencebook.StampProof
	// NoLayer is set when the other side keeps no signed records of its own:
	// there is no one to send the agreement to, and the holder's first act
	// goes ahead without the other side's stamp proof.
	NoLayer bool
}

// Agree seals the holder's agreement to the offer whose record digest is
// offerDigest, which opens its deal book, and returns it with its stamp
// proof. The agreement takes the offer's rows, witnesses, adjudicator and
// forums as they are, and cites the deal's admission decision (a GO, or a
// LIMIT; an ASK or a NO is refused) and, when given, the intent.
//
// The offerer agrees only once it holds the acceptor's agreement
// (NoteReceived); an acceptor agrees only to an offer that names its key. An
// offer of the holder's whose To is null is agreed to by the holder alone, at
// once. cad is the shelf's cadence: the agreement is stamped once, right
// after it is sealed, and an agreement already sealed is returned again with
// the newest proof that covers it, never sealed twice.
func (s *Store) Agree(ctx context.Context, cad *evidencebook.Cadence, offerDigest string, opts AgreeOptions) (Agreement, error) {
	offer, err := s.HeldOffer(ctx, offerDigest)
	if err != nil {
		return Agreement{}, err
	}
	if offer == nil {
		return Agreement{}, coded(CodeAgreeUnknownOffer, "", "this profile holds no offer with the digest "+offerDigest+": Store.NoteReceived takes one", offerDigest)
	}
	prop, _ := proposeOf(offer.Record)
	dealID := offer.Record.Block.DealID
	d, err := s.Deal(ctx, dealID)
	if err != nil {
		return Agreement{}, err
	}
	if d == nil {
		return Agreement{}, inputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
	}
	if d.State == "cancelled" || d.State == "declined" {
		return Agreement{}, inputError("this offer is " + d.State + ": there is no deal to agree to")
	}
	// A decline of this offer under way, stopped before its pair or its
	// state, and an agreement exclude each other.
	if pending, err := s.pendingDecline(ctx, dealID); err != nil {
		return Agreement{}, err
	} else if pending {
		return Agreement{}, coded(CodeAgreeDeclineUnfinished, "", "you began to decline this offer and it did not finish: call Store.DeclineOffer again to finish it. A declined offer has no deal to agree to.")
	}
	// An agreement already sealed is returned again, never sealed twice.
	if err := s.repairIfStopped(ctx, d); err != nil {
		return Agreement{}, err
	}
	if d.OwnAgreementID != "" {
		if _, err := s.cfg.DB.ExecContext(ctx, `UPDATE deal_versions SET state='agreed' WHERE deal_id=? AND state='agreeing'`, dealID); err != nil {
			return Agreement{}, err
		}
		return s.agreement(ctx, cad, d)
	}
	admit, err := s.agreementAdmit(ctx, dealID, opts.Admit)
	if err != nil {
		return Agreement{}, err
	}
	for _, want := range []struct{ field, digest, typ, what string }{
		{"admit", admit, xdealv1.TypeAdmit, "an admission decision"},
		{"intent", opts.Intent, xdealv1.TypeIntent, "an intent"},
	} {
		if want.digest == "" {
			continue
		}
		rec, held, err := s.journalRecord(ctx, want.digest)
		if err != nil {
			return Agreement{}, err
		}
		if !held {
			return Agreement{}, fieldError(want.field, " "+want.digest+" is not a record in your journal")
		}
		if rec == nil {
			return Agreement{}, fieldError(want.field, " "+want.digest+" is a record in your journal that is not an x-deal-v1 record, so not "+want.what)
		}
		if typ := rec.Block.RecordType; typ != want.typ {
			return Agreement{}, fieldError(want.field, " "+want.digest+" is a record of type "+typ+" in your journal, not "+want.what)
		}
		if err := citableAdmit(want.digest, rec); err != nil {
			return Agreement{}, err
		}
	}
	side, role := xdealv1.SideOfferer, prop.Role
	cptyKey, heldAcceptor := d.CounterpartyKey, ""
	// An offer whose to is null is to a counterparty with no layer (PROFILE
	// §4.7): its offerer agrees alone, at once.
	noLayer := offer.Own && prop.To.V == nil
	if !offer.Own {
		switch {
		case prop.To.V == nil:
			return Agreement{}, inputError("this offer names no key (to is null): it is to a party that keeps no signed records, and only its offerer agrees to it")
		case *prop.To.V != s.keyID():
			return Agreement{}, inputError("this offer was sent to another key, so you cannot agree to it: the other side would refuse an agreement from a key it did not name")
		}
		side, role = xdealv1.SideAcceptor, oppositeRole(prop.Role)
		cptyKey = offer.Signer
	} else if noLayer {
		cptyKey = ""
	} else if d.CounterpartyAgreement == "" {
		return Agreement{}, coded(CodeAgreeBeforeReceive, "", "the acceptor's agreement has not been received: Store.NoteReceived takes it, and you agree after")
	} else {
		heldAcceptor = d.CounterpartyAgreement
	}
	rowsDigest, err := xdealv1.RowsDigest(prop.TermsRows)
	if err != nil {
		return Agreement{}, err
	}
	layer := !noLayer
	counterpartyKey := xdealv1.Some(cptyKey)
	if noLayer {
		counterpartyKey = xdealv1.Null[string]{Present: true}
	}
	body := xdealv1.AgreeBody{
		Kind: xdealv1.KindAgreement, Offer: xdealv1.RefTo(offerDigest), RowsDigest: rowsDigest,
		Side: side, Role: role, Admit: refOf(admit), Intent: refOf(opts.Intent),
		Witnesses: prop.Witnesses, Adjudicator: prop.Adjudicator, Forums: forumsOf(prop),
		CounterpartyLayer: &layer, CounterpartyKey: counterpartyKey,
		CheckpointKey: hex.EncodeToString(s.cfg.CheckpointKey), ShelfLogID: s.cfg.ShelfLogID,
	}
	if _, err := s.cfg.DB.ExecContext(ctx, `INSERT INTO deal_v1 (deal_id, side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest) VALUES (?, ?, ?, ?, '', '', '', '') ON CONFLICT(deal_id) DO NOTHING`, dealID, side, role, offerDigest); err != nil {
		return Agreement{}, err
	}
	if _, err := s.cfg.DB.ExecContext(ctx, `UPDATE deal_v1 SET side=?, role=?, counterparty_key=? WHERE deal_id=?`, side, role, cptyKey, dealID); err != nil {
		return Agreement{}, err
	}
	// The writer fills the chain; the finished agreement is what is checked
	// against the offer it cites, before anything is stored.
	validate := func(rec xdealv1.Record) error {
		if err := xdealv1.CheckAgreement(rec, offer.Record, heldAcceptor); err != nil {
			return inputError("the agreement does not pass the x-deal-v1 checks: " + err.Error())
		}
		return nil
	}
	if _, err := s.SealInBook(ctx, dealID, BookRecord{Body: body, Seen: heldAcceptor, Validate: validate}); err != nil {
		return Agreement{}, err
	}
	if d, err = s.Deal(ctx, dealID); err != nil {
		return Agreement{}, err
	}
	return s.agreement(ctx, cad, d)
}

// agreement is the holder's sealed agreement with its stamp proof, taken
// right after the agreement. The offerer stamps here too: PROFILE §8 has it
// stamp at once when its first act waits on the acceptor's, and stamping
// always means a buyer acceptor who pays first never waits on an offerer
// that has not acted. A call again reuses the newest proof that covers the
// agreement and stamps only when none exists yet.
func (s *Store) agreement(ctx context.Context, cad *evidencebook.Cadence, d *Deal) (Agreement, error) {
	book, err := s.cfg.Shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return Agreement{}, err
	}
	sealed, err := book.Sealed(ctx, d.OwnAgreementID)
	if err != nil {
		return Agreement{}, err
	}
	payload, err := book.ResolvePayload(ctx, d.OwnAgreementID, d.OwnAgreementDigest)
	if err != nil {
		return Agreement{}, err
	}
	pair, err := xdealv1.BookPair(sealed, payload)
	if err != nil {
		return Agreement{}, err
	}
	proof, err := cad.StampProof(ctx, d.OwnAgreementID, 0, evidencebook.AudienceCounterparty)
	if err != nil {
		if _, err := cad.Stamp(ctx, s.cfg.Now()); err != nil {
			return Agreement{}, err
		}
		if proof, err = cad.StampProof(ctx, d.OwnAgreementID, 0, evidencebook.AudienceCounterparty); err != nil {
			return Agreement{}, err
		}
	}
	noLayer, err := s.NoLayer(ctx, d)
	if err != nil {
		return Agreement{}, err
	}
	return Agreement{DealID: d.DealID, Digest: d.OwnAgreementDigest, Side: d.Side, Pair: pair, StampProof: proof, NoLayer: noLayer}, nil
}

func refOf(digest string) *xdealv1.Ref {
	if digest == "" {
		return nil
	}
	r := xdealv1.RefTo(digest)
	return &r
}

// forumsOf is the offer's forums as the agreement carries them: never nil
// (an agreement with a null adjudicator carries an empty list).
func forumsOf(p *xdealv1.ProposeBody) []string {
	if p.Forums == nil {
		return []string{}
	}
	return p.Forums
}
