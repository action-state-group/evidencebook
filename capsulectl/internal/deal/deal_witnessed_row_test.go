// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verify reads --witness-directory once: the core then loads a private copy
// of those bytes, so the row the witnessed member names is the row the
// receipt was checked under even when W changes meanwhile (evidencebook#1073).
func TestSnapshotWitnessDirectory(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{}
		c.Flags().String("witness-directory", "", "")
		return c
	}
	path := filepath.Join(t.TempDir(), "witnesses.json")
	first := `{"witnesses":[{"name":"w","endpoint":"https://w.example","key_ids":["` + strings.Repeat("ab", 32) + `"],"independent_of":["a"]}]}`
	require.NoError(t, os.WriteFile(path, []byte(first), 0o600))

	c := newCmd()
	require.NoError(t, c.Flags().Set("witness-directory", path))
	rows, cleanup, err := snapshotWitnessDirectory(c)
	require.NoError(t, err)
	// W changes after the read: a same-name row with another key and list.
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(strings.ReplaceAll(first, "ab", "cd"), `["a"]`, `["b"]`)), 0o600))
	copyPath, _ := c.Flags().GetString("witness-directory")
	assert.NotEqual(t, path, copyPath, "the core reads the copy")
	got, err := os.ReadFile(copyPath)
	require.NoError(t, err)
	assert.Equal(t, first, string(got), "the copy holds the bytes the rows came from")
	require.Len(t, rows, 1)
	assert.Equal(t, []any{"a"}, rows[0].(map[string]any)["independent_of"])
	cleanup()
	assert.NoFileExists(t, copyPath)

	rows, cleanup, err = snapshotWitnessDirectory(newCmd())
	require.NoError(t, err)
	assert.Nil(t, rows, "no flag, no rows")
	cleanup()

	missing := newCmd()
	require.NoError(t, missing.Flags().Set("witness-directory", filepath.Join(t.TempDir(), "none.json")))
	_, cleanup, err = snapshotWitnessDirectory(missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none.json", "the error names the reader's own path")
	cleanup()
}

// A directory that does not decode gives no rows (the core refuses it).
func TestDecodeWitnessRows(t *testing.T) {
	assert.Len(t, decodeWitnessRows([]byte(`[{"name":"w"}]`)), 1, "a bare row list")
	assert.Len(t, decodeWitnessRows([]byte(`{"witnesses":[{"name":"w"}]}`)), 1)
	assert.Nil(t, decodeWitnessRows([]byte(`{"rows":[]}`)))
	assert.Nil(t, decodeWitnessRows([]byte(`"w"`)))
	assert.Nil(t, decodeWitnessRows([]byte(`{`)))
}

// `deal report --html --witness-directory W` on an x-deal-v0 copy: the page's
// report carries the W row's independent_of as verify reported it.
func TestV0PageCarriesTheWitnessRowsList(t *testing.T) {
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	row := rawKeyRow(f.endpoint, f.public)
	row["independent_of"] = []string{strings.Repeat("a1", 32)}
	page := filepath.Join(t.TempDir(), "keep.html")
	dealRun(t, "report", "--deal", f.dealID, "--html", page, "--witness-directory", writeDirectory(t, row))
	assert.Equal(t, map[string]any{"status": "verified", "witness": "test witness", "at": v0WitnessAt, "independent_of": []any{strings.Repeat("a1", 32)}},
		v0Entry(t, pageVerifyReport(t, mustRead(t, page)))["witnessed"])
}
