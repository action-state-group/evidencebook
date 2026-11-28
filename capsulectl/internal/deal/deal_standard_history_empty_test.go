package deal

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertEmptyOwnCard writes the holder's own card and its shared copy under
// derivation and checks the own copy's block carries `deals` as an empty
// array, never null (evidencebook#1074): the History card module refuses a
// `deals` member that is not an array (standard_history_card.js, readBlock),
// and a holder with no deal has an empty set of deal entries (SH §11.1: a deal
// that has not started has no deal entry). The shared copy carries no
// `deals` member at all.
func assertEmptyOwnCard(t *testing.T, derivation string) (own string) {
	t.Helper()
	dir := t.TempDir()
	own = filepath.Join(dir, "own.json")
	dealRun(t, "standard-history", "card", "--derivation", derivation, "--out", own)
	raw, err := os.ReadFile(own)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(raw), `"deals":[]`), "the own copy's bytes carry an empty array")
	assert.False(t, strings.Contains(string(raw), `"deals":null`), "the own copy's bytes carry no null deals")
	block := shBlock(t, readBundle(t, own))
	require.Contains(t, block, "deals", "the own copy carries the member")
	deals, ok := block["deals"].([]any)
	require.True(t, ok, "deals is an array: %#v", block["deals"])
	assert.Empty(t, deals)

	shared := filepath.Join(dir, "shared.json")
	dealRun(t, "standard-history", "card", "--derivation", derivation, "--out", shared, "--shared")
	assert.NotContains(t, shBlock(t, readBundle(t, shared)), "deals", "no deal entry leaves in the shared copy")
	return own
}

// A holder of the x-deal-v0 layout with no deal gets an own /1 card whose
// block carries `deals: []`, and the History card module evidencebook ships
// draws it as a card whose counts recount from that empty set.
func TestAnEmptyV0CardCarriesAnEmptyDealsArray(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	own := assertEmptyOwnCard(t, standardhistory.Derivation)
	model := shippedModuleModel(t, own)
	assert.Equal(t, "card", model.State, "the module draws the card: %+v", model)
	assert.True(t, model.Own, "as the holder's own copy")
	assert.Equal(t, "PASS", model.Properties["counts_recompute"], "recounted from no deal entry")
}

// A holder of x-deal-v1 deals with none started gets own /3 and /4 cards
// whose block carries `deals: []`. The module evidencebook ships (capsule-viewer
// go/v0.18.3) predates /3 and refuses any /3 or /4 block (its as_of carries
// coverage_start_time), so it is not run here: evidencebook#1075.
func TestAnEmptyV1CardCarriesAnEmptyDealsArray(t *testing.T) {
	for _, derivation := range []string{standardhistory.Derivation3, standardhistory.Derivation4} {
		t.Run(derivation, func(t *testing.T) {
			side := newV1Side(t)
			side.act(t)
			now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
			dealRun(t, "standard-history", "declare", "--holder-kind", "node")
			*now = now.Add(time.Hour)
			require.Equal(t, "ticked", dealRun(t, "tick")["state"])
			assertEmptyOwnCard(t, derivation)
		})
	}
}

// shippedModel is what the History card module's buildModelSync returns.
type shippedModel struct {
	State      string            `json:"state"`
	Own        bool              `json:"own"`
	Properties map[string]string `json:"properties"`
}

// shippedModuleModel runs the History card module evidencebook ships
// (capsulectl-book's assets, vendored from capsule-viewer) over the bundle at
// path in Node, as a runtime that verified the bundle hands it the bundle, its
// root's disclosed card and a passing verification, and returns its model.
// Without node on PATH the test is skipped.
func shippedModuleModel(t *testing.T, path string) shippedModel {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH; the History card module is JavaScript")
	}
	module, err := filepath.Abs("../book/assets/standard-history/standard_history_card.js")
	require.NoError(t, err)
	const harness = `
const fs = require("fs");
require(process.argv[1]);
const SH = globalThis.CapsuleViewerStandardHistory;
const bundle = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const disclosed = bundle.disclosures[bundle.root].agent_input;
const context = {
  bundle, root: bundle.root,
  resolvedDisclosures: new Map([[bundle.root, { agent_input: { state: "disclosed", payload: disclosed } }]]),
  verification: { graphClosure: { status: "pass" }, intervalCoverage: { status: "pass" }, perRecordMembership: { status: "pass" }, capsuleResults: {}, disclosures: [] },
};
const m = SH.buildModelSync(context);
process.stdout.write(JSON.stringify({ state: m.state, own: m.own, properties: m.properties }));
`
	cmd := exec.CommandContext(t.Context(), node, "-e", harness, module, path)
	cmd.Stdin = nil
	out, err := cmd.Output()
	require.NoError(t, err, "node: %s", out)
	var m shippedModel
	require.NoError(t, json.Unmarshal(out, &m), string(out))
	return m
}
