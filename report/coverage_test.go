// SPDX-License-Identifier: Apache-2.0

package report

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	htmlstd "html"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A report over a holder's time windows (evidencebook#689): Build checks its
// coverage from the bundle under the holder's key, states a covered range in
// a banner, and renders no page over a gap.

type windowClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *windowClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func (c *windowClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func seeded(seed byte) ed25519.PrivateKey {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed
	}
	return ed25519.NewKeyFromSeed(s)
}

const windowPack = `{"pack":"window-pack","version":1}`

// windowReport is a report over a holder's three month windows, October to
// December 2026, with one answer per window, or with the November answer
// left out when gap is set. It returns the bundle and the holder's key.
func windowReport(t *testing.T, gap bool) ([]byte, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	clock := &windowClock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	holderSigner, err := evidencebook.NewEd25519Signer(seeded(1))
	require.NoError(t, err)
	holderOriginals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "holder-originals.jsonl"), "holder", holderSigner.PublicKey())
	require.NoError(t, err)
	checkpointKey := seeded(2)
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{
		Path: filepath.Join(dir, "holder.db"), Operator: "holder", Signer: holderSigner, CheckpointKey: checkpointKey,
		Originals: holderOriginals, Now: clock.Now,
		Window: &evidencebook.WindowPolicy{Period: evidencebook.WindowMonth, Base: "holder", Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = shelf.Release() })
	pack, err := shelf.RegisterDerivation(evidencebook.DerivationDefinition{
		Document: []byte(windowPack), Recipients: []string{"requester"}, Proofs: true,
		Forms: []evidencebook.SubjectKind{evidencebook.SubjectFullHistory}, Fields: []string{"record_type"},
	})
	require.NoError(t, err)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	_, err = oct.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, Statement: json.RawMessage(`{"o":1}`)})
	require.NoError(t, err)
	clock.set(time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)
	chain, err := shelf.WindowChain(ctx, "2026-10", "2026-12", evidencebook.WindowChainOptions{})
	require.NoError(t, err)

	requesterKey := seeded(3)
	requesterSigner, err := evidencebook.NewEd25519Signer(requesterKey)
	require.NoError(t, err)
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "requester-records"))
	require.NoError(t, err)
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "requester-log.jsonl"), "requester", seeded(4))
	require.NoError(t, err)
	requesterOriginals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "requester-originals.jsonl"), "requester", requesterSigner.PublicKey())
	require.NoError(t, err)
	requester, err := evidencebook.Open(ctx, evidencebook.Config{BookID: "requester", Operator: "requester", Store: store, Substrate: substrate, Originals: requesterOriginals, Signer: requesterSigner, Now: clock.Now})
	require.NoError(t, err)
	t.Cleanup(func() { _ = requester.Release() })

	holderKey := hex.EncodeToString(checkpointKey.Public().(ed25519.PublicKey))
	keys := evidencebook.ResponderKeys{Signer: evidencebook.KeyID(holderSigner), Checkpoint: holderKey}
	chainRecord, err := requester.RecordWindowChain(ctx, chain, evidencebook.VerifyChainOptions{CheckpointKey: holderKey})
	require.NoError(t, err)
	var responses []string
	for i, w := range chain.Windows {
		if gap && w.Label == "2026-11" {
			continue
		}
		sent, err := requester.Request(ctx, evidencebook.EvidenceRequest{
			Subject: evidencebook.Subject{Kind: evidencebook.SubjectFullHistory}, Derivation: pack,
			Coverage: evidencebook.Coverage{ExpectedPin: digestHex(w.FinalCheckpoint)}, Nonce: fmt.Sprintf("%032x", i+1),
		}, "holder")
		require.NoError(t, err)
		reply, _, err := shelf.Respond(ctx, sent.Message(), evidencebook.RespondOptions{AuthenticatedRequester: "requester", Policy: evidencebook.DefaultSharePolicy()})
		require.NoError(t, err)
		recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, keys)
		require.NoError(t, err)
		responses = append(responses, recorded.RecordID)
	}
	evidence := make([]any, len(responses))
	for i, r := range responses {
		evidence[i] = map[string]any{"digest": r, "digest_alg": "SHA-256"}
	}
	claim := map[string]any{
		"id": "c1", "contract_ref": "ec:window-pack@1", "requirement_ref": "r1", "tier": "recomputed", "grade": "self-attested",
		"sufficiency": "SATISFIED", "verdict": "met", "evidence": evidence, "proofs": []any{},
		"presentation": map[string]any{"kind": "disclosure", "status": "SATISFIED", "evidence": evidence},
	}
	result, err := json.Marshal(map[string]any{
		"result_version": "evidence-result-v0", "generated_at": "2027-01-03T00:00:00Z", "claims": []any{claim},
		"aggregate": map[string]any{"buckets": map[string]any{"met": []any{"c1"}, "not_met": []any{}, "not_evaluable": []any{}},
			"coverage": map[string]any{"evaluated_population": 1, "excluded_not_applicable": 0, "unknown_count": 0}},
	})
	require.NoError(t, err)
	if !gap {
		report, err := requester.ReportFromWindows(ctx, chainRecord.RecordID, responses,
			func([]evidencebook.WindowInputs) (json.RawMessage, error) { return result, nil },
			evidencebook.ReportOptions{Derivation: pack}, evidencebook.VerifyChainOptions{})
		require.NoError(t, err)
		return report.Bundle.JSON, holderKey
	}
	// A Result over a gap, sealed and bundled outside ReportFromWindows,
	// which would refuse it.
	links := []evidencebook.Link{{Type: evidencebook.Cites, Target: chainRecord.RecordID}}
	for _, r := range responses {
		links = append(links, evidencebook.Link{Type: evidencebook.Cites, Target: r})
	}
	sealed, err := requester.Append(ctx, evidencebook.Entry{RecordType: evidencebook.RecordTypeEvidenceResult, EpistemicType: evidencebook.DerivedMetric, SubjectRef: "ec:window-pack@1", Links: links, Statement: result})
	require.NoError(t, err)
	_, err = requester.Checkpoint(ctx)
	require.NoError(t, err)
	bundle, err := requester.Bundle(ctx, evidencebook.BundleRequest{Root: sealed.RecordID, Payloads: evidencebook.PayloadsAll})
	require.NoError(t, err)
	return bundle.JSON, holderKey
}

