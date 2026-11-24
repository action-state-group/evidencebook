package deal

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/capsulectl/internal/packfile"
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
	assert.Equal(t, "month", res["window"])
	assert.Equal(t, "window: month (calendar months, UTC), fixed for this store; each deal's book is placed in the window its agreement is sealed in", res["notice"])
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
	assert.Equal(t, "month", listed["window"])
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
	var res map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &res), out)
	assert.Equal(t, "none", res["window"])
	assert.Equal(t, "no window: one journal for this store's whole history, fixed for this store", res["notice"])
	assert.Equal(t, "capsulectl: no window: one journal for this store's whole history, fixed for this store\n", notice.String())
	_, err = invoke(t, "", "--profile", "deal", "deal", "window", "list")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this deal store has no window")

	_, err = invoke(t, "", "deal", "init", "--profile", "other", "--dir", filepath.Join(t.TempDir(), "other"), "--no-witness", "--window", "fortnight")
	require.Error(t, err)
}

// windowPackDocument is a compiled Terms Pack definition for the respond
// tests; the book reads no member of it.
const windowPackDocument = `{"pack":"deal-window-pack","version":1,"rows":["has-record"]}`

// newRequesterBook is a requester's own book, on a shelf of its own.
func newRequesterBook(t *testing.T) *evidencebook.Book {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	signer, err := evidencebook.NewEd25519Signer(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{31}, ed25519.SeedSize)))
	require.NoError(t, err)
	originals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "originals.jsonl"), "requester", signer.PublicKey())
	require.NoError(t, err)
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{Path: filepath.Join(dir, "requester.db"), LockPath: filepath.Join(dir, "requester.lock"),
		Operator: "requester", Signer: signer, CheckpointKey: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{32}, ed25519.SeedSize)), Originals: originals})
	require.NoError(t, err)
	t.Cleanup(func() { _ = shelf.Release() })
	book, err := shelf.Open(ctx, "requester")
	require.NoError(t, err)
	return book
}

// windowRespond asks the holder (the selected profile) through `deal window
// respond` for the pack, pinned to the checkpoint statement pin, and records
// the answer in the requester's book.
func windowRespond(t *testing.T, requester *evidencebook.Book, pack, packFile string, pin []byte, nonce int, keys evidencebook.ResponderKeys) string {
	t.Helper()
	ctx := t.Context()
	sum := sha256.Sum256(pin)
	sent, err := requester.Request(ctx, evidencebook.EvidenceRequest{
		Subject: evidencebook.Subject{Kind: evidencebook.SubjectFullHistory}, Derivation: pack,
		Coverage: evidencebook.Coverage{ExpectedPin: hex.EncodeToString(sum[:])}, Nonce: fmt.Sprintf("%032x", nonce),
	}, "holder")
	require.NoError(t, err)
	dir := t.TempDir()
	reqFile, replyFile := filepath.Join(dir, "request.json"), filepath.Join(dir, "reply.json")
	require.NoError(t, os.WriteFile(reqFile, sent.Message().Octets, 0o600))
	out := dealRun(t, "window", "respond", "--request", reqFile, "--requester", "requester-1", "--output", replyFile, "--pack", packFile)
	require.Equal(t, evidencebook.OutcomeArtifact, out["outcome"], "%v", out)
	reply, err := os.ReadFile(replyFile)
	require.NoError(t, err)
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, evidencebook.Message{Octets: reply, MediaType: evidencebook.MediaTypeJSON}, keys)
	require.NoError(t, err)
	var st evidencebook.ResponseStatement
	require.NoError(t, json.Unmarshal(recorded.Header.Statement, &st))
	require.Equal(t, evidencebook.OutcomeArtifact, st.Outcome, st.Failure)
	return recorded.RecordID
}

