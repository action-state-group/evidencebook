// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireParentWitness checks a bundle's witnessed chain the way a neutral
// reader does, with AAC's own verifier (evidencebook#737): the bundle carries
// parent-log-witness/v1 and no x-cadence-witness/v0, the block verifies
// (pass under the witness directory at directoryPath, withheld with none:
// every link holds and no witness is trusted), and a block with a broken
// link, its leaf's salt changed, fails.
func requireParentWitness(t *testing.T, raw []byte, directoryPath string) {
	t.Helper()
	var directory witness.Directory
	wantStatus := witness.StatusWithheld
	if directoryPath != "" {
		parsed, err := witness.ParseDirectory(mustRead(t, directoryPath))
		require.NoError(t, err)
		directory, wantStatus = parsed, witness.StatusPass
	}
	b := decodeExact(t, raw) // numbers exact: a record's identity is over them
	exts := b["extensions"].(map[string]any)
	require.Contains(t, exts, DealParentWitnessExtension)
	assert.NotContains(t, exts, DealCadenceExtension, "v1 alone, never beside v0")
	assert.NotContains(t, exts[DealParentWitnessExtension], "cadence", "-03 names the member parent")

	good := aacbundle.VerifyStrict(b, aacbundle.StrictOptions{WitnessDirectory: &directory})
	require.NotNil(t, good.ParentWitness)
	assert.Equal(t, wantStatus, good.ParentWitness.Status, "%v", good.ParentWitness.Findings)

	broken := decodeExact(t, raw)
	broken["extensions"].(map[string]any)[DealParentWitnessExtension].(map[string]any)["salt"] = "0000"
	bad := aacbundle.VerifyStrict(broken, aacbundle.StrictOptions{WitnessDirectory: &directory})
	require.NotNil(t, bad.ParentWitness)
	assert.Equal(t, witness.StatusFail, bad.ParentWitness.Status)
	assert.Equal(t, []string{"parent_witness_entry_not_included"}, bad.ParentWitness.Findings)
}

// A state that is not a witnessed chain names no leaf and no receipt, so it
// is no parent-log-witness/v1 block.
func TestCadenceExtensionEntryByState(t *testing.T) {
	for _, state := range []string{"scheduled", "pending", "unverified", "not_configured"} {
		name, value := cadenceExtensionEntry(map[string]interface{}{"state": state})
		assert.Equal(t, DealCadenceExtension, name, state)
		assert.Equal(t, map[string]interface{}{"state": state}, value)
	}
	chain := map[string]interface{}{"state": "witnessed", "extent": "all", "salt": "s", "cadence": map[string]interface{}{"log_id": "c"}}
	name, block := cadenceExtensionEntry(chain)
	assert.Equal(t, DealParentWitnessExtension, name)
	assert.Equal(t, map[string]interface{}{"state": "witnessed", "extent": "all", "salt": "s", "parent": map[string]interface{}{"log_id": "c"}}, block)
	assert.Contains(t, chain, "cadence", "the chain the cadence built is not changed")
	assert.Equal(t, chain["cadence"], CadenceChainOf(map[string]interface{}{"extensions": map[string]interface{}{name: block}})["cadence"], "readers get it back as cadence")
}

// A sale's bundle and a History's deals carry their witnessed chain as
// parent-log-witness/v1 too, and AAC's verifier checks it.
func TestSaleAndHistoryBundlesCarryParentWitness(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	dealID := retailDeal(t)
	saleID, _ := newSale(t)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	directory := writeDirectory(t, rawKeyRow(endpoint, public))

	salePath := filepath.Join(t.TempDir(), "sale.json")
	dealRun(t, "sale", "bundle", "--sale", saleID, "--out", salePath)
	requireParentWitness(t, mustRead(t, salePath), directory)

	path, _ := writeHistory(t, []string{dealID}, sentTo, dealID)
	var history struct {
		Extensions map[string]struct {
			Deals map[string]json.RawMessage `json:"deals"`
		} `json:"extensions"`
	}
	require.NoError(t, json.Unmarshal(mustRead(t, path), &history))
	deals := history.Extensions[HistorySelectionExtension].Deals
	require.NotEmpty(t, deals)
	for _, d := range deals {
		requireParentWitness(t, d, directory)
	}
}
