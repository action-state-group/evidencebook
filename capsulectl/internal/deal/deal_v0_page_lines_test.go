// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capsulectl's x-deal-v0 pages draw with capsule-viewer's receipt modules
// (#869, EB2a). Each line Sherlock2 kept of the deal view's
// (capsule-viewer#383, comment 7176241436: lines 1, 2, 3, 4, 5, 8 and 9) is
// drawn by the viewer from what capsulectl's page carries; how each line is
// worded and every in-page check behind it (a tampered approval, a preimage
// that does not match, a withheld step) is the viewer's, tested in
// capsule-viewer's js-tests (receipt_deal_view_lines, receipt_settlement).
// These tests read capsulectl's real pages for the lines its data reaches
// (CAPSULECTL_CHROME).

// The holder's own page: who the deal was with (line 3), each act after
// the holder's yes (line 4), the card payment's line (line 1), the deal id
// (line 8) and "How it went", worded as the report words it. A shared copy
// draws no journey and no counterparty line, and says it is shared.
func TestV0PageDrawsTheHoldersLines(t *testing.T) {
	chrome := chromeOrSkip(t)
	dealFixture(t)
	id, _, _ := fawnDeal(t)
	var labels []string
	for _, s := range dealRun(t, "report", "--deal", id)["journey"].([]any) {
		labels = append(labels, s.(map[string]any)["label"].(string))
	}

	own := reportV0Page(t, chrome, id, "")
	assert.Equal(t, "capsuleviewer.receipt.unilateral/v0", own.Module)
	assert.Equal(t, id, own.DealID)
	assert.Empty(t, own.Shared)
	require.Len(t, own.Rungs["counterparty"], 1)
	assert.Contains(t, own.Rungs["counterparty"][0], "With eBay (ebay.com)")
	assert.Equal(t, "passed", own.AfterYes)
	assert.Equal(t, "missing", own.Settlement, "a card payment carries no settlement record")
	assert.Contains(t, own.Rungs["settlement"][0], "a card payment")
	require.Len(t, own.Journey, len(labels), "one row per move of the sealed journey")
	for i, row := range own.Journey {
		assert.True(t, strings.HasSuffix(row, " · "+labels[i]), "row %d %q words the move as the report does (%q)", i, row, labels[i])
	}
	assert.Contains(t, own.Journey[5], "You · Switched to eBay", "the switch is the user's")

	shared := reportV0Page(t, chrome, id, cli.DealAudienceCounterparty)
	assert.Equal(t, cli.DealAudienceCounterparty, shared.Shared)
	assert.Nil(t, shared.Journey, "a shared copy carries no journey")
	assert.Empty(t, shared.Rungs["counterparty"], "the copy the other side holds does not name them to themselves (u499)")
	assert.Equal(t, "withheld", shared.AfterYes, "steps are withheld from the shared copy")
	assert.Contains(t, shared.Rungs["after-yes"][0], "the holder's yes", "never \"your\" to the other side")
}

// assertPageJourneyIsTheReports checks, when Chrome is there, that the
// holder's page words each move of "How it went" as `deal report` words it.
func assertPageJourneyIsTheReports(t *testing.T, id string) {
	t.Helper()
	if os.Getenv("CAPSULECTL_CHROME") == "" {
		t.Log("CAPSULECTL_CHROME is not set: the page's journey is not read")
		return
	}
	var labels []string
	for _, s := range dealRun(t, "report", "--deal", id)["journey"].([]any) {
		labels = append(labels, s.(map[string]any)["label"].(string))
	}
	rows := reportV0Page(t, chromeOrSkip(t), id, "").Journey
	require.Len(t, rows, len(labels))
	for i, row := range rows {
		assert.True(t, row == labels[i] || strings.HasSuffix(row, " · "+labels[i]), "row %d %q words the move as the report does (%q)", i, row, labels[i])
	}
}

// An adjudicator's copy names the referee its side's sealed report states
// (line 8; evidencebook-deal#107), never "a referee you both chose"; the
// counterparty's copy names none.
func TestV0PageAdjudicatorCopyNamesTheReferee(t *testing.T) {
	chrome := chromeOrSkip(t)
	dealFixture(t)
	id, _, _ := fawnDeal(t)
	adj := reportV0Page(t, chrome, id, cli.DealAudienceAdjudicator)
	assert.Equal(t, cli.DealAudienceAdjudicator, adj.Shared)
	require.Len(t, adj.Rungs["referee"], 1)
	assert.Contains(t, adj.Rungs["referee"][0], "The referee this side named, as its sealed report states it: Lamp Court")
	assert.Contains(t, adj.Rungs["referee"][0], "does not check that the other side named the same one")
	assert.NotContains(t, adj.Text, "both chose")
	assert.Empty(t, reportV0Page(t, chrome, id, cli.DealAudienceCounterparty).Rungs["referee"])
}

