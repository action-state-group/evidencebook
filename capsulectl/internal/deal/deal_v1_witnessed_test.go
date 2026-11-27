// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"

	"github.com/action-state-group/evidencebook"
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
	assert.Equal(t, map[string]any{"status": "verified", "witness": "Action State's witness", "at": "2026-11-25T14:41:19Z", "independent_of": []any{}}, entry["witnessed"],
		"the row states no independent_of: [] (evidencebook#1073)")
	assert.Equal(t, "verified", entry["counterparty_half"].(map[string]any)["status"])

	plain := pluginVerify(t, "--bundle", keep)
	assert.Equal(t, "VALID", plain["verdict"])
	assert.NotContains(t, v1EntryOf(t, plain), "witnessed", "no proof given, no member")

	// What the member names besides its status: the W row of the witness it
	// reports on, when W lists one (evidencebook#1073).
	asWitness := func(name string, list []any) map[string]any {
		return map[string]any{"witness": name, "independent_of": list}
	}
	cases := map[string]struct {
		args   []string
		status string
		row    map[string]any
	}{
		"no directory": {[]string{"--stamp-proof", proof}, "withheld", nil},
		"another witness's directory": {[]string{"--stamp-proof", proof, "--witness-directory", writeJSON(t,
			`{"witnesses":[{"name":"Someone else","endpoint":"https://witness.agentactioncapsule.org","key_ids":["`+strings.Repeat("ab", 32)+`"],"independent_of":["`+strings.Repeat("cd", 32)+`"]}]}`)},
			"failed", asWitness("Someone else", []any{strings.Repeat("cd", 32)})},
		"a directory that lists another witness only": {[]string{"--stamp-proof", proof, "--witness-directory", writeJSON(t,
			`{"witnesses":[{"name":"Someone else","endpoint":"https://other.example","key_ids":["`+strings.Repeat("ab", 32)+`"]}]}`)}, "withheld", nil},
		"no receipt": {[]string{"--stamp-proof", editedProof(t, func(p map[string]any) { delete(p, "witness_receipts") }), "--witness-directory", dir}, "withheld", nil},
		"a receipt that does not verify": {[]string{"--stamp-proof", editedProof(t, func(p map[string]any) {
			rc := p["witness_receipts"].([]any)[0].(map[string]any)
			rc["entry_hash"] = strings.Repeat("0", len(rc["entry_hash"].(string)))
		}), "--witness-directory", dir}, "failed", asWitness("Action State's witness", []any{})},
		"a stamp proof that does not verify": {[]string{"--stamp-proof", editedProof(t, func(p map[string]any) {
			sp := p["stamp_proof"].(map[string]any)
			sp["shelf_checkpoint"] = "AAAA"
		}), "--witness-directory", dir}, "failed", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := pluginVerify(t, append([]string{"--bundle", keep}, c.args...)...)
			assert.Equal(t, "VALID", got["verdict"], "a witness result never changes the verdict")
			w := v1EntryOf(t, got)["witnessed"].(map[string]any)
			assert.NotEqual(t, "verified", w["status"])
			assert.Equal(t, c.status, w["status"], "%v", w)
			assert.NotContains(t, w, "at", "no time unless it verified")
			for _, k := range []string{"witness", "independent_of"} {
				if c.row == nil {
					assert.NotContains(t, w, k, "no row is named when W lists no witness of a receipt")
				} else {
					assert.Equal(t, c.row[k], w[k], k)
				}
			}
		})
	}
}

