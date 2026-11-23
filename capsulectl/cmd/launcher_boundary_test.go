package cmd_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two launchers share one module but not their process-global hooks: the
// deal build installs bundle extensions, the verify rule, the error phase and
// wording from an init(), and the book build must stay identical to what it
// was before the deal commands moved here. Neither launcher may link the
// other's package (evidencebook#610). The one thing they share is the deal
// view's files and module (internal/deal/assets: embeds and a builder, no
// init, no hook), which the book launcher puts in a settlement report that
// carries deal receipts (evidencebook#764).
func TestLaunchersDoNotLinkEachOther(t *testing.T) {
	const module = "github.com/action-state-group/evidencebook/capsulectl/"
	deps := func(pkg string) string {
		t.Helper()
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	// linked says whether the list of dependencies holds exactly pkg.
	linked := func(list, pkg string) bool {
		for _, line := range strings.Split(list, "\n") {
			if line == pkg {
				return true
			}
		}
		return false
	}
	book := deps(module + "cmd/capsulectl-book")
	deal := deps(module + "cmd/capsulectl-deal")
	assert.True(t, strings.Contains(book, module+"internal/book"), "the book launcher links the book commands")
	assert.False(t, linked(book, module+"internal/deal"), "the book launcher must not link the deal package")
	assert.True(t, linked(book, module+"internal/deal/assets"), "the book launcher links the deal view's files, to carry deal receipts")
	assert.True(t, linked(deal, module+"internal/deal"), "the deal launcher links the deal commands")
	assert.False(t, strings.Contains(deal, module+"internal/book"), "the deal launcher must not link the book package")
}
