// SPDX-License-Identifier: Apache-2.0

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

// pageCountersignedCopy is the countersigned copy a v1 page's bootstrap
// carries, or nil when it carries none.
func pageCountersignedCopy(t *testing.T, page []byte) *v1PageCountersignedCopy {
	t.Helper()
	var boot struct {
		Copy *v1PageCountersignedCopy `json:"countersigned_copy"`
	}
	require.NoError(t, json.Unmarshal(pageBootstrapData(t, page), &boot))
	return boot.Copy
}

// countersignedV1 runs the documented countersign sequence on the buyer's
// side of a new deal (TestAssuranceDocSequenceRunsOnAV1Deal): the withheld
// bundle, a countersignature by a test countersigner, `deal countersign`,
// and the profile's countersigner directory. It returns the deal, the
// countersigned bundle's digest and the countersigner's key.
func countersignedV1(t *testing.T) (*handshakeV1, string, string) {
	t.Helper()
	return countersignedV1Listing(t, nil)
}

// countersignedV1Listing is countersignedV1 with the countersigner's
// directory row stating independent_of: the list independentOf returns for
// the deal's two parties as the receipt page reads them (dealParties), or
// none when independentOf is nil.
func countersignedV1Listing(t *testing.T, independentOf func(holder, counterparty string) []string) (*handshakeV1, string, string) {
	t.Helper()
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "bundle.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", file, "--withheld")
	b := readBundleFile(t, file)
	digest := mustDigest(t, b)
	entry, key := countersignerEntry(t, digest, []cli.CountersignCheck{{Name: "range membership", Result: "established"}})
	directory := countersignerDirectoryFile(t, "Example Countersigner", key)
	if independentOf != nil {
		own := filepath.Join(t.TempDir(), "own.json")
		dealRun(t, "report", "--deal", h.dealID, "--bundle", own)
		row := map[string]any{"name": "Example Countersigner", "key_ids": []string{key}, "independent_of": independentOf(dealParties(t, b, readBundleFile(t, own)))}
		raw, err := json.Marshal([]any{row})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(directory, raw, 0o600))
	}
	attachInPlace(t, file, entry)
	dealRun(t, "countersign", "--deal", h.dealID, "--bundle", file, "--directory", directory)
	_, err := invoke(t, "", "profile", "update", "--profile", "deal", "--countersign-directory", directory)
	require.NoError(t, err)
	return h, digest, key
}

// `deal report --html` carries the stored countersigned copy behind the
// report's rung, and the directory row that named its signer, for the page's
// own check (evidencebook#1015); a deal with no rung carries none.
func TestAV1ReportHTMLCarriesTheCountersignedCopy(t *testing.T) {
	usePluginVerify(t)
	h, digest, key := countersignedV1(t)
	file := filepath.Join(t.TempDir(), "receipt.html")
	out := dealRun(t, "report", "--deal", h.dealID, "--html", file)
	assert.Equal(t, "countersigned", asStamp(t, out["countersign"]).Rung)
	page, err := os.ReadFile(file)
	require.NoError(t, err)
	carried := pageCountersignedCopy(t, page)
	require.NotNil(t, carried, "the page carries the countersigned copy")
	b, err := cli.DecodeBundleJSON(carried.Bundle)
	require.NoError(t, err)
	assert.Equal(t, digest, mustDigest(t, b), "the copy the countersigner signed")
	assert.NotEmpty(t, b["countersignatures"])
	assert.Equal(t, []v1PageCountersignerRow{{Name: "Example Countersigner", KeyIDs: []string{key}}}, carried.Countersigners)
	assert.Contains(t, string(page), "countersignedCopy: capsulectlDealPage.countersigned_copy")

	// A deal with nothing stored carries no copy.
	other := agreedBoth(t, offerTo)
	other.buyer.act(t)
	plain := filepath.Join(t.TempDir(), "plain.html")
	dealRun(t, "report", "--deal", other.dealID, "--html", plain)
	page, err = os.ReadFile(plain)
	require.NoError(t, err)
	assert.Nil(t, pageCountersignedCopy(t, page))
}