// evaluateAll is a one-claim Result citing every answer.
func evaluateAll(responses []string) func([]evidencebook.WindowInputs) (json.RawMessage, error) {
	return func([]evidencebook.WindowInputs) (json.RawMessage, error) {
		var evidence []any
		for _, id := range responses {
			evidence = append(evidence, map[string]any{"digest": id, "digest_alg": "SHA-256"})
		}
		claim := map[string]any{
			"id": "has-records", "contract_ref": "ec:deal-window-pack@1", "requirement_ref": "has-record", "tier": "recomputed", "grade": "self-attested",
			"sufficiency": "SATISFIED", "verdict": "met", "evidence": evidence, "proofs": []any{},
			"presentation": map[string]any{"kind": "disclosure", "status": "SATISFIED", "evidence": evidence},
		}
		return json.Marshal(map[string]any{
			"result_version": "evidence-result-v0", "generated_at": "2026-10-02T09:00:00Z", "claims": []any{claim},
			"aggregate": map[string]any{"buckets": map[string]any{"met": []any{"has-records"}, "not_met": []any{}, "not_evaluable": []any{}},
				"coverage": map[string]any{"evaluated_population": 1, "excluded_not_applicable": 0, "unknown_count": 0}},
		})
	}
}

// A requester's report over a windowed deal store's windows covers each
// window and each deal placed in it, through `deal window respond`.
func TestAReportOverADealStoresWindowsCoversItsDeals(t *testing.T) {
	seller, _ := newWindowedV1Side(t)
	buyer, _ := newWindowedV1Side(t)
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", offerFile)
	offer := res["offer"].(string)
	seller.act(t)
	dealRun(t, "receive", offerFile)
	dealRun(t, "agree", offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "agreement.json"))
	setWindowClock(t, time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	dealRun(t, "window", "roll")
	_ = buyer

	chainFile := filepath.Join(t.TempDir(), "chain.json")
	dealRun(t, "window", "chain", "--from", "2026-09", "--to", "2026-09", "--out", chainFile)
	var chain evidencebook.WindowChain
	require.NoError(t, json.Unmarshal(mustRead(t, chainFile), &chain))
	require.Len(t, chain.Windows, 1)
	require.Len(t, chain.Windows[0].Deals, 1, "the seller's deal is placed in September")
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	keys := evidencebook.ResponderKeys{Signer: p.TrustedKeys[0], Checkpoint: p.Checkpoint.TrustedKeys[0]}

	packFile := writeJSON(t, `{"document":`+windowPackDocument+`,"recipients":["requester-1"],"forms":["full_history"],"fields":["record_type","committed_at"]}`)
	pack, err := packfile.Digest([]byte(windowPackDocument))
	require.NoError(t, err)
	requester := newRequesterBook(t)
	window := windowRespond(t, requester, pack, packFile, chain.Windows[0].FinalCheckpoint, 1, keys)
	deal := windowRespond(t, requester, pack, packFile, chain.Windows[0].Deals[0].Latest, 2, keys)

	chainRecord, err := requester.RecordWindowChain(t.Context(), chain, evidencebook.VerifyChainOptions{CheckpointKey: keys.Checkpoint})
	require.NoError(t, err)
	// Without the deal's answer, no Result is sealed, and the gap is named.
	_, err = requester.ReportFromWindows(t.Context(), chainRecord.RecordID, []string{window}, evaluateAll([]string{window}), evidencebook.ReportOptions{Derivation: pack}, evidencebook.VerifyChainOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "of window 2026-09 has no answer")

	both := []string{window, deal}
	report, err := requester.ReportFromWindows(t.Context(), chainRecord.RecordID, both, evaluateAll(both), evidencebook.ReportOptions{Derivation: pack}, evidencebook.VerifyChainOptions{})
	require.NoError(t, err)
	coverage, err := evidencebook.VerifyReportCoverage(report.Bundle.JSON, evidencebook.CoverageOptions{CheckpointKey: keys.Checkpoint})
	require.NoError(t, err)
	assert.Empty(t, coverage.Findings)
	require.Len(t, coverage.Deals, 1)
	assert.Equal(t, chain.Windows[0].Deals[0].LogID, coverage.Deals[0].LogID)
	assert.Contains(t, coverage.Deals[0].Line, "answered over its whole book")

	// A pack file the strict reader refuses is refused before anything runs.
	bad := writeJSON(t, `{"document":`+windowPackDocument+`,"recipients":["a"],"recipients":["b"],"forms":["full_history"],"fields":[]}`)
	_, err = invoke(t, "", "--profile", "deal", "deal", "window", "respond", "--request", packFile, "--output", filepath.Join(t.TempDir(), "r.json"), "--pack", bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `member "recipients" appears twice`)
}
