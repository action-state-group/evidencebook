package deal

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/receipt"
	"github.com/action-state-group/evidencebook/capsulectl/internal/deal/assets"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The deal view is pinned by digest like the runtime it runs beside.
func TestDealViewIsPinned(t *testing.T) {
	sum := sha256.Sum256([]byte(DealViewJS))
	assert.Equal(t, strings.TrimSpace(DealViewJSSHA256), hex.EncodeToString(sum[:]), "update assets/deal-view.js.sha256 with the file")
}

func cspSource(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

var (
	pageCSP     = regexp.MustCompile(`<meta http-equiv="Content-Security-Policy" content="([^"]*)"`)
	pageScripts = regexp.MustCompile(`(?s)<script>(.*?)</script>`)
	pageStyles  = regexp.MustCompile(`(?s)<style>(.*?)</style>`)
)

// A deal page is built through the emitter's slots: its CSP pins every
// inline script and style the page holds and allows no network,
// capsule-viewer's two x-deal-v0 receipt modules run as digest-pinned
// modules (#869), and nothing is spliced in.
func TestTheDealPageIsBuiltThroughTheEmittersSlots(t *testing.T) {
	dealFixture(t)
	id := retailDeal(t)
	path := filepath.Join(t.TempDir(), "page.html")
	dealRun(t, "report", "--deal", id, "--html", path)
	page := string(mustRead(t, path))

	m := pageCSP.FindStringSubmatch(page)
	require.NotNil(t, m, "the page carries a CSP")
	directives := map[string]string{}
	for _, d := range strings.Split(m[1], ";") {
		d = strings.TrimSpace(d)
		name, value, _ := strings.Cut(d, " ")
		directives[name] = value
	}
	assert.Equal(t, "'none'", directives["default-src"])
	assert.Equal(t, "'none'", directives["connect-src"], "no network")
	scripts := pageScripts.FindAllStringSubmatch(page, -1)
	require.NotEmpty(t, scripts)
	for _, s := range scripts {
		assert.Contains(t, directives["script-src"], cspSource(s[1]), "every inline script is pinned: %.60s", s[1])
	}
	for _, s := range pageStyles.FindAllStringSubmatch(page, -1) {
		assert.Contains(t, directives["style-src"], cspSource(s[1]), "every inline style is pinned")
	}

	var codes []string
	for _, kind := range []string{receipt.Unilateral, receipt.Seller} {
		m, err := receipt.Module(kind)
		require.NoError(t, err)
		codes = append(codes, string(m.Code))
	}
	module := 0
	readers := 0
	for _, s := range scripts {
		if slices.Contains(codes, s[1]) {
			module++
		}
		assert.NotEqual(t, DealViewJS, s[1], "the deal view draws no single copy")
		if strings.Contains(s[1], "__BUNDLE__") {
			readers++
			assert.True(t, strings.HasPrefix(s[1], "window.__BUNDLE__ = ") || strings.Contains(s[1], "renderEvidenceGraph(window.__BUNDLE__,"),
				"only the bundle slot and the bootstrap name the bundle: %.80s", s[1])
		}
	}
	assert.Equal(t, 2, module, "each receipt module once")
	assert.Equal(t, 2, readers, "the bundle slot sets it, the bootstrap reads it")
	for _, code := range codes {
		assert.Contains(t, directives["script-src"], cspSource(code))
	}
	assert.NotContains(t, page, `id="deal-countersign"`, "nothing beside the emitter's slots")
	assert.Regexp(t, `<title>Receipt report: “[^<]+”</title>`, page, "the user's own copy is titled with the words they asked")
}

// The deal view never verifies, never reads the bundle itself and never
// recomputes a commitment: it reads the verified context it is handed.
func TestTheDealViewReadsOnlyTheVerifiedContext(t *testing.T) {
	for _, forbidden := range []string{"__BUNDLE__", "verifyBundle", "crypto.subtle", "bundle.disclosures", "bundle.records"} {
		assert.NotContains(t, DealViewJS, forbidden)
	}
	assert.Contains(t, DealViewJS, "EvidenceGraph.verifiedPayload(context")
}

type pageOpenings struct {
	Asked           bool   `json:"asked"`
	Representations []bool `json:"representations"`
}

func pageOpeningsOf(t *testing.T, path string) pageOpenings {
	t.Helper()
	var data struct {
		Openings pageOpenings `json:"openings"`
	}
	require.NoError(t, json.Unmarshal(pageData(t, mustRead(t, path)), &data))
	return data.Openings
}

func sellerWithRepresentations(t *testing.T) string {
	t.Helper()
	dealFixture(t)
	id := openTyped(t, sellerTyped)
	for _, body := range []string{
		`{"text": "The frame has one small scratch by the seat", "source_kind": "agent", "class": "condition"}`,
		`{"text": "Returns accepted within 7 days if unridden", "source_kind": "agent", "class": "refund_terms"}`,
		`{"text": "I paid 2400 for it new", "source_kind": "user"}`,
	} {
		dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t, body))
	}
	return id
}