// A stored countersignature that gives no rung (the holder's own key, even
// listed in the directory) puts no copy on the page.
func TestAV1ReportHTMLCarriesNoCopyWithoutARung(t *testing.T) {
	usePluginVerify(t)
	h := agreedBoth(t, offerTo)
	file := withheldV1(t, h)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	own, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	ownHex := hex.EncodeToString(own.Public().(ed25519.PublicKey))
	b := readBundleFile(t, file)
	entry := cli.CountersignatureEntry{
		Type: cli.CountersignAPI, Over: mustDigest(t, b),
		Signer:    cli.CountersignSigner{ID: "me", KeyID: ownHex},
		Statement: cli.CountersignStatement{Checks: []cli.CountersignCheck{{Name: "everything", Result: "established"}}, RecomputedAt: "2026-10-04T00:00:00Z", Scope: cli.CountersignScope{LedgerID: "deal"}},
	}
	signCountersignEntry(t, own, &entry)
	directory := countersignerDirectoryFile(t, "Looks Independent", ownHex)
	dealRun(t, "countersign", "--deal", h.dealID, "--bundle", file, "--entry", writeEntry(t, entry), "--directory", directory)
	_, err = invoke(t, "", "profile", "update", "--profile", "deal", "--countersign-directory", directory)
	require.NoError(t, err)
	html := filepath.Join(t.TempDir(), "receipt.html")
	out := dealRun(t, "report", "--deal", h.dealID, "--html", html)
	assert.Equal(t, "self_countersigned", asStamp(t, out["countersign"]).Finding)
	page, err := os.ReadFile(html)
	require.NoError(t, err)
	assert.Nil(t, pageCountersignedCopy(t, page))
}

// The receipt page, drawn in Chrome, fills COUNTERSIGNED from its own check
// of the copy it carries, through record k of n, and keeps it filled in part
// once the book has a later record.
func TestPresentationV1ReceiptCountersigned(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	usePluginVerify(t)
	chrome := startHeadlessChrome(t, binary)
	h, _, _ := countersignedV1(t)
	stamp := func(name string) (string, string) {
		file := filepath.Join(t.TempDir(), name)
		dealRun(t, "report", "--deal", h.dealID, "--html", file)
		page := chrome.open(t, "file://"+file, presentationViews[1])
		var got struct {
			State  string `json:"state"`
			Text   string `json:"text"`
			Errors []any  `json:"errors"`
		}
		page.eval(t, `(() => {
  const li = document.querySelector('[data-stamp="countersigned"]');
  return {
    state: li ? li.getAttribute("data-stamp-state") : "",
    text: li ? li.querySelector(".cv-rcpt-stamp__text").textContent : "",
    errors: window.__renderErrors || [],
  };
})()`, &got)
		assert.Empty(t, got.Errors)
		return got.State, got.Text
	}
	state, text := stamp("receipt.html")
	assert.Equal(t, "filled", state)
	assert.Regexp(t, `^Countersigned by Example Countersigner at .+, through record 1 of 1\. Its directory entry does not list it as independent of the parties\.$`, text)
	assert.NotContains(t, text, "as capsulectl reports")

	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	state, text = stamp("later.html")
	assert.Equal(t, "filled", state)
	assert.Regexp(t, `^Countersigned by Example Countersigner at .+, through record 1 of 2\. Its directory entry does not list it as independent of the parties\.$`, text)
}

