package deal

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receipt with the other party's half (9b frames 6 and 7,
// docs/design/two-sided-receipt.md), rendered in Chrome (CAPSULECTL_CHROME)
// at 390px: the buyer composes the seller's answer, or what it recorded when
// none came, with its own keep copy, and the page names the case from what
// it checked itself.
func TestPresentationReceiptWithTheirHalf(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	chrome := startHeadlessChrome(t, binary)
	pair := closedPair(t, received, received)
	party(t, pair.buyer.config)
	dir := t.TempDir()
	keepPath := filepath.Join(dir, "keep.json")
	dealRun(t, "report", "--deal", pair.id, "--bundle", keepPath)
	keep := exactBundle(t, string(mustRead(t, keepPath)))

	type shown struct {
		Banner  string   `json:"banner"`
		Checked string   `json:"checked"`
		Case    string   `json:"case"`
		Their   string   `json:"their"`
		Box     string   `json:"box"`
		Stamps  []string `json:"stamps"`
		Refusal string   `json:"refusal"`
		Private string   `json:"private"`
	}
	// The buyer's own copy, answered to its own request: a member the
	// composer observed is its own, never theirs, so each composition below
	// carries it as a second member and the page sets it aside.
	own := askFor(t, pair.buyer, pair.buyer, pair.id, map[string]any{"min_freshness": map[string]any{"size": 1}})
	// open composes the members (with the keep copy unless bare) and reads
	// the page as a reader at 390px sees it. A composition the page gate
	// refuses (a refusal member reads INCOMPLETE) is emitted ungated, as a
	// viewer that opens the bundle itself shows it.
	// view is the page view open reads; a subtest may take Ed25519 away.
	view := presentationViews[0]
	// openWith also takes the keep copy to carry (nil for none) and an edit
	// of the composition before its page is built.
	var openWith func(t *testing.T, withKeep map[string]interface{}, edit func(map[string]any), members ...askedCopy) shown
	open := func(t *testing.T, bare bool, members ...askedCopy) shown {
		t.Helper()
		if bare {
			return openWith(t, nil, nil, members...)
		}
		return openWith(t, keep, nil, members...)
	}
	openWith = func(t *testing.T, withKeep map[string]interface{}, edit func(map[string]any), members ...askedCopy) shown {
		t.Helper()
		party(t, pair.buyer.config)
		out := filepath.Join(t.TempDir(), "composed.json")
		args := []string{"--profile", "deal", "deal", "compose", "--deal", pair.id, "--output", out, "--member", own.recordID}
		for _, m := range members {
			args = append(args, "--member", m.recordID)
		}
		text, err := dealInvoke(t, "", args...)
		require.NoError(t, err, text)
		composition := exactBundle(t, string(mustRead(t, out)))
		if edit != nil {
			edit(composition)
		}
		page, err := composedPage(composition, withKeep, dealVerifyCommand("page.html"), cli.BuiltinWording())
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "page.html")
		require.NoError(t, os.WriteFile(path, []byte(page), 0o600))
		p := chrome.open(t, "file://"+path, view)
		var got shown
		p.eval(t, `(() => {
  const q = (s) => document.querySelector("#deal " + s);
  const banner = q(".deal-banner"), their = q("[data-their]");
  return {
    banner: banner ? banner.textContent : "",
    checked: banner ? banner.dataset.checked || "" : "",
    case: banner ? banner.dataset.case || "" : "",
    their: their ? their.dataset.their : "",
    box: their ? their.className + " | " + their.textContent : "",
    stamps: [...document.querySelectorAll("#deal [data-stamp]")].map((s) => s.dataset.stamp + " | " + s.textContent),
    refusal: (q("p.deal-bad") || {}).textContent || "",
    private: (q("[data-private]") || {}).textContent || "",
  };
})()`, &got)
		return got
	}
	ask := func(coverage map[string]any) askedCopy {
		return askFor(t, pair.buyer, pair.seller, pair.id, coverage)
	}
	fresh := map[string]any{"min_freshness": map[string]any{"size": 1}}

	theirs := ask(fresh)
	require.Equal(t, "artifact", theirs.outcome)
	absent := recordedAbsence(t, pair.buyer, pair.id)

	t.Run("stamped by both", func(t *testing.T) {
		got := open(t, false, theirs)
		assert.Equal(t, "both", got.Case)
		assert.Equal(t, "yes", got.Checked)
		assert.Contains(t, got.Banner, "Stamped by both sides · checked")
		assert.Equal(t, "verified", got.Their)
		assert.Contains(t, got.Box, "deal-theirs")
		assert.Contains(t, got.Box, "Their side · in their book · as received")
		assert.Contains(t, got.Box, "Their key "+pair.seller.signer[:16], "in their side's fold-out")
		assert.Contains(t, got.Box, "Their reply's signature, and the checkpoint it names, are checked by capsulectl verify, not on this page.")
		assert.NotContains(t, got.Box, "named it", "no label from the composer's declaration")
		require.Len(t, got.Stamps, 5)
		assert.Contains(t, got.Stamps[1], "passed | ✓Each side's record is stamped into its own book")
		assert.Contains(t, got.Stamps[2], "passed | ✓Their record and yours are about the same deal")
		assert.Contains(t, got.Stamps[2], "both records name the same deal; neither points at the other")
		assert.Contains(t, got.Stamps[4], "An outside witness recorded both books' snapshots")
		assert.Contains(t, got.Private, "This page is your private copy", "the page says on its face that it carries the keep copy")
		assert.Contains(t, got.Private, "send the composed bundle file, not this page")
	})

	t.Run("your receipt only: an absence", func(t *testing.T) {
		got := open(t, false, absent)
		assert.Equal(t, "only", got.Case)
		assert.Contains(t, got.Banner, "Your receipt only · checked")
		assert.Equal(t, "absent", got.Their)
		assert.Contains(t, got.Box, "deal-none")
		assert.Contains(t, got.Box, "No record. Your agent asked and nothing came back by")
		assert.Contains(t, got.Stamps[2], "missing | —Their record and yours are about the same deal")
	})

	t.Run("their record failed is never an absence", func(t *testing.T) {
		// A response signed under a key the copy's root is not sealed
		// under: recorded as an answer (the copy itself checks out under
		// the pinned checkpoint key), and failed on the page (V2).
		other := underAnotherKey(t, pair, fresh)
		failed := open(t, false, other)
		assert.Equal(t, "only", failed.Case)
		assert.Equal(t, "failed", failed.Their)
		assert.Contains(t, failed.Box, "Their record is in this file, but it did not check: failed.")
		assert.Contains(t, failed.Stamps[1], "failed | —Each side's record is stamped into its own book")
		assert.Contains(t, failed.Stamps[2], "failed | —Their record and yours are about the same deal")
		gone := open(t, false, absent)
		assert.NotEqual(t, gone.Box, failed.Box, "the failed box and the absence box differ (S4)")
		assert.NotEqual(t, gone.Stamps, failed.Stamps)
	})

	t.Run("a refusal whose signature this page did not check", func(t *testing.T) {
		// Coverage the seller cannot serve is answered with a signed
		// refusal.
		refused := ask(map[string]any{"min_freshness": map[string]any{"size": 1000000}})
		require.Equal(t, "refusal", refused.outcome)
		got := open(t, false, refused)
		assert.Equal(t, "only", got.Case)
		assert.Equal(t, "refused_unchecked", got.Their)
		assert.Contains(t, got.Box, "A refusal in their name came back on")
		assert.Contains(t, got.Box, "its signature was not checked here.")
	})

	t.Run("a verified copy beside an absence", func(t *testing.T) {
		got := open(t, false, theirs, absent)
		assert.Equal(t, "both", got.Case)
		assert.Contains(t, got.Box, "Also: your agent asked and nothing came back by")
	})

	t.Run("no keep copy", func(t *testing.T) {
		got := open(t, true, theirs)
		require.Empty(t, got.Refusal)
		assert.Contains(t, got.Banner, "Your copy is not in this file · not fully checked")
		assert.Equal(t, "no", got.Checked)
		assert.Contains(t, got.Banner, "Your own copy of this deal is not in this file.")
		assert.Contains(t, got.Stamps[1], "missing | —Each side's record is stamped into its own book and can't be changed after · missing · your record is not in this file")
		assert.Empty(t, got.Private, "no keep copy, no private-copy line")
	})

	// A member whose response names another checkpoint: the response is not
	// in the composed digest, so the core passes it, and the page fails it
	// (V3).
	t.Run("a response anchored at another checkpoint", func(t *testing.T) {
		got := openWith(t, keep, func(c map[string]any) {
			for _, raw := range c["extensions"].(map[string]any)["composed/v1"].(map[string]any)["members"].([]any) {
				if m := raw.(map[string]any); m["id"] == theirs.recordID {
					m["response"].(map[string]any)["anchor"] = strings.Repeat("0", 64) + ":1"
				}
			}
		}, theirs)
		assert.Equal(t, "failed", got.Their)
		assert.Contains(t, got.Banner, "Your receipt only")
	})
	t.Run("a composition whose digest does not hold", func(t *testing.T) {
		got := openWith(t, keep, func(c map[string]any) {
			c["extensions"].(map[string]any)["composed/v1"].(map[string]any)["composed_digest"] = strings.Repeat("0", 64)
		}, theirs)
		assert.Contains(t, got.Refusal, "Do not rely on it")
		assert.Empty(t, got.Banner)
	})
	t.Run("a keep copy of another deal", func(t *testing.T) {
		dealFixture(t)
		otherDeal := dealRun(t, "open", "--input", writeJSON(t, crossPartyBuyerOpen))["deal_id"].(string)
		path := filepath.Join(t.TempDir(), "other.json")
		dealRun(t, "report", "--deal", otherDeal, "--bundle", path)
		got := openWith(t, exactBundle(t, string(mustRead(t, path))), nil, theirs)
		assert.Contains(t, got.Refusal, "not the composer's own copy of this deal")
	})

	t.Run("a viewer without Ed25519", func(t *testing.T) {
		// Ed25519 taken away from WebCrypto, as in a viewer without it: the
		// page says it cannot check, and never calls their record bad.
		view.before = `(() => { const s = crypto.subtle; const ed = (args) => args.some((a) => a && String(typeof a === "string" ? a : a.name).toLowerCase() === "ed25519"); for (const m of ["importKey", "verify", "generateKey"]) { const f = s[m].bind(s); s[m] = (...args) => (ed(args) ? Promise.reject(new DOMException("no Ed25519 here", "NotSupportedError")) : f(...args)); } })();`
		defer func() { view.before = "" }()
		got := open(t, false, theirs)
		assert.Contains(t, got.Refusal, "This viewer can't check the signatures in this file here")
		assert.Empty(t, got.Their, "no failed box for their record")
	})

	t.Run("their half is yours under another label", func(t *testing.T) {
		// The buyer answers its own request: an artifact observed by the
		// composer is its own, not theirs.
		again := askFor(t, pair.buyer, pair.buyer, pair.id, fresh)
		got := open(t, false, again)
		assert.Equal(t, "none", got.Case)
		assert.Contains(t, got.Banner, "No record · checked")
	})
}

