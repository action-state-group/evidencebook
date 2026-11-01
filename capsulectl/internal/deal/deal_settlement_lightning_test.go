package deal

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One Lightning payment of 500 sat (500,000 msat), as the payee's wallet saw
// it: Lexe credits it net of a 5,000 msat receive fee. Synthetic values: no
// payment exists for them; the hash is the SHA-256 of the made-up preimage.
const (
	lnPreimage = "abababababababababababababababababababababababababababababababab"
	lnHash     = "9a2db2e23f1504cd056606553ac049c5e718e8f9ce9233876df1a7a1821af885"
	lnPayee    = "02a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
)

var lnInvoice = `{"bolt11":"lnbc5u1synthetic","payment_hash":"` + lnHash + `","payee":"` + lnPayee + `","amount_msat":500000,"expires_at_ms":1793600000000}`

func lnTransaction(status string) string {
	preimage := `,"preimage":"` + lnPreimage + `"`
	if status != "succeeded" {
		preimage = ""
	}
	return `{"id":"tx-1","payment_hash":"` + lnHash + `","inbound":true,"amount_msat":495000,"fee_msat":5000,"status":"` + status +
		`","created_at_ms":1790528400000,"settled_at_ms":1790528401000` + preimage + `}`
}

func lnMsat(value string) map[string]any {
	return map[string]any{"value": value, "assetCode": lightningBTC, "assetScale": lightningBTCScale}
}

// lnTerms is the buyer's terms leg for 500 sat, in millisatoshi.
func (b *buyerSide) lnTerms() (string, string) {
	return b.seal("ln-terms", map[string]any{"version": "0", "leg": "terms", "sealer_role": "payer", "amount": lnMsat("500000"),
		"deliverable": map[string]any{"content_digest": sha256Hex(deliveredContent)}})
}

func (b *buyerSide) lnPayerObserved(termsRef string) string {
	path, _ := b.seal("ln-payer-observed", map[string]any{"version": "0", "leg": "payer_observed", "sealer_role": "payer", "terms_ref": termsRef,
		"amount": lnMsat("500000"), "routing_fee": lnMsat("1000"), "status": "settled", "observed_at": "2026-09-27T17:30:00Z",
		"payment_ref": map[string]any{"type": "ln.payment_hash", "value": lnHash}})
	return path
}

func noteLightningReceived(t *testing.T, id, terms, invoice, transaction string) error {
	t.Helper()
	return dealRunErr(t, "note", "--deal", id, "--kind", "payment_received", "--terms", terms,
		"--ln-invoice", writeFile(t, "invoice.json", []byte(invoice)), "--ln-transaction", writeFile(t, "tx.json", []byte(transaction)))
}

func TestALightningPaymentReceivedJoinsTheBuyersLegs(t *testing.T) {
	id, _ := sellerDealWithDueDelivery(t)
	buyer := newBuyerSide(t)
	terms, termsID := buyer.lnTerms()
	require.NoError(t, noteLightningReceived(t, id, terms, lnInvoice, lnTransaction("succeeded")))

	alone := dealSettlement(t, "--deal", id, "--leg", terms)
	require.True(t, alone.Settlement.Conforming, alone.Settlement.Failures)
	assert.Equal(t, "payee_stated", alone.Settlement.Settlements[0].PaymentState)

	both := dealSettlement(t, "--deal", id, "--leg", terms, "--leg", buyer.lnPayerObserved(termsID))
	require.True(t, both.Settlement.Conforming, both.Settlement.Failures)
	s := both.Settlement.Settlements[0]
	assert.Equal(t, "agreed", s.PaymentState)
	assert.Equal(t, "settled", s.AgreedStatus)
	assert.Equal(t, "equal", s.TermsAmount, "495,000 credited plus 5,000 fee is the 500,000 msat terms amount")

	// The payee's node id and the invoice string stay out of the records; the
	// payment hash is the reference the two sides join on.
	own := strings.ToLower(dealOwnBundle(t, id))
	assert.NotContains(t, own, lnPayee)
	assert.NotContains(t, own, "lnbc5u1synthetic")
	assert.Contains(t, own, lnHash)
	assert.Contains(t, own, `"source":"lightning_wallet"`)
	// The preimage is sealed on the step's record: the proof the payment settled.
	assert.Contains(t, own, `"preimage":"`+lnPreimage+`"`)
	shared := filepath.Join(t.TempDir(), "shared.json")
	out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "counterparty", "--to", "Example Buyer", "--out", shared)
	require.NoError(t, err, out)
	assert.NotContains(t, strings.ToLower(string(mustRead(t, shared))), lnPayee)
}

