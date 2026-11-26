// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
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
// is no parent-log-witness/v1 block, and no new bundle carries it under any
// name (Sherlock2, buzz 8c3ec04f): its words come from the producer's state.
func TestCadenceExtensionEntryByState(t *testing.T) {
	for _, state := range []string{"scheduled", "pending", "unverified", "not_configured"} {
		_, _, ok := cadenceExtensionEntry(map[string]interface{}{"state": state})
		assert.False(t, ok, state)
	}
	chain := map[string]interface{}{"state": "witnessed", "extent": "all", "salt": "s", "cadence": map[string]interface{}{"log_id": "c"}}
	name, block, ok := cadenceExtensionEntry(chain)
	require.True(t, ok)
	assert.Equal(t, DealParentWitnessExtension, name)
	assert.Equal(t, map[string]interface{}{"state": "witnessed", "extent": "all", "salt": "s", "parent": map[string]interface{}{"log_id": "c"}}, block)
	assert.Contains(t, chain, "cadence", "the chain the cadence built is not changed")
	assert.Equal(t, chain["cadence"], CadenceChainOf(map[string]interface{}{"extensions": map[string]interface{}{name: block}})["cadence"], "readers get it back as cadence")

	// A foreign block carries no state and may state an extent that contradicts
	// its `earlier`: a reader takes both from the block's shape, as AAC's
	// verifier does.
	read := CadenceChainOf(map[string]interface{}{"extensions": map[string]interface{}{DealParentWitnessExtension: map[string]interface{}{"extent": "all", "earlier": map[string]interface{}{}}}})
	assert.Equal(t, "witnessed", read["state"])
	assert.Equal(t, "part", read["extent"])
}

// An unwitnessed bundle carries no witness state; a report made from the
// file reads this profile's own state for its checkpoint ("scheduled"), as
// the same deal's own report does (evidencebook#737, #958).
func TestReportFromAnUnwitnessedBundleDoesNotGuessTheWitnessState(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	dealID := retailDeal(t)

	out, bundlePath, _, _ := dealReportFiles(t, dealID)
	assert.Equal(t, "scheduled", out["assurance"].(map[string]any)["witness_state"], "made now, the report knows")
	var b map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, bundlePath), &b))
	assert.NotContains(t, b["extensions"], DealCadenceExtension)
	assert.NotContains(t, b["extensions"], DealParentWitnessExtension)

	// No copy carries a chain since u225, so a report made from the file says
	// what this profile's own book holds for its checkpoint.
	from, _, _, _ := dealReportFiles(t, dealID, "--from-bundle", bundlePath)
	assert.Equal(t, "scheduled", from["assurance"].(map[string]any)["witness_state"])
	assert.Equal(t, "scheduled", from["grade"].(map[string]any)["witness"].(map[string]any)["state"])
	assert.Equal(t, "self_attested", from["assurance"].(map[string]any)["rung"])
}
