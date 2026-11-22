// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var v1LnHash = strings.Repeat("ab", 32)

// buyerChecked is a deal both sides agreed to, with the buyer's intent and its
// passing pay check of 4500 USD under paymentRef; the buyer is acting.
func buyerChecked(t *testing.T, paymentRef string) (*handshakeV1, string) {
	t.Helper()
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+paymentRef+`"`)))
	require.Equal(t, "pass", out["verdict"], "%v", out)
	var check string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		check = checkDigest(t, journal)
	})
	return h, check
}

// settlementOf is the settlement member a pushed leg's Capsule carries.
func settlementOf(t *testing.T, file string) map[string]any {
	t.Helper()
	var pair struct {
		Record map[string]json.RawMessage `json:"record"`
	}
	require.NoError(t, json.Unmarshal(readPush(t, file).Record, &pair))
	var m map[string]any
	require.NoError(t, json.Unmarshal(pair.Record["settlement"], &m))
	return m
}

func sendRow(t *testing.T, dealID, check string) *v1Send {
	t.Helper()
	var v *v1Send
	withSession(t, func(s *dealSession) {
		var err error
		v, err = s.sendOf(t.Context(), dealID, check)
		require.NoError(t, err)
	})
	return v
}

// A payment is sent, then settled: --sent counts the amount from the send and
// seals the "payment sent" evidence; --status seals the one payer leg with its
// AAC member, the send closes, and the seller counts the leg.
func TestAPaymentIsSentThenSettled(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	sent := dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	require.NotEmpty(t, sent["sent"], "%v", sent)
	acts := v1ActEvents(t, h.dealID)
	require.Len(t, acts, 1)
	assert.Equal(t, sent["sent"], acts[0].Digest, "the act event's record is the payment-sent evidence")
	assert.Equal(t, "pay", acts[0].Event.Act.Action)
	assert.Equal(t, "out", acts[0].Event.Act.Direction)
	require.NotNil(t, acts[0].Event.Act.AmountMinor)
	assert.Equal(t, int64(4500), *acts[0].Event.Act.AmountMinor, "counted from the send")
	assert.NotEmpty(t, acts[0].Event.Act.AuthorizedBy)
	send := sendRow(t, h.dealID, check)
	require.NotNil(t, send)
	assert.Empty(t, send.Leg, "open")

	file := filepath.Join(t.TempDir(), "leg.json")
	out := dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--out", file)
	assert.Equal(t, "settled", out["status"])
	read, err := xdealv1.VerifyBook(readPush(t, file).Record)
	require.NoError(t, err)
	body, ok := settleOf(read.Record)
	require.True(t, ok)
	assert.Equal(t, xdealv1.LegPayer, body.Leg)
	require.NotNil(t, body.Check)
	assert.Equal(t, check, body.Check.Digest)
	m := settlementOf(t, file)
	assert.Equal(t, "payer_observed", m["leg"])
	assert.Equal(t, "settled", m["status"])
	assert.Equal(t, map[string]any{"value": "4500", "assetCode": "USD", "assetScale": float64(2)}, m["amount"])
	assert.Equal(t, map[string]any{"type": "ln.payment_hash", "value": v1LnHash}, m["payment_ref"])
	assert.Equal(t, out["digest"], sendRow(t, h.dealID, check).Leg, "closed by the leg")
	assert.Len(t, v1ActEvents(t, h.dealID), 1, "the send's act event is the act")

	again := dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--out", file)
	assert.Equal(t, out["digest"], again["digest"], "a re-run rewrites the same file")

	h.seller.act(t)
	assert.Equal(t, "kept, and counted", dealRun(t, "receive", file)["record"])
}

// The two calls hold their order and their binding.
func TestAPaymentsTwoCallsAreRefusedOutOfOrder(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	out := filepath.Join(t.TempDir(), "leg.json")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--payment-ref-type", "ln.payment_hash", "--out", out), "--sent before the handoff")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--status", "pending", "--out", out), "settled or failed")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash", "--payment-ref", strings.Repeat("cd", 32)), "check's payment_ref")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash"), "is in flight")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--payment-ref-type", "acp.order_id", "--out", out), "was sent under ln.payment_hash")
}

