package book

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/action-state-group/capsule-cli/cli"
)

const standardHistoryDir = "assets/standard-history"

func fileSHA(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(standardHistoryDir, name))
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestBookOffersTheHistoryCardInItsHandshake(t *testing.T) {
	require.Len(t, BookMetadata.Presentations, 1)
	p := BookMetadata.Presentations[0]
	var m struct {
		ID       string `json:"id"`
		Requires struct {
			Extensions struct {
				Required []string `json:"required"`
			} `json:"extensions"`
		} `json:"requires"`
		Executable struct {
			Carrier       string   `json:"carrier"`
			ScriptSHA256  string   `json:"script_sha256"`
			StyleSHA256   []string `json:"style_sha256"`
			WordingSHA256 string   `json:"wording_sha256"`
		} `json:"executable"`
	}
	require.NoError(t, json.Unmarshal(p.Manifest, &m))
	assert.Equal(t, "capsuleviewer.standard-history/v0", m.ID)
	assert.Equal(t, []string{"x-standard-history/v0"}, m.Requires.Extensions.Required)
	assert.Equal(t, "module-slot", m.Executable.Carrier)

	// Every file the handshake names is a regular file of this tree, beside the
	// launcher once released, and its digest is the file's and the manifest's pin.
	var scripts, styles, wordings []string
	for _, f := range p.Files {
		require.True(t, strings.HasPrefix(f.Path, "standard-history-module/"), f.Path)
		assert.NotContains(t, strings.Split(f.Path, "/"), "..")
		assert.Equal(t, fileSHA(t, strings.TrimPrefix(f.Path, "standard-history-module/")), f.SHA256, f.Path)
		switch f.Role {
		case "script":
			scripts = append(scripts, f.SHA256)
		case "style":
			styles = append(styles, f.SHA256)
		case "wording":
			wordings = append(wordings, f.SHA256)
		default:
			t.Fatalf("unknown role %q", f.Role)
		}
	}
	assert.Equal(t, []string{m.Executable.ScriptSHA256}, scripts)
	assert.Equal(t, m.Executable.StyleSHA256, styles)
	assert.Equal(t, []string{m.Executable.WordingSHA256}, wordings)

	// The manifest in the handshake is the bundle's manifest.json, byte for byte in meaning.
	disk, err := os.ReadFile(filepath.Join(standardHistoryDir, "manifest.json"))
	require.NoError(t, err)
	assert.JSONEq(t, string(disk), string(p.Manifest))
}

func TestHistoryCardPresentationsAreTheOnesTheCoreAccepts(t *testing.T) {
	// The handshake answer round-trips through the core's own type.
	raw, err := json.Marshal(BookMetadata)
	require.NoError(t, err)
	var back cli.PluginMetadata
	require.NoError(t, json.Unmarshal(raw, &back))
	require.Len(t, back.Presentations, 1)
	assert.Equal(t, BookMetadata.Presentations[0].Files, back.Presentations[0].Files)
}
