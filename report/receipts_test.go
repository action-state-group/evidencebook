// SPDX-License-Identifier: Apache-2.0

package report

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/settlement-receipts holds a Result whose claims are d1:: (and, in
// shared-bundle.json, d2::), all citing the holder's response record, and a
// receipt bundle rooted at that record; the root package's
// TestSettlementReceiptsFixture builds them (evidencebook#764).
const (
	settlementBundle  = "testdata/settlement-receipts/bundle.json"
	settlementReceipt = "testdata/settlement-receipts/receipt.json"
)

// receiptModule is a stand-in for the deal view: a script with a manifest
// that names a receipt module and pins the script and one stylesheet, as the
// real one's does (capsulectl owns the real one).
func receiptModule(t *testing.T, id string) emitter.Module {
	t.Helper()
	code := []byte(`/* receipt module stand-in */`)
	script := sha256.Sum256(code)
	style := sha256.Sum256([]byte(`.rcpt{}`))
	manifest, err := json.Marshal(map[string]any{
		"spec_version": "aac.presentation-manifest/v0", "id": id,
		"executable": map[string]any{"carrier": "module-slot", "script_sha256": hex.EncodeToString(script[:]), "style_sha256": []string{hex.EncodeToString(style[:])}},
	})
	require.NoError(t, err)
	return emitter.Module{Code: code, SHA256: hex.EncodeToString(script[:]), StyleSHA256: []string{hex.EncodeToString(style[:])}, Manifest: manifest}
}

func settlementView(t *testing.T, receipts ...Receipt) View {
	t.Helper()
	return View{Card: "settlement", ContractReport: true, Receipts: &Receipts{Module: receiptModule(t, ReceiptModuleID), Deals: receipts}}
}

// exchange is the capsule the fixture's claims cite and its receipt is rooted
// at: the exchange capsule id a node serves the receipt by.
func exchange(t *testing.T) string {
	t.Helper()
	var root struct {
		Root string `json:"root"`
	}
	require.NoError(t, json.Unmarshal(mustRead(t, settlementReceipt), &root))
	return root.Root
}

func carried(t *testing.T) Receipt {
	t.Helper()
	return Receipt{Exchange: exchange(t), Bundle: string(mustRead(t, settlementReceipt))}
}

func TestACarriedReceiptReachesThePageAndAHeldOneDoesNot(t *testing.T) {
	bundle := mustRead(t, settlementBundle)
	page, err := Build(bundle, settlementView(t, carried(t)))
	require.NoError(t, err)
	html := string(page.HTML)
	assert.Contains(t, html, "receipt module stand-in", "the page carries the receipt module")
	style := sha256.Sum256([]byte(`.rcpt{}`))
	assert.Contains(t, html, "'sha256-"+base64.StdEncoding.EncodeToString(style[:])+"'", "the module's stylesheet pin reaches the CSP")
	// The receipts setting is keyed by the deal the claims name: d1 carries the bundle text.
	receipts, ok := optionsOf(t, html)["receipts"].(map[string]any)
	require.True(t, ok, "the page carries the receipts setting")
	assert.Equal(t, map[string]any{"d1": string(mustRead(t, settlementReceipt))}, receipts)

	page, err = Build(bundle, settlementView(t, Receipt{Exchange: exchange(t), Held: true}))
	require.NoError(t, err)
	receipts, ok = optionsOf(t, string(page.HTML))["receipts"].(map[string]any)
	require.True(t, ok)
	held, present := receipts["d1"]
	assert.True(t, present)
	assert.Nil(t, held, "a deal held by policy is a null entry, never a missing one")
}

func TestANotClosedDealIsAnEntryApartFromHeldAndMissing(t *testing.T) {
	bundle := mustRead(t, settlementBundle)
	page, err := Build(bundle, settlementView(t, Receipt{Exchange: exchange(t), NotClosed: true}))
	require.NoError(t, err)
	assert.Empty(t, page.IgnoredReceipts)
	receipts, ok := optionsOf(t, string(page.HTML))["receipts"].(map[string]any)
	require.True(t, ok, "the page carries the receipts setting")
	got, present := receipts["d1"]
	assert.True(t, present, "a deal not closed is an entry, never a missing one (missing reads as built wrong)")
	assert.Equal(t, map[string]any{"state": "not-closed"}, got, "capsule-viewer go v0.23.0's value for a deal that had not closed: neither held's null nor a bundle")

	// Exactly one state per entry.
	for name, r := range map[string]Receipt{
		"with a bundle": {Exchange: exchange(t), Bundle: string(mustRead(t, settlementReceipt)), NotClosed: true},
		"with held":     {Exchange: exchange(t), Held: true, NotClosed: true},
	} {
		_, err := Build(bundle, settlementView(t, r))
		assert.ErrorIs(t, err, ErrRefused, name)
		assert.ErrorContains(t, err, "exactly one of them", name)
	}
}

func TestAReceiptNoDealCitesIsIgnored(t *testing.T) {
	// Neither a held entry nor a bundle (not even a bad one) for an exchange
	// the report's deals do not cite reaches the page or refuses the build.
	view := settlementView(t, Receipt{Exchange: "an-exchange-out-of-the-window", Held: true}, Receipt{Exchange: "another", Bundle: "not json"})
	page, err := Build(mustRead(t, settlementBundle), view)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, optionsOf(t, string(page.HTML))["receipts"])
	assert.NotContains(t, string(page.HTML), "an-exchange-out-of-the-window")
	assert.Equal(t, []string{"an-exchange-out-of-the-window", "another"}, page.IgnoredReceipts, "the caller is told which receipts reached no deal")

	// A matched receipt is carried and not reported as ignored; an empty id is ignored and reported.
	page, err = Build(mustRead(t, settlementBundle), settlementView(t, carried(t), Receipt{Exchange: "", Held: true}))
	require.NoError(t, err)
	assert.Equal(t, []string{""}, page.IgnoredReceipts)
	assert.Contains(t, optionsOf(t, string(page.HTML))["receipts"], "d1")
}

