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
	return noLayerAgreedWith(t, func(o string) string { return o })
}

// noLayerAgreedWith is noLayerAgreed with the offer changed by edit.
func noLayerAgreedWith(t *testing.T, edit func(string) string) (dealID string) {
	t.Helper()
	newV1Side(t)
	// The payment row is recomputed, so the payment check applies to it.
	offer := edit(strings.Replace(v1OfferInput, `"owed_by": "buyer",`, `"owed_by": "buyer", "recomputed": true,`, 1))
	require.NotEqual(t, v1OfferInput, offer)
	offered := dealRun(t, "offer", "--input", writeJSON(t, offer), "--out", filepath.Join(t.TempDir(), "o.json"))
	assert.Contains(t, offered["note"], "there is nothing to send")
	dealID = offered["deal_id"].(string)
	intent := dealRun(t, "note", "--deal", dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	agreed := dealRun(t, "agree", offered["offer"].(string), "--admit", sealAdmit(t, "GO"), "--intent", intent["intent"].(string), "--out", filepath.Join(t.TempDir(), "a.json"))
	assert.Equal(t, "offerer", agreed["side"])
	assert.Contains(t, agreed["note"], "the other side keeps no signed records of its own")
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
	assert.Contains(t, list, "counterparty: keeps no signed records")
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

// A record received for a no-layer deal (a referee's ruling, say) registers
// no one as the other side, and even a counterparty key and log already on
// the row (an earlier version's registration) leave seen null on the next
// record (#762).
func TestANoLayerDealRegistersNoCounterparty(t *testing.T) {
	dealID := noLayerAgreed(t)
	withSession(t, func(s *dealSession) {
		d, err := s.v1DealOf(t.Context(), dealID)
		require.NoError(t, err)
		noLayer, err := s.v1NoLayer(t.Context(), d)
		require.NoError(t, err)
		assert.True(t, noLayer)
		book, err := s.ownBookV1(t.Context(), d)
		require.NoError(t, err)
		// A book record from another key arriving for the deal.
		info := &receivedInfo{kind: xdealv1.TypeStatement, dealID: dealID, book: &xdealv1.BookRead{KeyID: strings.Repeat("cd", 32), LogID: "deal/" + strings.Repeat("ef", 16), Seq: 1, Record: book[0]}}
		require.NoError(t, s.noteReceived(t.Context(), info))
		d, err = s.v1DealOf(t.Context(), dealID)
		require.NoError(t, err)
		assert.Empty(t, d.CounterpartyKey, "no one is registered as the other side")
		assert.Empty(t, d.CounterpartyLogID)
		// As if an earlier version had registered one.
		_, err = s.db.ExecContext(t.Context(), `UPDATE deal_v1 SET counterparty_key=? WHERE deal_id=?`, strings.Repeat("cd", 32), dealID)
		require.NoError(t, err)
		_, err = s.db.ExecContext(t.Context(), `UPDATE deal_versions SET counterparty_log_id=? WHERE deal_id=?`, "deal/"+strings.Repeat("ef", 16), dealID)
		require.NoError(t, err)
	})
	dealRun(t, "statement", "--deal", dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "st.json"))
	for _, r := range ownBook(t, dealID) {
		assert.True(t, r.Block.Seen.IsNull(), "seen null on %s", r.Block.RecordType)
	}
}

// The no-layer agreement binds the offer, its rows, the role and the
// witnesses as any agreement does (CheckAgreement).
func TestANoLayerAgreementBindsItsOffer(t *testing.T) {
	dealID := noLayerAgreed(t)
	withSession(t, func(s *dealSession) {
		d, err := s.v1DealOf(t.Context(), dealID)
		require.NoError(t, err)
		offer, err := s.offerByDigest(t.Context(), d.OfferDigest)
		require.NoError(t, err)
		book, err := s.ownBookV1(t.Context(), d)
		require.NoError(t, err)
		require.NoError(t, xdealv1.CheckAgreement(book[0], offer.Record, ""))
		bad := book[0]
		ag := *bad.Body.(*xdealv1.AgreeBody)
		ag.Role = oppositeRole(ag.Role)
		bad.Body = &ag
		assert.Error(t, xdealv1.CheckAgreement(bad, offer.Record, ""), "the role is bound")
	})
}

// A delivery on a no-layer deal: the first perform goes ahead marked
// unguarded, a later one is not gated at all (#762).
func TestAPerformOnANoLayerDeal(t *testing.T) {
	dealID := noLayerAgreed(t)
	commit := `{"action":"commit","terms":{"item":"lamp","price_minor":4500,"currency":"USD"}}`
	perform := func() map[string]any {
		t.Helper()
		check := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, commit))
		require.Equal(t, "pass", check["verdict"], "%v", check)
		return dealRun(t, "perform", "--deal", dealID, "--act", "deliver", "--check", check["check_id"].(string), "--observed", "self", "--out", filepath.Join(t.TempDir(), "p.json"))
	}
	first := perform()
	gate, _ := json.Marshal(first["first_act_gate"])
	assert.Contains(t, string(gate), xdealv1.GateNoLayer, "%v", first)
	second := perform()
	assert.Nil(t, second["first_act_gate"], "a later act is not gated: %v", second)
	for _, r := range ownBook(t, dealID) {
		assert.True(t, r.Block.Seen.IsNull())
	}
}

// A no-layer deal whose offer names a witness that timed none of its
// records: /3 leaves it out (no_witness_time) and /4 counts it, from its
// first act, as for any deal (SH §12).
func TestANoLayerDealNamingAWitnessOnTheCards(t *testing.T) {
	dealID := noLayerAgreedWith(t, func(o string) string {
		return strings.Replace(o, `"witnesses": []`, `"witnesses": [{"key_id": "`+strings.Repeat("ab", 32)+`"}]`, 1)
	})
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	check := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+v1LnHash+`"`)))
	id := check["check_id"].(string)
	dealRun(t, "settle", "--deal", dealID, "--check", id, "--sent", "--payment-ref-type", "ln.payment_hash")
	dealRun(t, "settle", "--deal", dealID, "--check", id, "--status", "settled", "--out", filepath.Join(t.TempDir(), "paid.json"))
	clockAt(t, time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	withSession(t, func(s *dealSession) {
		v3, err := s.standardHistoryV1(t.Context(), 0, "standard_history/3")
		require.NoError(t, err)
		require.Len(t, v3.Deals, 1)
		require.NotNil(t, v3.Deals[0].LeftOut)
		assert.Equal(t, "no_witness_time", *v3.Deals[0].LeftOut)
		v4, err := s.standardHistoryV1(t.Context(), 0, "standard_history/4")
		require.NoError(t, err)
		require.Len(t, v4.Deals, 1)
		assert.Nil(t, v4.Deals[0].LeftOut, "/4 counts it")
	})
}
