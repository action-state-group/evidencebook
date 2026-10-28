package book

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeResultFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "result.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// validResultFixture is a Result the schema accepts.
func validResultFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "result", "pos-example-org-claims-result.json"))
	require.NoError(t, err)
	return string(raw)
}

func TestResultOpenTextMode(t *testing.T) {
	path := writeResultFixture(t, validResultFixture(t))
	out, e := invoke(t, "", "result", "open", path)
	require.NoError(t, e)
	assert.Contains(t, out, "Aggregate:")
	assert.Contains(t, out, "Coverage:")
	assert.Contains(t, out, "evaluated_population: 3")
	assert.Contains(t, out, "excluded_not_applicable: 1")
	assert.Contains(t, out, "unknown_count: 0")
	assert.Contains(t, out, "no longer DRAFT")
}

func TestResultOpenJSONMode(t *testing.T) {
	path := writeResultFixture(t, validResultFixture(t))
	out, e := invoke(t, "", "result", "open", path, "--format", "json")
	require.NoError(t, e)
	var parsed struct {
		Aggregate map[string]any `json:"aggregate"`
		Note      string         `json:"note"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	coverage, ok := parsed.Aggregate["coverage"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 3, coverage["evaluated_population"])
	assert.Contains(t, parsed.Note, "no longer DRAFT")
}

func TestResultOpenRejectsMissingAggregate(t *testing.T) {
	path := writeResultFixture(t, `{"claims": []}`)
	_, e := invoke(t, "", "result", "open", path)
	require.Error(t, e)
	assert.Equal(t, 2, cli.ExitCode(e))
}

func TestResultOpenRejectsAggregateWithoutCoverage(t *testing.T) {
	path := writeResultFixture(t, `{"aggregate": {"status": "ok"}}`)
	_, e := invoke(t, "", "result", "open", path)
	require.Error(t, e)
	assert.Equal(t, 2, cli.ExitCode(e))
}

// A Result that carries an aggregate coverage but fails only the
// schema (here: a claim with no tier) is refused.
func TestResultOpenRejectsSchemaViolation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "result", "neg-untiered-claim.json"))
	require.NoError(t, err)
	_, e := invoke(t, "", "result", "open", writeResultFixture(t, string(raw)))
	require.Error(t, e)
	assert.Equal(t, 2, cli.ExitCode(e))
}

func TestResultOpenRejectsNonObject(t *testing.T) {
	path := writeResultFixture(t, `[1, 2, 3]`)
	_, e := invoke(t, "", "result", "open", path)
	require.Error(t, e)
}

func TestResultOpenRejectsTrailingData(t *testing.T) {
	path := writeResultFixture(t, validResultFixture(t)+"\n{}")
	_, e := invoke(t, "", "result", "open", path)
	require.Error(t, e)
}

func TestResultOpenRejectsBadFormat(t *testing.T) {
	path := writeResultFixture(t, validResultFixture(t))
	_, e := invoke(t, "", "result", "open", path, "--format", "xml")
	require.Error(t, e)
}

func TestResultOpenMissingFile(t *testing.T) {
	_, e := invoke(t, "", "result", "open", filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, e)
	assert.Equal(t, 2, cli.ExitCode(e))
}