// The member's independent_of is the W row's list as W states it, whatever
// it says; a list that is not a list of strings is left out, never guessed
// (evidencebook#1073). With a receipt of a witness W does not list before
// the verified one, the verified receipt's row is named.
func TestV1WitnessedNamesTheRowsIndependentOf(t *testing.T) {
	keep := filepath.Join(witnessedVectors, "keep.json")
	proof := filepath.Join(witnessedVectors, "proof.json")
	key := "39bb654c9dc0afe1c0edef0deffaa69099b8518836c9ba26e0491535840f96b5"
	row := func(independentOf string) string {
		r := `{"name":"Action State's witness","endpoint":"https://witness.agentactioncapsule.org","key_ids":["` + key + `"]`
		if independentOf != "" {
			r += `,"independent_of":` + independentOf
		}
		return r + `}`
	}
	a, b := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
	cases := map[string]struct {
		directory string
		want      any // nil: no member
	}{
		"a list":                 {`{"witnesses":[` + row(`["`+a+`","`+b+`"]`) + `]}`, []any{a, b}},
		"an empty list":          {`{"witnesses":[` + row(`[]`) + `]}`, []any{}},
		"no list":                {`{"witnesses":[` + row(``) + `]}`, []any{}},
		"a bare row list":        {`[` + row(`["`+a+`"]`) + `]`, []any{a}},
		"not a list":             {`{"witnesses":[` + row(`"`+a+`"`) + `]}`, nil},
		"a list of non-strings":  {`{"witnesses":[` + row(`[1]`) + `]}`, nil},
		"the first matching row": {`{"witnesses":[` + row(`["`+a+`"]`) + `,` + row(`["`+b+`"]`) + `]}`, []any{a}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := v1EntryOf(t, pluginVerify(t, "--bundle", keep, "--stamp-proof", proof, "--witness-directory", writeJSON(t, c.directory)))["witnessed"].(map[string]any)
			assert.Equal(t, "verified", w["status"])
			assert.Equal(t, "Action State's witness", w["witness"])
			if c.want == nil {
				assert.NotContains(t, w, "independent_of")
			} else {
				assert.Equal(t, c.want, w["independent_of"])
			}
		})
	}

	unlistedFirst := editedProof(t, func(p map[string]any) {
		good := p["witness_receipts"].([]any)[0].(map[string]any)
		other := map[string]any{}
		for k, v := range good {
			other[k] = v
		}
		other["ts_url"] = "https://unlisted.example"
		p["witness_receipts"] = []any{other, good}
	})
	w := v1EntryOf(t, pluginVerify(t, "--bundle", keep, "--stamp-proof", unlistedFirst, "--witness-directory", writeJSON(t, `{"witnesses":[`+row(`["`+a+`"]`)+`]}`)))["witnessed"].(map[string]any)
	assert.Equal(t, map[string]any{"status": "verified", "witness": "Action State's witness", "at": "2026-11-25T14:41:19Z", "independent_of": []any{a}}, w)
}

// --stamp-proof goes with --bundle, and reads only a proof file.
func TestV1WitnessedRefusals(t *testing.T) {
	plugin := filepath.Join(installedCapsulectl(t), "plugins", "capsulectl-deal")
	out, err := exec.Command(plugin, "verify", "--bundle", filepath.Join(witnessedVectors, "keep.json"), "--stamp-proof", filepath.Join(witnessedVectors, "witnesses.json")).CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "is not a proof file")
}

// A stamp proof binds to the copy only through the copy's own signed
// checkpoint: the same key, log, root and size. Same root and size under
// another key, or another log, is not of this copy.
func TestStampProofOfCopy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(witnessedVectors, "keep.json"))
	require.NoError(t, err)
	vb, err := evidencebook.VerifyBundle(raw)
	require.NoError(t, err)
	pr, err := readStampProof(filepath.Join(witnessedVectors, "proof.json"))
	require.NoError(t, err)
	v, err := evidencebook.VerifyStampProof(*pr.StampProof)
	require.NoError(t, err)
	assert.Empty(t, stampProofOfCopy(vb, v))

	otherKey := v
	otherKey.KeyID = strings.Repeat("ab", 32)
	assert.Contains(t, stampProofOfCopy(vb, otherKey), "another key")
	otherLog := v
	otherLog.BookCheckpoint.LogID = "deal/other"
	assert.Contains(t, stampProofOfCopy(vb, otherLog), "another log")
	otherSize := v
	otherSize.BookCheckpoint.TreeSize++
	assert.Contains(t, stampProofOfCopy(vb, otherSize), "not of this copy's checkpoint")
	unsigned := vb
	unsigned.AnchorAuthenticated = false
	assert.Contains(t, stampProofOfCopy(unsigned, v), "not signed")
}