// A deal with no act needing approval draws no "after your yes" line.
func TestV0PageWithNoActDrawsNoAfterYesLine(t *testing.T) {
	chrome := chromeOrSkip(t)
	pair := closedPair(t, received, received)
	party(t, pair.buyer.config)
	got := reportV0Page(t, chrome, pair.id, "")
	assert.Empty(t, got.Rungs["after-yes"])
	assert.Empty(t, got.AfterYes)
}

// A Lightning payment the seller received (lines 1 and 2): its rail, its
// proof checked in the page against the invoice, and its size band; with no
// preimage the wallet's word that it is final; a failed payment is no
// settlement to state. The seller's copy draws with the seller module.
// Synthetic preimage (deal_settlement_lightning_test.go).
func TestV0PageStatesALightningSettlementAndItsBand(t *testing.T) {
	chrome := chromeOrSkip(t)
	const band = "Size: 0.000001 to under 0.00001 BTC."

	id, _ := sellerDealWithDueDelivery(t)
	terms, _ := newBuyerSide(t).lnTerms()
	require.NoError(t, noteLightningReceived(t, id, terms, lnInvoice, lnTransaction("succeeded")))
	got := reportV0Page(t, chrome, id, "")
	assert.Equal(t, "capsuleviewer.receipt.seller/v0", got.Module)
	assert.Equal(t, "proof", got.Settlement, "the page's own preimage check passes the sealed preimage")
	assert.Contains(t, got.Rungs["settlement"][0], "settled on Lightning; this page checked its proof against its invoice")
	assert.Contains(t, got.Rungs["settlement"][0], band, "495,000 msat credited plus the 5,000 msat fee is one 500 sat band, in BTC")

	other, _ := sellerDealWithDueDelivery(t)
	terms2, _ := newBuyerSide(t).lnTerms()
	without := strings.Replace(lnTransaction("succeeded"), `,"preimage":"`+lnPreimage+`"`, "", 1)
	require.NoError(t, noteLightningReceived(t, other, terms2, lnInvoice, without))
	final := reportV0Page(t, chrome, other, "")
	assert.Equal(t, "final", final.Settlement)
	assert.Contains(t, final.Rungs["settlement"][0], "this file carries no proof to check")
	assert.Contains(t, final.Rungs["settlement"][0], band)

	failed, _ := sellerDealWithDueDelivery(t)
	terms3, _ := newBuyerSide(t).lnTerms()
	require.NoError(t, noteLightningReceived(t, failed, terms3, lnInvoice, lnTransaction("failed")))
	assert.Empty(t, reportV0Page(t, chrome, failed, "").Rungs["settlement"], "a failed payment is not settled")
}

// A page built from a copy that still carries its witnessed chain, as copies
// did before u225: the copy carries witness evidence, but a witness time
// reaches an x-deal-v0 page only as capsulectl's verify report states it
// (u219), so the page says no witness result was reported, never a witness
// time of its own.
func TestV0PageOfAChainedCopyDrawsNoWitnessTimeOfItsOwn(t *testing.T) {
	chrome := chromeOrSkip(t)
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	dealID := retailDeal(t)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	copyPath := filepath.Join(t.TempDir(), "copy.json")
	dealRun(t, "report", "--deal", dealID, "--bundle", copyPath)
	chained := withHolderChain(t, p, dealID, copyPath)
	page := filepath.Join(t.TempDir(), "page.html")
	dealRun(t, "report", "--deal", dealID, "--from-bundle", chained, "--html", page)
	require.Contains(t, string(mustRead(t, page)), "parent-log-witness/v1", "the page's bundle carries the chain as v1")
	got := readV0Page(t, chrome, page)
	require.Empty(t, got.Errors)
	assert.NotContains(t, got.WitnessNotice, "book-witnessed", "no witness time of its own (capsule-viewer#489 draws it in the core)")
}

// editBootstrapReport rewrites the verify report a page's bootstrap carries
// and the bootstrap's pin in the page's CSP, as a page edited by hand would
// be: the bundle is untouched.
func editBootstrapReport(t *testing.T, page []byte, edit func(report map[string]any)) []byte {
	t.Helper()
	const prefix = "globalThis.capsulectlDealPage = Object.freeze("
	start := bytes.Index(page, []byte(prefix))
	require.GreaterOrEqual(t, start, 0)
	end := start + bytes.Index(page[start:], []byte("</script>"))
	old := string(page[start:end])
	var boot map[string]any
	dec := json.NewDecoder(strings.NewReader(old[len(prefix):]))
	require.NoError(t, dec.Decode(&boot))
	rest := old[len(prefix)+int(dec.InputOffset()):]
	edit(boot["verifier_report"].(map[string]any))
	data, err := json.Marshal(boot)
	require.NoError(t, err)
	updated := prefix + string(data) + rest
	out := bytes.Replace(page, []byte(old), []byte(updated), 1)
	return bytes.Replace(out, []byte(cspSource(old)), []byte(cspSource(updated)), 1)
}

