// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// portParityPackages are the Go packages whose exported APIs docs/PORT-PARITY.tsv
// covers. The adapter packages are Go-only carriage run by cmd/evidencebook.
var portParityPackages = []string{".", "dealprofile", "dealprofile/xdealv1", "rowstatus"}

var portParityStatus = regexp.MustCompile(`^(ported|not needed|(frozen|needed-by-mesh) #[1-9][0-9]*)$`)

// exportedAPIs lists the exported top-level funcs, types and vars of the
// non-test Go files in dir as "package<TAB>kind<TAB>symbol" keys. Constants and
// methods belong to their type's or feature's row.
func exportedAPIs(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	out := map[string]bool{}
	add := func(kind, name string) { out[dir+"\t"+kind+"\t"+name] = true }
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.IsExported() {
						add("func", d.Name.Name)
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if s.Name.IsExported() {
								add("type", s.Name.Name)
							}
						case *ast.ValueSpec:
							if d.Tok != token.VAR {
								continue
							}
							for _, n := range s.Names {
								if n.IsExported() {
									add("var", n.Name)
								}
							}
						}
					}
				}
			}
		}
	}
	return out
}

// TestPortParity fails when an exported Go API has no row in
// docs/PORT-PARITY.tsv, when a row names an API Go no longer exports, or when a
// Rust or Python status is not ported, not needed, frozen #N or needed-by-mesh #N.
// A frozen row names its "Port to ..." issue; see docs/PORT-PARITY.md.
func TestPortParity(t *testing.T) {
	data, err := os.ReadFile("docs/PORT-PARITY.tsv")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Equal(t, "package\tkind\tsymbol\tfeature\trust\tpython", lines[0])

	rows := map[string]bool{}
	var keys []string
	for i, line := range lines[1:] {
		f := strings.Split(line, "\t")
		require.Len(t, f, 6, "docs/PORT-PARITY.tsv line %d", i+2)
		key := strings.Join(f[:3], "\t")
		assert.False(t, rows[key], "duplicate row %q", key)
		rows[key] = true
		keys = append(keys, line)
		assert.NotEmpty(t, f[3], "line %d has no feature", i+2)
		assert.Regexp(t, portParityStatus, f[4], "line %d: Rust status", i+2)
		assert.Regexp(t, portParityStatus, f[5], "line %d: Python status", i+2)
	}
	assert.True(t, sort.StringsAreSorted(keys), "rows must stay sorted")

	want := map[string]bool{}
	for _, dir := range portParityPackages {
		for k := range exportedAPIs(t, dir) {
			want[k] = true
		}
	}
	for k := range want {
		assert.True(t, rows[k], "exported Go API has no row in docs/PORT-PARITY.tsv (add %q with a Rust and a Python status): %s", k, strings.ReplaceAll(k, "\t", " "))
	}
	for k := range rows {
		assert.True(t, want[k], "docs/PORT-PARITY.tsv names an API Go no longer exports: %s", strings.ReplaceAll(k, "\t", " "))
	}
}

// TestPortParityNegative pins the check's own failure modes.
func TestPortParityNegative(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/a.go", []byte("package a\nfunc Exported() {}\nfunc hidden() {}\ntype T struct{}\nvar V int\nconst C = 1\nfunc (T) M() {}\n"), 0o600))
	require.NoError(t, os.WriteFile(dir+"/a_test.go", []byte("package a\nfunc TestOnly() {}\n"), 0o600))
	got := exportedAPIs(t, dir)
	assert.Equal(t, map[string]bool{
		dir + "\tfunc\tExported": true,
		dir + "\ttype\tT":        true,
		dir + "\tvar\tV":         true,
	}, got)
	for _, ok := range []string{"ported", "not needed", "frozen #12", "needed-by-mesh #7"} {
		assert.Regexp(t, portParityStatus, ok)
	}
	for _, bad := range []string{"", "frozen", "frozen #", "frozen #0", "TODO", "frozen 12", "ported #3"} {
		assert.NotRegexp(t, portParityStatus, bad)
	}
}
