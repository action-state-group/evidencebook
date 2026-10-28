package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installDealView puts the built capsulectl-deal and its deal view files in a
// fresh trusted root, as an operator who unpacked deal-view-VERSION.tar.gz
// beside the launcher has them, and returns the root.
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
	for name, body := range map[string]string{"deal-view.js": DealViewJS, "deal-view.css": DealViewCSS} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
		require.NoError(t, os.Chmod(filepath.Join(dir, name), 0o644))
	}
	return root
}

// The handshake offers the deal view, and the core accepts the files beside
// the launcher against the digests it states: `plugin ls` lists the module
// with no refusal, `presentation list` selects it for a deal receipt, and
// `presentation build` carries it in the page.
func TestTheHandshakeOffersTheDealView(t *testing.T) {
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
	assert.Contains(t, stdout, `"capsulectl.deal-view/v0"`)

	bundle := filepath.Join(presentationDir, "2-unilateral-receipt", "bundle.json")
	stdout, err = invoke(t, "", "presentation", "list", "--bundle", bundle)
	require.NoError(t, err, stdout)
	var list struct {
		Selected map[string]any `json:"selected"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &list), stdout)
	assert.Equal(t, "capsulectl.deal-view/v0", list.Selected["id"], stdout)
	assert.Equal(t, "plugin", list.Selected["source"])
	assert.Equal(t, Name, list.Selected["plugin"])

	b, page, err := passBuild(t, bundle)
	require.NoError(t, err)
	require.Len(t, b.Modules, 1, "the page carries the deal view")
	assert.Equal(t, "capsulectl.deal-view/v0", b.Modules[0]["id"])
	assert.Contains(t, page, DealViewJS)
}

// A launcher without the deal view's files beside it keeps its commands; the
// core refuses its presentations and says which file is missing.
func TestADealViewNotUnpackedIsRefusedAndTheCommandsStay(t *testing.T) {
	root := installDealView(t)
	require.NoError(t, os.RemoveAll(filepath.Join(root, dealViewDir)))
	stdout, err := invoke(t, "", "plugin", "ls")
	require.NoError(t, err, stdout)
	assert.Contains(t, stdout, `"missing_file"`)
	assert.Contains(t, stdout, dealViewDir+"/deal-view.js")
	assert.True(t, strings.Contains(stdout, `"canary"`), "the plugin's verbs are still listed")
}
