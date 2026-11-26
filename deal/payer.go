// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/action-state-group/agent-action-capsule/go/settlement"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// A payer leg is sealed in two calls (xdealv1-write-path §13; Sherlock2,
// buzz 037352f4 and 44899e76). SendPayment runs before the payment is
// handed to the rail: the gate, the authorization and the caller's checks
// run on the candidate leg, then the send, its local step (which counts the
// amount from that moment) and a journal evidence record "payment sent" are
// stored, and the send is open. Settle with the check runs when the rail
// answers and seals the one payer leg, bound to the send. While a send is
// open no journal record that changes the deal's details is sealed
// (SealJournal), so the payment already sent stays authorized.

// Send is a payer leg's send (deal_v1_sends): the check it rests on, the
// step that counts it, what it bound (amount, currency, the payment_ref
// type, value and whole object, the leg a refund reverses) and the payer leg
// that closed it ("" while open).
type Send struct {
	DealID, Check string
	N             int64
	AmountMinor   int64
	Currency      string
	RefType, Ref  string
	RefJSON       json.RawMessage
	Reverses      string
	Leg           string
}

// SendOf is the deal's send on check; nil when there is none.
func (s *Store) SendOf(ctx context.Context, dealID, check string) (*Send, error) {
	v := Send{DealID: dealID, Check: check}
	var refJSON string
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT event_n, amount_minor, currency, ref_type, ref_value, ref_json, reverses, leg_digest FROM deal_v1_sends WHERE deal_id=? AND check_digest=?`, dealID, check).
		Scan(&v.N, &v.AmountMinor, &v.Currency, &v.RefType, &v.Ref, &refJSON, &v.Reverses, &v.Leg)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.RefJSON = json.RawMessage(refJSON)
	return &v, nil
}

// CheckRecord is the body of the deal's journal check whose record digest
// is given.
func (s *Store) CheckRecord(ctx context.Context, dealID, digest string) (*xdealv1.CheckBody, error) {
	journal, err := s.DealJournal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	for _, r := range journal {
		if r.Block.RecordType != xdealv1.TypeCheck {
			continue
		}
		d, err := xdealv1.Digest(r)
		if err != nil {
			return nil, err
		}
		if d != digest {
			continue
		}
		switch b := r.Body.(type) {
		case *xdealv1.CheckBody:
			return b, nil
		case xdealv1.CheckBody:
			return &b, nil
		}
	}
	return nil, fieldError("check", " "+digest+" is not a check of deal "+dealID+" in your journal")
}

// payerLeg is what both payer calls read first: the deal, the leg's body
// (payer, its check, statements, marks, remedy row and the leg it
// reverses), the check's body with the amount the leg pays, and the terms
// the leg names.
type payerLeg struct {
	a        *actDeal
	body     xdealv1.SettleBody
	marks    []string
	cb       *xdealv1.CheckBody
	amount   int64
	currency string
	termsRef string
	// AmountNotChecked is set when the check is a cancel that states a fee
	// and no amount: the refund's amount is not held to it (evidencebook#743).
	amountNotChecked bool
}

// RefundFeeNotChecked is why a refund's amount was not checked: the cancel
// check states a cancellation fee, and the record does not say who bears
// it, so the refund owed is not known (evidencebook#743).
const RefundFeeNotChecked = "the cancel check states a cancellation fee and the record does not say who bears it, so this refund's amount (the full cancelled amount its send recorded) is not checked (evidencebook#743)"

// refundFeeGuard refuses a new refund send on a cancel check that states a
// fee (evidencebook#743); a test turns it off to reach a send made before.
var refundFeeGuard = true

func (s *Store) loadPayerLeg(ctx context.Context, dealID, check, row, reverses string, stmts []xdealv1.StatementItem, marks []string, remedyRow string, sending bool) (*payerLeg, error) {
	a, err := s.loadActDeal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	r, currency, err := a.paymentRow(row)
	if err != nil {
		return nil, err
	}
	leg, err := s.legFor(ctx, a, r, reverses)
	if err != nil {
		return nil, err
	}
	p := &payerLeg{a: a, currency: currency}
	if p.body.Statements, err = a.checkStatements(stmts); err != nil {
		return nil, err
	}
	if p.marks, err = a.checkMarks(marks); err != nil {
		return nil, err
	}
	p.body.Leg = leg
	if reverses != "" {
		rv := xdealv1.RefTo(reverses)
		p.body.Reverses = &rv
	}
	if p.body.RemedyFor, err = a.remedyFor(remedyRow); err != nil {
		return nil, err
	}
	if p.termsRef, err = s.termsRef(ctx, a.d); err != nil {
		return nil, err
	}
	if leg != xdealv1.LegPayer {
		what := "payment"
		if reverses != "" {
			what = "refund"
		}
		return nil, coded(CodeSettlePayee, "", "you do not owe row "+r.ID+" of deal "+dealID+": what you received is a payee leg, which cites no check (Store.Settle without one)", what, r.ID, dealID)
	}
	cb, err := s.CheckRecord(ctx, dealID, check)
	if err != nil {
		return nil, err
	}
	switch {
	case reverses == "" && cb.Action != "pay":
		return nil, coded(CodeCheckAction, "", "a payment rests on a pay check, and check "+check+" is a "+cb.Action+" check", "payment", check, cb.Action)
	case reverses != "" && cb.Action != "cancel":
		return nil, coded(CodeCheckAction, "", "a refund rests on a cancel check, and check "+check+" is a "+cb.Action+" check", "refund", check, cb.Action)
	}
	// A seller's cancel check records the refund as cancelled_amount_minor.
	// With a cancellation fee beside it, the record does not say who bears
	// the fee, so the refund owed is not known and no amount is picked.
	if cb.AmountMinor == nil && cb.Action == "cancel" && cb.CancelledAmountMinor != nil && cb.FeeMinor != nil && *cb.FeeMinor != 0 {
		if sending && refundFeeGuard {
			return nil, fieldError("check", " "+check+" is a cancel that states a cancellation fee, and the record does not say who bears it, so the refund owed is not known and no refund is sent for it (evidencebook#743)")
		}
		p.amountNotChecked = !sending
	}
	amount := cb.AmountMinor
	if amount == nil && cb.Action == "cancel" && cb.CancelledAmountMinor != nil {
		amount = cb.CancelledAmountMinor
	}
	if amount == nil {
		return nil, fieldError("check", " "+check+" records no amount: a payment rests on a check of its amount")
	}
	if cb.Currency != "" {
		p.currency = cb.Currency
	}
	p.cb, p.amount = cb, *amount
	ref := xdealv1.RefTo(check)
	p.body.Check = &ref
	return p, nil
}

// payerMember is a payer leg's settlement member.
func (s *Store) payerMember(termsRef string, amount int64, currency string, ref map[string]any, status string, routingFee *int64) (json.RawMessage, error) {
	amt, err := legAmount(amount, currency)
	if err != nil {
		return nil, err
	}
	m := map[string]any{"version": settlement.Version, "leg": "payer_observed", "sealer_role": "payer", "terms_ref": termsRef,
		"amount": amt, "payment_ref": ref, "status": status, "observed_at": s.nowText()}
	if routingFee != nil {
		fee, err := legAmount(*routingFee, currency)
		if err != nil {
			return nil, err
		}
		m["routing_fee"] = fee
	}
	return settlementMember(m)
}

// PaymentSent is what SendPayment stored: the "payment sent" evidence
// record's digest, the check, the amount and currency the send counts, and
// the first-act gate's answer and the authorization of the candidate leg.
type PaymentSent struct {
	Evidence      string
	Check         string
	AmountMinor   int64
	Currency      string
	Gate          xdealv1.ActGate
	Authorization *xdealv1.ActAuthorization
}

// SendIn is a payment's send: the check it rests on (a pay check, or a
// cancel check for a refund, with Reverses the leg of the holder's own book
// it refunds), the payment row (Row, or the offer's only one), the
// payment_ref the send binds (AAC's flat object; on a card checkout with no
// reference before paying, a merchant order with an empty value), and the
// leg's statements, marks and remedy row. Before is the caller's own check
// of the candidate leg. Step builds the send's local step, given the facts,
// the leg's authorization and its amount; it returns the step's local form
// and the nonce that opens the evidence record's detail commitment, which
// it draws itself when it records it ("" lets the store draw one). Nil
// stores a step with no local form.
type SendIn struct {
	Check, Row, Reverses string
	PaymentRef           json.RawMessage
	Statements           []xdealv1.StatementItem
	Marks                []string
	RemedyRow            string
	// Producer is the evidence record's producer block member (nil: none).
	Producer any
	Before   BeforeSeal
	Step     func(f StepFacts, auth *xdealv1.ActAuthorization, amountMinor int64, currency string) (local json.RawMessage, sentNonce string, err error)
}

// SendPayment opens a payer leg's send (the design's deal.settle, first
// call), before the payment is handed to the rail: the leg Settle will seal
// is checked as it will be sealed (the first-act gate, the authorization
// and rule 5's amount, and Before), so a payment they would refuse never
// reaches the rail. Then the send, its step and the journal's "payment
// sent" evidence (about the deal's intent, committing to what the send
// bound) are stored in one transaction, and the evidence is appended. A
// check authorizes one payment: a second send on it is refused.
func (s *Store) SendPayment(ctx context.Context, dealID string, in SendIn) (PaymentSent, error) {
	p, err := s.loadPayerLeg(ctx, dealID, in.Check, in.Row, in.Reverses, in.Statements, in.Marks, in.RemedyRow, true)
	if err != nil {
		return PaymentSent{}, err
	}
	if prev, err := s.SendOf(ctx, dealID, in.Check); err != nil {
		return PaymentSent{}, err
	} else if prev != nil && prev.Leg != "" {
		return PaymentSent{}, inputError("check " + in.Check + " is used: its payment was sealed as leg " + prev.Leg + ", and a check authorizes one payment")
	} else if prev != nil {
		return PaymentSent{}, coded(CodeSendInFlight, "", "a payment on check "+in.Check+" is in flight ("+strconv.FormatInt(prev.AmountMinor, 10)+" "+prev.Currency+", "+prev.RefType+" "+prev.Ref+"): when the rail answers, seal its leg with Store.Settle",
			in.Check, strconv.FormatInt(prev.AmountMinor, 10), prev.Currency, prev.RefType, prev.Ref, dealID, prev.Reverses)
	}
	if in.Reverses != "" {
		if err := s.payerRefundTaken(ctx, p.a, in.Reverses); err != nil {
			return PaymentSent{}, err
		}
	}
	for _, r := range acts(p.a.book) {
		if b, ok := settleOf(r); ok && b.Check != nil && b.Check.Digest == in.Check {
			return PaymentSent{}, inputError("check " + in.Check + " already has a payer leg: a check authorizes one payment")
		}
	}
	ref, err := checkPaymentRef(in.PaymentRef, true)
	if err != nil {
		return PaymentSent{}, err
	}
	member, err := s.payerMember(p.termsRef, p.amount, p.currency, ref, LegSettled, nil)
	if err != nil {
		return PaymentSent{}, err
	}
	// The candidate leg, checked as Settle will seal it.
	cand := xdealv1.Record{Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID,
		RecordType: xdealv1.TypeSettle, At: s.nowText(), Seq: int64(len(p.a.book)) + 1, Marks: p.marks}, Body: &p.body}
	gate, err := s.gate(ctx, p.a.d, p.a.book, cand)
	if err != nil {
		return PaymentSent{}, err
	}
	if gate.Gated && !gate.Allowed {
		return PaymentSent{}, inputError("your first act on deal " + dealID + " waits on the other side's stamp proof: " + gate.Reason)
	}
	auth, err := s.Authorize(ctx, dealID, acts(p.a.book), cand, member)
	if err != nil {
		return PaymentSent{}, err
	}
	if in.Before != nil {
		if err := in.Before(cand, auth); err != nil {
			return PaymentSent{}, err
		}
	}
	refType, _ := ref["type"].(string)
	refValue, _ := ref["value"].(string)
	refJSON, err := json.Marshal(ref)
	if err != nil {
		return PaymentSent{}, err
	}
	evidence, err := s.storeSend(ctx, dealID, in.Producer, Send{DealID: dealID, Check: in.Check, AmountMinor: p.amount, Currency: p.currency, RefType: refType, Ref: refValue, RefJSON: refJSON, Reverses: in.Reverses},
		func(f StepFacts) (json.RawMessage, string, error) {
			if in.Step == nil {
				return nil, "", nil
			}
			return in.Step(f, auth, p.amount, p.currency)
		})
	if err != nil {
		return PaymentSent{}, err
	}
	return PaymentSent{Evidence: evidence, Check: in.Check, AmountMinor: p.amount, Currency: p.currency, Gate: gate, Authorization: auth}, nil
}

// afterSendStoredHook lets a test stop a run as a dying process would,
// after a send is stored and before its evidence is appended.
var afterSendStoredHook func() error

// storeSend stores the send, its step and the "payment sent" evidence in
// one transaction, then appends the evidence; a failed append withdraws the
// send and its step unless the record was stored, as storeSteps does.
func (s *Store) storeSend(ctx context.Context, dealID string, producer any, v Send, step func(StepFacts) (json.RawMessage, string, error)) (string, error) {
	existing, err := s.Steps(ctx, dealID)
	if err != nil {
		return "", err
	}
	var intent string
	for _, st := range existing {
		if st.Kind == xdealv1.TypeIntent {
			intent = st.Digest
			break
		}
	}
	if intent == "" {
		return "", inputError("deal " + dealID + " has no intent yet: the payment-sent record is about the deal's intent (SealJournal with a step of kind intent)")
	}
	n, prev := int64(1), ""
	if len(existing) > 0 {
		n, prev = existing[len(existing)-1].N+1, existing[len(existing)-1].Digest
	}
	f := StepFacts{N: n, At: s.nowText(), Prev: prev}
	local, nonce, err := step(f)
	if err != nil {
		return "", err
	}
	if local == nil {
		local = json.RawMessage("null")
	}
	if nonce == "" {
		if nonce, err = s.randomHex(32); err != nil {
			return "", err
		}
	}
	text, err := json.Marshal(map[string]any{"check": v.Check, "amount_minor": v.AmountMinor, "currency": v.Currency, "ref_type": v.RefType, "ref": v.Ref})
	if err != nil {
		return "", err
	}
	commitment, err := dealprofile.CommitText(nonce, string(text))
	if err != nil {
		return "", err
	}
	ref := xdealv1.RefTo(intent)
	block := map[string]any{"profile": xdealv1.Profile, "canonicalization": xdealv1.Canonicalization, "record_type": xdealv1.TypeEvidence,
		"at": f.At, "deal_id": dealID, "commit_alg": dealprofile.CommitAlg,
		"refs": []any{map[string]any{"rel": "about", "type": ref.Type, "digest_alg": ref.DigestAlg, "digest": ref.Digest}}}
	if producer != nil {
		block["producer"] = producer
	}
	raw, err := json.Marshal(map[string]any{
		xdealv1.Profile: block,
		"body":          map[string]any{"source": "payment_sent", "verified": false, "detail_commitment": commitment},
	})
	if err != nil {
		return "", err
	}
	if err := xdealv1.CheckSealed(raw, xdealv1.SealContext{OfferExists: true}); err != nil {
		return "", inputError("the payment-sent evidence does not pass the profile's checks: " + err.Error())
	}
	payload, digest, err := xdealv1.Encode(raw)
	if err != nil {
		return "", err
	}
	tx, err := s.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deal_v1_events (deal_id, n, kind, local, record_digest, payload) VALUES (?, ?, 'act', ?, ?, ?)`, dealID, n, string(local), digest, payload); err != nil {
		return "", errors.Join(err, tx.Rollback())
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deal_v1_sends (deal_id, check_digest, event_n, amount_minor, currency, ref_type, ref_value, ref_json, reverses) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		dealID, v.Check, n, v.AmountMinor, v.Currency, v.RefType, v.Ref, string(v.RefJSON), v.Reverses); err != nil {
		return "", errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if afterSendStoredHook != nil {
		// A process that dies here leaves the send and its payload stored;
		// the next Steps appends the evidence.
		if err := afterSendStoredHook(); err != nil {
			return "", err
		}
	}
	if err := s.appendJournal(ctx, payload); err != nil {
		held, herr := s.journalHolds(ctx, digest)
		if herr != nil || held {
			return "", errors.Join(err, herr)
		}
		dtx, derr := s.cfg.DB.BeginTx(ctx, nil)
		if derr != nil {
			return "", errors.Join(err, derr)
		}
		if _, derr = dtx.ExecContext(ctx, `DELETE FROM deal_v1_sends WHERE deal_id=? AND check_digest=?`, dealID, v.Check); derr == nil {
			_, derr = dtx.ExecContext(ctx, `DELETE FROM deal_v1_events WHERE deal_id=? AND n=?`, dealID, n)
		}
		if derr != nil {
			return "", errors.Join(err, derr, dtx.Rollback())
		}
		return "", errors.Join(err, dtx.Commit())
	}
	return digest, s.stepAppended(ctx, dealID, digest)
}

