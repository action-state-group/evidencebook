package book

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
	assert.Contains(t, out, `"dropped_payloads":[]`, "an empty list prints as [], not null")
	assert.Contains(t, out, `"dropped_records":[]`)
}

func TestBundleDiffListsDroppedRecords(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := bookProfile(t, "a")
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
	require.Greater(t, len(ids), 1)
	dir := t.TempDir()
	cut := func(name string, root string, include []string) string {
		b, err := opened.book.Bundle(t.Context(), evidencebook.BundleRequest{Root: root, Include: include, WithholdPayloads: true, At: &anchor})
		require.NoError(t, err)
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, b.JSON, 0o600))
		return path
	}
	higher, lower := cut("higher.json", ids[len(ids)-1], ids), cut("lower.json", ids[0], ids[:1])
	out, err := invoke(t, "", "bundle-diff", "--higher", higher, "--lower", lower)
	require.NoError(t, err, out)
	var got bundleDiffResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, sortedIDs(ids[1:]), got.DroppedRecords)
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
	t.Run("an empty or malformed checkpoint key does not switch the pin off", func(t *testing.T) {
		for _, k := range []string{"", "zz", "00"} {
			_, err := invoke(t, "", "bundle-diff", "--higher", higher, "--lower", lower, "--checkpoint-key", k)
			assert.ErrorContains(t, err, "--checkpoint-key is a 32-byte Ed25519 public key", k)
		}
	})
	t.Run("missing flag", func(t *testing.T) {
		_, err := invoke(t, "", "bundle-diff", "--higher", higher)
		assert.ErrorContains(t, err, "--higher and --lower are required")
	})
	t.Run("a bundle that does not verify", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.json")
		require.NoError(t, os.WriteFile(bad, []byte(`{}`), 0o600))
		_, err := invoke(t, "", "bundle-diff", "--higher", higher, "--lower", bad)
		assert.ErrorContains(t, err, "the --lower bundle does not verify")
	})
}

// The core shows the plugin's metadata list (help, plugin listing, doctor);
// dispatch does not depend on it. Every command BookCommands registers is in it.
func TestBookMetadataListsEveryCommand(t *testing.T) {
	listed := map[string]bool{}
	for _, name := range BookMetadata.Subcommands {
		listed[name] = true
	}
	for _, c := range BookCommands() {
		assert.True(t, listed[c.Name()], "%s is missing from BookMetadata.Subcommands", c.Name())
	}
}

// Two bundles of one log under different checkpoint keys are not two levels
// of one history, whether or not the caller pins a key.
func TestBundleDiffRefusesDifferentCheckpointKeys(t *testing.T) {
	higher, _, key, _ := twoLevels(t)
	// A second book with the same log id and its own keys.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, keys := bookProfile(t, "a")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	opened, err := openBook(t.Context(), p, true)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.release()) }()
	all, err := opened.book.Query(t.Context(), evidencebook.Filter{})
	require.NoError(t, err)
	b, err := opened.book.Bundle(t.Context(), evidencebook.BundleRequest{Root: all[len(all)-1].RecordID, Include: []string{all[len(all)-1].RecordID}, WithholdPayloads: true})
	require.NoError(t, err)
	other := filepath.Join(t.TempDir(), "other.json")
	require.NoError(t, os.WriteFile(other, b.JSON, 0o600))
	require.NotEqual(t, hex.EncodeToString(key), hex.EncodeToString(keys.checkpoint))

	for _, extra := range [][]string{nil, {"--checkpoint-key", hex.EncodeToString(key)}} {
		_, err = invoke(t, "", append([]string{"bundle-diff", "--higher", higher, "--lower", other}, extra...)...)
		require.Error(t, err)
		assert.ErrorContains(t, err, "signed by different keys")
	}
}

func sortedIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}