func TestADealNoReceiptNamesIsNotCarried(t *testing.T) {
	page, err := Build(mustRead(t, settlementBundle), settlementView(t))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, optionsOf(t, string(page.HTML))["receipts"], "an empty receipts setting: every deal reads not carried, none held")
}

func TestNoReceiptsLeavesThePageUnchanged(t *testing.T) {
	bundle := mustRead(t, settlementBundle)
	plain, err := Build(bundle, View{Card: "settlement", ContractReport: true})
	require.NoError(t, err)
	assert.Contains(t, string(plain.HTML), `renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"))`, "no options: the default bootstrap")
	assert.NotContains(t, string(plain.HTML), "receipt module stand-in")
	empty, err := Build(bundle, View{Card: "settlement", ContractReport: true, Receipts: nil})
	require.NoError(t, err)
	assert.Equal(t, string(plain.HTML), string(empty.HTML))
}

func TestReceiptsAreRefusedBeforeAnythingIsRendered(t *testing.T) {
	bundle := mustRead(t, settlementBundle)
	refusedBy := func(bundle []byte, view View, want string) {
		t.Helper()
		page, err := Build(bundle, view)
		assert.Nil(t, page)
		assert.ErrorIs(t, err, ErrRefused)
		assert.ErrorContains(t, err, want)
	}
	refused := func(view View, want string) { t.Helper(); refusedBy(bundle, view, want) }
	good := string(mustRead(t, settlementReceipt))
	held := Receipt{Exchange: exchange(t), Held: true}

	refused(settlementView(t, carried(t), carried(t)), `deal "d1" has more than one receipt`)
	refused(settlementView(t, carried(t), held), `deal "d1" has more than one receipt`)
	refused(settlementView(t, held, carried(t)), `deal "d1" has more than one receipt`)
	refused(settlementView(t, Receipt{Exchange: exchange(t)}), "exactly one of them")
	refused(settlementView(t, Receipt{Exchange: exchange(t), Bundle: good, Held: true}), "exactly one of them")
	refused(settlementView(t, Receipt{Exchange: exchange(t), Bundle: "not json"}), "does not decode")
	tampered := strings.Replace(good, `"root":"`, `"root":"0`, 1)
	refused(settlementView(t, Receipt{Exchange: exchange(t), Bundle: tampered}), "does not pass the page gate")
	// The claims of two deals cite the exchange capsule: its deal is ambiguous.
	refusedBy(mustRead(t, "testdata/settlement-receipts/shared-bundle.json"), settlementView(t, carried(t)), "more than one deal cite")

	view := settlementView(t, carried(t))
	view.Receipts.Module = receiptModule(t, "capsulectl.deal-view-composed/v0")
	refused(view, "the module must carry the manifest of")
	view.Receipts.Module = receiptModule(t, "not.a.receipt.module/v0")
	refused(view, "the module must carry the manifest of")
	view.Receipts.Module = emitter.Module{Code: []byte("x"), SHA256: "00"}
	refused(view, "the module must carry the manifest of")
	view = settlementView(t, carried(t))
	view.Diagnostic = true
	refused(view, "a diagnostic page carries none")
}

