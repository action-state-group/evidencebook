package deal

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildAs makes this test's seals come from a named build.
func buildAs(t *testing.T, version, commit string) {
	t.Helper()
	oldV, oldC := cli.CLIVersion, cli.CLICommit
	cli.CLIVersion, cli.CLICommit = version, commit
	t.Cleanup(func() { cli.CLIVersion, cli.CLICommit = oldV, oldC })
}

// buildAsPlugin makes this test's seals come from a release of this plugin
// (scripts/release-build.sh sets cli.PluginVersion and cli.PluginCommit).
func buildAsPlugin(t *testing.T, version, commit string) {
	t.Helper()
	oldV, oldC := cli.PluginVersion, cli.PluginCommit
	cli.PluginVersion, cli.PluginCommit = version, commit
	t.Cleanup(func() { cli.PluginVersion, cli.PluginCommit = oldV, oldC })
}

func TestDealRecordsNameTheBuildThatSealedThem(t *testing.T) {
	dealFixture(t)
	buildAs(t, "v0.1.0-rc4", "abc1234")
	dealID := retailDeal(t)
	export := filepath.Join(t.TempDir(), "records.json")
	dealRun(t, "export", "--deal", dealID, "--output", export)
	var records []map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, export), &records))
	require.NotEmpty(t, records)
	for _, r := range records {
		assert.Equal(t, map[string]any{"name": "capsulectl", "version": "v0.1.0-rc4", "commit": "abc1234"}, r["x-deal-v0"].(map[string]any)["producer"])
	}

	// An upgrade mid-deal: the old steps still re-derive and verify exactly
	// (each keeps the build it was sealed by), and the new ones name the new
	// build.
	buildAs(t, "v0.1.0-rc5", "def5678")
	closed := dealRun(t, "close", "--deal", dealID, "--input", writeJSON(t, `{"status":"received","delivered":{"item":"cat sticker"}}`))
	assert.Equal(t, "completed", closed["outcome"])
	report := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assert.Equal(t, []any{"capsulectl v0.1.0-rc4 (abc1234)", "capsulectl v0.1.0-rc5 (def5678)"}, report["produced_by"])
	assert.Contains(t, report["assurance"].(map[string]any)["text"], "Produced by capsulectl v0.1.0-rc4 (abc1234), then capsulectl v0.1.0-rc5 (def5678).")
}

func TestDealProducerOfAnUnrecordedStep(t *testing.T) {
	events := []sealedEvent{{Event: dealEvent{}}, {Event: dealEvent{Producer: &dealProducer{Name: "capsulectl", Version: "v0.1.0-rc4", Commit: "abc1234"}}}}
	assert.Equal(t, []string{dealUnrecordedProducer, "capsulectl v0.1.0-rc4 (abc1234)"}, dealProducers(events))
	assert.Equal(t, "capsulectl 0.1.0-dev (unknown)", (&dealProducer{Name: "capsulectl", Version: "0.1.0-dev", Commit: "unknown"}).String(), "a development build says so")
}

// The page carries what it needs to compare builds, and fetches nothing.
func TestDealPageComparesBuildsWithoutFetching(t *testing.T) {
	dealFixture(t)
	buildAs(t, "v0.1.0-rc3", "d3c6a9b")
	dealID := retailDeal(t)
	buildAs(t, "v0.1.0-rc4", "abc1234")
	page := filepath.Join(t.TempDir(), "r.html")
	dealRun(t, "report", "--deal", dealID, "--html", page)
	html := string(mustRead(t, page))
	ext := dealReportOf(embeddedBundle(t, html))
	assert.Equal(t, "v0.1.0-rc4", ext["page_version"])
	assert.Equal(t, []any{"capsulectl v0.1.0-rc3 (d3c6a9b)"}, ext["produced_by"])
	assert.Contains(t, html, "Produced by an older version")
	assert.Contains(t, html, "https://github.com/action-state-group/capsule-cli/releases")
	for _, call := range []string{"fetch(", "XMLHttpRequest", "sendBeacon", "WebSocket", "navigator.connection"} {
		assert.NotContains(t, DealViewJS, call, "the deal view makes no network call")
	}
}

