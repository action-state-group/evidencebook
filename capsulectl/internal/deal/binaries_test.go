package deal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// coreDir is the capsule-cli module this plugin builds against, as go
// resolves it: its files are read, never written.
var coreDir = sync.OnceValue(func() string {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/action-state-group/capsule-cli").Output()
	dir := strings.TrimSpace(string(out))
	if err != nil || dir == "" {
		panic(fmt.Sprintf("go list -m github.com/action-state-group/capsule-cli: %q, %v", dir, err))
	}
	return dir
})

// installed is capsulectl with the deal plugin installed, as an operator has
// it: the core binary in dir, and capsulectl-deal in
// dir/plugins, a trusted plugin root. They are built once per test binary.
var installed struct {
	once sync.Once
	dir  string
	err  error
	out  string
}

// installedCapsulectl builds the binaries once and returns the directory
// holding capsulectl. It points CAPSULECTL_PLUGIN_ROOTS at the plugins, so a
// capsulectl the test runs finds them.
func installedCapsulectl(t *testing.T) string {
	t.Helper()
	dir, err := buildInstalled()
	require.NoError(t, err, installed.out)
	t.Setenv("CAPSULECTL_PLUGIN_ROOTS", filepath.Join(dir, "plugins"))
	return dir
}

// buildInstalled builds the core and the deal plugin once per test run.
func buildInstalled() (string, error) {
	installed.once.Do(func() {
		installed.dir, installed.err = os.MkdirTemp("", "capsulectl-bin-")
		if installed.err != nil {
			return
		}
		plugins := filepath.Join(installed.dir, "plugins")
		if installed.err = os.Mkdir(plugins, 0o700); installed.err != nil {
			return
		}
		for path, pkg := range map[string]string{
			filepath.Join(installed.dir, "capsulectl"): "github.com/action-state-group/capsule-cli/cmd/capsulectl",
			filepath.Join(plugins, "capsulectl-deal"):  "github.com/action-state-group/evidencebook/capsulectl/cmd/capsulectl-deal",
		} {
			out, err := exec.Command("go", "build", "-o", path, pkg).CombinedOutput()
			if err != nil {
				installed.err, installed.out = err, string(out)
				return
			}
		}
	})
	return installed.dir, installed.err
}

// builtPluginVerify is selfVerifyBinary in tests: a page's verify run is the
// built plugin's, not the test binary's (which would run the tests).
func builtPluginVerify() (string, error) {
	dir, err := buildInstalled()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, installed.out)
	}
	return filepath.Join(dir, "plugins", "capsulectl-deal"), nil
}

// removeInstalled deletes the built binaries; TestMain calls it.
func removeInstalled() {
	if installed.dir != "" {
		_ = os.RemoveAll(installed.dir)
	}
	if everydayEnginePath != "" {
		_ = os.RemoveAll(filepath.Dir(everydayEnginePath))
	}
}