func TestAReceiptTheModuleDoesNotTakeIsRefused(t *testing.T) {
	// A VALID bundle the module's manifest does not take could not be opened.
	var deal moduleManifest
	deal.ID = ReceiptModuleID
	deal.Requires.Extensions.Required = []string{"x-deal-v0"}
	err := checkReceiptBundle("d1", carried(t), deal)
	assert.ErrorContains(t, err, "not one the "+ReceiptModuleID+" module draws")
	require.NoError(t, checkReceiptBundle("d1", carried(t), moduleManifest{ID: ReceiptModuleID}))
}

// The caller's Gate runs on each carried receipt, after the page gate, and
// its refusal refuses the receipt; held and not-closed receipts carry no
// bundle and are not handed to it (evidencebook#735).
func TestTheCallersGateChecksEachCarriedReceipt(t *testing.T) {
	claims := []cli.ResultClaim{{ID: "d1::paid", Evidence: []string{"ex1"}}, {ID: "d2::paid", Evidence: []string{"ex2"}}}
	module := receiptModule(t, ReceiptModuleID)
	good := carried(t).Bundle
	var seen []string
	pass := func(b []byte) error { seen = append(seen, string(b)); return nil }
	_, err := checkReceipts(&Receipts{Module: module, Gate: pass, Deals: []Receipt{{Exchange: "ex1", Bundle: good}, {Exchange: "ex2", Held: true}}}, claims)
	require.NoError(t, err)
	assert.Equal(t, []string{good}, seen, "only the carried bundle is checked")
	refuse := func([]byte) error { return cli.InputError("the deal section does not verify") }
	_, err = checkReceipts(&Receipts{Module: module, Gate: refuse, Deals: []Receipt{{Exchange: "ex1", Bundle: good}}}, claims)
	assert.ErrorContains(t, err, "does not pass the caller's check")
	assert.ErrorContains(t, err, "the deal section does not verify")
}

func TestAMultiDealResultMapsEachReceiptToItsOwnDeal(t *testing.T) {
	claims := []cli.ResultClaim{
		{ID: "d1::paid", Evidence: []string{"ex1", "leg1"}}, {ID: "d1::served", Evidence: []string{"ex1"}},
		{ID: "d2::paid", Evidence: []string{"ex2"}}, {ID: "d3::paid", Evidence: []string{"ex3"}},
		{ID: "no-deal", Evidence: []string{"ex4"}},
	}
	module := receiptModule(t, ReceiptModuleID)
	good := string(mustRead(t, settlementReceipt))
	// The stand-in's bundle is used for both: the mapping is what is tested.
	got, err := checkReceipts(&Receipts{Module: module, Deals: []Receipt{{Exchange: "ex1", Bundle: good}, {Exchange: "ex2", Held: true}}}, claims)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"d1": good}, got.texts)
	assert.Equal(t, []string{"d2"}, got.withheld, "d3 has no receipt and is neither")
	got, err = checkReceipts(&Receipts{Module: module, Deals: []Receipt{{Exchange: "ex3", NotClosed: true}, {Exchange: "ex2", NotClosed: true}}}, claims)
	require.NoError(t, err)
	assert.Equal(t, []string{"d2", "d3"}, got.notClosed)
	assert.Empty(t, got.withheld)
	got, err = checkReceipts(&Receipts{Module: module, Deals: []Receipt{{Exchange: "ex4", Held: true}}}, claims)
	require.NoError(t, err, "a claim with no deal cites no exchange capsule, so the receipt is ignored")
	assert.Empty(t, got.withheld)
	_, err = checkReceipts(&Receipts{Module: module, Deals: []Receipt{{Exchange: "ex1", Held: true}, {Exchange: "leg1", Held: true}}}, claims)
	assert.ErrorContains(t, err, `deal "d1" has more than one receipt`, "two capsules of one deal name one deal twice")
}

func TestAPageWithReceiptsGetsNoPermalink(t *testing.T) {
	bundle := mustRead(t, settlementBundle)
	for name, receipts := range map[string][]Receipt{
		"carried":   {carried(t)},
		"held only": {{Exchange: exchange(t), Held: true}},
		"none":      nil,
	} {
		page, err := Build(bundle, settlementView(t, receipts...))
		require.NoError(t, err, name)
		_, err = page.Permalink("")
		assert.ErrorIs(t, err, ErrRefused, name)
		assert.ErrorContains(t, err, "receipts", name)
	}
}

