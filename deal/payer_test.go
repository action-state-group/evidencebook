// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paidCheck readies the buyer's payment with the library alone: its intent,
// a passing pay check of 6000 USD and the person's approval.
func paidCheck(t *testing.T, buyer *side, dealID, label string) string {
	t.Helper()
	ctx := t.Context()
	steps, err := buyer.store.Steps(ctx, dealID)
	require.NoError(t, err)
	if len(steps) == 0 {
		_, err = buyer.store.SealJournal(ctx, dealID, JournalStep{Kind: xdealv1.TypeIntent, Build: func(StepFacts) (StepParts, error) {
			return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeIntent, Body: map[string]any{"verbatim_commitment": nonceOf("intent"), "allowed": []any{"pay"}}}}, nil
		}})
		require.NoError(t, err)
	}
	amount := int64(6000)
	card := nonceOf(label + " card")
	buyer.tick()
	checked, err := buyer.store.Check(ctx, dealID, CheckIn{Check: xdealv1.CheckBody{CheckBody: dealprofile.CheckBody{Action: "pay", AmountMinor: &amount, Currency: "USD"}, PaymentRef: strings.Repeat("c", 64)},
		Verdict: passVerdict(card)})
	require.NoError(t, err)
	buyer.tick()
	_, err = buyer.store.SealJournal(ctx, dealID, approvalStep(checked.Verdict, card, nonceOf(label+" said")))
	require.NoError(t, err)
	buyer.tick()
	return checked.Check
}

var lnRef = json.RawMessage(`{"type":"ln.payment_hash","value":"` + strings.Repeat("c", 64) + `"}`)

// agreedBuyerFirst is agreedFull with the buyer holding the seller's stamp
// proof over its own agreement, so its first act passes the gate.
func agreedBuyerFirst(t *testing.T) (buyer, seller *side, dealID string) {
	t.Helper()
	buyer, seller, dealID = agreedFull(t)
	// The seller stamps after holding the buyer's agreement; its newest
	// agreement file carries that proof.
	d, err := seller.store.Deal(t.Context(), dealID)
	require.NoError(t, err)
	seller.tick()
	ag, err := seller.store.Agree(t.Context(), seller.cadence, d.OfferDigest, AgreeOptions{})
	require.NoError(t, err)
	proof := ag.StampProof
	_, err = buyer.shelf.Receive(t.Context(), ag.Pair, &proof)
	require.NoError(t, err)
	return buyer, seller, dealID
}

func TestSendAndSettleAPayment(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedBuyerFirst(t)
	check := paidCheck(t, buyer, dealID, "pay")

	// No leg before its send.
	_, err := buyer.store.Settle(ctx, dealID, SettleIn{Check: check, Status: LegSettled, PaymentRef: lnRef})
	assertCoded(t, err, CodeNotSent, dealID, check)

	var stepSeen StepFacts
	sent, err := buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef,
		Step: func(f StepFacts, auth *xdealv1.ActAuthorization, amount int64, currency string) (json.RawMessage, string, error) {
			stepSeen = f
			require.NotNil(t, auth)
			return json.RawMessage(`{"kind":"act","amount":6000}`), "", nil
		}})
	require.NoError(t, err, "%v", err)
	assert.Equal(t, int64(6000), sent.AmountMinor)
	assert.Equal(t, "USD", sent.Currency)
	assert.Equal(t, int64(5), stepSeen.N, "after the intent, check, verdict and approval")
	journal, err := buyer.store.DealJournal(ctx, dealID)
	require.NoError(t, err)
	assert.Equal(t, xdealv1.TypeEvidence, journal[len(journal)-1].Block.RecordType, "the payment-sent evidence")

	// A second send on the check is refused while the first is in flight.
	_, err = buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef})
	var ie *InputError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, CodeSendInFlight, ie.Code)

	buyer.tick()
	leg, err := buyer.store.Settle(ctx, dealID, SettleIn{Check: check, Status: LegSettled, PaymentRef: lnRef})
	require.NoError(t, err)
	b, _ := settleOf(leg.Record)
	assert.Equal(t, xdealv1.LegPayer, b.Leg)
	require.NotNil(t, leg.Authorization)
	send, err := buyer.store.SendOf(ctx, dealID, check)
	require.NoError(t, err)
	assert.Equal(t, leg.Digest, send.Leg, "the leg closed its send")

	buyer.tick()
	again, err := buyer.store.Settle(ctx, dealID, SettleIn{Check: check, Status: LegSettled, PaymentRef: lnRef})
	require.NoError(t, err)
	assert.True(t, again.Again)
	_, err = buyer.store.Settle(ctx, dealID, SettleIn{Check: check, Status: LegFailed, PaymentRef: lnRef})
	assert.Contains(t, inputReason(t, err), "already sealed as leg")
	_, err = buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef})
	assert.Contains(t, inputReason(t, err), "is used: its payment was sealed as leg")
}

