package deal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const shop = "https://Shop.Example"

// merchantPayCheck runs a pay check with the given extra input members and returns
// its check id.
func merchantPayCheck(t *testing.T, dealID, extra string) string {
	t.Helper()
	out := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, extra)))
	require.Equal(t, "pass", out["verdict"], "%v", out)
	return out["check_id"].(string)
}

// legRef is the payment_ref a pushed leg file's settlement member carries.
func legRef(t *testing.T, file string) map[string]any {
	t.Helper()
	ref, _ := settlementOf(t, file)["payment_ref"].(map[string]any)
	return ref
}

// Condition 1: when the checkout shows the order number before paying, the
// pay check carries it and binds it like a Lightning hash; it is trimmed, and
// a different --payment-ref is refused (evidencebook#771).
func TestAMerchantOrderKnownBeforePayingBindsThePayment(t *testing.T) {
	dealID := noLayerAgreed(t)
	check := merchantPayCheck(t, dealID, `,"payment_ref":"  A-1234  "`)
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop, "--payment-ref", "B-9"), "--payment-ref differs")
	dealRun(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop)
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--status", "settled", "--payment-ref", "B-9", "--out", filepath.Join(t.TempDir(), "x.json")), "--payment-ref differs")
	file := filepath.Join(t.TempDir(), "paid.json")
	dealRun(t, "settle", "--deal", dealID, "--check", check, "--status", "settled", "--payment-ref", " A-1234 ", "--out", file)
	assert.Equal(t, map[string]any{"type": merchantOrderType, "value": "A-1234", "origin": "https://shop.example"}, legRef(t, file))
	// Bound before paying: no "recorded after payment" note.
	notes := rowNotes(t, dealID)
	assert.NotContains(t, notes, noteRefAfterPayment)
	assert.Contains(t, notes, noteOneSided, "condition 6: the no-layer row says it is one-sided")
}

// Condition 2: with no reference before paying, the order number comes
// with --status; the note shows only on a settled leg with an order number.
func TestAMerchantOrderRecordedAfterPayment(t *testing.T) {
	dealID := noLayerAgreed(t)
	check := merchantPayCheck(t, dealID, `,"payment_ref_after_payment":true`)
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash"), "is sent as "+merchantOrderType)
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop, "--payment-ref", "A-1"), "leave out --payment-ref here")
	dealRun(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop)
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--status", "settled", "--out", filepath.Join(t.TempDir(), "x.json")), "the merchant's order number")
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--status", "settled", "--payment-ref", "A-1", "--payment-ref-origin", "https://other.example", "--out", filepath.Join(t.TempDir(), "x.json")), "origin")
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--status", "settled", "--payment-ref", "4111-1111-1111-1111", "--out", filepath.Join(t.TempDir(), "x.json")), "looks like a card number", "condition 4 on the order id given after paying")
	file := filepath.Join(t.TempDir(), "paid.json")
	dealRun(t, "settle", "--deal", dealID, "--check", check, "--status", "settled", "--payment-ref", "A-1", "--out", file)
	assert.Equal(t, "A-1", legRef(t, file)["value"])
	assert.Contains(t, rowNotes(t, dealID), noteRefAfterPayment)
}

// A failed checkout that left no order number seals a leg whose value is ""
// and shows no "recorded after payment" note.
func TestAFailedCheckoutLeavesNoOrderNumber(t *testing.T) {
	dealID := noLayerAgreed(t)
	check := merchantPayCheck(t, dealID, `,"payment_ref_after_payment":true`)
	dealRun(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop)
	file := filepath.Join(t.TempDir(), "failed.json")
	dealRun(t, "settle", "--deal", dealID, "--check", check, "--status", "failed", "--out", file)
	assert.Equal(t, "", legRef(t, file)["value"])
	assert.Equal(t, "failed", settlementOf(t, file)["status"])
	assert.NotContains(t, rowNotes(t, dealID), noteRefAfterPayment)
	acts := v1ActEvents(t, dealID)
	require.Len(t, acts, 1)
	assert.Nil(t, acts[0].Event.Act.AmountMinor, "the spend is released")
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop), "is used", "the failed checkout used up its check")
}