// With more than one stored countersigned copy, the page carries the copy
// behind the report's rung (the one reaching furthest, even when another was
// stored later), with one row per signer resolved on that copy and none for
// an unresolved signer or another copy's signer.
func TestAV1ReportHTMLCarriesTheDecidingCopy(t *testing.T) {
	usePluginVerify(t)
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	early := filepath.Join(t.TempDir(), "early.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", early, "--withheld")
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	later := filepath.Join(t.TempDir(), "later.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", later, "--withheld")

	checks := []cli.CountersignCheck{{Name: "range membership", Result: "established"}}
	laterDigest := mustDigest(t, readBundleFile(t, later))
	first, firstKey := countersignerEntry(t, laterDigest, checks)
	second, secondKey := countersignerEntry(t, laterDigest, checks)
	unlisted, _ := countersignerEntry(t, laterDigest, checks)
	earlyEntry, earlyKey := countersignerEntry(t, mustDigest(t, readBundleFile(t, early)), checks)
	directory := filepath.Join(t.TempDir(), "countersigners.json")
	require.NoError(t, os.WriteFile(directory, []byte(`[{"name":"First","key_ids":["`+firstKey+`"]},{"name":"Second","key_ids":["`+secondKey+`"]},{"name":"Early","key_ids":["`+earlyKey+`"]}]`), 0o600))
	// The copy reaching furthest is stored first; the earlier one after it.
	for _, e := range []cli.CountersignatureEntry{first, second, unlisted} {
		attachInPlace(t, later, e)
	}
	dealRun(t, "countersign", "--deal", h.dealID, "--bundle", later, "--directory", directory)
	attachInPlace(t, early, earlyEntry)
	dealRun(t, "countersign", "--deal", h.dealID, "--bundle", early, "--directory", directory)
	_, err := invoke(t, "", "profile", "update", "--profile", "deal", "--countersign-directory", directory)
	require.NoError(t, err)

	file := filepath.Join(t.TempDir(), "receipt.html")
	out := dealRun(t, "report", "--deal", h.dealID, "--html", file)
	stamp := asStamp(t, out["countersign"])
	assert.Equal(t, stamp.Steps, stamp.ThroughStep, "the rung is the copy reaching furthest")
	page, err := os.ReadFile(file)
	require.NoError(t, err)
	carried := pageCountersignedCopy(t, page)
	require.NotNil(t, carried)
	b, err := cli.DecodeBundleJSON(carried.Bundle)
	require.NoError(t, err)
	assert.Equal(t, laterDigest, mustDigest(t, b), "the copy behind the rung, not the one stored last")
	assert.ElementsMatch(t, []v1PageCountersignerRow{
		{Name: "First", KeyIDs: []string{firstKey}},
		{Name: "Second", KeyIDs: []string{secondKey}},
	}, carried.Countersigners, "one row per signer resolved on that copy")
}

// dealParties are an x-deal-v1 deal's two parties as its receipt page reads
// them (capsule-viewer countersigned-copy.ts): the holder's key, which the
// withheld copy declares (producer-key/v1), and the counterparty_key of the
// sealed agreement in the holder's own bundle, the one the page shows.
func dealParties(t *testing.T, withheld, own map[string]interface{}) (holder, counterparty string) {
	t.Helper()
	declared, ok := cli.DeclaredProducerKey(withheld)
	require.True(t, ok, "the withheld copy declares the holder's key")
	ext, _ := own["extensions"].(map[string]interface{})
	v1, _ := ext["x-deal-v1"].(map[string]interface{})
	book, _ := v1["book"].(map[string]interface{})
	agreement, _ := book["agreement"].(map[string]interface{})
	payload, _ := agreement["payload"].(map[string]interface{})
	body, _ := payload["body"].(map[string]interface{})
	counterparty, _ = body["counterparty_key"].(string)
	require.NotEmpty(t, counterparty, "the agreement names the counterparty")
	return hex.EncodeToString(declared), counterparty
}

