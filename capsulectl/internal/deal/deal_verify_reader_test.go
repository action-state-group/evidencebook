// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenCopy is a committed copy of testdata/deal-verify, decoded.
func goldenCopy(t *testing.T, name string) (string, map[string]any) {
	t.Helper()
	path := filepath.Join(dealVerifyDir, name+".json")
	value, err := cli.DecodeBundleJSON(mustRead(t, path))
	require.NoError(t, err)
	return path, value
}

// extensionEntry is the report's entry of kind, or nil.
func extensionEntry(report map[string]any, kind string) map[string]any {
	exts, _ := report["extensions"].([]any)
	for _, raw := range exts {
		if e, _ := raw.(map[string]any); e["kind"] == kind {
			return e
		}
	}
	return nil
}

// The core alone reads no deal format; the plugin's verb reads it with its
// reader, names itself on the entry, and its verdict follows the deal
// section: a copy that lost its report reads INVALID through the plugin and
// VALID-or-not-shown through the core alone.
func TestThePluginVerbReadsTheDealSection(t *testing.T) {
	path, _ := goldenCopy(t, "v1-wrong-sealed-report")
	out, err := invoke(t, "", "verify", "--bundle", path)
	require.ErrorIs(t, err, cli.ErrBundleInvalid)
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	entry := extensionEntry(report, DealProfileV1)
	require.NotNil(t, entry, out)
	assert.Equal(t, dealReaderName, entry["read_by"])
	assert.Equal(t, "fail", entry["status"])
	assert.Equal(t, []any{"sealed_report_unverified"}, entry["findings"])

	// The core's own verify, with no reader, leaves the section uninterpreted.
	core := cli.NewCommandWith(DealGroup())
	var buf bytes.Buffer
	core.SetOut(&buf)
	core.SetErr(&buf)
	core.SetArgs([]string{"verify", "--bundle", path})
	_ = core.ExecuteContext(t.Context())
	var coreReport map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &coreReport), buf.String())
	coreEntry := extensionEntry(coreReport, DealProfileV1)
	require.NotNil(t, coreEntry)
	assert.Equal(t, "uninterpreted", coreEntry["status"], "the core reads no deal format")
	assert.NotContains(t, coreEntry, "read_by")
}

// verify's other inputs stay the core's: --bundle beside --history is the
// core's input error, not a deal verdict.
func TestThePluginVerbLeavesOtherInputsToTheCore(t *testing.T) {
	path, _ := goldenCopy(t, "v1-keep")
	_, err := invoke(t, "", "verify", "--bundle", path, "--history", path)
	require.Error(t, err)
	assert.ErrorContains(t, err, "give one of --capsule, --bundle and --history")
}

// The page gate with the plugin's reader refuses a copy whose deal section
// fails, which the core's gate alone lets through; and a copy that passes
// passes both.
func TestTheDealPageGateReadsTheDealSection(t *testing.T) {
	_, failing := goldenCopy(t, "v1-wrong-sealed-report")
	assert.NoError(t, cli.PageGate(failing), "the core alone reads no deal section")
	err := dealCopyPageGate(failing)
	require.Error(t, err)
	assert.ErrorContains(t, err, "sealed_report_unverified")

	_, good := goldenCopy(t, "v1-keep")
	assert.NoError(t, dealCopyPageGate(good))
	verdict, _ := dealBundleVerdict(good)
	assert.Equal(t, "VALID", verdict)
}

// A stripped copy (the deal section gone, its sealed report still
// disclosed) is caught by the reader's Finish, under the deal profile's kind.
func TestTheReaderCatchesAStrippedCopy(t *testing.T) {
	_, stripped := goldenCopy(t, "v0-no-section")
	verdict, _, report, err := dealAssess(stripped)
	require.NoError(t, err)
	assert.Equal(t, "INVALID", verdict)
	entry := extensionEntry(map[string]any{"extensions": toAnySlice(report["extensions"])}, DealProfile)
	require.NotNil(t, entry, "%v", report["extensions"])
	assert.Equal(t, dealReaderName, entry["read_by"])
	assert.Equal(t, "fail", entry["status"])
}

// The share record of a copy lists its standalone report as revealed, which
// the core's DisclosureRecord no longer does.
func TestTheDisclosureRecordAddsTheStandaloneReport(t *testing.T) {
	_, value := goldenCopy(t, "v1-counterparty")
	x, _ := value["extensions"].(map[string]any)[DealReportExtension].(map[string]any)
	require.NotNil(t, x)
	id := x["record"].(map[string]any)["capsule_id"].(string)
	core, err := cli.DisclosureRecord(value)
	require.NoError(t, err)
	assert.NotContains(t, core["revealed"], id)
	record, err := dealDisclosureRecord(value)
	require.NoError(t, err)
	assert.Contains(t, record["revealed"], id)
}

// toAnySlice turns a report's []map[string]any extensions into []any.
func toAnySlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out
	}
	return nil
}