// recordedAbsence records a request no one answered, its window closed.
func recordedAbsence(t *testing.T, asker dealParty, dealID string) askedCopy {
	t.Helper()
	party(t, asker.config)
	path := filepath.Join(t.TempDir(), "request.json")
	sent := runJSON(t, "deal", "request", "--request", writeJSONFile(t, "ask.json", map[string]any{
		"subject": map[string]any{"kind": "correlation", "value": dealID}, "coverage": map[string]any{"min_freshness": map[string]any{"size": 1}},
	}), "--responder", "the other party", "--output", path)
	asked := cli.DealClock()
	old := cli.DealClock
	cli.DealClock = func() time.Time { return asked.Add(time.Hour) }
	defer func() { cli.DealClock = old }()
	runJSON(t, "deal", "request", "--absent-until", asked.Add(2*time.Minute).Format(time.RFC3339), "--for", sent["record_id"].(string))
	return askedCopy{recordID: sent["record_id"].(string), requestPath: path, outcome: "recorded_absence"}
}

// underAnotherKey asks for the seller's copy and records the answer as
// signed by a fresh key K, pinned as the responder key: the copy is the
// seller's own, under the seller's checkpoint key, but its root is not
// sealed under K.
func underAnotherKey(t *testing.T, pair reconcilePair, coverage map[string]any) askedCopy {
	t.Helper()
	dir := t.TempDir()
	got := askedCopy{requestPath: filepath.Join(dir, "request.json"), responsePath: filepath.Join(dir, "response.json")}
	party(t, pair.buyer.config)
	sent := runJSON(t, "deal", "request", "--request", writeJSONFile(t, "ask.json", map[string]any{
		"subject": map[string]any{"kind": "correlation", "value": pair.id}, "coverage": coverage,
	}), "--responder", "the other party", "--output", got.requestPath)
	got.recordID = sent["record_id"].(string)
	party(t, pair.seller.config)
	runJSON(t, "deal", "respond", "--request", got.requestPath, "--requester", "the asking party", "--output", got.responsePath)
	var response dealResponse
	require.NoError(t, json.Unmarshal(mustRead(t, got.responsePath), &response))
	public, private, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	a := response.Artifact
	a.KeyID = hex.EncodeToString(public)
	body, err := a.SigningBody()
	require.NoError(t, err)
	a.Sig = hex.EncodeToString(ed25519.Sign(private, body))
	encoded, err := json.Marshal(dealResponse{Artifact: a})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(got.responsePath, encoded, 0o600))
	party(t, pair.buyer.config)
	recorded := runJSON(t, "deal", "request", "--response", got.responsePath, "--for", got.recordID,
		"--responder-key", a.KeyID, "--responder-checkpoint-key", pair.seller.checkpoint)
	got.outcome = recorded["outcome"].(string)
	require.Equal(t, "artifact", got.outcome)
	return got
}

