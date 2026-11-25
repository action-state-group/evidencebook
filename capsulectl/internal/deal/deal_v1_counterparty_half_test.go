// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v1Entry is the x-deal-v1 entry of a verify result.
func v1Entry(t *testing.T, res map[string]interface{}) map[string]interface{} {
	t.Helper()
	for _, raw := range res["extensions"].([]interface{}) {
		if e := raw.(map[string]interface{}); e["kind"] == xdealv1.Profile {
			return e
		}
	}
	t.Fatalf("no x-deal-v1 entry: %v", res["extensions"])
	return nil
}

// rewriteBundle edits a bundle file's extensions and writes the result next
// to it.
func rewriteBundle(t *testing.T, file string, edit func(exts map[string]interface{})) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	var b map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &b))
	edit(b["extensions"].(map[string]interface{}))
	out, err := json.Marshal(b)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "edited.json")
	require.NoError(t, os.WriteFile(path, out, 0o600))
	return path
}

// The holder's own copy carries the other party's half (evidencebook#850):
// their agreement with the stamp proof that covers it, what the History
// card reads for "stamped by both". verify --bundle checks it from the copy
// alone and names their key; a copy whose half does not verify is INVALID,
// since the holder assembled it.
func TestAV1KeepCopyCarriesTheOtherPartysHalf(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "bundle.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", file)
	value := v1BundleOf(t, file)
	exts := value["extensions"].(map[string]interface{})
	require.Contains(t, exts, xdealv1.CounterpartyHalfExtension)

	res, err := verifyBundleOutput(t, file)
	require.NoError(t, err)
	assert.Equal(t, "VALID", res["verdict"])
	half := v1Entry(t, res)["counterparty_half"].(map[string]interface{})
	assert.Equal(t, "verified", half["status"])
	assert.Equal(t, h.seller.party.signer[:16], half["key"], "their key, as their agreement is signed")

	// A half whose stamp proof is not of their agreement: INVALID.
	forged := rewriteBundle(t, file, func(exts map[string]interface{}) {
		half := exts[xdealv1.CounterpartyHalfExtension].(map[string]interface{})
		proof := half["stamp_proof"].(map[string]interface{})
		proof["book_checkpoint"] = proof["shelf_checkpoint"]
	})
	res, err = verifyBundleOutput(t, forged)
	assert.Error(t, err)
	assert.Equal(t, "INVALID", res["verdict"])
	e := v1Entry(t, res)
	assert.Equal(t, "fail", e["status"])
	assert.Equal(t, []interface{}{DealV1CounterpartyHalfFailed}, e["findings"])
	assert.Equal(t, "failed", e["counterparty_half"].(map[string]interface{})["status"])
	raw, err := os.ReadFile(forged)
	require.NoError(t, err)
	forgedValue, err := cli.DecodeBundleJSON(raw)
	require.NoError(t, err)
	err = CheckDealStandaloneReport(forgedValue)
	require.Error(t, err)
	assert.Contains(t, err.Error(), DealV1CounterpartyHalfFailed, "the reason names the half, not the report")

	// Stripped, the copy reads as one without it: no counterparty_half
	// member (absent), never a failure.
	stripped := rewriteBundle(t, file, func(exts map[string]interface{}) { delete(exts, xdealv1.CounterpartyHalfExtension) })
	res, err = verifyBundleOutput(t, stripped)
	require.NoError(t, err)
	assert.Equal(t, "VALID", res["verdict"])
	assert.NotContains(t, v1Entry(t, res), "counterparty_half")
}

// A shared copy never carries the half, and one that does is INVALID: only
// the holder's own copy holds it (Sherlock2, evidencebook#850).
func TestAV1SharedCopyCarriesNoHalf(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	keep := filepath.Join(t.TempDir(), "keep.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", keep)
	shared := filepath.Join(t.TempDir(), "shared.json")
	dealRun(t, "report", "--deal", h.dealID, "--share", "counterparty", "--to", "Kettle Shop", "--bundle", shared)
	value := v1BundleOf(t, shared)
	assert.NotContains(t, value["extensions"].(map[string]interface{}), xdealv1.CounterpartyHalfExtension)
	res, err := verifyBundleOutput(t, shared)
	require.NoError(t, err)
	assert.NotContains(t, v1Entry(t, res), "counterparty_half", "absent")

	raw, err := os.ReadFile(keep)
	require.NoError(t, err)
	var k map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &k))
	moved := rewriteBundle(t, shared, func(exts map[string]interface{}) {
		exts[xdealv1.CounterpartyHalfExtension] = k["extensions"].(map[string]interface{})[xdealv1.CounterpartyHalfExtension]
	})
	res, err = verifyBundleOutput(t, moved)
	assert.Error(t, err)
	assert.Equal(t, "INVALID", res["verdict"])
	assert.Equal(t, []interface{}{DealV1CounterpartyHalfFailed}, v1Entry(t, res)["findings"])
}