func TestReceiptsOnAResultWithNoDealsAreRefused(t *testing.T) {
	bundle := mustRead(t, "testdata/derived-report/bundle.json")
	view := View{Card: "outcome", ContractReport: true, Receipts: &Receipts{Module: receiptModule(t, ReceiptModuleID)}}
	_, err := Build(bundle, view)
	assert.ErrorIs(t, err, ErrRefused)
	assert.ErrorContains(t, err, "no claim of the Result names a deal")
}

var optionsCall = regexp.MustCompile(`renderEvidenceGraph\(window\.__BUNDLE__, document\.getElementById\("app"\), undefined, (\{.*\})\);`)

// optionsOf is the render options the page passes to renderEvidenceGraph.
func optionsOf(t *testing.T, html string) map[string]any {
	t.Helper()
	m := optionsCall.FindStringSubmatch(html)
	require.NotNil(t, m, "the page passes render options")
	var options map[string]any
	require.NoError(t, json.Unmarshal([]byte(m[1]), &options))
	return options
}

func TestAModuleManifestTakesABundleByItsExtensions(t *testing.T) {
	var m moduleManifest
	require.NoError(t, json.Unmarshal([]byte(`{"id":"x","requires":{"extensions":{"required":["x-deal-v0"]}},"forbids":{"extensions":["composed/v1"]}}`), &m))
	assert.True(t, m.accepts("", map[string]interface{}{"x-deal-v0": 1}, nil))
	assert.False(t, m.accepts("", nil, nil), "a required extension is missing")
	assert.False(t, m.accepts("", map[string]interface{}{"x-deal-v0": 1, "composed/v1": 1}, nil), "a forbidden extension is present")
	assert.True(t, moduleManifest{}.accepts("", nil, nil), "a manifest with no requires takes any bundle")
}

func TestAModuleManifestForbidsTheProfilesOfAResultRoot(t *testing.T) {
	var m moduleManifest
	require.NoError(t, json.Unmarshal([]byte(`{"id":"x","forbids":{"profiles":["spec_version:report/v1","result_version:evidence-result-v0"]}}`), &m))
	bundleWith := func(input map[string]any) map[string]any {
		return map[string]any{"root": "r", "disclosures": map[string]any{"r": map[string]any{"agent_input": input}}}
	}
	assert.True(t, m.accepts("", nil, profilesOf(bundleWith(map[string]any{"deal": "x"}))))
	assert.False(t, m.accepts("", nil, profilesOf(bundleWith(map[string]any{"spec_version": "report/v1"}))))
	assert.False(t, m.accepts("", nil, profilesOf(bundleWith(map[string]any{"result_version": "evidence-result-v0"}))))
	assert.False(t, m.accepts("", nil, profilesOf(bundleWith(map[string]any{"record_type": "evidence_result", "statement": map[string]any{}}))), "a book-form Result root, whatever its statement holds")
	assert.False(t, m.accepts("", nil, profilesOf(map[string]any{"root": "r", "disclosures": map[string]any{"r": map[string]any{"agent_output": map[string]any{"result_version": "evidence-result-v0"}}}})), "a Result in agent_output")
	assert.Empty(t, profilesOf(map[string]any{}), "a bundle with no disclosed root declares no profile")
}

func TestProfilesOfReadsAResultRootAsTheBuilderGetsIt(t *testing.T) {
	var bundle map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, settlementBundle), &bundle))
	assert.Equal(t, map[string]bool{"result_version:evidence-result-v0": true}, profilesOf(bundle), "the settlement Result's root, in book form, declares its result version")
	var receipt map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, settlementReceipt), &receipt))
	assert.Empty(t, profilesOf(receipt), "a receipt rooted at a response record declares no profile")
}

func TestAModuleManifestRequiresItsBundleKind(t *testing.T) {
	var m moduleManifest
	require.NoError(t, json.Unmarshal([]byte(`{"id":"x","requires":{"bundle_kind":"evidence-bundle/v2"}}`), &m))
	assert.True(t, m.accepts("evidence-bundle/v2", nil, nil))
	assert.False(t, m.accepts("evidence-bundle/v1", nil, nil))
	assert.False(t, m.accepts("", nil, nil))
}
