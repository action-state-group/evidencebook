package deal

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every wording key this plugin's code names is in the built-in packs this
// process speaks with (the core's and this plugin's own), so no line is left
// out for a missing key (Line drops one). capsule-cli's
// TestEveryWordingKeyTheCodeNamesIsInThePack checks the core's code; this is
// the same check over this plugin's code (capsule-cli#326, design note
// section 3.1; capsule-cli#428).
func TestEveryWordingKeyTheDealCodeNamesIsInThePack(t *testing.T) {
	w := cli.BuiltinWording()
	key := regexp.MustCompile(`"((?:say|card|report|action)\.[a-z_.]*[a-z_])"`)
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	checked := 0
	var missing []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		checked++
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range key.FindAllStringSubmatch(string(src), -1) {
			if _, ok := w.Entries[m[1]]; !ok {
				missing = append(missing, f+": "+m[1])
			}
		}
	}
	require.Positive(t, checked, "the glob matched no source file")
	assert.Empty(t, missing, "wording keys the built-in pack lacks")
}