// A failed payment keeps its approval used and counts no spend.
func TestAFailedPaymentCountsNoSpend(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	file := filepath.Join(t.TempDir(), "leg.json")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "failed", "--out", file)
	assert.Equal(t, "failed", settlementOf(t, file)["status"])
	acts := v1ActEvents(t, h.dealID)
	require.Len(t, acts, 1)
	assert.Nil(t, acts[0].Event.Act.AmountMinor, "no spend")
	assert.NotEmpty(t, acts[0].Event.Act.AuthorizedBy, "the approval stays used")
}

// While a payment is in flight, a record that changes the deal's details is
// refused; once it is settled, it is sealed.
func TestADetailChangeWaitsWhileAPaymentIsInFlight(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	change := []string{"note", "--deal", h.dealID, "--kind", "change", "--input", writeJSON(t, `{"source":"checkout page","who":{"domain":"kettle.example","payee":"Kettle Shop Two"}}`)}
	assert.Contains(t, v1Err(t, change...), "is in flight")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--out", filepath.Join(t.TempDir(), "leg.json"))
	dealRun(t, change...)
}

// On x402 the send binds the check's nonce, and the leg carries the
// transaction with its network; a Lightning leg whose value differs from its
// check's is refused.
func TestAnX402PaymentCarriesItsTransaction(t *testing.T) {
	nonce := strings.Repeat("0f", 32)
	h, check := buyerChecked(t, nonce)
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "x402.transaction"), "--payment-ref-network is required")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "x402.transaction", "--payment-ref-network", "base")
	assert.Equal(t, nonce, sendRow(t, h.dealID, check).Ref, "the send binds the nonce known before paying")
	file := filepath.Join(t.TempDir(), "leg.json")
	tx := "0x" + strings.Repeat("9a", 32)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--payment-ref", tx, "--payment-ref-network", "base", "--out", file)
	assert.Equal(t, map[string]any{"type": "x402.transaction", "value": tx, "network": "base"}, settlementOf(t, file)["payment_ref"])
}

// What the seller received is a payee leg with its receive fee; with no fee it
// is refused; a later observation supersedes an earlier one; a re-run seals
// nothing.
func TestAPayeeLegRecordsWhatWasReceived(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.seller.act(t)
	base := []string{"settle", "--deal", h.dealID, "--payment-ref-type", "ln.payment_hash", "--payment-ref", v1LnHash, "--received", "4470"}
	assert.Contains(t, v1Err(t, append(base, "--status", "settled", "--out", filepath.Join(t.TempDir(), "x.json"))...), "--receive-fee are required")
	pending := filepath.Join(t.TempDir(), "pending.json")
	first := dealRun(t, append(base, "--receive-fee", "30", "--status", "pending", "--out", pending)...)
	m := settlementOf(t, pending)
	assert.Equal(t, "payee_observed", m["leg"])
	assert.Equal(t, map[string]any{"value": "30", "assetCode": "USD", "assetScale": float64(2)}, m["receive_fee"])
	again := dealRun(t, append(base, "--receive-fee", "30", "--status", "pending", "--out", pending)...)
	assert.Equal(t, first["digest"], again["digest"])

	settled := filepath.Join(t.TempDir(), "settled.json")
	dealRun(t, append(base, "--receive-fee", "30", "--status", "settled", "--supersedes", first["digest"].(string), "--out", settled)...)
	read, err := xdealv1.VerifyBook(readPush(t, settled).Record)
	require.NoError(t, err)
	body, _ := settleOf(read.Record)
	require.NotNil(t, body.Supersedes)
	assert.Equal(t, first["digest"], body.Supersedes.Digest)
	assert.Empty(t, v1ActEvents(t, h.dealID), "what was received is not an act")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", strings.Repeat("ee", 32), "--sent"), "you do not owe")
}