func TestAFailedPaymentRewritesItsStep(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedBuyerFirst(t)
	check := paidCheck(t, buyer, dealID, "fail")
	_, err := buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef,
		Step: func(StepFacts, *xdealv1.ActAuthorization, int64, string) (json.RawMessage, string, error) {
			return json.RawMessage(`{"amount":6000}`), "", nil
		}})
	require.NoError(t, err)
	buyer.tick()
	_, err = buyer.store.Settle(ctx, dealID, SettleIn{Check: check, Status: LegFailed, PaymentRef: lnRef,
		CloseStep: func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{"amount":null}`), nil }})
	require.NoError(t, err)
	steps, err := buyer.store.Steps(ctx, dealID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"amount":null}`, string(steps[len(steps)-1].Local))
}

func TestSendPaymentRefusals(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedBuyerFirst(t)
	check := paidCheck(t, buyer, dealID, "refusals")

	refused := errors.New("refused by the caller")
	_, err := buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef,
		Before: func(xdealv1.Record, *xdealv1.ActAuthorization) error { return refused }})
	require.ErrorIs(t, err, refused)
	send, err := buyer.store.SendOf(ctx, dealID, check)
	require.NoError(t, err)
	assert.Nil(t, send, "nothing was stored")

	_, err = buyer.store.SendPayment(ctx, dealID, SendIn{Check: strings.Repeat("0", 64), PaymentRef: lnRef})
	assert.Contains(t, inputReason(t, err), "is not a check of deal")
	_, err = buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: json.RawMessage(`{"type":"made.up","value":"x"}`)})
	assert.Contains(t, inputReason(t, err), "neither a registered payment_ref type")

	// A send whose evidence never reached the journal is withdrawn.
	stop := errors.New("the append failed")
	appendJournalHook = func() error { return stop }
	t.Cleanup(func() { appendJournalHook = nil })
	_, err = buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef})
	require.ErrorIs(t, err, stop)
	appendJournalHook = nil
	send, err = buyer.store.SendOf(ctx, dealID, check)
	require.NoError(t, err)
	assert.Nil(t, send, "withdrawn: a retry sends once")
	_, err = buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef})
	require.NoError(t, err)
}

// The seller's own payee legs cite no check; a check on a row it is owed is
// refused.
func TestSettleWithACheckOnARowYouAreOwed(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	_, err := seller.store.Settle(ctx, dealID, SettleIn{Check: strings.Repeat("1", 64), Status: LegSettled, PaymentRef: lnRef})
	assertCoded(t, err, CodeSettlePayee, "payment", "paid_as_agreed", dealID)
}

