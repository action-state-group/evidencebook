package deal

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noLayerAgreed is a deal with a counterparty that runs no layer (a shop):
// the profile's own offer to no key (to null, PROFILE §4.7), its admission
// decision and intent, and its agreement, sealed alone (#762).
func noLayerAgreed(t *testing.T) (dealID string) {
	t.Helper()
	newV1Side(t)
	// The payment row is recomputed, so the payment check applies to it.
	offer := strings.Replace(v1OfferInput, `"owed_by": "buyer",`, `"owed_by": "buyer", "recomputed": true,`, 1)
	require.NotEqual(t, v1OfferInput, offer)
	offered := dealRun(t, "offer", "--input", writeJSON(t, offer), "--out", filepath.Join(t.TempDir(), "o.json"))
	assert.Contains(t, offered["note"], "there is nothing to send")
	dealID = offered["deal_id"].(string)
	intent := dealRun(t, "note", "--deal", dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	agreed := dealRun(t, "agree", offered["offer"].(string), "--admit", sealAdmit(t, "GO"), "--intent", intent["intent"].(string), "--out", filepath.Join(t.TempDir(), "a.json"))
	assert.Equal(t, "offerer", agreed["side"])
	assert.Contains(t, agreed["note"], "the other side keeps no records of its own")
	return dealID
}

// ownBook is the deal's own book, as sealed.
func ownBook(t *testing.T, dealID string) []xdealv1.Record {
	t.Helper()
	var book []xdealv1.Record
	withSession(t, func(s *dealSession) {
		d, err := s.v1DealOf(t.Context(), dealID)
		require.NoError(t, err)
		book, err = s.ownBookV1(t.Context(), d)
		require.NoError(t, err)
	})
	return book
}

// A retail purchase on x-deal-v1: the agreement is the no-layer shape, the
// first payment goes ahead unguarded and marked, every book record has seen
// null, and the deal reads end to end: settlement, report, bundle, list and
// the Standard History card (#762).
func TestARetailPurchaseWithNoLayerOnTheOtherSide(t *testing.T) {
	dealID := noLayerAgreed(t)
	book := ownBook(t, dealID)
	require.Len(t, book, 1)
	ag, ok := agreeOf(book[0])
	require.True(t, ok)
	require.NotNil(t, ag.CounterpartyLayer)
	assert.False(t, *ag.CounterpartyLayer, "counterparty_layer false")
	assert.True(t, ag.CounterpartyKey.IsNull(), "counterparty_key null")
	assert.Equal(t, xdealv1.SideOfferer, ag.Side)

	check := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+v1LnHash+`"`)))
	require.Equal(t, "pass", check["verdict"], "%v", check)
	id := check["check_id"].(string)
	sent := dealRun(t, "settle", "--deal", dealID, "--check", id, "--sent", "--payment-ref-type", "ln.payment_hash")
	require.NotEmpty(t, sent["sent"], "the first payment is not held for a stamp proof: %v", sent)
	paid := dealRun(t, "settle", "--deal", dealID, "--check", id, "--status", "settled", "--out", filepath.Join(t.TempDir(), "paid.json"))
	gate, _ := json.Marshal(paid["first_act_gate"])
	assert.Contains(t, string(gate), xdealv1.GateNoLayer, "the first act is marked unguarded: %v", paid)
	dealRun(t, "statement", "--deal", dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "st.json"))

	book = ownBook(t, dealID)
	require.Len(t, book, 3, "agreement, payer leg, statement")
	for _, r := range book {
		assert.True(t, r.Block.Seen.IsNull(), "seen is null on %s", r.Block.RecordType)
	}

	clockAt(t, time.Date(2026, 11, 4, 10, 20, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	row := settlementRow(t, dealID)
	assert.Equal(t, xdealv1.NotRunNoAcceptorKey, row["not_run"], "%v", row)

	report := dealRun(t, "report", "--deal", dealID)
	restsOn := report["report"].(map[string]any)["rests_on"].(map[string]any)
	assert.NotContains(t, restsOn, "counterparty", "no counterparty book to rest on")
	assert.Equal(t, false, restsOn["counterparty_layer"])
	bundle := filepath.Join(t.TempDir(), "b.json")
	dealRun(t, "report", "--deal", dealID, "--bundle", bundle)
	verified, err := verifyWithDirectory(t, bundle, "")
	require.NoError(t, err)
	assert.NotEqual(t, "INVALID", verified["verdict"], "%v", verified)

	list, err := invoke(t, "", "--profile", "deal", "deal", "list", "--json")
	require.NoError(t, err)
	assert.Contains(t, list, "keeps no records of its own")
}

// The no-layer deal starts at its first act: before it, the Standard
// History card counts nothing; after it, the deal is on the card (#762).
func TestANoLayerDealIsOnTheCardFromItsFirstAct(t *testing.T) {
	card := func() int {
		t.Helper()
		var n int
		withSession(t, func(s *dealSession) {
			got, err := s.standardHistoryV1(t.Context(), 0, "standard_history/3")
			require.NoError(t, err)
			n = len(got.Deals)
		})
		return n
	}
	dealID := noLayerAgreed(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	clockAt(t, time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	assert.Equal(t, 0, card(), "not started before the first act")

	check := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+v1LnHash+`"`)))
	id := check["check_id"].(string)
	dealRun(t, "settle", "--deal", dealID, "--check", id, "--sent", "--payment-ref-type", "ln.payment_hash")
	dealRun(t, "settle", "--deal", dealID, "--check", id, "--status", "settled", "--out", filepath.Join(t.TempDir(), "paid.json"))
	clockAt(t, time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	assert.Equal(t, 1, card(), "started at the first act")
}

// Only the offerer of an offer to no key agrees to it, and only an offer
// naming your key can be agreed to by you (#762).
func TestAgreeRefusesAnOfferItCannotAnswer(t *testing.T) {
	h := offeredTo(t, func(string) string { return offerTo(newV1Side(t).party.signer) })
	h.seller.act(t)
	assert.Contains(t, v1Err(t, "agree", h.offer, "--admit", sealAdmit(t, "GO")), "this offer was sent to another key")
}

// A no-layer deal holds no counterparty key or log, so a later book record
// still has seen null. (A referee's ruling received on it is guarded in
// noteReceived and sealBookV1; no test here builds a referee's case book.)
func TestANoLayerDealRegistersNoCounterparty(t *testing.T) {
	dealID := noLayerAgreed(t)
	withSession(t, func(s *dealSession) {
		d, err := s.v1DealOf(t.Context(), dealID)
		require.NoError(t, err)
		noLayer, err := s.v1NoLayer(t.Context(), d)
		require.NoError(t, err)
		assert.True(t, noLayer)
		assert.Empty(t, d.CounterpartyKey)
		assert.Empty(t, d.CounterpartyLogID)
	})
	dealRun(t, "statement", "--deal", dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "st.json"))
	for _, r := range ownBook(t, dealID) {
		assert.True(t, r.Block.Seen.IsNull())
	}
}
