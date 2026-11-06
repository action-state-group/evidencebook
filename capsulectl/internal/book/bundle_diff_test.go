package book

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoLevels cuts one book's history into a full bundle and a lower one that
// withholds its payloads, and returns their paths and the checkpoint key.
func twoLevels(t *testing.T) (higher, lower string, key []byte, payloads []string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, keys := bookProfile(t, "a")
	appendHalves(t, p, half{"x1", "r1", "p1"}, half{"x2", "r2", "p2"})
	opened, err := openBook(t.Context(), p, true)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.release()) }()
	anchor, err := opened.book.Checkpoint(t.Context())
	require.NoError(t, err)
	all, err := opened.book.Query(t.Context(), evidencebook.Filter{})
	require.NoError(t, err)
	var ids []string
	for _, r := range all {
		ids = append(ids, r.RecordID)
	}
	dir := t.TempDir()
	write := func(name string, withhold bool) string {
		b, err := opened.book.Bundle(t.Context(), evidencebook.BundleRequest{Root: ids[len(ids)-1], Include: ids, WithholdPayloads: withhold, At: &anchor})
		require.NoError(t, err)
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, b.JSON, 0o600))
		return path
	}
	higher, lower = write("higher.json", false), write("lower.json", true)
	d, err := evidencebook.DiffBundles(mustRead(t, higher), mustRead(t, lower))
	require.NoError(t, err)
	return higher, lower, keys.checkpoint, d.DroppedPayloads
}

func TestBundleDiffListsWhatTheLowerLevelDrops(t *testing.T) {
	higher, lower, key, payloads := twoLevels(t)
	require.NotEmpty(t, payloads)
	out, err := invoke(t, "", "bundle-diff", "--higher", higher, "--lower", lower, "--checkpoint-key", hex.EncodeToString(key))
	require.NoError(t, err, out)
	var got bundleDiffResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, payloads, got.DroppedPayloads)
	assert.Empty(t, got.DroppedRecords)
	assert.Equal(t, hex.EncodeToString(key), got.AnchorKeyID)
}

func TestBundleDiffSameLevelIsEmpty(t *testing.T) {
	higher, _, _, _ := twoLevels(t)
	out, err := invoke(t, "", "bundle-diff", "--higher", higher, "--lower", higher)
	require.NoError(t, err, out)
	var got bundleDiffResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got.DroppedPayloads)
	assert.Empty(t, got.DroppedRecords)
}

func TestBundleDiffRefusals(t *testing.T) {
	higher, lower, key, _ := twoLevels(t)
	t.Run("swapped levels: the lower discloses what the higher does not", func(t *testing.T) {
		_, err := invoke(t, "", "bundle-diff", "--higher", lower, "--lower", higher)
		assert.ErrorContains(t, err, "not a lower level")
	})
	t.Run("wrong checkpoint key", func(t *testing.T) {
		other := make([]byte, len(key))
		_, err := invoke(t, "", "bundle-diff", "--higher", higher, "--lower", lower, "--checkpoint-key", hex.EncodeToString(other))
		assert.ErrorContains(t, err, "not signed by --checkpoint-key")
	})
	t.Run("missing flag", func(t *testing.T) {
		_, err := invoke(t, "", "bundle-diff", "--higher", higher)
		assert.ErrorContains(t, err, "--higher and --lower are required")
	})
	t.Run("a bundle that does not verify", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.json")
		require.NoError(t, os.WriteFile(bad, []byte(`{}`), 0o600))
		_, err := invoke(t, "", "bundle-diff", "--higher", higher, "--lower", bad)
		assert.ErrorContains(t, err, "cannot be compared")
	})
}
