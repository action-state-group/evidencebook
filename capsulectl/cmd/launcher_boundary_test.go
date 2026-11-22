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
// other's package (evidencebook#610).
func TestLaunchersDoNotLinkEachOther(t *testing.T) {
	const module = "github.com/action-state-group/evidencebook/capsulectl/"
	deps := func(pkg string) string {
		t.Helper()
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	book := deps(module + "cmd/capsulectl-book")
	deal := deps(module + "cmd/capsulectl-deal")
	assert.True(t, strings.Contains(book, module+"internal/book"), "the book launcher links the book commands")
	assert.False(t, strings.Contains(book, module+"internal/deal"), "the book launcher must not link the deal package")
	assert.True(t, strings.Contains(deal, module+"internal/deal"), "the deal launcher links the deal commands")
	assert.False(t, strings.Contains(deal, module+"internal/book"), "the deal launcher must not link the book package")
}
