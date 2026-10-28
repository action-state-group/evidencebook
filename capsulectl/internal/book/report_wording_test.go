package book

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/evidencebook/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wordingPackJSON = `{"wording_pack_version":"aac.wording-pack/v0","id":"example.host/v0","locale":"en","entries":{"page.title":"Monthly outcomes"}}`

func wordingFixture(t *testing.T, pack string) (path, digest string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "wording.json")
	require.NoError(t, os.WriteFile(path, []byte(pack), 0o600))
	sum := sha256.Sum256([]byte(pack))
	return path, hex.EncodeToString(sum[:])
}

// The page `report build --wording` writes is the SDK's page for the same
// bundle and View.Wording, byte for byte; without the flag it is the page
// the goldens hold.
func TestReportBuildWordingMatchesTheSDK(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bundlePath := filepath.Join(presentationDir, "4-monthly-outcome", "bundle.json")
	raw := mustRead(t, bundlePath)
	pack, digest := wordingFixture(t, wordingPackJSON)

	for name, tc := range map[string]struct {
		args    []string
		wording *report.Wording
	}{
		"without the flag": {nil, nil},
		"with the flag":    {[]string{"--wording", pack, "--wording-digest", digest}, &report.Wording{Pack: wordingPackJSON, SHA256: digest}},
	} {
		t.Run(name, func(t *testing.T) {
			_, out, err := runReportBuild(t, bundlePath, append([]string{"--card", "outcome"}, tc.args...)...)
			require.NoError(t, err)
			want, err := report.Build(raw, report.View{Card: "outcome", Wording: tc.wording})
			require.NoError(t, err)
			assert.Equal(t, string(want.HTML), string(mustRead(t, out)))
		})
	}

	_, plain, err := runReportBuild(t, bundlePath, "--card", "outcome")
	require.NoError(t, err)
	_, worded, err := runReportBuild(t, bundlePath, "--card", "outcome", "--wording", pack, "--wording-digest", digest)
	require.NoError(t, err)
	assert.NotEqual(t, string(mustRead(t, plain)), string(mustRead(t, worded)), "the pack changes the page")
	assert.Contains(t, string(mustRead(t, worded)), "Monthly outcomes")
}

func TestReportBuildRefusesAnUnpinnedOrBadWording(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bundlePath := filepath.Join(presentationDir, "4-monthly-outcome", "bundle.json")
	pack, digest := wordingFixture(t, wordingPackJSON)
	badShape, badShapeDigest := wordingFixture(t, `{"wording_pack_version":"aac.wording-pack/v9","id":"x","locale":"en","entries":{}}`)
	wrongDigest := hex.EncodeToString(make([]byte, 32))

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a digest that does not match": {[]string{"--wording", pack, "--wording-digest", wrongDigest}, "--wording "},
		"a pack that is not a pack":    {[]string{"--wording", badShape, "--wording-digest", badShapeDigest}, "--wording "},
		"a missing file":               {[]string{"--wording", filepath.Join(t.TempDir(), "none.json"), "--wording-digest", digest}, ""},
		"no digest":                    {[]string{"--wording", pack}, "--wording needs --wording-digest"},
		"a digest with no pack":        {[]string{"--wording-digest", digest}, "--wording-digest needs --wording"},
		"a permalink":                  {[]string{"--wording", pack, "--wording-digest", digest, "--permalink"}, "--permalink cannot be used with --wording"},
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