// The rules history counts an x-deal-v1 payment from its send, read as the
// v0-shaped record of its own deal; a failed one stays in it with no amount.
func TestTheRulesHistoryCountsAV1Payment(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	history := func() []interface{} {
		var got []interface{}
		withSession(t, func(s *dealSession) {
			var err error
			got, _, err = s.rulesHistory(t.Context())
			require.NoError(t, err)
		})
		return got
	}
	assert.Empty(t, history(), "nothing acted yet")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	got := history()
	require.Len(t, got, 1, "counted from the send")
	raw, err := json.Marshal(got[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"amount_minor":4500`)
}

// A refund: the seller refunds the payment it received with a payer leg on its
// cancel check that reverses its own payee leg; the buyer's refund received
// reverses its own payer leg and returns the money once, when settled.
func TestARefundOnBothSides(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	paid := dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--out", filepath.Join(t.TempDir(), "paid.json"))

	h.seller.act(t)
	received := dealRun(t, "settle", "--deal", h.dealID, "--payment-ref-type", "ln.payment_hash", "--payment-ref", v1LnHash,
		"--received", "4500", "--receive-fee", "0", "--status", "settled", "--out", filepath.Join(t.TempDir(), "received.json"))
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening))
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"cancel","amount_minor":4500,"terms":{"item":"lamp","price_minor":4500,"currency":"USD"}}`))
	require.Equal(t, "pass", out["verdict"], "%v", out)
	var cancel string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		cancel = checkDigest(t, journal)
	})
	refundRef := strings.Repeat("71", 32)
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", cancel, "--sent", "--payment-ref-type", "ln.payment_hash", "--payment-ref", refundRef), "you do not owe",
		"without --reverses the seller does not owe the row")
	dealRun(t, "settle", "--deal", h.dealID, "--check", cancel, "--reverses", received["digest"].(string), "--sent", "--payment-ref-type", "ln.payment_hash", "--payment-ref", refundRef)
	refundFile := filepath.Join(t.TempDir(), "refund.json")
	dealRun(t, "settle", "--deal", h.dealID, "--check", cancel, "--reverses", received["digest"].(string), "--status", "settled", "--out", refundFile)
	read, err := xdealv1.VerifyBook(readPush(t, refundFile).Record)
	require.NoError(t, err)
	body, _ := settleOf(read.Record)
	assert.Equal(t, xdealv1.LegPayer, body.Leg, "the seller refunding is the payer")
	acts := v1ActEvents(t, h.dealID)
	require.Len(t, acts, 1)
	assert.Equal(t, "cancel", acts[0].Event.Act.Action)
	assert.Empty(t, acts[0].Event.Act.Direction, "a seller's refund counts toward no buyer cap")

	h.buyer.act(t)
	base := []string{"settle", "--deal", h.dealID, "--reverses", paid["digest"].(string), "--payment-ref-type", "ln.payment_hash", "--payment-ref", refundRef, "--received", "4500", "--receive-fee", "0"}
	pending := dealRun(t, append(base, "--status", "pending", "--out", filepath.Join(t.TempDir(), "rp.json"))...)
	credits := func() int {
		n := 0
		for _, se := range v1ActEvents(t, h.dealID) {
			if se.Event.Act.Direction == "in" {
				n++
			}
		}
		return n
	}
	assert.Equal(t, 0, credits(), "a pending refund returns nothing yet")
	dealRun(t, append(base, "--status", "settled", "--supersedes", pending["digest"].(string), "--out", filepath.Join(t.TempDir(), "rs.json"))...)
	assert.Equal(t, 1, credits(), "settled: the money comes back once")
	assert.Contains(t, v1Err(t, append(base, "--status", "settled", "--out", filepath.Join(t.TempDir(), "r2.json"))...), "already refunded")
}