// A run that stopped after sealing the payer leg, before closing its send,
// is finished by the same call made again: the leg is found by its check,
// returned, and its send closed. A CloseStep that fails leaves the leg
// sealed and the send open, and the same call finishes it too.
func TestAPayerLegSealedBeforeAStopIsFinished(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedBuyerFirst(t)
	check := paidCheck(t, buyer, dealID, "stop")
	_, err := buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef,
		Step: func(StepFacts, *xdealv1.ActAuthorization, int64, string) (json.RawMessage, string, error) {
			return json.RawMessage(`{"amount":6000}`), "", nil
		}})
	require.NoError(t, err)
	buyer.tick()
	stop := errors.New("the process died")
	afterLegSealedHook = func() error { return stop }
	t.Cleanup(func() { afterLegSealedHook = nil })
	in := SettleIn{Check: check, Status: LegFailed, PaymentRef: lnRef,
		CloseStep: func(json.RawMessage) (json.RawMessage, error) { return nil, stop }}
	_, err = buyer.store.Settle(ctx, dealID, in)
	require.ErrorIs(t, err, stop)
	afterLegSealedHook = nil
	send, err := buyer.store.SendOf(ctx, dealID, check)
	require.NoError(t, err)
	assert.Empty(t, send.Leg, "sealed, not closed")

	// The CloseStep fails: the leg is found again, the send stays open.
	_, err = buyer.store.Settle(ctx, dealID, in)
	require.ErrorIs(t, err, stop)
	send, err = buyer.store.SendOf(ctx, dealID, check)
	require.NoError(t, err)
	assert.Empty(t, send.Leg)

	in.CloseStep = func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{"amount":null}`), nil }
	got, err := buyer.store.Settle(ctx, dealID, in)
	require.NoError(t, err)
	assert.True(t, got.Again, "the leg the stopped run sealed")
	send, err = buyer.store.SendOf(ctx, dealID, check)
	require.NoError(t, err)
	assert.Equal(t, got.Digest, send.Leg)
	steps, err := buyer.store.Steps(ctx, dealID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"amount":null}`, string(steps[len(steps)-1].Local))
}

// A details change a stopped run stored and did not append is appended
// before a payment is authorized, so it voids the check it came after.
func TestAPendingDetailsChangeIsReadBeforeAPayment(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedBuyerFirst(t)
	check := paidCheck(t, buyer, dealID, "pending")
	raw, err := json.Marshal(map[string]any{
		xdealv1.Profile: map[string]any{"profile": xdealv1.Profile, "canonicalization": xdealv1.Canonicalization, "record_type": xdealv1.TypeDetailChange,
			"at": buyer.now.UTC().Format("2006-01-02T15:04:05Z"), "deal_id": dealID},
		"body": map[string]any{"source": "counterparty", "changed": []any{"when"}},
	})
	require.NoError(t, err)
	payload, digest, err := xdealv1.Encode(raw)
	require.NoError(t, err)
	steps, err := buyer.store.Steps(ctx, dealID)
	require.NoError(t, err)
	_, err = buyer.db.ExecContext(ctx, `INSERT INTO deal_v1_events (deal_id, n, kind, local, record_digest, payload) VALUES (?, ?, 'change', 'null', ?, ?)`, dealID, len(steps)+1, digest, payload)
	require.NoError(t, err)
	buyer.tick()

	_, err = buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef})
	var na *NotAuthorizedError
	require.ErrorAs(t, err, &na)
	assert.Contains(t, err.Error(), "changed the details after its check")
	held, err := buyer.store.journalHolds(ctx, digest)
	require.NoError(t, err)
	assert.True(t, held, "the stopped run's record was appended")
}

// RefFor and LegRef give the references from the check and the send.
func TestTheReferenceCallbacks(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedBuyerFirst(t)
	check := paidCheck(t, buyer, dealID, "callbacks")
	var sawCheck *xdealv1.CheckBody
	_, err := buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, RefFor: func(cb *xdealv1.CheckBody) (json.RawMessage, error) {
		sawCheck = cb
		return json.RawMessage(`{"type":"ln.payment_hash","value":"` + cb.PaymentRef + `"}`), nil
	}})
	require.NoError(t, err)
	require.NotNil(t, sawCheck)
	buyer.tick()
	var sawSend *Send
	leg, err := buyer.store.Settle(ctx, dealID, SettleIn{Check: check, Status: LegSettled, LegRef: func(_ *xdealv1.CheckBody, send *Send) (json.RawMessage, error) {
		sawSend = send
		return send.RefJSON, nil
	}})
	require.NoError(t, err)
	require.NotNil(t, sawSend)
	assert.Equal(t, sawCheck.PaymentRef, sawSend.Ref)
	assert.False(t, leg.Again)
}