func TestALightningPaymentReceivedIsRefusedWhenItDoesNotAddUp(t *testing.T) {
	id, _ := sellerDealWithDueDelivery(t)
	terms, _ := newBuyerSide(t).lnTerms()
	for name, tc := range map[string]struct{ invoice, transaction, want string }{
		"another payment's preimage": {lnInvoice, strings.Replace(lnTransaction("succeeded"), lnPreimage, strings.Repeat("cd", 32), 1), "not this payment's"},
		"a preimage on a failure":    {lnInvoice, strings.Replace(lnTransaction("failed"), `"id"`, `"preimage":"`+lnPreimage+`","id"`, 1), "did not succeed"},
		"still pending":              {lnInvoice, lnTransaction("pending"), "still pending"},
		"another invoice's payment":  {lnInvoice, strings.Replace(lnTransaction("succeeded"), lnHash, strings.Repeat("ab", 32), 1), "payment_hash differs"},
		"outbound":                   {lnInvoice, strings.Replace(lnTransaction("succeeded"), `"inbound":true`, `"inbound":false`, 1), "not inbound"},
		"amount does not add up":     {lnInvoice, strings.Replace(lnTransaction("succeeded"), `"fee_msat":5000`, `"fee_msat":4000`, 1), "not the invoice's 500000 msat"},
		"amount-less invoice":        {strings.Replace(lnInvoice, `"amount_msat":500000`, `"amount_msat":null`, 1), lnTransaction("succeeded"), "fixed amount_msat"},
		"no payee":                   {strings.Replace(lnInvoice, lnPayee, "nobody", 1), lnTransaction("succeeded"), "needs payee"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorContains(t, noteLightningReceived(t, id, terms, tc.invoice, tc.transaction), tc.want)
		})
	}

	// One rail per note.
	err := dealRunErr(t, "note", "--deal", id, "--kind", "payment_received", "--terms", terms,
		"--ln-invoice", writeFile(t, "invoice.json", []byte(lnInvoice)), "--ln-transaction", writeFile(t, "tx.json", []byte(lnTransaction("succeeded"))),
		"--settle-response", writeFile(t, "settle.json", []byte(x402Settle(true))))
	assert.ErrorContains(t, err, "not both")
	for _, flag := range [][]string{{"--receive-fee", "0"}, {"--asset-scale", "11"}} {
		err = dealRunErr(t, append([]string{"note", "--deal", id, "--kind", "payment_received", "--terms", terms,
			"--ln-invoice", writeFile(t, "invoice.json", []byte(lnInvoice)), "--ln-transaction", writeFile(t, "tx.json", []byte(lnTransaction("succeeded")))}, flag...)...)
		assert.ErrorContains(t, err, "not both", flag)
	}
	err = dealRunErr(t, "note", "--deal", id, "--kind", "payment_received", "--terms", terms, "--ln-invoice", writeFile(t, "invoice.json", []byte(lnInvoice)))
	assert.ErrorContains(t, err, "needs --ln-transaction")
}

func TestALightningPaymentToAnotherNodeIsRefused(t *testing.T) {
	id, _ := sellerDealWithDueDelivery(t)
	terms, _ := newBuyerSide(t).lnTerms()
	require.NoError(t, noteLightningReceived(t, id, terms, lnInvoice, lnTransaction("succeeded")))
	other := strings.Replace(lnInvoice, lnPayee, "03"+strings.Repeat("cd", 32), 1)
	assert.ErrorContains(t, noteLightningReceived(t, id, terms, other, lnTransaction("succeeded")), "received to another account")
}

func TestAFailedLightningPaymentIsRecordedAsFailed(t *testing.T) {
	id, _ := sellerDealWithDueDelivery(t)
	buyer := newBuyerSide(t)
	terms, termsID := buyer.lnTerms()
	failed := strings.Replace(lnTransaction("failed"), `"settled_at_ms":1790528401000`, `"settled_at_ms":null`, 1)
	require.NoError(t, noteLightningReceived(t, id, terms, lnInvoice, failed))
	out := dealSettlement(t, "--deal", id, "--leg", terms, "--leg", buyer.lnPayerObserved(termsID))
	assert.Equal(t, "mismatch", out.Settlement.Settlements[0].PaymentState)
}

func TestALightningPayIsCheckedAsIrreversible(t *testing.T) {
	dealFixture(t)
	dealID := dealRun(t, "open", "--input", filepath.Join(bookingFixture, "open.json"))["deal_id"].(string)
	check := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t,
		`{"action":"pay","amount_minor":38000,"authorized_max_minor":38000,"recourse":{"rail":"lightning","refundable":false}}`))
	assert.Equal(t, "pause", check["verdict"], "a Lightning payment cannot be pulled back")
	assert.Contains(t, jsonString(check), "irreversible_rail")
	assert.Contains(t, jsonString(check), "Lightning = no card protection")
}

// The command prints no preimage (its output reaches the agent's transcript),
// and a wallet that reports none still seals the payment, without one.
func TestALightningPreimageIsSealedNotPrinted(t *testing.T) {
	id, _ := sellerDealWithDueDelivery(t)
	terms, _ := newBuyerSide(t).lnTerms()
	out, err := invoke(t, "", "--profile", "deal", "deal", "note", "--deal", id, "--kind", "payment_received", "--terms", terms,
		"--ln-invoice", writeFile(t, "invoice.json", []byte(lnInvoice)), "--ln-transaction", writeFile(t, "tx.json", []byte(lnTransaction("succeeded"))))
	require.NoError(t, err, out)
	assert.NotContains(t, out, lnPreimage)

	other, _ := sellerDealWithDueDelivery(t)
	terms2, _ := newBuyerSide(t).lnTerms()
	without := strings.Replace(lnTransaction("succeeded"), `,"preimage":"`+lnPreimage+`"`, "", 1)
	require.NoError(t, noteLightningReceived(t, other, terms2, lnInvoice, without))
	assert.NotContains(t, dealOwnBundle(t, other), `"preimage"`)
}
