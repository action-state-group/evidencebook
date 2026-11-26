// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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
	m := pageBootstrap.FindSubmatch(page)
	require.NotNil(t, m, "the page carries its bootstrap data")
	var boot struct {
		Copy *v1PageCountersignedCopy `json:"countersigned_copy"`
	}
	require.NoError(t, json.Unmarshal(m[1], &boot))
	return boot.Copy
}

// countersignedV1 runs the documented countersign sequence on the buyer's
// side of a new deal (TestAssuranceDocSequenceRunsOnAV1Deal): the withheld
// bundle, a countersignature by a test countersigner, `deal countersign`,
// and the profile's countersigner directory. It returns the deal, the
// countersigned bundle's digest and the countersigner's key.
func countersignedV1(t *testing.T) (*handshakeV1, string, string) {
	t.Helper()
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "bundle.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", file, "--withheld")
	b := readBundleFile(t, file)
	digest := mustDigest(t, b)
	entry, key := countersignerEntry(t, digest, []cli.CountersignCheck{{Name: "range membership", Result: "established"}})
	directory := countersignerDirectoryFile(t, "Example Countersigner", key)
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