func digestHex(b []byte) string {
	sum := sha256Sum(b)
	return hex.EncodeToString(sum[:])
}

func TestBuildStatesTheCheckedCoverageOfAReportOverWindows(t *testing.T) {
	bundle, holderKey := windowReport(t, false)
	page, err := Build(bundle, View{Card: "outcome", Coverage: &Coverage{HolderCheckpointKey: holderKey}})
	require.NoError(t, err)
	html := string(page.HTML)
	assert.Contains(t, html, `<div class="aac-banner" data-state="verified" role="status"><strong>Covers 2026-10 to 2026-12.</strong> Each of the 3 windows has its answer from the holder's book for that window, and the windows link with no gap. The page's builder checked this from this page's bundle, under the holder checkpoint key the builder obtained, when the page was built; the page does not check it again when opened.`)
	assert.Contains(t, html, "Without witness receipts the windows' continuity is as the holder signed it. "+htmlstd.EscapeString(evidencebook.ReportLimit))
	require.NotNil(t, page.Coverage)
	assert.Len(t, page.Coverage.Windows, 3)
	_, err = page.Permalink("")
	require.ErrorIs(t, err, ErrRefused, "the permalink carries the bundle alone and would state no range")

	plain, err := Build(bundle, View{Card: "outcome"})
	require.NoError(t, err)
	assert.NotContains(t, string(plain.HTML), `role="status"><strong>Covers`)
	assert.Nil(t, plain.Coverage)
}

