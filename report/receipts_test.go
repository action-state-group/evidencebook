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

	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/settlement-receipts holds a Result whose claims are d1:: and d2::
// (both cite the holder's response record) and a receipt bundle rooted at that
// record; the root package's TestSettlementReceiptsFixture builds both
// (evidencebook#764).
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

func settlementView(t *testing.T, deals ...Receipt) View {
	t.Helper()
	return View{Card: "settlement", ContractReport: true, Receipts: &Receipts{Module: receiptModule(t, ReceiptModuleID), Deals: deals}}
}

func carried(t *testing.T, deal string) Receipt {
	t.Helper()
	return Receipt{Deal: deal, Bundle: string(mustRead(t, settlementReceipt))}
}

func TestACarriedReceiptReachesThePageAndAHeldOneDoesNot(t *testing.T) {
	bundle := mustRead(t, settlementBundle)
	page, err := Build(bundle, settlementView(t, carried(t, "d1"), Receipt{Deal: "d2", Held: true}))
	require.NoError(t, err)
	html := string(page.HTML)
	assert.Contains(t, html, "receipt module stand-in", "the page carries the receipt module")
	style := sha256.Sum256([]byte(`.rcpt{}`))
	assert.Contains(t, html, "'sha256-"+base64.StdEncoding.EncodeToString(style[:])+"'", "the module's stylesheet pin reaches the CSP")
	// The receipts setting: d1 carries the bundle text, d2 is null (held).
	settings := optionsOf(t, html)
	receipts, ok := settings["receipts"].(map[string]any)
	require.True(t, ok, "the page carries the receipts setting")
	assert.Equal(t, string(mustRead(t, settlementReceipt)), receipts["d1"])
	held, present := receipts["d2"]
	assert.True(t, present)
	assert.Nil(t, held, "a deal held by policy is a null entry, never a missing one")
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
	good := mustRead(t, settlementReceipt)
	refused := func(view View, want string) {
		t.Helper()
		page, err := Build(bundle, view)
		assert.Nil(t, page)
		assert.ErrorIs(t, err, ErrRefused)
		assert.ErrorContains(t, err, want)
	}
	held := func(deal string) Receipt { return Receipt{Deal: deal, Held: true} }

	refused(settlementView(t, carried(t, "d1")), `deal "d2" has no entry`)
	refused(settlementView(t, carried(t, "d1"), held("d2"), held("d3")), `"d3" is not a deal`)
	refused(settlementView(t, carried(t, "d1"), carried(t, "d1"), held("d2")), `deal "d1" has more than one entry`)
	refused(settlementView(t, carried(t, "d1"), held("d1"), held("d2")), `deal "d1" has more than one entry`)
	refused(settlementView(t, held("d1"), carried(t, "d1"), held("d2")), `deal "d1" has more than one entry`)
	refused(settlementView(t, Receipt{Deal: "d1"}, held("d2")), `either carry its receipt bundle or be held`)
	refused(settlementView(t, Receipt{Deal: "d1", Bundle: string(good), Held: true}, held("d2")), `either carry its receipt bundle or be held`)
	refused(settlementView(t), `has no entry`)
	refused(settlementView(t, Receipt{Deal: "d1", Bundle: "not json"}, held("d2")), "does not decode")
	tampered := strings.Replace(string(good), `"root":"`, `"root":"0`, 1)
	refused(settlementView(t, Receipt{Deal: "d1", Bundle: tampered}, held("d2")), "does not pass the page gate")

	view := settlementView(t, carried(t, "d1"), held("d2"))
	view.Receipts.Module = receiptModule(t, "capsulectl.deal-view-composed/v0")
	refused(view, "the module must carry the manifest of")
	view.Receipts.Module = receiptModule(t, "not.a.receipt.module/v0")
	refused(view, "the module must carry the manifest of")
	view.Receipts.Module = emitter.Module{Code: []byte("x"), SHA256: "00"}
	refused(view, "the module must carry the manifest of")
	view = settlementView(t, carried(t, "d1"), held("d2"))
	view.Diagnostic = true
	refused(view, "a diagnostic page carries none")
}

func TestAReceiptFiledUnderAnotherDealIsRefused(t *testing.T) {
	var root struct {
		Root string `json:"root"`
	}
	require.NoError(t, json.Unmarshal(mustRead(t, settlementReceipt), &root))
	// d1's claims cite the receipt's root; a Result whose d1 cites something
	// else is the "wrong deal" case: the receipt's root is not among d1's
	// evidence.
	module := moduleManifest{ID: ReceiptModuleID}
	err := checkReceiptBundle(Receipt{Deal: "d1", Bundle: string(mustRead(t, settlementReceipt))}, map[string]bool{"somebody-else's-capsule": true}, module)
	assert.ErrorContains(t, err, "which no claim of that deal cites as evidence")
	require.NoError(t, checkReceiptBundle(Receipt{Deal: "d1", Bundle: string(mustRead(t, settlementReceipt))}, map[string]bool{root.Root: true}, module))

	// A VALID bundle the module's manifest does not take is refused too: the
	// page could not open it.
	var deal moduleManifest
	deal.ID = ReceiptModuleID
	deal.Requires.Extensions.Required = []string{"x-deal-v0"}
	err = checkReceiptBundle(Receipt{Deal: "d1", Bundle: string(mustRead(t, settlementReceipt))}, map[string]bool{root.Root: true}, deal)
	assert.ErrorContains(t, err, "not one the "+ReceiptModuleID+" module draws")
}

func TestAPageWithReceiptsGetsNoPermalink(t *testing.T) {
	bundle := mustRead(t, settlementBundle)
	for name, receipts := range map[string][]Receipt{
		"carried":   {carried(t, "d1"), {Deal: "d2", Held: true}},
		"held only": {{Deal: "d1", Held: true}, {Deal: "d2", Held: true}},
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
	assert.True(t, m.accepts(map[string]interface{}{"x-deal-v0": 1}, nil))
	assert.False(t, m.accepts(nil, nil), "a required extension is missing")
	assert.False(t, m.accepts(map[string]interface{}{"x-deal-v0": 1, "composed/v1": 1}, nil), "a forbidden extension is present")
	assert.True(t, moduleManifest{}.accepts(nil, nil), "a manifest with no requires takes any bundle")
}

func TestAModuleManifestForbidsTheProfilesOfAResultRoot(t *testing.T) {
	var m moduleManifest
	require.NoError(t, json.Unmarshal([]byte(`{"id":"x","forbids":{"profiles":["spec_version:report/v1","result_version:evidence-result-v0"]}}`), &m))
	bundleWith := func(input map[string]any) map[string]any {
		return map[string]any{"root": "r", "disclosures": map[string]any{"r": map[string]any{"agent_input": input}}}
	}
	assert.True(t, m.accepts(nil, profilesOf(bundleWith(map[string]any{"deal": "x"}))))
	assert.False(t, m.accepts(nil, profilesOf(bundleWith(map[string]any{"spec_version": "report/v1"}))))
	assert.False(t, m.accepts(nil, profilesOf(bundleWith(map[string]any{"result_version": "evidence-result-v0"}))))
	assert.False(t, m.accepts(nil, profilesOf(bundleWith(map[string]any{"statement": map[string]any{"result_version": "evidence-result-v0"}}))), "a book-form Result root")
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
