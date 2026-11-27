package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-viewer/go/receipt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installDealView puts the built capsulectl-deal and its presentation files
// (PresentationFiles: capsule-viewer's x-deal-v0 receipt modules and the deal
// view's composed module) in a fresh trusted root, as an operator who
// unpacked deal-view-VERSION.tar.gz beside the launcher has them, and returns
// the root.
func installDealView(t *testing.T) string {
	t.Helper()
	launcher, err := os.ReadFile(filepath.Join(installedCapsulectl(t), "plugins", "capsulectl-deal"))
	require.NoError(t, err)
	root := pluginRoot(t)
	trustPresentationsIn(t, root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeLauncher(t, root, "capsulectl-deal", string(launcher), 0o755)
	dir := filepath.Join(root, dealViewDir)
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.Chmod(dir, 0o755))
	files, err := PresentationFiles()
	require.NoError(t, err)
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), body, 0o644))
		require.NoError(t, os.Chmod(filepath.Join(dir, name), 0o644))
	}
	return root
}

// The handshake offers capsule-viewer's x-deal-v0 receipt modules and the
// deal view's composed one, never the deal view's single-copy module (#869),
// and the core accepts the files beside the launcher against the digests it
// states: `plugin ls` lists the modules with no refusal, `presentation list`
// selects the viewer's for a deal receipt, and `presentation build` carries
// it in the page.
func TestTheHandshakeOffersTheViewersReceiptModules(t *testing.T) {
	installDealView(t)
	stdout, err := invoke(t, "", "plugin", "ls")
	require.NoError(t, err, stdout)
	var ls struct {
		Plugins []map[string]any `json:"plugins"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &ls), stdout)
	var deal map[string]any
	for _, p := range ls.Plugins {
		if p["name"] == Name {
			deal = p
		}
	}
	require.NotNil(t, deal, stdout)
	assert.Nil(t, deal["presentations_refused"], stdout)
	for _, id := range []string{`"capsuleviewer.receipt.unilateral/v0"`, `"capsuleviewer.receipt.seller/v0"`, `"capsulectl.deal-view-composed/v0"`} {
		assert.Contains(t, stdout, id)
	}
	assert.NotContains(t, stdout, `"capsulectl.deal-view/v0"`)

	bundle := filepath.Join(presentationDir, "2-unilateral-receipt", "bundle.json")
	stdout, err = invoke(t, "", "presentation", "list", "--bundle", bundle)
	require.NoError(t, err, stdout)
	var list struct {
		Selected map[string]any `json:"selected"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &list), stdout)
	assert.Equal(t, "capsuleviewer.receipt.unilateral/v0", list.Selected["id"], stdout)
	assert.Equal(t, "plugin", list.Selected["source"])
	assert.Equal(t, Name, list.Selected["plugin"])

	b, page, err := passBuild(t, bundle)
	require.NoError(t, err)
	var ids []any
	for _, m := range b.Modules {
		ids = append(ids, m["id"])
	}
	assert.Contains(t, ids, "capsuleviewer.receipt.unilateral/v0", "the page carries the viewer's module")
	assert.NotContains(t, ids, "capsulectl.deal-view/v0")
	module, err := receipt.Module(receipt.Unilateral)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(page, string(module.Code)), "one script, inlined once")
}

// A launcher without the deal view's files beside it keeps its commands; the
// core refuses its presentations and says which file is missing.
func TestADealViewNotUnpackedIsRefusedAndTheCommandsStay(t *testing.T) {
	root := installDealView(t)
	require.NoError(t, os.RemoveAll(filepath.Join(root, dealViewDir)))
	stdout, err := invoke(t, "", "plugin", "ls")
	require.NoError(t, err, stdout)
	assert.Contains(t, stdout, `"missing_file"`)
	assert.Contains(t, stdout, dealViewDir+"/receipt_unilateral.js")
	assert.True(t, strings.Contains(stdout, `"canary"`), "the plugin's verbs are still listed")
}
