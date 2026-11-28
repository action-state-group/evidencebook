// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

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

// pageVerifyReport is the verify report a page carries in its bootstrap:
// the one JSON value after the bootstrap's prefix (its strings may hold
// ");", so the value is decoded, not cut by a pattern).
func pageVerifyReport(t *testing.T, page []byte) map[string]any {
	t.Helper()
	var boot struct {
		Report map[string]any `json:"verifier_report"`
	}
	require.NoError(t, json.Unmarshal(pageBootstrapData(t, page), &boot))
	return boot.Report
}

// pageBootstrapData is the one JSON value after the page bootstrap's prefix.
func pageBootstrapData(t *testing.T, page []byte) json.RawMessage {
	t.Helper()
	_, rest, ok := bytes.Cut(page, []byte("globalThis.capsulectlDealPage = Object.freeze("))
	require.True(t, ok, "the page carries its bootstrap data")
	var data json.RawMessage
	require.NoError(t, json.NewDecoder(bytes.NewReader(rest)).Decode(&data))
	return data
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
	// A person's receipt page runs on the viewer's named pack, never one
	// named "example" (Sherlock2 on evidencebook#956).
	assert.Contains(t, string(page), "capsuleviewer.receipt.wording.en/v1")
	assert.NotContains(t, string(page), "wording.example")
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
		return selfVerifyReport(context.Background(), []byte(`{}`), nil, "", nil)
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
	// A two-sided copy: the other party's half as capsulectl reported it, and
	// the file-scoped witness line in place of the log line (capsule-viewer
	// #420, #421).
	assert.Contains(t, got.Text, "Signed by both sides: this copy carries the other party's signed agreement")
	assert.Contains(t, got.Text, "This file carries no witness receipt, so this page could not check a time-stamp itself.")
	assert.NotContains(t, got.Text, "has vouched for this log")
	assert.Contains(t, got.Text, "This page's words: wording pack capsuleviewer.receipt.wording.en/v1, sha256 ", "the footer names the viewer's pack (evidencebook#956)")
	assert.Empty(t, got.Errors)
}

// holderWitnessEvidence lists where a copy carries a stamp proof or witness
// receipts of the holder's own: anywhere but the other party's half
// (x-deal-v1-counterparty-half, which the holder received from them).
func holderWitnessEvidence(t *testing.T, copy []byte) []string {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(copy, &v))
	var found []string
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				p := path + "/" + k
				if p == "/extensions/x-deal-v1-counterparty-half" {
					continue
				}
				if k == "stamp_proof" || k == "witness_receipts" || k == "shelf_checkpoint" {
					found = append(found, p)
				}
				walk(c, p)
			}
		case []any:
			for _, c := range x {
				walk(c, path+"[]")
			}
		}
	}
	walk(v, "")
	return found
}

// No copy carries witness evidence of the holder's own (Sherlock2,
// 2026-11-25: a stamp proof shows the holder's shelf checkpoint and log
// position): not the keep copy, not the page --html writes with
// --witness-directory, not a shared or adjudicator's copy, not the withheld
// bundle a countersigner is shown. The page's verify report carries only
// the witnessed result.
func TestAV1NoCopyCarriesWitnessEvidence(t *testing.T) {
	usePluginVerify(t)
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	dir := t.TempDir()
	directory := filepath.Join(witnessedVectors, "witnesses.json")
	abs, err := filepath.Abs(directory)
	require.NoError(t, err)
	files := map[string][]string{
		"keep.json":        {"--bundle"},
		"page.html":        {"--witness-directory", abs, "--html"},
		"counterpart.json": {"--share", "counterparty", "--to", "Kettle Shop", "--bundle"},
		"withheld.json":    {"--withheld", "--bundle"},
	}
	for name, flags := range files {
		path := filepath.Join(dir, name)
		dealRun(t, append(append([]string{"report", "--deal", h.dealID}, flags...), path)...)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		if name == "page.html" {
			m := regexp.MustCompile(`<script type="application/json">([\s\S]*?)</script>`).FindSubmatch(raw)
			require.NotNil(t, m)
			assert.Empty(t, holderWitnessEvidence(t, m[1]), name)
			report := pageVerifyReport(t, raw)
			for _, e := range report["extensions"].([]any) {
				if m := e.(map[string]any); m["kind"] == "x-deal-v1" {
					if w, ok := m["witnessed"].(map[string]any); ok {
						assert.NotEqual(t, "verified", w["status"], "no witness in these tests")
					}
				}
			}
			continue
		}
		assert.Empty(t, holderWitnessEvidence(t, raw), name)
	}
}

