package deal

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gateElementExpression is the expression gateElements stands for.
var gateElementExpression = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script\s*>|<style\b[^>]*>(.*?)</style\s*>`)

// gateElementPieces are what the random pages are made of: each part of
// both expressions in every case, the long s that folds to 's', word and
// non-word bytes after a name, \s and non-\s bytes before '>', and invalid
// UTF-8.
var gateElementPieces = []string{
	"<", ">", "/", "</", "<script", "<SCRIPT", "<ScRiPt", "<ſcript", "<scrip", "<style", "<STYLE", "<ſtyle", "<styl",
	"</script", "</SCRIPT", "</ſcript", "</style", "</STYLE", "</ſtyle", "script", "style",
	" ", "\t", "\n", "\f", "\r", "\v", "a", "Z", "_", "0", "-", "é", "ſ", "\xc5", "\xbf", "\xff",
	`type="application/json"`, "x", "=", "\"", "'",
}

func randomGatePage(r *rand.Rand) []byte {
	var b strings.Builder
	for range r.IntN(40) {
		b.WriteString(gateElementPieces[r.IntN(len(gateElementPieces))])
	}
	return []byte(b.String())
}

// gateElements returns what the expression returns, on random pages built
// to hit every edge of it, and on every page and bundle the tests keep.
func TestGateElementsMatchTheExpression(t *testing.T) {
	r := rand.New(rand.NewPCG(1040, 1))
	for range raceRounds(200000) {
		page := randomGatePage(r)
		require.Equal(t, gateElementExpression.FindAllSubmatchIndex(page, -1), gateElements(page), "%q", page)
	}
	files := 0
	for _, root := range []string{"testdata", dealProfileDir} {
		require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".json")) {
				return err
			}
			page, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, gateElementExpression.FindAllSubmatchIndex(page, -1), gateElements(page), path)
			files++
			return nil
		}))
	}
	require.Positive(t, files)
}

func FuzzGateElements(f *testing.F) {
	r := rand.New(rand.NewPCG(1040, 2))
	for range 64 {
		f.Add(randomGatePage(r))
	}
	f.Fuzz(func(t *testing.T, page []byte) {
		require.Equal(t, gateElementExpression.FindAllSubmatchIndex(page, -1), gateElements(page), "%q", page)
	})
}