// A send whose evidence was stored but reported failed keeps the send and
// its step; a send stopped after it was stored, before its evidence was
// appended, is finished by the next read. Either way the evidence is in the
// journal once. (Moved from capsulectl-deal's TestASendWhose... and
// TestASendStoppedBeforeItsAppendIsFinished, evidencebook#899 phase 3.)
func TestASendStoppedAroundItsAppend(t *testing.T) {
	ctx := t.Context()
	evidence := func(p *side, dealID string) int {
		journal, err := p.store.DealJournal(ctx, dealID)
		require.NoError(t, err)
		n := 0
		for _, r := range journal {
			if r.Block.RecordType == xdealv1.TypeEvidence {
				n++
			}
		}
		return n
	}
	for _, stop := range []string{"after the append", "before the append"} {
		t.Run(stop, func(t *testing.T) {
			buyer, _, dealID := agreedBuyerFirst(t)
			check := paidCheck(t, buyer, dealID, stop)
			died := errors.New("the process died")
			if stop == "after the append" {
				appendJournalAfterHook = func() error { return died }
			} else {
				afterSendStoredHook = func() error { return died }
			}
			t.Cleanup(func() { appendJournalAfterHook, afterSendStoredHook = nil, nil })
			_, err := buyer.store.SendPayment(ctx, dealID, SendIn{Check: check, PaymentRef: lnRef})
			require.ErrorIs(t, err, died)
			appendJournalAfterHook, afterSendStoredHook = nil, nil
			send, err := buyer.store.SendOf(ctx, dealID, check)
			require.NoError(t, err)
			require.NotNil(t, send, "the send stays stored")
			_, err = buyer.store.Steps(ctx, dealID)
			require.NoError(t, err)
			assert.Equal(t, 1, evidence(buyer, dealID), "the evidence once")
			_, err = buyer.store.Steps(ctx, dealID)
			require.NoError(t, err)
			assert.Equal(t, 1, evidence(buyer, dealID), "and never again")
		})
	}
}

// A refund on a cancel check that states a fee is refused at its send; one
// sent before that rule completes, and the call says its amount is not
// checked (evidencebook#743).
func TestARefundOnAFeeCancelCheck(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	received, err := seller.store.Settle(ctx, dealID, payeeIn(LegSettled))
	require.NoError(t, err)
	_, err = seller.store.SealJournal(ctx, dealID, JournalStep{Kind: xdealv1.TypeIntent, Build: func(StepFacts) (StepParts, error) {
		return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeIntent, Body: map[string]any{"verbatim_commitment": nonceOf("seller intent"), "allowed": []any{"cancel"}}}}, nil
	}})
	require.NoError(t, err)
	cancelled, fee := int64(6000), int64(500)
	card := nonceOf("cancel card")
	seller.tick()
	checked, err := seller.store.Check(ctx, dealID, CheckIn{Check: xdealv1.CheckBody{CheckBody: dealprofile.CheckBody{Action: "cancel", CancelledAmountMinor: &cancelled, FeeMinor: &fee, Currency: "USD"}},
		Verdict: passVerdict(card)})
	require.NoError(t, err)
	seller.tick()
	_, err = seller.store.SealJournal(ctx, dealID, approvalStep(checked.Verdict, card, nonceOf("said")))
	require.NoError(t, err)
	seller.tick()
	refund := SendIn{Check: checked.Check, Reverses: received.Digest, PaymentRef: json.RawMessage(`{"type":"ln.payment_hash","value":"` + strings.Repeat("7", 64) + `"}`)}
	_, err = seller.store.SendPayment(ctx, dealID, refund)
	assert.Contains(t, inputReason(t, err), "does not say who bears it")

	refundFeeGuard = false
	t.Cleanup(func() { refundFeeGuard = true })
	_, err = seller.store.SendPayment(ctx, dealID, refund)
	require.NoError(t, err)
	refundFeeGuard = true
	seller.tick()
	leg, err := seller.store.Settle(ctx, dealID, SettleIn{Check: checked.Check, Reverses: received.Digest, Status: LegSettled, PaymentRef: refund.PaymentRef})
	require.NoError(t, err)
	assert.True(t, leg.AmountNotChecked)
}