// A page whose words do not recompute to their sealed commitment is never
// written, and the refusal names the step, never the words.
func TestADealPageWhoseWordsDoNotMatchIsNotWritten(t *testing.T) {
	id := sellerWithRepresentations(t)
	path := filepath.Join(t.TempDir(), "bundle.json")
	dealRun(t, "report", "--deal", id, "--bundle", path)
	fresh := func() map[string]interface{} {
		b, err := cli.DecodeBundleJSON(mustRead(t, path))
		require.NoError(t, err)
		return b
	}
	report := func(b map[string]interface{}) map[string]interface{} {
		r, _, _ := DealSealedReport(b)
		require.NotNil(t, r)
		return r
	}
	_, err := dealPageOpenings(fresh())
	require.NoError(t, err, "the deal as sealed")

	asked := fresh()
	report(asked)["asked_opening"].(map[string]interface{})["text"] = "Sell it for 100"
	_, err = dealPageOpenings(asked)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the words you asked do not match")
	assert.NotContains(t, err.Error(), "Sell it for 100")

	said := fresh()
	report(said)["representations"].([]interface{})[0].(map[string]interface{})["text"] = "No scratches at all"
	_, err = dealPageOpenings(said)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a statement the agent made does not match")
	assert.NotContains(t, err.Error(), "No scratches at all")
}

// The deal view is a presentation module: a schema-valid manifest that pins
// its script and its one stylesheet, registered by the script itself at
// load with the same manifest (but for the script's own digest, which a
// script cannot carry), and its stylesheet is the committed file's bytes.
func TestTheDealViewIsAPresentationModule(t *testing.T) {
	schema, err := cli.PresentationSchema("manifest")
	require.NoError(t, err)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(DealViewComposedManifestJSON))
	require.NoError(t, err)
	require.NoError(t, schema.Validate(doc))

	var manifest map[string]interface{}
	require.NoError(t, json.Unmarshal(DealViewComposedManifestJSON, &manifest))
	assert.Equal(t, "capsulectl.deal-view-composed/v0", manifest["id"])
	css := mustRead(t, "assets/deal-view.css")
	executable := manifest["executable"].(map[string]interface{})
	assert.Equal(t, "module-slot", executable["carrier"])
	assert.Equal(t, strings.TrimSpace(DealViewJSSHA256), executable["script_sha256"])
	assert.Equal(t, []interface{}{hexSHA256(css)}, executable["style_sha256"])

	m := regexp.MustCompile("(?s)const DEAL_VIEW_CSS = `(.*?)`;").FindStringSubmatch(DealViewJS)
	require.NotNil(t, m, "the script carries its stylesheet")
	assert.Equal(t, string(css), m[1], "assets/deal-view.css and the script's stylesheet are the same bytes")

	// The script registers its one module, the composed one, with the same
	// manifest but for the script's own digest; a single copy's receipt is
	// capsule-viewer's (#869), so no module of its matches a single copy.
	codes := regexp.MustCompile(`EvidenceGraph\.registerPresentation\(\{\n  manifest: (\{.*\}),\n`).FindAllStringSubmatch(DealViewJS, -1)
	require.Len(t, codes, 1, "the script registers its one module at load")
	assert.Equal(t, 1, strings.Count(DealViewJS, "registerPresentation("))
	assert.Equal(t, strings.Repeat("0", 64), regexp.MustCompile(`"script_sha256": "([0-9a-f]{64})"`).FindStringSubmatch(codes[0][1])[1])
	var inCode map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(codes[0][1]), &inCode))
	inCode["executable"].(map[string]interface{})["script_sha256"] = strings.TrimSpace(DealViewJSSHA256)
	assert.Equal(t, manifest, inCode)
	assert.Equal(t, []interface{}{"composed/v1"}, cli.AsList(cli.ManifestMap(cli.ManifestMap(manifest, "requires"), "extensions")["required"]))
	assert.NotContains(t, DealViewJS, "capsulectlDealView =", "no entry point beside the module")

	module, err := dealViewModule()
	require.NoError(t, err)
	assert.Equal(t, []string{hexSHA256(css)}, module.StyleSHA256)
}