// A counterparty copy keeps the producer on the records it discloses: a
// software version is not a private value.
func TestDealSharedRecordsKeepTheProducer(t *testing.T) {
	dealFixture(t)
	buildAs(t, "v0.1.0-rc4", "abc1234")
	dealID := openPrivateDeal(t)
	page := filepath.Join(t.TempDir(), "receipt.html")
	shareRun(t, dealID, "counterparty", page)
	b := embeddedBundle(t, string(mustRead(t, page)))
	found := 0
	for _, d := range b["disclosures"].(map[string]any) {
		rec, _ := d.(map[string]any)["agent_input"].(map[string]any)
		if rec == nil || rec["type"] == "deal_report" {
			continue
		}
		if p, ok := rec["x-deal-v0"].(map[string]any)["producer"]; ok {
			assert.Equal(t, "v0.1.0-rc4", p.(map[string]any)["version"])
			found++
		}
	}
	assert.Positive(t, found)
	assert.False(t, dealShareKeys["name"], "the producer is allowed in its own shape, not by opening \"name\" everywhere")
}

func TestShareableProducerShapes(t *testing.T) {
	ok := func(name, version, commit string) bool {
		return shareableProducer(map[string]interface{}{"name": name, "version": version, "commit": commit})
	}
	assert.True(t, ok("capsulectl", "v0.1.0-rc4", "d3c6a9bbf4eeaeedef67d1e3fe0d0f53e7164a70"))
	assert.True(t, ok("capsulectl", "0.1.0-dev", "unknown"), "a development build is shareable as such")
	assert.True(t, ok("capsulectl", "v1.2.3", "abc1234"))
	assert.True(t, ok("capsulectl-deal", "v0.1.0", "d3c6a9bbf4eeaeedef67d1e3fe0d0f53e7164a70"), "this plugin's release stamp")
	assert.False(t, ok("capsulectl", "v0.1.0-482913", "abc1234"), "a code cannot ride in the version")
	assert.False(t, ok("capsulectl", "v0.1.0", "jane@example.com"))
	assert.False(t, ok("Jane Doe", "v0.1.0", "abc1234"))
	assert.False(t, shareableProducer(map[string]interface{}{"name": "capsulectl", "version": "v0.1.0", "commit": "abc1234", "note": "x"}))
}

// A release of this plugin seals its own name and tag, not the core it
// embeds (capsule-cli#326 design note section 4.2), and a shared copy keeps
// that stamp.
func TestDealRecordsNameThePluginRelease(t *testing.T) {
	dealFixture(t)
	buildAs(t, "v0.1.0-rc23", "abc1234")
	buildAsPlugin(t, "v0.1.0", "def5678")
	dealID := openPrivateDeal(t)
	export := filepath.Join(t.TempDir(), "records.json")
	dealRun(t, "export", "--deal", dealID, "--output", export)
	var records []map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, export), &records))
	require.NotEmpty(t, records)
	for _, r := range records {
		assert.Equal(t, map[string]any{"name": "capsulectl-deal", "version": "v0.1.0", "commit": "def5678"}, r["x-deal-v0"].(map[string]any)["producer"])
	}

	page := filepath.Join(t.TempDir(), "receipt.html")
	shareRun(t, dealID, "counterparty", page)
	b := embeddedBundle(t, string(mustRead(t, page)))
	found := 0
	for _, d := range b["disclosures"].(map[string]any) {
		rec, _ := d.(map[string]any)["agent_input"].(map[string]any)
		if rec == nil || rec["type"] == "deal_report" {
			continue
		}
		if p, ok := rec["x-deal-v0"].(map[string]any)["producer"]; ok {
			assert.Equal(t, map[string]any{"name": "capsulectl-deal", "version": "v0.1.0", "commit": "def5678"}, p)
			found++
		}
	}
	assert.Positive(t, found, "the shared copy keeps the plugin's stamp")
}