// buyerCheckedFor is buyerChecked on an offer of the deal's choosing, paying
// amount under paymentRef with an intent that allows it.
func buyerCheckedFor(t *testing.T, offer func(string) string, amount int, paymentRef string) (*handshakeV1, string) {
	t.Helper()
	h := agreedBoth(t, offer)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, `{"verbatim":"buy the lamp, at most 70 dollars","max_total_minor":7000}`), "--opening", writeJSON(t, v1Opening))
	in := fmt.Sprintf(`{"action":"pay","amount_minor":%d,"authorized_max_minor":%d,"terms":{"item":"lamp","price_minor":%d,"currency":"USD"},"payment_ref":%q}`, amount, amount, amount, paymentRef)
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, in))
	require.Equal(t, "pass", out["verdict"], "%v", out)
	var check string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		check = checkDigest(t, journal)
	})
	return h, check
}

// The legs both sides seal with deal settle join in the library's payment
// check under the offer's terms leg and pass.
func TestBothSidesLegsPassThePaymentCheck(t *testing.T) {
	h, check := buyerCheckedFor(t, paymentOffer, 6000, v1LnHash)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	payer := filepath.Join(t.TempDir(), "payer.json")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--statement", "paid_as_agreed=met", "--out", payer)
	h.seller.act(t)
	dealRun(t, "receive", payer)
	payee := filepath.Join(t.TempDir(), "payee.json")
	dealRun(t, "settle", "--deal", h.dealID, "--payment-ref-type", "ln.payment_hash", "--payment-ref", v1LnHash,
		"--received", "6000", "--receive-fee", "0", "--status", "settled", "--statement", "paid_as_agreed=met", "--out", payee)
	offer, err := os.ReadFile(h.offerFile)
	require.NoError(t, err)
	res, err := xdealv1.PaymentCheck(xdealv1.PaymentInput{Offer: offer, OfferDigest: h.offer, AcceptorKey: h.seller.party.signer, Row: "paid_as_agreed",
		Legs: [][]byte{readPush(t, payer).Record, readPush(t, payee).Record}})
	require.NoError(t, err)
	assert.Equal(t, xdealv1.PaymentPass, res.Check, "%+v", res)
}

// On x402 the leg carries the rail's transaction: --status without it is
// refused rather than sealing the check's nonce as the transaction; a re-run
// with other inputs, another network or another --reverses is refused.
func TestAnAnsweredPaymentIsBoundToItsSend(t *testing.T) {
	nonce := strings.Repeat("0f", 32)
	h, check := buyerChecked(t, nonce)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "x402.transaction", "--payment-ref-network", "base")
	file := filepath.Join(t.TempDir(), "leg.json")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--out", file), "--payment-ref is required")
	tx := "0x" + strings.Repeat("9a", 32)
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--payment-ref", tx, "--payment-ref-network", "ethereum", "--out", file), "sent with network base")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--reverses", strings.Repeat("aa", 32), "--status", "settled", "--payment-ref", tx, "--out", file), "--reverses must name")
	args := []string{"settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--payment-ref", tx, "--out", file}
	first := dealRun(t, args...)
	assert.Equal(t, first["digest"], dealRun(t, args...)["digest"], "the same inputs rewrite the file")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--status", "failed", "--payment-ref", tx, "--out", file), "already sealed")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "x402.transaction", "--payment-ref-network", "base"), "is used")
}