func TestBuildRendersNoPageOverAGapOrUnderAnotherKey(t *testing.T) {
	bundle, holderKey := windowReport(t, true)
	page, err := Build(bundle, View{Card: "outcome", Coverage: &Coverage{HolderCheckpointKey: holderKey}})
	require.ErrorIs(t, err, ErrRefused)
	assert.Nil(t, page)
	assert.Contains(t, err.Error(), "window 2026-11 has no answer")

	covered, _ := windowReport(t, false)
	stranger := hex.EncodeToString(seeded(9).Public().(ed25519.PublicKey))
	page, err = Build(covered, View{Card: "outcome", Coverage: &Coverage{HolderCheckpointKey: stranger}})
	require.ErrorIs(t, err, ErrRefused, "under a key the caller did not obtain, nothing is covered")
	assert.Nil(t, page)

	for _, view := range []View{
		{Card: "outcome", Coverage: &Coverage{}},
		{Card: "outcome", Diagnostic: true, Coverage: &Coverage{HolderCheckpointKey: holderKey}},
	} {
		page, err := Build(covered, view)
		require.ErrorIs(t, err, ErrRefused)
		assert.Nil(t, page)
	}
	// A report that is not over windows has no coverage to check.
	page, err = Build(mustRead(t, bookFormBundle), View{Card: "human_role", Coverage: &Coverage{HolderCheckpointKey: holderKey}})
	require.ErrorIs(t, err, ErrRefused)
	assert.Nil(t, page)
	assert.True(t, strings.Contains(err.Error(), "window chain"), err.Error())
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

// The range comes from the bundle's window chain statement, so the banner
// escapes it.
func TestTheCoverageBannerEscapesItsRange(t *testing.T) {
	out, err := markCoverage(`<div id="app"></div>`, evidencebook.ReportCoverage{From: "<b>", To: "x", Windows: make([]evidencebook.VerifiedWindow, 1)}, false)
	require.NoError(t, err)
	assert.Contains(t, out, "Covers &lt;b&gt; to x.")
	assert.NotContains(t, out, "Covers <b>")
	assert.Contains(t, out, "Each of the 1 window has its answer")
	witnessed, err := markCoverage(`<div id="app"></div>`, evidencebook.ReportCoverage{From: "a", To: "b", Windows: make([]evidencebook.VerifiedWindow, 2)}, true)
	require.NoError(t, err)
	assert.Contains(t, witnessed, "carries a receipt from a witness the page's builder named")
	assert.NotContains(t, witnessed, "Without witness receipts")
}

// A deal store's report (evidencebook#797): the banner counts the deals
// placed in the windows and states the report's limit once, beside the
// coverage; each deal's line follows in a list.
func TestTheCoverageBannerNamesEachPlacedDeal(t *testing.T) {
	line := func(id string) evidencebook.DealCoverage {
		// The line as evidencebook's coverage check writes it (window_report.go).
		return evidencebook.DealCoverage{LogID: id, Window: "2026-09", Line: "deal " + id + ", placed in window 2026-09, answered over its whole book, through its checkpoint of 4 records issued 2026-10-02T09:00:00Z, records after the window included <&>"}
	}
	one, err := markCoverage(appElement, evidencebook.ReportCoverage{From: "2026-09", To: "2026-09", Windows: make([]evidencebook.VerifiedWindow, 1),
		Deals: []evidencebook.DealCoverage{line("deal/a")}, Limit: evidencebook.ReportLimit}, false)
	require.NoError(t, err)
	assert.Contains(t, one, "The one deal placed in these windows has its answer too, over its whole book.")
	assert.Contains(t, one, "<details><summary>The deal and what each answer covers</summary><ul><li>"+htmlstd.EscapeString(line("deal/a").Line)+"</li></ul></details>")
	assert.Equal(t, 1, strings.Count(one, htmlstd.EscapeString(evidencebook.ReportLimit)), "the limit once")
	assert.Less(t, strings.Index(one, htmlstd.EscapeString(evidencebook.ReportLimit)), strings.Index(one, "<details>"), "the limit before the list")

	two, err := markCoverage(appElement, evidencebook.ReportCoverage{From: "2026-09", To: "2026-09", Windows: make([]evidencebook.VerifiedWindow, 1),
		Deals: []evidencebook.DealCoverage{line("deal/a"), line("deal/b")}, Limit: evidencebook.ReportLimit}, false)
	require.NoError(t, err)
	assert.Contains(t, two, "Each of the 2 deals placed in these windows has its answer too, over its whole book.")
	assert.Contains(t, two, "<summary>The 2 deals and what each answer covers</summary>")
	assert.Equal(t, 2, strings.Count(two, "<li>"))
	assert.NotContains(t, two, "<&>", "each line is escaped")
}
