package book

import (
	"os"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --diagnostic writes a marked page for a bundle that does not verify, and
// the command still fails with the bundle's own error: a diagnostic page is
// never a success.
func TestReportBuildDiagnosticWritesAMarkedPageAndStillFails(t *testing.T) {
	disclosed, _ := bookFormBundle(t)
	tampered := rewriteBundle(t, disclosed, func(value map[string]interface{}) {
		record := value["records"].([]interface{})[0].(map[string]interface{})
		sig := []byte(record["signature"].(string))
		if sig[len(sig)-1] == '0' {
			sig[len(sig)-1] = '1'
		} else {
			sig[len(sig)-1] = '0'
		}
		record["signature"] = string(sig)
	})
	result, out, err := runReportBuild(t, tampered, "--card", "obligation", "--diagnostic")
	require.ErrorIs(t, err, cli.ErrBundleInvalid)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Equal(t, "diagnostic", result.Page)
	assert.True(t, result.Diagnostic)
	assert.Equal(t, "INVALID", result.Verdict)
	assert.Equal(t, out, result.Report)
	assert.Regexp(t, `^diagnostic page, not a verified report · \d+ of \d+ records? verified · \d+ of \d+ claims? not evaluable$`, result.StatusLine)
	assert.Less(t, result.RecordsVerified, result.Records)
	html, readErr := os.ReadFile(out)
	require.NoError(t, readErr)
	assert.Contains(t, string(html), "DIAGNOSTIC PAGE: not a verified report")
	assert.Empty(t, result.Permalink)
}

func TestReportBuildDiagnosticRefusals(t *testing.T) {
	disclosed, _ := bookFormBundle(t)
	tampered := rewriteBundle(t, disclosed, func(value map[string]interface{}) {
		record := value["records"].([]interface{})[0].(map[string]interface{})
		record["signature"] = "00" + record["signature"].(string)[2:]
	})
	// The combination itself is refused, for a bundle that would otherwise
	// get a diagnostic page.
	for _, flag := range []string{"--seal", "--permalink"} {
		_, out, err := runReportBuild(t, tampered, "--card", "obligation", "--diagnostic", flag)
		require.ErrorIs(t, err, cli.ErrInput, flag)
		assert.Contains(t, cli.SafeError(err), "--diagnostic cannot be used with --seal or --permalink", flag)
		assert.NoFileExists(t, out, flag)
	}
	// A bundle that verifies has no diagnostic page.
	_, out, err := runReportBuild(t, disclosed, "--card", "obligation", "--diagnostic")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "this bundle verifies")
	assert.NoFileExists(t, out)
}

// A page that is written reports counts with denominators, and a refused one
// reports them too.
func TestReportBuildStatusLineHasDenominators(t *testing.T) {
	disclosed, _ := bookFormBundle(t)
	result, out, err := runReportBuild(t, disclosed, "--card", "obligation")
	require.NoError(t, err)
	assert.FileExists(t, out)
	assert.Regexp(t, `^\d+ of \d+ records? verified · \d+ of \d+ claims? not evaluable$`, result.StatusLine)
	assert.NotContains(t, result.StatusLine, "%")
	assert.Equal(t, result.Records, result.RecordsVerified)
	assert.False(t, result.Diagnostic)

	refused, _, err := runReportBuild(t, sealedResultFixture, "--card", "outcome")
	require.ErrorIs(t, err, cli.ErrPartial)
	assert.Equal(t, "not written", refused.Page)
	assert.Regexp(t, `of \d+ records? verified`, refused.StatusLine)
}
