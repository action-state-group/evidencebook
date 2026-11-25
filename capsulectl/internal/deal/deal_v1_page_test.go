// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// usePluginVerify points the page's verify run at the built plugin: in a test
// the running binary is the test binary, not capsulectl-deal.
func usePluginVerify(t *testing.T) {
	t.Helper()
	plugin := filepath.Join(installedCapsulectl(t), "plugins", "capsulectl-deal")
	old := selfVerifyBinary
	selfVerifyBinary = func() (string, error) { return plugin, nil }
	t.Cleanup(func() { selfVerifyBinary = old })
}

var pageBootstrap = regexp.MustCompile(`globalThis\.capsulectlDealPage = Object\.freeze\((.*?)\);`)

// pageVerifyReport is the verify report a page carries in its bootstrap.
func pageVerifyReport(t *testing.T, page []byte) map[string]any {
	t.Helper()
	m := pageBootstrap.FindSubmatch(page)
	require.NotNil(t, m, "the page carries capsulectl's report")
	var boot struct {
		Report map[string]any `json:"verifier_report"`
	}
	require.NoError(t, json.Unmarshal(m[1], &boot))
	return boot.Report
}

// `deal report --html` on an x-deal-v1 deal writes your own copy's receipt
// page: the bundle --bundle writes, drawn by capsule-viewer's x-deal-v1
// module, with this binary's own `verify --bundle` report of that bundle.
func TestAV1ReportHTMLCarriesTheBinarysOwnVerify(t *testing.T) {
	usePluginVerify(t)
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "receipt.html")
	out := dealRun(t, "report", "--deal", h.dealID, "--html", file)
	assert.Equal(t, file, out["html"])
	assert.Nil(t, out["bundle"])
	require.NotEmpty(t, out["sealed_report"])
	page, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(page), "capsuleviewer.receipt.deal/v1")
	report := pageVerifyReport(t, page)
	assert.Equal(t, "capsule-cli-result/v1", report["spec_version"])
	assert.Equal(t, "VALID", report["verdict"])
	var entry map[string]any
	for _, e := range report["extensions"].([]any) {
		if m := e.(map[string]any); m["kind"] == "x-deal-v1" {
			entry = m
		}
	}
	require.NotNil(t, entry, "%v", report["extensions"])
	assert.Equal(t, "pass", entry["status"])
	assert.Equal(t, "standalone", entry["report"])
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--html", file), "exists")
}

// --html is your own copy's page: it takes no --out, --bundle, --withheld or
// a --share other than keep.
func TestAV1ReportHTMLRefusals(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "receipt.html")
	other := filepath.Join(t.TempDir(), "other.json")
	for _, extra := range [][]string{{"--out", other}, {"--bundle", other}, {"--withheld"}} {
		assert.Contains(t, v1Err(t, append([]string{"report", "--deal", h.dealID, "--html", file}, extra...)...), "your own copy's receipt page", extra)
	}
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--html", file, "--share", "counterparty", "--to", "Kettle Shop"), "goes to --bundle with --share")
	// --read-only writes nothing, --html included; an explicit --share keep
	// leaves every other refusal in place.
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--html", file, "--read-only"), "--read-only writes nothing")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--html", file, "--share", "keep", "--read-only"), "--read-only writes nothing")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--html", file, "--share", "keep", "--email", filepath.Join(t.TempDir(), "e.eml")), "receipt email")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--html", file, "--share", "keep", "--from-bundle", other), "countersignatures")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--html", " "), "cannot be empty")
	_, err := os.Lstat(file)
	assert.ErrorIs(t, err, os.ErrNotExist, "no page is written")
}

// The report carried is whatever verify printed, a verdict other than VALID
// included (verify exits non-zero then); output that is not a report is an
// error, and no page is drawn without one.
func TestSelfVerifyReport(t *testing.T) {
	dir := t.TempDir()
	script := func(name, body string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700))
		return path
	}
	run := func(path string) ([]byte, error) {
		old := selfVerifyBinary
		selfVerifyBinary = func() (string, error) { return path, nil }
		defer func() { selfVerifyBinary = old }()
		return selfVerifyReport(context.Background(), []byte(`{}`))
	}
	got, err := run(script("invalid", `echo '{"spec_version":"capsule-cli-result/v1","verdict":"INVALID","bundle_digest":"ab"}'; exit 1`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"spec_version":"capsule-cli-result/v1","verdict":"INVALID","bundle_digest":"ab"}`, string(got))
	// capsulectl's error envelope has the same spec_version and is no report.
	_, err = run(script("envelope", `echo '{"spec_version":"capsule-cli-result/v1","error":"operation failed"}'; exit 2`))
	assert.ErrorContains(t, err, "gave no report")
	_, err = run(script("errorful", `echo '{"spec_version":"capsule-cli-result/v1","verdict":"VALID","bundle_digest":"ab","error":"x"}'`))
	assert.ErrorContains(t, err, "gave no report")
	_, err = run(script("nothing", `echo boom >&2; exit 2`))
	assert.ErrorContains(t, err, "gave no report: boom")
	_, err = run(script("other", `echo '{"spec_version":"something-else/v1"}'`))
	assert.ErrorContains(t, err, "gave no report")
	_, err = run(filepath.Join(dir, "missing"))
	assert.ErrorContains(t, err, "verify --bundle for the page")
}

// In a browser the page draws the x-deal-v1 receipt with capsulectl's line,
// never the can't-check line or a check of the page's own (CAPSULECTL_CHROME).
func TestPresentationV1ReceiptPage(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	usePluginVerify(t)
	chrome := startHeadlessChrome(t, binary)
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "receipt.html")
	dealRun(t, "report", "--deal", h.dealID, "--html", file)
	page := chrome.open(t, "file://"+file, presentationViews[1])
	var got struct {
		Module int    `json:"module"`
		Cannot int    `json:"cannot"`
		Verify string `json:"verify"`
		Text   string `json:"text"`
		Errors []any  `json:"errors"`
	}
	page.eval(t, `(() => ({
  module: document.querySelectorAll('[data-receipt-module="capsuleviewer.receipt.deal/v1"]').length,
  cannot: document.querySelectorAll('[data-cannot-check-reason]').length,
  verify: (document.querySelector("[data-verify]") || {}).getAttribute?.("data-verify") || "",
  text: document.getElementById("app").textContent,
  errors: window.__renderErrors || [],
}))()`, &got)
	assert.Positive(t, got.Module, "the x-deal-v1 receipt is drawn")
	assert.Zero(t, got.Cannot, "no can't-check line")
	assert.Equal(t, "verified", got.Verify)
	assert.Contains(t, got.Text, "capsulectl verify reported its signature check as: pass. This page did not check the signature itself.")
	assert.NotContains(t, got.Text, "This page checked it itself")
	assert.Empty(t, got.Errors)
}
