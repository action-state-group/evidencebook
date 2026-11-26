// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The producer key the viewer's fixture trusts (the 22… seed).
const viewerFixtureProducerKey = "a09aa5f47a6759802ff955f8dc2d2a14a5c99d23be97f864127ff9383455a4f0"

// v0Entry is a verify report's one x-deal-v0 entry.
func v0Entry(t *testing.T, report map[string]any) map[string]any {
	t.Helper()
	var found map[string]any
	for _, e := range report["extensions"].([]any) {
		if entry := e.(map[string]any); entry["kind"] == DealProfile {
			require.Nil(t, found, "one x-deal-v0 entry")
			found = entry
		}
	}
	require.NotNil(t, found, "an x-deal-v0 entry")
	return found
}

// verifyReport runs `verify` with args and returns its report.
func verifyReport(t *testing.T, args ...string) map[string]any {
	t.Helper()
	out, _ := invoke(t, "", append([]string{"verify"}, args...)...)
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &report), out)
	return report
}

// `verify --bundle --countersign-directory D --producer-key K` reports an
// x-deal-v0 copy's countersignatures as capsule-viewer's field contract reads
// them (capsule-viewer#449, tests/testdata/deal-v0-report/README.md): the
// fixture's report member, byte for byte in JSON, beside an unchanged
// verdict.
func TestV0VerifyCountersignMatchesTheViewerContract(t *testing.T) {
	dir := "testdata/deal-v0-report"
	raw, err := os.ReadFile(filepath.Join(dir, "countersigned-keep.verify.json"))
	require.NoError(t, err)
	var want map[string]any
	require.NoError(t, json.Unmarshal(raw, &want))
	wantMember := v0Entry(t, want)["countersign"].(map[string]any)

	// The fixture names the directory as a path relative to it.
	t.Chdir(dir)
	got := verifyReport(t, "--bundle", "countersigned-keep.bundle.json",
		"--countersign-directory", "countersigners.json", "--producer-key", viewerFixtureProducerKey)
	assert.Equal(t, want["verdict"], got["verdict"], "the member never changes the verdict")
	entry := v0Entry(t, got)
	gotJSON, err := json.Marshal(entry["countersign"])
	require.NoError(t, err)
	wantJSON, err := json.Marshal(wantMember)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), string(gotJSON))
	assert.Equal(t, want["bundle_digest"], got["bundle_digest"])

	// Without --producer-key, the producer's countersignature reads as an
	// independent one: the page must pass the profile's keys.
	alone := v0Entry(t, verifyReport(t, "--bundle", "countersigned-keep.bundle.json", "--countersign-directory", "countersigners.json"))
	entries := alone["countersign"].(map[string]any)["entries"].([]any)
	require.Len(t, entries, 3)
	assert.Equal(t, "unresolved_signer", entries[1].(map[string]any)["state"])

	// Without --countersign-directory the entry has no member.
	none := v0Entry(t, verifyReport(t, "--bundle", "countersigned-keep.bundle.json"))
	assert.NotContains(t, none, "countersign")
}

// A directory that cannot be read rides in the member as an error, the
// report still printed; a copy with no countersignatures has no member.
func TestV0VerifyCountersignMemberEdges(t *testing.T) {
	dir := "testdata/deal-v0-report"
	bundle, err := filepath.Abs(filepath.Join(dir, "countersigned-keep.bundle.json"))
	require.NoError(t, err)
	missing := filepath.Join(t.TempDir(), "nowhere.json")
	entry := v0Entry(t, verifyReport(t, "--bundle", bundle, "--countersign-directory", missing, "--producer-key", viewerFixtureProducerKey))
	member := entry["countersign"].(map[string]any)
	assert.Equal(t, missing, member["directory"])
	assert.NotEmpty(t, member["error"])
	assert.NotContains(t, member, "entries")

	var b map[string]any
	raw, err := os.ReadFile(bundle)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &b))
	delete(b, "countersignatures")
	bare := filepath.Join(t.TempDir(), "bare.json")
	data, err := json.Marshal(b)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bare, data, 0o600))
	assert.NotContains(t, v0Entry(t, verifyReport(t, "--bundle", bare, "--countersign-directory", filepath.Join(dir, "countersigners.json"))), "countersign")
}

// The flags go with --bundle, and --producer-key with the directory.
func TestV0VerifyCountersignFlagRefusals(t *testing.T) {
	_, err := invoke(t, "", "verify", "--capsule", "x.json", "--countersign-directory", "d.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go with --bundle")
	_, err = invoke(t, "", "verify", "--bundle", "testdata/deal-v0-report/countersigned-keep.bundle.json", "--producer-key", viewerFixtureProducerKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--producer-key goes with --countersign-directory")
	_, err = invoke(t, "", "verify", "--bundle", "testdata/deal-v0-report/countersigned-keep.bundle.json", "--countersign-directory", "d.json", "--producer-key", "zz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--producer-key")
}
