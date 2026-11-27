package deal

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openingSeller is the seller member of the deal's opening intent record and
// the opening intent's local event, read in the current profile.
func openingSeller(t *testing.T, dealID string) (*xdealv1.Seller, sealedEvent) {
	t.Helper()
	var seller *xdealv1.Seller
	var ev sealedEvent
	withSession(t, func(s *dealSession) {
		events, err := s.v1Events(t.Context(), dealID)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(events), 2)
		ev = events[1]
		require.Equal(t, "intent", ev.Event.Kind)
		rec, held, err := s.journalRecord(t.Context(), ev.Digest)
		require.NoError(t, err)
		require.True(t, held)
		b, ok := rec.Body.(*xdealv1.IntentBody)
		require.True(t, ok, "%T", rec.Body)
		seller = b.Seller
	})
	return seller, ev
}

// assertCommits checks that a commitment is the step's nonce over text.
func assertCommits(t *testing.T, ev sealedEvent, nonceName, text, commitment string) {
	t.Helper()
	nonce := ev.Event.Nonces[nonceName]
	require.NotEmpty(t, nonce, nonceName)
	want, err := dealprofile.CommitText(nonce, text)
	require.NoError(t, err)
	assert.Equal(t, want, commitment, nonceName)
}

// A buyer's opening names the seller by commitment only, under the key its
// agreement names, and the agreement cites that intent by default
// (evidencebook#1031). Here the buyer made the offer, so the key is the
// offer's to.
func TestABuyersOpeningNamesTheSellerAndItsAgreementCitesIt(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	require.Equal(t, "kept, and counted", dealRun(t, "receive", h.sellerAgree)["record"])
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))

	seller, ev := openingSeller(t, h.dealID)
	require.NotNil(t, seller, "a buyer's opening with who.name names the seller")
	require.NotNil(t, seller.Key.V)
	assert.Equal(t, h.seller.party.signer, *seller.Key.V, "the offer's to, the key the agreement names")
	assertCommits(t, ev, "seller_name", "Kettle Shop", seller.NameCommitment)
	assertCommits(t, ev, "seller_domain", "kettle.example", seller.DomainCommitment)
	assert.Equal(t, "Kettle Shop", ev.Event.Seller.Name, "the raw name stays in the local event")
	withSession(t, func(s *dealSession) {
		rec, _, err := s.journalRecord(t.Context(), ev.Digest)
		require.NoError(t, err)
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		assert.NotContains(t, strings.ToLower(string(raw)), "kettle", "the record carries no raw name or domain")
	})

	out := filepath.Join(t.TempDir(), "buyer-agreement.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", out)
	own, err := xdealv1.VerifyBook(readPush(t, out).Record)
	require.NoError(t, err)
	ag, _ := agreeOf(own.Record)
	require.NotNil(t, ag.Intent, "with no --intent the agreement cites the opening intent")
	assert.Equal(t, ev.Digest, ag.Intent.Digest)
	assert.Equal(t, *seller.Key.V, *ag.CounterpartyKey.V)
}

// When the seller made the offer, the buyer accepts it, and the key is the
// offer's signer.
func TestAnAcceptingBuyersOpeningNamesTheOfferSigner(t *testing.T) {
	seller := newV1Side(t)
	buyer := newV1Side(t)
	seller.act(t)
	in := strings.Replace(offerTo(buyer.party.signer), `"role": "buyer"`, `"role": "seller"`, 1)
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, in), "--out", offerFile)
	dealID, offer := res["deal_id"].(string), res["offer"].(string)
	buyer.act(t)
	require.Equal(t, "kept, and counted", dealRun(t, "receive", offerFile)["record"])
	dealRun(t, "note", "--deal", dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent),
		"--opening", writeJSON(t, `{"type": "purchase", "who": {"name": "Alder"}, "recourse": {"rail": "card", "refundable": true}}`))

	got, ev := openingSeller(t, dealID)
	require.NotNil(t, got)
	require.NotNil(t, got.Key.V)
	assert.Equal(t, seller.party.signer, *got.Key.V, "the offer's signer")
	assertCommits(t, ev, "seller_name", "Alder", got.NameCommitment)
	assert.Empty(t, got.DomainCommitment, "no domain was given")

	out := filepath.Join(t.TempDir(), "agreement.json")
	dealRun(t, "agree", offer, "--admit", sealAdmit(t, "GO"), "--out", out)
	own, err := xdealv1.VerifyBook(readPush(t, out).Record)
	require.NoError(t, err)
	ag, _ := agreeOf(own.Record)
	assert.Equal(t, xdealv1.SideAcceptor, ag.Side)
	require.NotNil(t, ag.Intent)
	assert.Equal(t, ev.Digest, ag.Intent.Digest)
}

// A seller's opening names nobody: who is the buyer there, and only a
// buyer's book names the seller.
func TestASellersOpeningNamesNoSeller(t *testing.T) {
	h := openedV1(t)
	got, ev := openingSeller(t, h.dealID)
	assert.Nil(t, got)
	assert.Nil(t, ev.Event.Seller)
	assert.NotContains(t, ev.Event.Nonces, "seller_name")
}

// An opening whose who gives no name names no seller.
func TestAnOpeningWithNoNameNamesNoSeller(t *testing.T) {
	h := receivedOffer(t)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent),
		"--opening", writeJSON(t, `{"type": "purchase", "who": {"domain": "kettle.example"}, "recourse": {"rail": "card", "refundable": true}}`))
	got, _ := openingSeller(t, h.dealID)
	assert.Nil(t, got)
}

// With no layer, the seller's key is null, as the agreement's
// counterparty_key is.
func TestANoLayerBuyerNamesTheSellerUnderANullKey(t *testing.T) {
	dealID := noLayerAgreed(t)
	got, ev := openingSeller(t, dealID)
	require.NotNil(t, got)
	assert.True(t, got.Key.IsNull())
	book := ownBook(t, dealID)
	ag, _ := agreeOf(book[0])
	require.NotNil(t, ag.Intent)
	assert.Equal(t, ev.Digest, ag.Intent.Digest)
	assert.True(t, ag.CounterpartyKey.IsNull())
}

// An agreement is sealed once: an opening sealed after it is not cited,
// and agreeing again returns the same agreement.
func TestAnOpeningAfterTheAgreementIsNotCited(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	require.Equal(t, "kept, and counted", dealRun(t, "receive", h.sellerAgree)["record"])
	admit := sealAdmit(t, "GO")
	first := filepath.Join(t.TempDir(), "first.json")
	dealRun(t, "agree", h.offer, "--admit", admit, "--out", first)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	seller, _ := openingSeller(t, h.dealID)
	require.NotNil(t, seller, "the opening still names the seller")

	again := filepath.Join(t.TempDir(), "again.json")
	dealRun(t, "agree", h.offer, "--admit", admit, "--out", again)
	a, err := xdealv1.VerifyBook(readPush(t, first).Record)
	require.NoError(t, err)
	b, err := xdealv1.VerifyBook(readPush(t, again).Record)
	require.NoError(t, err)
	assert.Equal(t, a.RecordID, b.RecordID)
	ag, _ := agreeOf(b.Record)
	assert.Nil(t, ag.Intent)
}