// dealPageBuildsDriver loads a deal page's runtime and deal view under node
// and prints the view's comparison of the builds that sealed the steps with
// the build that made the page.
const dealPageBuildsDriver = `
const fs = require("fs");
const vm = require("vm");
const { webcrypto } = require("crypto");
const html = fs.readFileSync(process.argv[2], "utf8");
const scripts = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
const bundleScript = scripts.find((s) => s.startsWith("window.__BUNDLE__ = "));
const runtime = scripts.find((s) => s.includes("var EvidenceGraph = "));
const bootstrap = scripts.find((s) => s.includes('document.getElementById("app")'));
const context = vm.createContext({ console, TextEncoder, TextDecoder, atob, btoa, URL, setTimeout, clearTimeout, queueMicrotask, crypto: webcrypto });
vm.runInContext("globalThis.window = globalThis;", context);
for (const source of [bundleScript, runtime, ...scripts.filter((s) => s !== bundleScript && s !== runtime && s !== bootstrap)]) vm.runInContext(source, context);
const EG = vm.runInContext("EvidenceGraph", context);
` + verifiedContextJS + `(async () => {
  const ctx = await verifiedContext(context, EG, vm.runInContext("window.__BUNDLE__", context));
  const resolution = await EG.resolvePresentation(ctx);
  const view = await resolution.module.buildModel(ctx);
  process.stdout.write(JSON.stringify({ refusal: view.refusal || "", page: view.pageBuild, older: view.olderBuilds, earlier: view.earlierBuilds, notes: view.releaseNotes }));
})();
`

// The page compares versions only between builds of the same name: a step
// sealed by capsulectl before the move is said apart, as an earlier build and
// not an older capsulectl-deal; a capsulectl-deal step is compared with the
// page's capsulectl-deal tag; and the release-notes link is the plugin's.
func TestDealPageComparesBuildsByName(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	driver := filepath.Join(dir, "driver.js")
	require.NoError(t, os.WriteFile(driver, []byte(dealPageBuildsDriver), 0o600))
	type view struct {
		Refusal string            `json:"refusal"`
		Page    map[string]string `json:"page"`
		Older   []string          `json:"older"`
		Earlier []string          `json:"earlier"`
		Notes   string            `json:"notes"`
	}
	pageOf := func(dealID string) view {
		t.Helper()
		page := filepath.Join(t.TempDir(), "r.html")
		dealRun(t, "report", "--deal", dealID, "--html", page)
		out, err := exec.Command(node, driver, page).CombinedOutput()
		require.NoError(t, err, string(out))
		var v view
		require.NoError(t, json.Unmarshal(out, &v), string(out))
		require.Empty(t, v.Refusal)
		return v
	}
	const notes = "https://github.com/action-state-group/evidencebook-deal/releases/tag/"

	dealFixture(t)
	buildAs(t, "v0.1.0-rc23", "abc1234")
	before := retailDeal(t) // sealed by capsulectl itself, before the move
	buildAsPlugin(t, "v0.1.0", "def5678")
	after := retailDeal(t)

	buildAsPlugin(t, "v0.1.1", "0123456")
	v := pageOf(before)
	assert.Equal(t, map[string]string{"name": "capsulectl-deal", "version": "v0.1.1"}, v.Page)
	assert.Empty(t, v.Older, "a capsulectl stamp is not a capsulectl-deal version")
	assert.Equal(t, []string{"capsulectl v0.1.0-rc23"}, v.Earlier, "a capsulectl stamp predates any capsulectl-deal, whatever its tag")
	assert.Equal(t, notes+"v0.1.1", v.Notes)
	v = pageOf(after)
	assert.Equal(t, []string{"v0.1.0"}, v.Older, "same name: compared by tag")
	assert.Empty(t, v.Earlier)

	buildAsPlugin(t, "v0.1.0", "def5678")
	assert.Empty(t, pageOf(after).Older, "the build that sealed it made the page")

	// A build without a plugin release: the page names the core, as pages
	// built inside capsule-cli did, and links capsule-cli's releases.
	buildAsPlugin(t, "", "")
	buildAs(t, "v0.1.0-rc24", "abc1234")
	v = pageOf(before)
	assert.Equal(t, map[string]string{"name": "capsulectl", "version": "v0.1.0-rc24"}, v.Page)
	assert.Equal(t, []string{"v0.1.0-rc23"}, v.Older)
	assert.Empty(t, v.Earlier)
	assert.Equal(t, "https://github.com/action-state-group/capsule-cli/releases", v.Notes)
	v = pageOf(after)
	assert.Empty(t, v.Older, "builds of another name are not compared")
	assert.Empty(t, v.Earlier, "a capsulectl-deal step on a core-named page is not an earlier build")
}