// editBundle rewrites the bundle a page carries in its JSON element (data,
// which the CSP does not pin).
func editBundle(t *testing.T, page []byte, edit func(b map[string]any)) []byte {
	t.Helper()
	html := string(page)
	b := embeddedBundle(t, html)
	m := regexp.MustCompile(`(?s)(<script type="application/json"[^>]*>)(.*?)(</script>)`).FindStringSubmatchIndex(html)
	require.NotNil(t, m)
	edit(b)
	data, err := json.Marshal(b)
	require.NoError(t, err)
	return []byte(html[:m[4]] + string(data) + html[m[5]:])
}

// Line 9 (capsule-viewer#383, CV3b): the countersignatures capsulectl
// resolved reach the page only through the verify report it carries, bound to
// the copy's countersignatures. The page draws each in capsulectl's voice;
// a report bound to other countersignatures, or with its entries reordered,
// is ignored and the page keeps its own line; a bound entry capsulectl found
// invalid refuses the copy.
func TestV0PageDrawsCapsulectlsCountersignatures(t *testing.T) {
	chrome := chromeOrSkip(t)
	dealFixture(t)
	dealID := openJetSki(t)
	_, bundlePath, _, _ := dealReportFiles(t, dealID)
	digest, err := aacbundle.BundleDigest(readBundleFile(t, bundlePath))
	require.NoError(t, err)
	first, signerKey := countersignerEntry(t, digest, []cli.CountersignCheck{{Name: "range membership", Result: "established"}})
	directory := countersignerDirectoryFile(t, "Example Countersigner", signerKey)
	dealRun(t, "countersign", "--deal", dealID, "--bundle", bundlePath, "--entry", writeEntry(t, first), "--directory", directory)
	// A second countersignature by a key the directory does not list.
	second, _ := countersignerEntry(t, digest, nil)
	stored := readBundleFile(t, bundlePath)
	raw, err := json.Marshal(second)
	require.NoError(t, err)
	var secondValue any
	require.NoError(t, json.Unmarshal(raw, &secondValue))
	stored["countersignatures"] = append(stored["countersignatures"].([]any), secondValue)
	data, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bundlePath, data, 0o600))

	_, _, html, _ := dealReportFiles(t, dealID, "--from-bundle", bundlePath, "--directory", directory)
	page := mustRead(t, html)
	got := readV0Page(t, chrome, html)
	require.Empty(t, got.Errors)
	assert.Equal(t, []string{"resolved", "unresolved_signer"}, got.Countersign, "each countersignature as capsulectl resolved it, in order")
	assert.Contains(t, strings.Join(got.Rungs["countersign"], " "), "Example Countersigner")

	write := func(p []byte) string {
		path := filepath.Join(t.TempDir(), "page.html")
		require.NoError(t, os.WriteFile(path, p, 0o600))
		return path
	}
	// A countersignature dropped from the bundle: the report no longer binds.
	dropped := readV0Page(t, chrome, write(editBundle(t, page, func(b map[string]any) {
		b["countersignatures"] = b["countersignatures"].([]any)[:1]
	})))
	assert.Equal(t, "capsuleviewer.receipt.unilateral/v0", dropped.Module)
	assert.Empty(t, dropped.Countersign, "a report bound to other countersignatures is ignored")
	assert.NotEmpty(t, dropped.Rungs["countersign"], "the page keeps its own line")

	entries := func(r map[string]any) []any {
		for _, e := range r["extensions"].([]any) {
			if m := e.(map[string]any); m["kind"] == DealProfile {
				return m["countersign"].(map[string]any)["entries"].([]any)
			}
		}
		t.Fatal("no x-deal-v0 entry")
		return nil
	}
	reordered := readV0Page(t, chrome, write(editBootstrapReport(t, page, func(r map[string]any) {
		e := entries(r)
		e[0], e[1] = e[1], e[0]
	})))
	assert.Equal(t, "capsuleviewer.receipt.unilateral/v0", reordered.Module)
	assert.Empty(t, reordered.Countersign, "entries out of the bundle's order are ignored")

	invalid := readV0Page(t, chrome, write(editBootstrapReport(t, page, func(r map[string]any) {
		entries(r)[1].(map[string]any)["state"] = "invalid"
	})))
	assert.Empty(t, invalid.Module, "a bound invalid entry refuses the copy: no receipt is drawn")
}