// `deal report --html` carries the countersigner row's independent_of on
// the page's row, as the directory states it, and prints it nowhere else
// (evidencebook#1019): the page decides from it (capsule-viewer#457, u649).
func TestAV1ReportHTMLCarriesTheRowsIndependentOf(t *testing.T) {
	usePluginVerify(t)
	var holder, counterparty string
	h, _, key := countersignedV1Listing(t, func(hk, ck string) []string {
		holder, counterparty = hk, ck
		return []string{holder, counterparty}
	})
	file := filepath.Join(t.TempDir(), "receipt.html")
	out := dealRun(t, "report", "--deal", h.dealID, "--html", file)
	page, err := os.ReadFile(file)
	require.NoError(t, err)
	carried := pageCountersignedCopy(t, page)
	require.NotNil(t, carried)
	assert.Equal(t, []v1PageCountersignerRow{{Name: "Example Countersigner", KeyIDs: []string{key}, IndependentOf: []string{holder, counterparty}}}, carried.Countersigners)
	printed, err := json.Marshal(out["countersign"])
	require.NoError(t, err)
	assert.NotContains(t, string(printed), "independent_of", "deal report's printed JSON is unchanged")
	assert.NotContains(t, string(printed), counterparty)
}

// Two directory rows under one name with different independent_of lists
// stay two page rows, each key with its own row's list; entries of one row
// share a page row.
func TestV1CountersignedCopyKeepsEachRowsList(t *testing.T) {
	a, b, c := strings.Repeat("a1", 32), strings.Repeat("b2", 32), strings.Repeat("c3", 32)
	stamp := dealGradeCountersign{Rung: "countersigned", copy: []byte(`{}`), copyDigest: "d"}
	stamp.Entries = []dealStoredCountersignature{
		{State: "resolved", Signer: "Same Name", KeyID: a, BundleDigest: "d", independentOf: []string{"p", "q"}},
		{State: "resolved", Signer: "Same Name", KeyID: b, BundleDigest: "d"},
		{State: "resolved", Signer: "Same Name", KeyID: c, BundleDigest: "d", independentOf: []string{"p", "q"}},
		{State: "not_independent", Signer: "", KeyID: strings.Repeat("dd", 32), BundleDigest: "d"},
		{State: "resolved", Signer: "Other Copy", KeyID: a, BundleDigest: "e", independentOf: []string{"p", "q"}},
	}
	assert.Equal(t, []v1PageCountersignerRow{
		{Name: "Same Name", KeyIDs: []string{a, c}, IndependentOf: []string{"p", "q"}},
		{Name: "Same Name", KeyIDs: []string{b}},
	}, v1CountersignedCopy(stamp).Countersigners)
}

// The receipt page, drawn in Chrome, drops "Its directory entry does not
// list it as independent of the parties." only when the signer's row lists
// both parties, and keeps it for a row naming one party or a list that
// misses the holder's key (u649, evidencebook#1019).
func TestPresentationV1ReceiptCountersignedIndependence(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	usePluginVerify(t)
	chrome := startHeadlessChrome(t, binary)
	const label = " Its directory entry does not list it as independent of the parties."
	for _, tc := range []struct {
		name    string
		list    func(holder, counterparty string) []string
		labeled bool
	}{
		{"both parties", func(h, c string) []string { return []string{h, c} }, false},
		{"counterparty only", func(h, c string) []string { return []string{c} }, true},
		{"holder only", func(h, c string) []string { return []string{h} }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := countersignedV1Listing(t, tc.list)
			file := filepath.Join(t.TempDir(), "receipt.html")
			dealRun(t, "report", "--deal", h.dealID, "--html", file)
			page := chrome.open(t, "file://"+file, presentationViews[1])
			var got struct {
				State  string `json:"state"`
				Text   string `json:"text"`
				Errors []any  `json:"errors"`
			}
			page.eval(t, `(() => {
  const li = document.querySelector('[data-stamp="countersigned"]');
  return {
    state: li ? li.getAttribute("data-stamp-state") : "",
    text: li ? li.querySelector(".cv-rcpt-stamp__text").textContent : "",
    errors: window.__renderErrors || [],
  };
})()`, &got)
			assert.Empty(t, got.Errors)
			assert.Equal(t, "filled", got.State)
			assert.Regexp(t, `^Countersigned by Example Countersigner at .+, through record 1 of 1\.`, got.Text)
			if tc.labeled {
				assert.True(t, strings.HasSuffix(got.Text, label), got.Text)
			} else {
				assert.NotContains(t, got.Text, "independent", got.Text)
			}
		})
	}
}