// --witness-directory goes with an x-deal-v1 --html page only.
func TestAV1ReportWitnessDirectoryRefusals(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	w := filepath.Join(witnessedVectors, "witnesses.json")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--witness-directory", w), "use it with --html")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--html", filepath.Join(t.TempDir(), "p.html"), "--witness-directory", filepath.Join(t.TempDir(), "missing.json")), "cannot read --witness-directory")
}

// The page path with a real witness (the demo's vectors): v1ReportPage gives
// its own verify the proof and the directory, and the page carries exactly
// verify's witnessed result, never the proof or its receipts. In a browser
// the page says that time in capsulectl's voice (CAPSULECTL_CHROME).
func TestV1ReportPageCarriesTheWitnessedResultOnly(t *testing.T) {
	usePluginVerify(t)
	keep, err := os.ReadFile(filepath.Join(witnessedVectors, "keep.json"))
	require.NoError(t, err)
	proof, err := readStampProof(filepath.Join(witnessedVectors, "proof.json"))
	require.NoError(t, err)
	dir, err := filepath.Abs(filepath.Join(witnessedVectors, "witnesses.json"))
	require.NoError(t, err)
	page, err := v1ReportPage(t.Context(), keep, "capsulectl deal verify --bundle receipt.html", proof, dir, nil)
	require.NoError(t, err)
	report := pageVerifyReport(t, page)
	var entry map[string]any
	for _, e := range report["extensions"].([]any) {
		if m := e.(map[string]any); m["kind"] == "x-deal-v1" {
			entry = m
		}
	}
	require.NotNil(t, entry)
	assert.Equal(t, map[string]any{"status": "verified", "witness": "Action State's witness", "at": "2026-11-25T14:41:19Z", "independent_of": []any{}}, entry["witnessed"])
	// The row's independent_of reaches the page as verify reported it
	// (evidencebook#1073).
	listed := filepath.Join(t.TempDir(), "witnesses.json")
	require.NoError(t, os.WriteFile(listed, []byte(`{"witnesses":[{"name":"Action State's witness","endpoint":"https://witness.agentactioncapsule.org","key_ids":["39bb654c9dc0afe1c0edef0deffaa69099b8518836c9ba26e0491535840f96b5"],"independent_of":["`+strings.Repeat("a1", 32)+`"]}]}`), 0o600))
	listedPage, err := v1ReportPage(t.Context(), keep, "capsulectl deal verify --bundle receipt.html", proof, listed, nil)
	require.NoError(t, err)
	assert.Equal(t, []any{strings.Repeat("a1", 32)}, v1EntryOf(t, pageVerifyReport(t, listedPage))["witnessed"].(map[string]any)["independent_of"])
	m := regexp.MustCompile(`<script type="application/json">([\s\S]*?)</script>`).FindSubmatch(page)
	require.NotNil(t, m)
	assert.Empty(t, holderWitnessEvidence(t, m[1]), "the page's bundle carries no proof of the holder's")
	assert.NotContains(t, string(page), `"witness_receipts"`, "the page carries no receipt")

	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		return
	}
	file := filepath.Join(t.TempDir(), "receipt.html")
	require.NoError(t, os.WriteFile(file, page, 0o600))
	// The page shows times in the reader's own zone (capsule-viewer#423):
	// a fixed zone for the browser makes the line exact.
	t.Setenv("TZ", "UTC")
	chrome := startHeadlessChrome(t, binary)
	opened := chrome.open(t, "file://"+file, presentationViews[1])
	var text string
	opened.eval(t, `document.getElementById("app").textContent`, &text)
	assert.Contains(t, text, "Time-stamped by Action State's witness at Nov 25, 2026, 2:41 PM UTC (14:41:19 UTC), as capsulectl verify reported on your own book. This file carries no witness receipt, so this page did not check it itself.")
	assert.NotContains(t, text, "so this page could not check a time-stamp itself", "the header line is left out on this page (capsule-viewer#424)")
	// The demo's own copy draws the receipt design's top line and its seal
	// (capsule-viewer#442; the seal's cases:
	// https://github.com/action-state-group/capsule-viewer/issues/382#issuecomment-7190374309).
	var top []string
	opened.eval(t, `[document.querySelector("[data-top-line]").getAttribute("data-top-line"), document.querySelector("[data-top-line]").textContent,
  [...document.querySelectorAll("svg[data-seal]")].map((e) => e.getAttribute("data-seal")).join(",")]`, &top)
	assert.Equal(t, []string{"settled", "Sale settled, as your records show", "done"}, top)
}

