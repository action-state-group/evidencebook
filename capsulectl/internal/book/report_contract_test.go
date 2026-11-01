package book

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// outcomeContractJSON is a compiled contract for the 4-monthly-outcome
// fixture's contract_ref (example-support-outcomes/v1@1).
const outcomeContractJSON = `{"id":"example-support-outcomes/v1","version":"1","requirements":[
{"id":"resolution.done_in_full","profile":"outcome","tier":"must_have"},
{"id":"resolution.right_change","profile":"outcome","tier":"must_have"},
{"id":"policy.confirmed_before_acting","profile":"outcome","tier":"must_have"},
{"id":"communication.no_invented_terms","profile":"outcome","tier":"informational"},
{"id":"communication.plain_words","profile":"outcome"}]}`

// The page `report build --contract` writes is the SDK's page for the same
// bundle and View.Contract, byte for byte, and the result names the contract.
func TestReportBuildContractMatchesTheSDK(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bundlePath := filepath.Join(presentationDir, "4-monthly-outcome", "bundle.json")
	raw := mustRead(t, bundlePath)
	path, digest := wordingFixture(t, outcomeContractJSON)

	result, out, err := runReportBuild(t, bundlePath, "--card", "outcome", "--contract", path, "--contract-digest", digest)
	require.NoError(t, err)
	want, err := report.Build(raw, report.View{Card: "outcome", Contract: &report.Contract{Text: outcomeContractJSON, SHA256: digest}})
	require.NoError(t, err)
	assert.Equal(t, string(want.HTML), string(mustRead(t, out)))
	assert.Equal(t, "example-support-outcomes/v1@1", result.ContractRef)
	assert.Equal(t, digest, result.ContractSHA256)

	plain, plainOut, err := runReportBuild(t, bundlePath, "--card", "outcome")
	require.NoError(t, err)
	assert.Empty(t, plain.ContractRef)
	assert.Empty(t, plain.ContractSHA256)
	assert.NotEqual(t, string(mustRead(t, plainOut)), string(mustRead(t, out)), "the contract changes the page")
}

func TestReportBuildRefusesAnUnpinnedOrBadContract(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bundlePath := filepath.Join(presentationDir, "4-monthly-outcome", "bundle.json")
	path, digest := wordingFixture(t, outcomeContractJSON)
	other, otherDigest := wordingFixture(t, strings.Replace(outcomeContractJSON, `"version":"1"`, `"version":"2"`, 1))
	notJSON, notJSONDigest := wordingFixture(t, `{"id":`)

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a digest that does not match": {[]string{"--contract", path, "--contract-digest", strings.Repeat("0", 64)}, "--contract bytes do not hash"},
		"a contract that is not JSON":  {[]string{"--contract", notJSON, "--contract-digest", notJSONDigest}, "--contract is not JSON"},
		"another contract":             {[]string{"--contract", other, "--contract-digest", otherDigest}, "--contract example-support-outcomes/v1@2 is not one the Result cites"},
		"a missing file":               {[]string{"--contract", filepath.Join(t.TempDir(), "none.json"), "--contract-digest", digest}, ""},
		"no digest":                    {[]string{"--contract", path}, "--contract needs --contract-digest"},
		"a digest with no contract":    {[]string{"--contract-digest", digest}, "--contract-digest needs --contract"},
		"a permalink":                  {[]string{"--contract", path, "--contract-digest", digest, "--permalink"}, "--permalink cannot be used with --contract"},
	} {
		t.Run(name, func(t *testing.T) {
			_, out, err := runReportBuild(t, bundlePath, append([]string{"--card", "outcome"}, tc.args...)...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			_, statErr := os.Stat(out)
			assert.True(t, os.IsNotExist(statErr), "no page is written")
		})
	}
}
