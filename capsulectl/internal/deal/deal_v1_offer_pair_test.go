package deal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paymentOffer is the test offer whose row is recomputed, which makes it a
// payment row, addressed to a key.
func paymentOffer(key string) string {
	return strings.Replace(offerTo(key), `"owed_by": "buyer",`, `"owed_by": "buyer", "recomputed": true,`, 1)
}

// sealTermsLegs makes the offer verb's Capsule carry the offer's terms leg, as
// an offer with a payment row must (§6.3): the verb does not yet do it.
func sealTermsLegs(t *testing.T) {
	t.Helper()
	was := sealSentLib
	sealSentLib = func(ctx context.Context, shelf *evidencebook.Shelf, e evidencebook.Entry, actionID string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		rec, err := xdealv1.Decode(e.Payloads[0])
		require.NoError(t, err)
		leg, err := xdealv1.TermsLeg(rec)
		require.NoError(t, err)
		e.Members = map[string]json.RawMessage{xdealv1.SettlementMember: leg}
		return was(ctx, shelf, e, actionID)
	}
	t.Cleanup(func() { sealSentLib = was })
}

// The status provider runs a payment row's check only when it is given the
// offer's pair. The acceptor never sent the offer, so the pair it holds is the
// one it received, kept byte for byte: with none, its payment check is skipped.
func TestAReceivedOffersPairIsKeptAsReceived(t *testing.T) {
	sealTermsLegs(t)
	h := &handshakeV1{seller: newV1Side(t), buyer: newV1Side(t)}
	h.offerFile = filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, paymentOffer(h.seller.party.signer)), "--out", h.offerFile)
	h.offer = res["offer"].(string)
	raw, err := os.ReadFile(h.offerFile)
	require.NoError(t, err)
	first, err := xdealv1.VerifySent(raw, nil)
	require.NoError(t, err)
	prop, ok := proposeOf(first.Record)
	require.True(t, ok)
	require.NotEmpty(t, xdealv1.PaymentRows(prop.TermsRows), "the offer has a payment row")

	h.seller.act(t)
	require.Equal(t, "kept, and counted", dealRun(t, "receive", h.offerFile)["record"])
	withSession(t, func(s *dealSession) {
		pair, err := s.sentOfferPair(t.Context(), h.offer)
		require.NoError(t, err)
		assert.Equal(t, string(raw), string(pair), "the received file, never re-serialized")
	})
	// Receiving it again keeps the same bytes.
	dealRun(t, "receive", h.offerFile)
	withSession(t, func(s *dealSession) {
		pair, err := s.sentOfferPair(t.Context(), h.offer)
		require.NoError(t, err)
		assert.Equal(t, string(raw), string(pair))
	})

	// The offerer reads its own sent pair.
	h.buyer.act(t)
	withSession(t, func(s *dealSession) {
		pair, err := s.sentOfferPair(t.Context(), h.offer)
		require.NoError(t, err)
		assert.NotNil(t, pair)
	})
}

// An offer received before the pair was kept has a NULL pair, and so has an
// offer this profile holds neither sent nor received: the check is not run.
// Receiving the offer again keeps its pair; a pair already kept is not replaced.
func TestAnOfferWithNoPairRunsNoCheck(t *testing.T) {
	h := newHandshakeV1(t)
	raw, err := os.ReadFile(h.offerFile)
	require.NoError(t, err)
	h.seller.act(t)
	_, err = openWritableDB(t).Exec(`UPDATE deal_v1_offers SET pair=NULL`)
	require.NoError(t, err)
	pairOf := func() []byte {
		var pair []byte
		withSession(t, func(s *dealSession) {
			var err error
			pair, err = s.sentOfferPair(t.Context(), h.offer)
			require.NoError(t, err)
		})
		return pair
	}
	assert.Nil(t, pairOf(), "received before the pair was kept")
	withSession(t, func(s *dealSession) {
		pair, err := s.sentOfferPair(t.Context(), h.offer+"x")
		require.NoError(t, err)
		assert.Nil(t, pair, "not held")
	})

	dealRun(t, "receive", h.offerFile)
	assert.Equal(t, string(raw), string(pairOf()), "received again, the pair is kept")

	_, err = openWritableDB(t).Exec(`UPDATE deal_v1_offers SET pair=?`, []byte("kept"))
	require.NoError(t, err)
	dealRun(t, "receive", h.offerFile)
	assert.Equal(t, "kept", string(pairOf()), "a pair already kept is not replaced")
}

// settleLeg seals a settle record with its settlement member in the holder's
// deal book and returns the file the other side receives.
func settleLeg(t *testing.T, s *dealSession, dealID string, body *xdealv1.SettleBody, member map[string]any) string {
	t.Helper()
	leg, err := json.Marshal(member)
	require.NoError(t, err)
	sealed, err := s.sealBookV1(t.Context(), dealID, bookRecordIn{Body: body, Members: map[string]json.RawMessage{xdealv1.SettlementMember: leg}})
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "leg.json")
	out, err := json.Marshal(v1Push{Record: sealed.Pair})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, out, 0o600))
	return file
}

// The payer's leg says six thousand cents settled; the payee's says fewer
// arrived. The offerer's check sees the disagreement; the acceptor's must too,
// which needs the offer's pair it received: without it the row read agreed and
// met, and the /3 card showed a held deal whose settlement legs disagree.
func TestTheAcceptorsPaymentCheckSeesDisagreeingLegs(t *testing.T) {
	sealTermsLegs(t)
	h := agreedBoth(t, paymentOffer)
	raw, err := os.ReadFile(h.offerFile)
	require.NoError(t, err)
	sent, err := xdealv1.VerifySent(raw, nil)
	require.NoError(t, err)
	usd := func(v string) map[string]any { return map[string]any{"value": v, "assetCode": "USD", "assetScale": 2} }
	ref := map[string]any{"type": "acp.order_id", "value": "order-0001"}

	clockAt(t, time.Date(2026, 11, 1, 10, 20, 0, 0, time.UTC))
	h.buyer.act(t)
	var payer string
	withSession(t, func(s *dealSession) {
		payer = settleLeg(t, s, h.dealID, &xdealv1.SettleBody{Leg: xdealv1.LegPayer,
			Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: xdealv1.ValueMet}}, Check: refOf(strings.Repeat("1", 64))},
			map[string]any{"version": "0", "leg": "payer_observed", "sealer_role": "payer", "terms_ref": sent.CapsuleID,
				"amount": usd("6000"), "payment_ref": ref, "status": "settled", "observed_at": "2026-11-01T10:15:00Z"})
	})
	h.seller.act(t)
	require.Equal(t, "kept, and counted", dealRun(t, "receive", payer)["record"])
	withSession(t, func(s *dealSession) {
		_ = settleLeg(t, s, h.dealID, &xdealv1.SettleBody{Leg: xdealv1.LegPayee,
			Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: xdealv1.ValueMet}}},
			map[string]any{"version": "0", "leg": "payee_observed", "sealer_role": "payee", "terms_ref": sent.CapsuleID,
				"received": usd("5000"), "receive_fee": usd("0"), "payment_ref": ref, "status": "settled", "observed_at": "2026-11-01T10:18:00Z"})
	})

	card, deals := card3(t, h, h.seller, time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC))
	require.Len(t, deals, 1)
	assert.Equal(t, "in_disagreement", deals[0].Rows[0].Status, "the legs disagree")
	assert.NotEqual(t, "met", deals[0].Rows[0].Value)
	assert.EqualValues(t, 0, card["outcomes"].(map[string]any)["held"], "a deal whose legs disagree is not held: %v", card["outcomes"])
}
