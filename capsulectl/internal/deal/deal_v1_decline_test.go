package deal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// offeredTo is a buyer's offer sent to the seller and received by it; the
// seller acts last.
func offeredTo(t *testing.T, offer func(key string) string) *handshakeV1 {
	t.Helper()
	h := &handshakeV1{}
	h.seller = newV1Side(t)
	h.buyer = newV1Side(t)
	h.offerFile = filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offer(h.seller.party.signer)), "--out", h.offerFile)
	h.dealID, h.offer = res["deal_id"].(string), res["offer"].(string)
	h.seller.act(t)
	dealRun(t, "receive", h.offerFile)
	return h
}

func v1DealState(t *testing.T, dealID string) string {
	t.Helper()
	var state string
	require.NoError(t, v1DB(t).QueryRow(`SELECT state FROM deal_versions WHERE deal_id=?`, dealID).Scan(&state))
	return state
}

// The acceptor declines an offer sent to it: the file verifies as the
// offer's decline, both sides read the deal as declined, and an offer is
// declined once, whatever the reason given again.
func TestAnOfferSentToYouIsDeclined(t *testing.T) {
	h := offeredTo(t, offerTo)
	file := filepath.Join(t.TempDir(), "decline.json")
	out := dealRun(t, "offer", "decline", h.offer, "--reason", "not_selected", "--out", file)
	assert.Equal(t, h.offer, out["declined"])
	sent, err := xdealv1.VerifySent(readPair(t, file), nil)
	require.NoError(t, err)
	ag, ok := agreeOf(sent.Record)
	require.True(t, ok)
	assert.Equal(t, xdealv1.KindDecline, ag.Kind)
	assert.Equal(t, "not_selected", ag.Reason)
	assert.Equal(t, "declined", v1DealState(t, h.dealID))

	for _, reason := range []string{"not_selected", "other"} {
		assert.Contains(t, v1Err(t, "offer", "decline", h.offer, "--reason", reason, "--out", filepath.Join(t.TempDir(), "again.json")), "already declined")
	}
	assert.Contains(t, v1Err(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "a.json")), "declined")

	h.buyer.act(t)
	dealRun(t, "receive", file)
	assert.Equal(t, "declined", v1DealState(t, h.dealID), "the offerer reads its offer declined")
}

// Only an offer sent to your key can be declined: not your own offer, not
// an open one (no to), not one sent to another key, and not once you agreed.
func TestADeclineNeedsAnOfferSentToYourKey(t *testing.T) {
	h := offeredTo(t, offerTo)
	h.buyer.act(t)
	assert.Contains(t, v1Err(t, "offer", "decline", h.offer, "--out", filepath.Join(t.TempDir(), "d.json")), "withdraw it with `deal offer cancel`")

	open := offeredTo(t, func(string) string { return v1OfferInput }) // to: null
	assert.Contains(t, v1Err(t, "offer", "decline", open.offer, "--out", filepath.Join(t.TempDir(), "d.json")), "not sent to your key")

	other := newV1Side(t)
	elsewhere := offeredTo(t, func(string) string { return offerTo(other.party.signer) })
	assert.Contains(t, v1Err(t, "offer", "decline", elsewhere.offer, "--out", filepath.Join(t.TempDir(), "d.json")), "not sent to your key")

	agreed := offeredTo(t, offerTo)
	dealRun(t, "agree", agreed.offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "a.json"))
	assert.Contains(t, v1Err(t, "offer", "decline", agreed.offer, "--out", filepath.Join(t.TempDir(), "d.json")), "you agreed to this offer")
	assert.Contains(t, v1Err(t, "offer", "decline", agreed.offer, "--reason", "maybe"), "not_selected or other")
}

