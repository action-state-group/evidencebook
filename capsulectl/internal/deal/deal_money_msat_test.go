package deal

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func msatPay(n int64, dir string) sealedEvent {
	return sealedEvent{Event: dealEvent{Kind: "act", Act: &dealAct{Action: "pay", AmountMinor: &n, Currency: "BTC", Direction: dir, Rail: "lightning"}}}
}

func settledLeg(received, fee string) sealedEvent {
	leg := `{"leg":"payee_observed","status":"settled","received":{"value":"` + received + `","assetCode":"` + lightningBTC + `","assetScale":11},"receive_fee":{"value":"` + fee + `","assetCode":"` + lightningBTC + `","assetScale":11}}`
	return sealedEvent{Event: dealEvent{Kind: "evidence", Evidence: &dealEvidence{Settlement: []byte(leg)}}}
}

func TestMsatMoneyReadsInMsat(t *testing.T) {
	assert.Equal(t, "500 msat", formatMoney(500, "BTC"))
	assert.Equal(t, "-1200 msat", formatMoney(-1200, "btc"))
	assert.Equal(t, "1,200 msat", spokenMoney(1200, "BTC"))
	assert.Equal(t, "500 msat", spokenMoney(500, "BTC"))

	m := buildDealMoney([]sealedEvent{msatPay(500, "out")}, "BTC")
	assert.Equal(t, "Paid 500 msat, returned 0 msat: net 500 msat.", m.Text)
	assert.EqualValues(t, 500, m.NetMinor)

	m = buildDealMoney([]sealedEvent{msatPay(500, "out"), msatPay(200, "in")}, "BTC")
	assert.Equal(t, "Paid 500 msat, returned 200 msat: net 300 msat.", m.Text)
}

func TestPayeeSideShowsTheSealedSettlementLeg(t *testing.T) {
	m := buildDealMoney([]sealedEvent{settledLeg("400", "100")}, "BTC")
	if assert.NotNil(t, m) {
		assert.Equal(t, "Received 400 msat, receive fee 100 msat.", m.Text)
		assert.Zero(t, m.PaidMinor)
	}
	// With a paying act too, the received line follows the paid line.
	m = buildDealMoney([]sealedEvent{msatPay(500, "out"), settledLeg("400", "100")}, "BTC")
	assert.Equal(t, "Paid 500 msat, returned 0 msat: net 500 msat. Received 400 msat, receive fee 100 msat.", m.Text)
	// A leg that did not settle is not money received.
	failed := settledLeg("400", "100")
	failed.Event.Evidence.Settlement = []byte(`{"leg":"payee_observed","status":"failed","received":{"value":"400","assetCode":"` + lightningBTC + `","assetScale":11}}`)
	assert.Nil(t, buildDealMoney([]sealedEvent{failed}, "BTC"))
}

func TestFiatMoneyReadsAsBefore(t *testing.T) {
	n := int64(50)
	pay := sealedEvent{Event: dealEvent{Kind: "act", Act: &dealAct{Action: "pay", AmountMinor: &n, Currency: "USD"}}}
	m := buildDealMoney([]sealedEvent{pay}, "USD")
	assert.Equal(t, "Paid $0.50, returned $0.00: net $0.50.", m.Text)
	assert.Equal(t, "$1,700", spokenMoney(170000, "USD"))
	assert.Equal(t, "¥500", formatMoney(500, "JPY"))
}

func TestReceivedAmountReadsLightningInMsat(t *testing.T) {
	assert.Equal(t, "400 msat", receivedAmount([]byte(`{"received":{"value":"400","assetCode":"`+lightningBTC+`","assetScale":11}}`)))
	assert.Equal(t, "", receivedAmount([]byte(`{"received":{"value":"400","assetCode":"`+lightningBTC+`","assetScale":8}}`)), "another scale of the asset is not read as msat")
}

// The seller's report states the sealed Lightning leg in msat, the unit the
// wallet counted (synthetic invoice and transaction, no real payment).
func TestTheSellersReportStatesTheLightningLegInMsat(t *testing.T) {
	id, _ := sellerDealWithDueDelivery(t)
	terms, _ := newBuyerSide(t).lnTerms()
	require.NoError(t, noteLightningReceived(t, id, terms, lnInvoice, lnTransaction("succeeded")))
	report := dealRun(t, "report", "--deal", id)
	money, _ := report["money"].(map[string]any)
	require.NotNil(t, money, report)
	assert.Equal(t, "Received 495000 msat, receive fee 5000 msat.", money["text"])
}

func TestReceivedTextEdgeCases(t *testing.T) {
	usdc := func(v string, scale int) sealedEvent {
		leg := `{"leg":"payee_observed","status":"settled","received":{"value":"` + v + `","assetCode":"eip155:8453/erc20:0x833589fcd6edb6e08f4c7c32d4f71b54bda02913","assetScale":` + fmt.Sprint(scale) + `}}`
		return sealedEvent{Event: dealEvent{Kind: "evidence", Evidence: &dealEvidence{Settlement: []byte(leg)}}}
	}
	// A USDC leg reads at its own scale, and a fee-less leg states a zero fee.
	assert.Equal(t, "Received 1.50 USDC, receive fee 0.00 USDC.", buildDealMoney([]sealedEvent{usdc("1500000", 6)}, "USD").Text)
	assert.Equal(t, "1.50 USDC", receivedAmount([]byte(`{"received":{"value":"150000000","assetCode":"eip155:8453/erc20:0x833589fcd6edb6e08f4c7c32d4f71b54bda02913","assetScale":8}}`)))
	// A leg in another asset or scale than the first is left out.
	m := buildDealMoney([]sealedEvent{settledLeg("400", "100"), usdc("1500000", 6)}, "BTC")
	assert.Equal(t, "Received 400 msat, receive fee 100 msat.", m.Text)
	// A malformed value is skipped, never a panic.
	bad := settledLeg("not-a-number", "0")
	assert.Nil(t, buildDealMoney([]sealedEvent{bad}, "BTC"))
	assert.Equal(t, "", receivedAmount([]byte(`{"received":{"value":"","assetCode":"`+lightningBTC+`","assetScale":11}}`)))
}

func TestPayeeOnlyMoneyStatesNoPaidFigures(t *testing.T) {
	m := buildDealMoney([]sealedEvent{settledLeg("400", "100")}, "BTC")
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	assert.JSONEq(t, `{"currency":"BTC","received":"Received 400 msat, receive fee 100 msat.","text":"Received 400 msat, receive fee 100 msat."}`, string(raw))
	paid, err := json.Marshal(buildDealMoney([]sealedEvent{msatPay(500, "out")}, "BTC"))
	require.NoError(t, err)
	assert.Contains(t, string(paid), `"net_minor":500`)
}

func TestAShareKeepsAnMsatAmount(t *testing.T) {
	assert.Empty(t, codeLocs("limit 40000 msat, asked 50000 msat"))
}

func TestAPayeeOnlyDealKeepsItsOpeningTermsInTheList(t *testing.T) {
	price := int64(500)
	open := sealedEvent{Event: dealEvent{Kind: "open", Open: &dealOpen{Terms: dealTerms{PriceMinor: &price, Currency: "BTC"}}}}
	sum := dealSummaryOf([]sealedEvent{open, settledLeg("400", "100")})
	if assert.NotNil(t, sum.Amount) {
		assert.Equal(t, "opening_terms", sum.Amount.Basis)
		assert.EqualValues(t, 500, sum.Amount.AmountMinor)
	}
}