// A bundle with two rounds (#81, PROFILE rule 7 and "Later bundles"): the
// deal closes pending, a first report is sealed, then a later outcome and
// close and a second report. The later bundle carries the first report's
// Capsule with its payload withheld; the page reads the latest round, and
// the earlier report shows neither as missing evidence nor as a refusal.
func TestPresentationReceiptReadsTheLatestRound(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	chrome := startHeadlessChrome(t, binary)
	inLogReports(t)
	dealFixture(t)
	id := retailDeal(t)
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"pending"}`))
	dir := t.TempDir()
	dealRun(t, "report", "--deal", id, "--bundle", filepath.Join(dir, "first.json"))
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, received))
	later := filepath.Join(dir, "later.json")
	dealRun(t, "report", "--deal", id, "--bundle", later)
	bundle := exactBundle(t, string(mustRead(t, later)))
	reports := 0
	for _, raw := range bundle["records"].([]any) {
		if r := raw.(map[string]any); r["action_id"] == "capsulectl-deal-report" {
			reports++
		}
	}
	require.Equal(t, 2, reports, "the later bundle carries both rounds' reports")

	page := filepath.Join(dir, "page.html")
	dealRun(t, "report", "--deal", id, "--html", page)
	p := chrome.open(t, "file://"+page, presentationViews[0])
	var got struct {
		Refusal string `json:"refusal"`
		Text    string `json:"text"`
	}
	p.eval(t, `(() => ({
  refusal: (document.querySelector("#deal p.deal-bad") || {}).textContent || "",
  text: (document.getElementById("deal") || {}).textContent || "",
}))()`, &got)
	assert.Empty(t, got.Refusal, "the earlier report is not a refusal")
	assert.NotContains(t, got.Text, "step not verified")
	assert.NotContains(t, got.Text, "Open: ", "the latest round is closed, not the first round's open state")
	assert.Contains(t, got.Text, "Your copy only · checked")
}
