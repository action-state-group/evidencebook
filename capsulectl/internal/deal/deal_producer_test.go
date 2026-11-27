package deal

import (
	"encoding/json"
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
	// The page no longer warns of an older build: Sherlock2 dropped that
	// line (capsule-viewer#383 comment 7176241436, line 6); the facts stay
	// sealed above.
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
	// The last evidencebook-deal release and the first capsulectl/v* release of
	// this repository (evidencebook#610) are both shareable, and so is every
	// version release.yml accepts: its shape check is this one's.
	for _, version := range []string{"v0.1.0-rc45", "v0.1.0-rc46", "v9999.9999.9999-rc999", "v0.1.0-alpha", "v0.1.0-beta1", "v0.1.0-dev7"} {
		assert.True(t, ok("capsulectl-deal", version, "d3c6a9bbf4eeaeedef67d1e3fe0d0f53e7164a70"), version)
	}
	for _, version := range []string{"v0.1.0-rc1000", "v10000.0.0", "v0.1.0-rc46.1", "v0.1.0+build"} {
		assert.False(t, ok("capsulectl-deal", version, "d3c6a9bbf4eeaeedef67d1e3fe0d0f53e7164a70"), version)
	}
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

// The page's comparison of builds (an older or earlier build than the one
// that made the page, and its release notes) was the deal view's line 6,
// which Sherlock2 dropped when capsule-viewer's x-deal-v0 module took over
// capsulectl's pages (capsule-viewer#383 comment 7176241436, #869); the
// facts stay sealed in the report (page_version, produced_by above).