// Condition 3: the origin's normal form.
func TestTheCheckoutOriginsNormalForm(t *testing.T) {
	for in, want := range map[string]string{
		"https://Shop.Example":          "https://shop.example",
		"https://shop.example/":         "https://shop.example",
		"HTTPS://SHOP.EXAMPLE:443":      "https://shop.example",
		"http://shop.example:80":        "http://shop.example",
		"https://Bücher.example":        "https://xn--bcher-kva.example",
		" https://bücher.example ":      "https://xn--bcher-kva.example",
		"https://www.Shop.Example/":     "https://www.shop.example",
		"https://shop.example:443/":     "https://shop.example",
		"https://SHOP.example":          "https://shop.example",
		"https://xn--bcher-kva.EXAMPLE": "https://xn--bcher-kva.example",
	} {
		got, err := normalOrigin(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for in, why := range map[string]string{
		"https://shop.example/checkout": "path",
		"https://shop.example/?order=1": "query",
		"https://user@shop.example":     "user info",
		"https://shop.example:8443":     "port",
		"https://shop.example/#done":    "fragment",
		"ftp://shop.example":            "scheme",
		"shop.example":                  "scheme",
		"https://":                      "host",
	} {
		_, err := normalOrigin(in)
		require.Error(t, err, in)
		assert.Contains(t, err.Error(), why, in)
	}
}

// Condition 4: no card data; separators are set aside, and a hex reference
// or an ordinary order number passes.
func TestNoCardDataInAReference(t *testing.T) {
	for _, card := range []string{"4111111111111111", "4111 1111 1111 1111", "4111-1111-1111-1111", "5555555555554444", "378282246310005"} {
		assert.True(t, cardLike(card), card)
	}
	for _, ok := range []string{v1LnHash, "A-1234", "4111111111111112", "123456", "ORDER 4111"} {
		assert.False(t, cardLike(ok), ok)
	}
	dealID := noLayerAgreed(t)
	out := v1Err(t, "check", "--deal", dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"4111 1111 1111 1111"`)))
	assert.Contains(t, out, "looks like a card number", "refused at deal check, before anything is sealed")
}

// Condition 5: exactly one x- type is accepted; a payee leg never takes an
// empty value; the after-paying flag is a pay check's only.
func TestOnlyTheMerchantOrderTypeIsAccepted(t *testing.T) {
	dealID := noLayerAgreed(t)
	check := merchantPayCheck(t, dealID, `,"payment_ref":"A-1"`)
	for _, typo := range []string{"x-deal-v1.merchant_orders", "x-deal-v1.other", "x-merchant_order"} {
		assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", typo, "--payment-ref-origin", shop), "--payment-ref-type must be one of", typo)
	}
	assert.Contains(t, v1Err(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash", "--payment-ref-origin", shop), "goes with "+merchantOrderType+" only")
	assert.Contains(t, v1Err(t, "check", "--deal", dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"A-2","payment_ref_after_payment":true`))), "leave out one of them")
	assert.Contains(t, v1Err(t, "check", "--deal", dealID, "--input", writeJSON(t, `{"action":"commit","terms":{"item":"lamp","price_minor":4500,"currency":"USD"},"payment_ref_after_payment":true}`)), "pay check only")
	p := paymentRefFlags{Type: merchantOrderType, Origin: shop}
	_, err := p.member()
	assert.ErrorContains(t, err, "the merchant's order number", "an empty value outside a pay check's send is refused")
}

