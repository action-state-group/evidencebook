// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const witnessedVectors = "testdata/v1-witnessed"

// pluginVerify runs the built plugin's `verify` and returns its report.
func pluginVerify(t *testing.T, args ...string) map[string]any {
	t.Helper()
	plugin := filepath.Join(installedCapsulectl(t), "plugins", "capsulectl-deal")
	out, err := exec.Command(plugin, append([]string{"verify"}, args...)...).Output()
	var report map[string]any
	require.NoError(t, json.Unmarshal(out, &report), "%v: %s", err, out)
	return report
}

// v1EntryOf is the report's x-deal-v1 entry.
func v1EntryOf(t *testing.T, report map[string]any) map[string]any {
	t.Helper()
	for _, e := range report["extensions"].([]any) {
		if m := e.(map[string]any); m["kind"] == "x-deal-v1" {
			return m
		}
	}
	t.Fatalf("no x-deal-v1 entry: %v", report["extensions"])
	return nil
}

// editedProof writes the vector proof after edit.
func editedProof(t *testing.T, edit func(p map[string]any)) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(witnessedVectors, "proof.json"))
	require.NoError(t, err)
	var p map[string]any
	require.NoError(t, json.Unmarshal(raw, &p))
	edit(p)
	out, err := json.Marshal(p)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "proof.json")
	require.NoError(t, os.WriteFile(path, out, 0o600))
	return path
}

// `verify --bundle --stamp-proof --witness-directory` reports the copy's own
// book's witness time in its x-deal-v1 entry, from a proof beside the copy:
// verified with the directory's name and the receipt's own time; withheld
// without a directory or a receipt that verifies; failed for a receipt or a
// proof that does not verify. The verdict is never changed by it
// (Sherlock2, 2026-11-25), and with no --stamp-proof the entry has no member.
func TestV1WitnessedFromAProofBeside(t *testing.T) {
	keep := filepath.Join(witnessedVectors, "keep.json")
	proof := filepath.Join(witnessedVectors, "proof.json")
	dir := filepath.Join(witnessedVectors, "witnesses.json")

	got := pluginVerify(t, "--bundle", keep, "--stamp-proof", proof, "--witness-directory", dir)
	assert.Equal(t, "VALID", got["verdict"])
	entry := v1EntryOf(t, got)
	assert.Equal(t, map[string]any{"status": "verified", "witness": "Action State's witness", "at": "2026-11-25T14:41:19Z"}, entry["witnessed"])
	assert.Equal(t, "verified", entry["counterparty_half"].(map[string]any)["status"])

	plain := pluginVerify(t, "--bundle", keep)
	assert.Equal(t, "VALID", plain["verdict"])
	assert.NotContains(t, v1EntryOf(t, plain), "witnessed", "no proof given, no member")

	cases := map[string]struct {
		args   []string
		status string
	}{
		"no directory": {[]string{"--stamp-proof", proof}, "withheld"},
		"another witness's directory": {[]string{"--stamp-proof", proof, "--witness-directory", writeJSON(t,
			`{"witnesses":[{"name":"Someone else","endpoint":"https://witness.agentactioncapsule.org","key_ids":["`+strings.Repeat("ab", 32)+`"]}]}`)}, "failed"},
		"no receipt": {[]string{"--stamp-proof", editedProof(t, func(p map[string]any) { delete(p, "witness_receipts") }), "--witness-directory", dir}, "withheld"},
		"a receipt that does not verify": {[]string{"--stamp-proof", editedProof(t, func(p map[string]any) {
			rc := p["witness_receipts"].([]any)[0].(map[string]any)
			rc["entry_hash"] = strings.Repeat("0", len(rc["entry_hash"].(string)))
		}), "--witness-directory", dir}, "failed"},
		"a stamp proof that does not verify": {[]string{"--stamp-proof", editedProof(t, func(p map[string]any) {
			sp := p["stamp_proof"].(map[string]any)
			sp["shelf_checkpoint"] = "AAAA"
		}), "--witness-directory", dir}, "failed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := pluginVerify(t, append([]string{"--bundle", keep}, c.args...)...)
			assert.Equal(t, "VALID", got["verdict"], "a witness result never changes the verdict")
			w := v1EntryOf(t, got)["witnessed"].(map[string]any)
			assert.NotEqual(t, "verified", w["status"])
			assert.Equal(t, c.status, w["status"], "%v", w)
			assert.NotContains(t, w, "witness", "no witness is named unless it verified")
		})
	}
}

// --stamp-proof goes with --bundle, and reads only a proof file.
func TestV1WitnessedRefusals(t *testing.T) {
	plugin := filepath.Join(installedCapsulectl(t), "plugins", "capsulectl-deal")
	out, err := exec.Command(plugin, "verify", "--bundle", filepath.Join(witnessedVectors, "keep.json"), "--stamp-proof", filepath.Join(witnessedVectors, "witnesses.json")).CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "is not a proof file")
}
