package deal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deal store whose journal is split by window (evidencebook#797).

// newWindowedV1Side is newV1Side on a store created with --window month,
// and the notice deal init printed.
func newWindowedV1Side(t *testing.T) (v1Side, string) {
	t.Helper()
	dealTestEnv(t, t.TempDir())
	var notice bytes.Buffer
	old := dealNoticeOut
	dealNoticeOut = &notice
	t.Cleanup(func() { dealNoticeOut = old })
	out, err := invoke(t, "", "deal", "init", "--profile", "deal", "--dir", filepath.Join(t.TempDir(), "deal"), "--no-witness", "--materiality", neutralMateriality, "--window", "month")
	require.NoError(t, err, out)
	var res map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &res), out)
	assert.Equal(t, "window: month (calendar months, UTC), fixed for this store; each deal's book is placed in the window its agreement is sealed in", res["window"])
	return v1Side{party: thisDealParty(t)}, notice.String()
}

func setWindowClock(t *testing.T, at time.Time) {
	t.Helper()
	old := cli.DealClock
	cli.DealClock = func() time.Time { return at }
	t.Cleanup(func() { cli.DealClock = old })
}

func TestADealOnAWindowedStoreIsPlacedInItsAgreementsWindow(t *testing.T) {
	seller, notice := newWindowedV1Side(t)
	assert.Contains(t, notice, "capsulectl: window: month (calendar months, UTC), fixed for this store")
	buyer, _ := newWindowedV1Side(t)

	// The buyer offers in September; the seller agrees in September.
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", offerFile)
	dealID, offer := res["deal_id"].(string), res["offer"].(string)
	seller.act(t)
	got := dealRun(t, "receive", offerFile)
	require.Equal(t, "kept, and counted", got["record"], "%v", got)
	sellerAgree := filepath.Join(t.TempDir(), "agreement.json")
	agreed := dealRun(t, "agree", offer, "--admit", sealAdmit(t, "GO"), "--out", sellerAgree)
	require.Equal(t, true, agreed["stamp_proof"], "%v", agreed)

	// The buyer agrees in October: its offer is in September's journal, its
	// deal book is placed in October.
	setWindowClock(t, time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	buyer.act(t)
	got = dealRun(t, "receive", sellerAgree)
	require.Equal(t, "kept, and counted", got["record"], "%v", got)
	buyerAgree := filepath.Join(t.TempDir(), "buyer-agreement.json")
	out := dealRun(t, "agree", offer, "--admit", sealAdmit(t, "GO"), "--out", buyerAgree)
	require.Equal(t, true, out["stamp_proof"], "%v", out)

	listed := dealRun(t, "window", "list")
	windows := listed["windows"].([]any)
	require.Len(t, windows, 2, "%v", listed)
	sep, oct := windows[0].(map[string]any), windows[1].(map[string]any)
	assert.Equal(t, "2026-09", sep["label"])
	assert.Equal(t, true, sep["closed"], "the agreement's Place rolled September closed")
	assert.Empty(t, sep["placed"])
	require.Len(t, oct["placed"], 1, "the buyer's deal book is placed in October, its agreement's window")

	// The per-deal report reads the offer from the earlier window.
	report := dealRun(t, "report", "--deal", dealID)
	assert.NotEmpty(t, report, "%v", report)

	// September's chain verifies under the profile's checkpoint key.
	chainFile := filepath.Join(t.TempDir(), "chain.json")
	chainOut := dealRun(t, "window", "chain", "--from", "2026-09", "--to", "2026-09", "--out", chainFile)
	assert.Equal(t, float64(1), chainOut["windows"])
	raw, err := os.ReadFile(chainFile)
	require.NoError(t, err)
	var chain evidencebook.WindowChain
	require.NoError(t, json.Unmarshal(raw, &chain))
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	_, findings := evidencebook.VerifyWindowChain(chain, evidencebook.VerifyChainOptions{CheckpointKey: p.Checkpoint.TrustedKeys[0]})
	assert.Empty(t, findings)

	// The seller rolls in October: its deal is in September's close.
	seller.act(t)
	rolled := dealRun(t, "window", "roll")
	assert.Equal(t, "2026-10", rolled["current"].(map[string]any)["label"])
	listed = dealRun(t, "window", "list")
	sepSeller := listed["windows"].([]any)[0].(map[string]any)
	require.Len(t, sepSeller["placed"], 1, "the seller agreed in September: %v", listed)

	// An x-deal-v0 deal has no place on a windowed store.
	withSession(t, func(s *dealSession) {
		err := s.useDeal(t.Context(), "deal-0123456789abcdef", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "holds x-deal-v1 deals only")
	})
}

func TestAStoreWithoutAWindowSaysSoAndRefusesTheWindowVerbs(t *testing.T) {
	dealTestEnv(t, t.TempDir())
	var notice bytes.Buffer
	old := dealNoticeOut
	dealNoticeOut = &notice
	t.Cleanup(func() { dealNoticeOut = old })
	out, err := invoke(t, "", "deal", "init", "--profile", "deal", "--dir", filepath.Join(t.TempDir(), "deal"), "--no-witness", "--materiality", neutralMateriality)
	require.NoError(t, err, out)
	assert.Equal(t, "capsulectl: no window: one journal for this store's whole history, fixed for this store\n", notice.String())
	_, err = invoke(t, "", "--profile", "deal", "deal", "window", "list")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this deal store has no window")

	_, err = invoke(t, "", "deal", "init", "--profile", "other", "--dir", filepath.Join(t.TempDir(), "other"), "--no-witness", "--window", "fortnight")
	require.Error(t, err)
}