// The receipt design (capsule-viewer#442, Steven 2026-11-26: "we had these
// designs, why not showing that look and feel. keep the ladder and the
// producer envelope etc."): the default `deal report --html` page draws the
// design's surfaces on top, every sealed record and the producer's lines
// under "For checkers", and the ladder of rungs under "Technical details"
// only (CAPSULECTL_CHROME).
func TestPresentationV1ReceiptDesign(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	usePluginVerify(t)
	chrome := startHeadlessChrome(t, binary)
	h := paidLegs(t, "6000")
	// Past the contest window, the term is met: settled, and both sides say
	// the same.
	clockAt(t, time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	file := filepath.Join(t.TempDir(), "receipt.html")
	dealRun(t, "report", "--deal", h.dealID, "--html", file)
	page := chrome.open(t, "file://"+file, presentationViews[1])
	var got struct {
		Surfaces  []string `json:"surfaces"`
		Header    string   `json:"header"`
		TopLine   string   `json:"top_line"`
		Seals     []string `json:"seals"`
		Cards     []string `json:"cards"`
		Steps     []string `json:"steps"`
		Proof     string   `json:"proof"`
		NotProven string   `json:"not_proven"`
		L1        string   `json:"l1"`
		Records   int      `json:"records"`
		L2Rungs   []string `json:"l2_rungs"`
		UpperRung int      `json:"upper_rungs"`
		L2        string   `json:"l2"`
		Errors    []any    `json:"errors"`
	}
	page.eval(t, `(() => {
  const level = (l) => document.querySelector('[data-receipt-module="capsuleviewer.receipt.deal/v1"] [data-level="' + l + '"]');
  const l0 = level("L0"), l1 = level("L1"), l2 = level("L2");
  const attr = (root, sel, name) => (root.querySelector(sel) || { getAttribute: () => "" }).getAttribute(name);
  const all = (root, sel, name) => [...root.querySelectorAll(sel)].map((e) => e.getAttribute(name));
  return {
    surfaces: all(l0, "[data-surface]", "data-surface"),
    header: (l0.querySelector('[data-surface="header"]') || {}).textContent || "",
    top_line: attr(l0, "[data-top-line]", "data-top-line"),
    seals: all(l0, "svg[data-seal]", "data-seal"),
    cards: all(l0, "[data-card]", "data-card"),
    steps: all(l0, "[data-step]", "data-step-kind"),
    proof: attr(l0, "[data-proof]", "data-proof"),
    not_proven: (l0.querySelector('[data-surface="not-proven"]') || {}).textContent || "",
    l1: l1.textContent,
    records: l1.querySelectorAll("[data-inventory-record]").length,
    l2_rungs: all(l2, "[data-rung]", "data-rung"),
    upper_rungs: l0.querySelectorAll("[data-rung]").length + l1.querySelectorAll("[data-rung]").length,
    l2: l2.textContent,
    errors: window.__renderErrors || [],
  };
})()`, &got)
	assert.Equal(t, []string{"header", "top", "cards", "stamps", "steps", "else", "proof", "check", "not-proven"}, got.Surfaces)
	assert.Contains(t, got.Header, "Sale receipt")
	assert.Contains(t, got.Header, "Private: your copy")
	assert.Contains(t, got.Header, "Download proof file")
	assert.Equal(t, "settled", got.TopLine)
	assert.Equal(t, []string{"done"}, got.Seals)
	assert.Equal(t, []string{"paid", "status", "other"}, got.Cards)
	// The copy carries the agreement and the buyer's payer leg in its book,
	// and the seller's payee leg in book.relied_on: the receipt model draws
	// each once (the receipt view-model note, section 2, steps[]).
	assert.Equal(t, []string{"agree", "settle", "settle"}, got.Steps, "one step per record the copy carries")
	assert.Equal(t, "passed", got.Proof)
	assert.Contains(t, got.NotProven, "What this does not prove")
	// For checkers: every sealed record once, and the producer's lines.
	assert.Contains(t, got.L1, "For checkers: every sealed record")
	assert.Equal(t, 3, got.Records)
	assert.Contains(t, got.L1, "Producer version")
	assert.Contains(t, got.L1, "Profile checked against")
	// Technical details: the ladder, and nowhere above it.
	assert.Contains(t, got.L2, "Technical details")
	// The witness line is the page core's, not a rung of the module's ladder
	// (capsule-viewer#489).
	assert.Equal(t, []string{"counterparty-half", "countersign"}, got.L2Rungs)
	assert.Zero(t, got.UpperRung)
	assert.Empty(t, got.Errors)
	// The item comes from the offer the copy itself carries (book.offer,
	// evidencebook#1065), sourced into the sent pair's agent_input.
	var item []string
	page.eval(t, `(() => { const e = document.querySelector("[data-hero-item]"); return e ? [e.textContent, [...e.querySelectorAll("[data-ptr]")].map((n) => n.getAttribute("data-ptr")).join(",")] : []; })()`, &item)
	require.Len(t, item, 2, "the page draws the item")
	assert.Contains(t, item[0], "lamp")
	assert.Contains(t, item[1], "/agent_input/body/terms/item")
}