// A decline stopped after its pair is stored, or with only its journal
// record, blocks an agreement; its re-run finishes it (the stored pair
// written again unchanged, a journal-only one repeated as a new operation).
func TestAStoppedDeclineBlocksAnAgreementAndFinishes(t *testing.T) {
	h := offeredTo(t, offerTo)
	first := filepath.Join(t.TempDir(), "d1.json")
	dealRun(t, "offer", "decline", h.offer, "--out", first)
	// Stopped after the pair was stored, before the state and the file.
	_, err := openWritableDB(t).Exec(`UPDATE deal_sent SET state=?, written=0 WHERE kind=?`, sentSealed, xdealv1.KindDecline)
	require.NoError(t, err)
	_, err = openWritableDB(t).Exec(`UPDATE deal_versions SET state='offer_received' WHERE deal_id=?`, h.dealID)
	require.NoError(t, err)
	assert.Contains(t, v1Err(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "a.json")), "you began to decline this offer")
	again := filepath.Join(t.TempDir(), "d2.json")
	dealRun(t, "offer", "decline", h.offer, "--reason", "other", "--out", again)
	firstBytes, err := os.ReadFile(first)
	require.NoError(t, err)
	againBytes, err := os.ReadFile(again)
	require.NoError(t, err)
	assert.Equal(t, firstBytes, againBytes, "the stored decline is written again, whatever --reason says now")
	assert.Equal(t, "declined", v1DealState(t, h.dealID))

	// Stopped inside the library call: the journal holds it, no pair.
	j := offeredTo(t, offerTo)
	dealRun(t, "offer", "decline", j.offer, "--out", filepath.Join(t.TempDir(), "j1.json"))
	_, err = openWritableDB(t).Exec(`UPDATE deal_sent SET state=?, pair=NULL, written=0 WHERE kind=?`, sentSealing, xdealv1.KindDecline)
	require.NoError(t, err)
	_, err = openWritableDB(t).Exec(`UPDATE deal_versions SET state='offer_received' WHERE deal_id=?`, j.dealID)
	require.NoError(t, err)
	assert.Contains(t, v1Err(t, "agree", j.offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "a.json")), "you began to decline this offer")
	repeated := filepath.Join(t.TempDir(), "j2.json")
	dealRun(t, "offer", "decline", j.offer, "--out", repeated)
	_, err = xdealv1.VerifySent(readPair(t, repeated), nil)
	require.NoError(t, err)
	assert.Equal(t, "declined", v1DealState(t, j.dealID))
}

// An agreement sealed first is never overwritten by a decline re-run.
func TestADeclineNeverOvertakesAnAgreement(t *testing.T) {
	h := offeredTo(t, offerTo)
	agreement := filepath.Join(t.TempDir(), "a.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", agreement)
	before := v1DealState(t, h.dealID)
	assert.Contains(t, v1Err(t, "offer", "decline", h.offer, "--out", filepath.Join(t.TempDir(), "d.json")), "you agreed to this offer")
	assert.Equal(t, before, v1DealState(t, h.dealID))
	var n int
	require.NoError(t, v1DB(t).QueryRow(`SELECT COUNT(*) FROM deal_sent WHERE kind=?`, xdealv1.KindDecline).Scan(&n))
	assert.Zero(t, n, "nothing sealed")
}

// A sale to several buyers on x-deal-v1: each buyer sends an offer to the
// seller; the seller agrees to one (the whole exchange) and declines the
// others as not_selected; each losing buyer reads its offer declined.
func TestASaleToSeveralBuyersDeclinesTheOthers(t *testing.T) {
	seller := newV1Side(t)
	type buyer struct {
		side        v1Side
		offer, file string
		dealID      string
	}
	var buyers []*buyer
	for i := 0; i < 3; i++ {
		b := &buyer{side: newV1Side(t), file: filepath.Join(t.TempDir(), "offer.json")}
		item := []string{"lamp", "chair", "desk"}[i]
		res := dealRun(t, "offer", "--input", writeJSON(t, strings.Replace(offerTo(seller.party.signer), `"item": "lamp"`, `"item": "`+item+`"`, 1)), "--out", b.file)
		b.offer, b.dealID = res["offer"].(string), res["deal_id"].(string)
		buyers = append(buyers, b)
	}
	seller.act(t)
	for _, b := range buyers {
		dealRun(t, "receive", b.file)
	}
	winner := buyers[0]
	sellerAgr := filepath.Join(t.TempDir(), "seller-agreement.json")
	dealRun(t, "agree", winner.offer, "--admit", sealAdmit(t, "GO"), "--out", sellerAgr)
	declines := map[*buyer]string{}
	for _, b := range buyers[1:] {
		f := filepath.Join(t.TempDir(), "decline.json")
		dealRun(t, "offer", "decline", b.offer, "--reason", "not_selected", "--out", f)
		declines[b] = f
	}

	winner.side.act(t)
	dealRun(t, "receive", sellerAgr)
	buyerAgr := filepath.Join(t.TempDir(), "buyer-agreement.json")
	dealRun(t, "agree", winner.offer, "--admit", sealAdmit(t, "GO"), "--out", buyerAgr)
	seller.act(t)
	dealRun(t, "receive", buyerAgr)
	assert.Equal(t, "agreed", v1DealState(t, winner.dealID))

	for b, f := range declines {
		_, err := xdealv1.VerifySent(readPair(t, f), nil)
		require.NoError(t, err)
		b.side.act(t)
		dealRun(t, "receive", f)
		assert.Equal(t, "declined", v1DealState(t, b.dealID))
	}
}