// A send the authorization refuses (a paused check with no approval) seals
// nothing and counts nothing.
func TestARefusedSendSealsNothing(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"pay","amount_minor":9000,"authorized_max_minor":9000,"terms":{"item":"lamp","price_minor":9000,"currency":"USD"},"payment_ref":"`+v1LnHash+`"}`))
	require.NotEqual(t, "pass", out["verdict"], "over the intent's cap: %v", out)
	var check string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		check = checkDigest(t, journal)
	})
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash"), "not authorized")
	assert.Nil(t, sendRow(t, h.dealID, check))
	assert.Empty(t, v1ActEvents(t, h.dealID))
}

// A run stopped after the payer leg is sealed and before its send is closed is
// finished by the next command, and the same --status rewrites the leg.
func TestALegSealedBeforeItsSendClosedIsFinished(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	afterLegSealedHook = func() error { return fmt.Errorf("stopped") }
	file := filepath.Join(t.TempDir(), "leg.json")
	args := []string{"settle", "--deal", h.dealID, "--check", check, "--status", "failed", "--out", file}
	assert.Contains(t, v1Err(t, args...), "stopped")
	afterLegSealedHook = nil
	require.Empty(t, sendRow(t, h.dealID, check).Leg, "the run stopped before closing")
	out := dealRun(t, args...)
	assert.Equal(t, out["digest"], sendRow(t, h.dealID, check).Leg, "closed by reconciliation")
	acts := v1ActEvents(t, h.dealID)
	require.Len(t, acts, 1)
	assert.Nil(t, acts[0].Event.Act.AmountMinor, "the failed payment counts no spend")
}

// Refund edges: a failed payment cannot be refunded; a failed refund leaves
// the leg free for a new one; two refunds of one leg cannot be in flight at
// once; the money returned is what was received, net of the fee.
func TestRefundEdges(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	paid := dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--out", filepath.Join(t.TempDir(), "paid.json"))

	h.seller.act(t)
	received := dealRun(t, "settle", "--deal", h.dealID, "--payment-ref-type", "ln.payment_hash", "--payment-ref", v1LnHash,
		"--received", "4500", "--receive-fee", "0", "--status", "settled", "--out", filepath.Join(t.TempDir(), "received.json"))["digest"].(string)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening))
	cancelCheck := func() string {
		out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"cancel","amount_minor":4500,"terms":{"item":"lamp","price_minor":4500,"currency":"USD"}}`))
		require.Equal(t, "pass", out["verdict"], "%v", out)
		var d string
		withSession(t, func(s *dealSession) {
			journal, err := s.v1DealJournal(t.Context(), h.dealID)
			require.NoError(t, err)
			d = checkDigest(t, journal)
		})
		return d
	}
	refund := func(check, ref string) []string {
		return []string{"settle", "--deal", h.dealID, "--check", check, "--reverses", received, "--payment-ref-type", "ln.payment_hash", "--payment-ref", ref}
	}
	first := cancelCheck()
	dealRun(t, append(refund(first, strings.Repeat("71", 32)), "--sent")...)
	second := cancelCheck()
	assert.Contains(t, v1Err(t, append(refund(second, strings.Repeat("72", 32)), "--sent")...), "already refunded", "one refund of a leg in flight at a time")
	failedFile := filepath.Join(t.TempDir(), "rf.json")
	dealRun(t, "settle", "--deal", h.dealID, "--check", first, "--reverses", received, "--status", "failed", "--out", failedFile)
	again := dealRun(t, "settle", "--deal", h.dealID, "--check", first, "--reverses", received, "--status", "failed", "--out", failedFile)
	assert.NotEmpty(t, again["digest"], "a refund's re-run rewrites its file")
	dealRun(t, append(refund(second, strings.Repeat("72", 32)), "--sent")...)

	h.buyer.act(t)
	base := []string{"settle", "--deal", h.dealID, "--reverses", paid["digest"].(string), "--payment-ref-type", "ln.payment_hash", "--payment-ref", strings.Repeat("72", 32), "--received", "4470", "--receive-fee", "30", "--status", "settled"}
	rf := filepath.Join(t.TempDir(), "rr.json")
	dealRun(t, append(base, "--out", rf)...)
	dealRun(t, append(base, "--out", rf)...)
	var credit *int64
	n := 0
	for _, se := range v1ActEvents(t, h.dealID) {
		if se.Event.Act.Direction == "in" {
			credit, n = se.Event.Act.AmountMinor, n+1
		}
	}
	require.Equal(t, 1, n, "a re-run credits nothing more")
	assert.Equal(t, int64(4470), *credit, "what came back, net of the fee")
}

// A failed payment moved no money: it cannot be refunded.
func TestAFailedPaymentCannotBeRefunded(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	failed := dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "failed", "--out", filepath.Join(t.TempDir(), "f.json"))
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--reverses", failed["digest"].(string), "--payment-ref-type", "ln.payment_hash", "--payment-ref", v1LnHash,
		"--received", "4500", "--receive-fee", "0", "--status", "settled", "--out", filepath.Join(t.TempDir(), "r.json")), "nothing to refund")
}