// payerRefundTaken refuses a new refund send of a leg that is already being
// refunded: another send that reverses any leg of its chain, open or closed
// by a leg that did not fail.
func (s *Store) payerRefundTaken(ctx context.Context, a *actDeal, target string) error {
	chain := legChain(a.book, target)
	rows, err := s.cfg.DB.QueryContext(ctx, `SELECT reverses, leg_digest FROM deal_v1_sends WHERE deal_id=?`, a.d.DealID)
	if err != nil {
		return err
	}
	var legs []string
	for rows.Next() {
		var rev, l string
		if err := rows.Scan(&rev, &l); err != nil {
			return errors.Join(err, rows.Close())
		}
		if chain[rev] {
			legs = append(legs, l)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	taken := coded(CodeSettleRefunded, "", "leg "+target+" is already refunded: a later observation of that refund names it in Supersedes, and a failed refund leaves the leg free to refund again", target)
	for _, l := range legs {
		if l == "" {
			return taken
		}
		for i, r := range a.book {
			if d, err := xdealv1.Digest(r); err == nil && d == l {
				if st, err := s.legStatusAt(ctx, a.d, i+1); err != nil {
					return err
				} else if st != LegFailed {
					return taken
				}
			}
		}
	}
	return nil
}

// settlePayer seals a payer leg when the rail answers (the design's
// deal.settle, second call): bound to its send, with the status the rail
// gave (settled or failed), the payment_ref the leg carries and any routing
// fee paid. Called again with the same input it returns the leg already
// sealed; a payer leg is never sealed twice for one check. A failed payment
// counts no spend: closeStep, when set, rewrites the send's local step in
// the same transaction that closes the send.
func (s *Store) settlePayer(ctx context.Context, dealID string, in SettleIn) (Acted, error) {
	p, err := s.loadPayerLeg(ctx, dealID, in.Check, in.Row, in.Reverses, in.Statements, in.Marks, in.RemedyRow, false)
	if err != nil {
		return Acted{}, err
	}
	if in.Status != LegSettled && in.Status != LegFailed {
		return Acted{}, inputError("a payer leg's status is settled or failed, when the rail answers")
	}
	send, err := s.SendOf(ctx, dealID, in.Check)
	if err != nil {
		return Acted{}, err
	}
	if send == nil {
		return Acted{}, coded(CodeNotSent, "", "no payment on check "+in.Check+" was sent: open its send with Store.SendPayment before the handoff", dealID, in.Check)
	}
	if in.Reverses != send.Reverses {
		return Acted{}, coded(CodeSentOtherwise, "", "the payment on check "+in.Check+" was sent reversing "+strconv.Quote(send.Reverses)+": the leg reverses what its send did", in.Check, send.Reverses)
	}
	if send.AmountMinor != p.amount || send.Currency != p.currency {
		return Acted{}, conflictf("the payment was sent as %d %s, and check %s records %d %s", send.AmountMinor, send.Currency, in.Check, p.amount, p.currency)
	}
	ref, err := checkPaymentRef(in.PaymentRef, in.Status == LegFailed || send.RefType == xdealv1.MerchantOrderType)
	if err != nil {
		return Acted{}, err
	}
	member, err := s.payerMember(p.termsRef, send.AmountMinor, p.currency, ref, in.Status, in.RoutingFee)
	if err != nil {
		return Acted{}, err
	}
	if send.Leg != "" {
		for i, r := range p.a.book {
			d, err := xdealv1.Digest(r)
			if err != nil || d != send.Leg {
				continue
			}
			held, err := s.SettlementAt(ctx, p.a.d, i+1)
			if err != nil {
				return Acted{}, err
			}
			same, err := sameRecordInput(r, &p.body, p.marks)
			if err != nil {
				return Acted{}, err
			}
			if !same || !sameButObservedAt(held, member) {
				return Acted{}, inputError("the payment on check " + in.Check + " is already sealed as leg " + send.Leg + " with other inputs; a check authorizes one payment, and a payer leg is never sealed again")
			}
			out, err := s.again(ctx, p.a.d, i+1, r)
			out.AmountNotChecked = p.amountNotChecked
			return out, err
		}
		return Acted{}, conflictf("the payer leg %s of check %s is not in your deal book", send.Leg, in.Check)
	}
	out, err := s.sealAct(ctx, p.a, BookRecord{Body: p.body, Marks: p.marks, Members: map[string]json.RawMessage{"settlement": member}}, member, in.Before)
	if err != nil {
		return Acted{}, err
	}
	out.AmountNotChecked = p.amountNotChecked
	if afterLegSealedHook != nil {
		if err := afterLegSealedHook(); err != nil {
			return Acted{}, err
		}
	}
	return out, s.CloseSend(ctx, dealID, in.Check, out.Digest, in.Status, in.CloseStep)
}

// afterLegSealedHook lets a test stop a run between sealing a payer leg and
// closing its send.
var afterLegSealedHook func() error

// CloseSend records the payer leg that closed a send. A failed payment
// counts no spend: closeStep, when set, rewrites the send's local step in the
// same transaction. A caller that finds a payer leg a stopped run sealed but
// did not record closes its send with it.
func (s *Store) CloseSend(ctx context.Context, dealID, check, leg, status string, closeStep func(json.RawMessage) (json.RawMessage, error)) error {
	send, err := s.SendOf(ctx, dealID, check)
	if err != nil || send == nil {
		return err
	}
	tx, err := s.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deal_v1_sends SET leg_digest=? WHERE deal_id=? AND check_digest=?`, leg, dealID, check); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if status == LegFailed && closeStep != nil {
		var local string
		if err := tx.QueryRowContext(ctx, `SELECT local FROM deal_v1_events WHERE deal_id=? AND n=?`, dealID, send.N).Scan(&local); err != nil {
			return errors.Join(err, tx.Rollback())
		}
		next, err := closeStep(json.RawMessage(local))
		if err != nil {
			return errors.Join(err, tx.Rollback())
		}
		if _, err := tx.ExecContext(ctx, `UPDATE deal_v1_events SET local=? WHERE deal_id=? AND n=?`, string(next), dealID, send.N); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	return tx.Commit()
}