// Conditions 2 and 6 in deal report: the notes sit beside the report, keyed
// by row id, and the printed report is the payload, with no note in it. The
// private-type note shows when the check read the legs unjoined.
func TestTheRowNotesSitBesideTheReport(t *testing.T) {
	dealID := noLayerAgreed(t)
	check := merchantPayCheck(t, dealID, `,"payment_ref_after_payment":true`)
	dealRun(t, "settle", "--deal", dealID, "--check", check, "--sent", "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop)
	dealRun(t, "settle", "--deal", dealID, "--check", check, "--status", "settled", "--payment-ref", "A-1", "--out", filepath.Join(t.TempDir(), "paid.json"))
	tickCovering(t)
	report := dealRun(t, "report", "--deal", dealID)
	notes, ok := report["row_notes"].(map[string]any)
	require.True(t, ok, "%v", report)
	raw, _ := json.Marshal(notes["paid_as_agreed"])
	assert.Contains(t, string(raw), "recorded after payment")
	payload, _ := json.Marshal(report["report"])
	assert.NotContains(t, string(payload), "recorded after payment", "the report itself is the sealed payload")
	withSession(t, func(s *dealSession) {
		d, err := s.v1DealOf(t.Context(), dealID)
		require.NoError(t, err)
		got, err := s.v1PaymentRowNotes(t.Context(), d, []string{"paid_as_agreed"}, map[string]xdealv1.PaymentResult{"paid_as_agreed": {Reason: xdealv1.ReasonUnjoined}})
		require.NoError(t, err)
		assert.Contains(t, got["paid_as_agreed"], notePrivateType)
	})
}

// rowNotes is the payment row's notes in deal settlement.
func rowNotes(t *testing.T, dealID string) []string {
	t.Helper()
	tickCovering(t)
	out := dealRun(t, "settlement", "--deal", dealID)
	var notes []string
	for _, r := range out["rows"].([]any) {
		for _, n := range r.(map[string]any)["notes"].([]any) {
			notes = append(notes, n.(string))
		}
	}
	return notes
}

// tickCovering moves the clock two days on and ticks, so a checkpoint covers
// the book before it is read.
func tickCovering(t *testing.T) {
	t.Helper()
	clockAt(t, cli.DealClock().Add(48*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
}

// Both sides' legs under the private type, through VerifyBook and
// PaymentCheck: AAC's verifier reads the pair unjoined (Settlement Records
// -01), no leg is excluded, and the payee leg's origin member passes the
// settlement member's checks (evidencebook#771).
func TestBothSidesLegsUnderTheMerchantOrderReadUnjoined(t *testing.T) {
	h, check := buyerCheckedFor(t, paymentOffer, 6000, "A-77")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop)
	payer := filepath.Join(t.TempDir(), "payer.json")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--status", "settled", "--out", payer)
	h.seller.act(t)
	dealRun(t, "receive", payer)
	payee := filepath.Join(t.TempDir(), "payee.json")
	dealRun(t, "settle", "--deal", h.dealID, "--payment-ref-type", merchantOrderType, "--payment-ref", "A-77", "--payment-ref-origin", "https://SHOP.example/",
		"--received", "6000", "--receive-fee", "0", "--status", "settled", "--out", payee)
	assert.Equal(t, legRef(t, payer), legRef(t, payee), "both sides' references are one normal form")
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop,
		"--received", "6000", "--receive-fee", "0", "--status", "settled", "--out", filepath.Join(t.TempDir(), "p2.json")), "the merchant's order number", "a payee leg takes no empty value")
	offer, err := os.ReadFile(h.offerFile)
	require.NoError(t, err)
	res, err := xdealv1.PaymentCheck(xdealv1.PaymentInput{Offer: offer, OfferDigest: h.offer, AcceptorKey: h.seller.party.signer, Row: "paid_as_agreed",
		Legs: [][]byte{readPush(t, payer).Record, readPush(t, payee).Record}})
	require.NoError(t, err)
	assert.Empty(t, res.Check)
	assert.Equal(t, xdealv1.ReasonUnjoined, res.Reason, "%+v", res)
	assert.Empty(t, res.Excluded, "no leg is excluded")

	// Each side's own settlement and report say why the pair is unjoined:
	// the seller, who wrote no send, from its payee leg (#772 review).
	h.buyer.act(t)
	dealRun(t, "receive", payee)
	for _, side := range []v1Side{h.buyer, h.seller} {
		side.act(t)
		notes := rowNotes(t, h.dealID)
		assert.Contains(t, notes, notePrivateType)
		report := dealRun(t, "report", "--deal", h.dealID)
		raw, _ := json.Marshal(report["row_notes"])
		assert.Contains(t, string(raw), "a generic verifier does not join")
	}
	// An origin on a Lightning payment's --status is refused plainly.
	h2, check2 := buyerCheckedFor(t, paymentOffer, 6000, v1LnHash)
	dealRun(t, "settle", "--deal", h2.dealID, "--check", check2, "--sent", "--payment-ref-type", "ln.payment_hash")
	assert.Contains(t, v1Err(t, "settle", "--deal", h2.dealID, "--check", check2, "--status", "settled", "--payment-ref-origin", shop, "--out", filepath.Join(t.TempDir(), "x.json")), "goes with "+merchantOrderType+" only")
}

// The send-time recheck (#718/#723) holds in the after-paying mode: a
// second card payment that the first puts over the cap is refused at
// --sent, and fits again once the first failed (evidencebook#771).
func TestARecheckHoldsForAPaymentRecordedAfterPaying(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	pinStub(t, "10000", "5000")
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	check := func() string {
		out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"pay","amount_minor":3000,"authorized_max_minor":3000,"terms":{"item":"lamp","price_minor":3000,"currency":"USD"},"payment_ref_after_payment":true}`))
		require.Equal(t, "pass", out["verdict"], "each fits the cap alone: %v", out)
		return out["check_id"].(string)
	}
	a, b := check(), check()
	sent := []string{"--payment-ref-type", merchantOrderType, "--payment-ref-origin", shop}
	dealRun(t, append([]string{"settle", "--deal", h.dealID, "--check", a, "--sent"}, sent...)...)
	assert.Contains(t, v1Err(t, append([]string{"settle", "--deal", h.dealID, "--check", b, "--sent"}, sent...)...), "no longer passes")
	dealRun(t, "settle", "--deal", h.dealID, "--check", a, "--status", "failed", "--out", filepath.Join(t.TempDir(), "a.json"))
	dealRun(t, append([]string{"settle", "--deal", h.dealID, "--check", b, "--sent"}, sent...)...)
}