// The page's theme is tokens only; the receipt's layout is the receipt
// modules' own pinned stylesheets, and the page's style-src lists them.
func TestTheDealPageThemeIsTokensOnly(t *testing.T) {
	for _, line := range strings.Split(strings.TrimSpace(dealThemeCSS), "\n") {
		assert.Regexp(t, `^(@media \(prefers-color-scheme: dark\) \{ )?:root \{( --aac-[a-z-]+: [^;{}]+;)+ \}( \})?$`, line)
	}
	dealFixture(t)
	id := retailDeal(t)
	path := filepath.Join(t.TempDir(), "page.html")
	dealRun(t, "report", "--deal", id, "--html", path)
	page := string(mustRead(t, path))
	for _, s := range pageStyles.FindAllStringSubmatch(page, -1) {
		assert.NotContains(t, s[1], "#deal", "the deal view's rules are not in the page's own styles")
	}
	styles, err := receipt.Styles(receipt.Unilateral)
	require.NoError(t, err)
	for _, style := range styles {
		assert.Contains(t, pageCSP.FindStringSubmatch(page)[1], cspSource(string(style)))
	}
}

// A deal view the page's runtime would refuse, one ambiguous with a
// built-in, or one whose manifest does not pin its script is refused: the
// composed page, which still draws with it until EB2b (#869), is not
// written.
func TestADealViewThePageWouldRefuseWritesNoPage(t *testing.T) {
	for name, edit := range map[string]func(m map[string]interface{}){
		"runtime":   func(m map[string]interface{}) { m["runtime_min"] = "0.2.0" },
		"api":       func(m map[string]interface{}) { m["presentation_api"] = "aac.presentation-api/v1" },
		"ambiguous": func(m map[string]interface{}) { delete(m, "forbids") },
		"unpinned": func(m map[string]interface{}) {
			m["executable"].(map[string]interface{})["script_sha256"] = strings.Repeat("0", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var copied map[string]interface{}
			require.NoError(t, json.Unmarshal(DealViewComposedManifestJSON, &copied))
			edit(copied)
			raw, err := json.Marshal(copied)
			require.NoError(t, err)
			// The module is built from the assets package's manifest.
			old := assets.ComposedManifestJSON
			assets.ComposedManifestJSON = raw
			t.Cleanup(func() { assets.ComposedManifestJSON = old })
			_, err = dealViewModule()
			require.Error(t, err)
		})
	}
}

// The page's title, what a tab or a host's preview shows before any script
// runs, says only what that copy already shows in the clear: the user's own
// copy quotes the words they asked (checked when the page was built); a
// shared copy, from which those words are withheld, says only whom it is
// for.
func TestTheDealPageTitleSaysOnlyWhatTheCopyShows(t *testing.T) {
	dealFixture(t)
	id := retailDeal(t)
	asked := "Order the cat sticker in my cart at Example Stickers"
	title := func(page string) string {
		m := regexp.MustCompile(`<title>([^<]*)</title>`).FindStringSubmatch(page)
		require.NotNil(t, m)
		return m[1]
	}
	dir := t.TempDir()
	own := filepath.Join(dir, "own.html")
	dealRun(t, "report", "--deal", id, "--html", own)
	assert.Equal(t, "Receipt report: “"+asked+"”", title(string(mustRead(t, own))))
	for audience, want := range map[string]string{
		"counterparty": "Receipt report: a shared copy for the other party",
		"adjudicator":  "Receipt report: a shared copy for an adjudicator",
	} {
		path := filepath.Join(dir, audience+".html")
		dealRun(t, "report", "--deal", id, "--html", path, "--share", audience, "--to", "the shop's support desk")
		assert.Equal(t, want, title(string(mustRead(t, path))), audience)
	}

	report := func(audience string, text string) map[string]interface{} {
		return map[string]interface{}{"extensions": map[string]interface{}{dealprofile.Profile: map[string]interface{}{
			"audience": audience, "asked_opening": map[string]interface{}{"nonce": "n", "text": text}}}}
	}
	assert.Equal(t, "Receipt report", dealPageTitle(report("keep", asked), map[string]interface{}{"asked": false}), "words not checked when the page was built are not quoted")
	assert.Equal(t, "Receipt report: “a b”", dealPageTitle(report("keep", " a\n\tb "), map[string]interface{}{"asked": true}))
	assert.Equal(t, "Receipt report: “Order the [31msticker”", dealPageTitle(report("keep", "Order\x00 the \x1b[31msticker\u202e"), map[string]interface{}{"asked": true}),
		"NUL, the escape character and a bidi override are dropped (what is left of the escape sequence is plain text)")
	assert.Equal(t, "Receipt report", dealPageTitle(report("keep", "\x00\u202e\u200b"), map[string]interface{}{"asked": true}), "nothing left to quote")
	long := dealPageTitle(report("keep", strings.Repeat("x", 500)), map[string]interface{}{"asked": true})
	assert.Equal(t, "Receipt report: “"+strings.Repeat("x", dealPageTitleWords-1)+"…”", long)
	assert.Equal(t, "Receipt report: a shared copy for the other party", dealPageTitle(report(cli.DealAudienceCounterparty, asked), map[string]interface{}{"asked": true}), "a shared copy never quotes them")
}
