// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"
	"testing"
)

const cllModule = "github.com/action-state-group/cll-go"

// checkedPackage type-checks this package's non-test sources.
func checkedPackage(t *testing.T) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("github.com/action-state-group/evidencebook", fset, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// apiSurface visits every type reachable from the exported API: exported
// functions, exported types (their fields, methods and interface methods),
// and exported variables and constants. Unexported named types of this
// package reached from the API are followed into.
type apiSurface struct {
	pkg     *types.Package
	seen    map[types.Type]bool
	visit   func(where string, t *types.Named)
	params  func(where string, sig *types.Signature)
	current string
}

func (a *apiSurface) walk(t types.Type) {
	if t == nil || a.seen[t] {
		return
	}
	a.seen[t] = true
	switch t := t.(type) {
	case *types.Named:
		a.visit(a.current, t)
		if t.Obj().Pkg() == a.pkg && !t.Obj().Exported() {
			a.walk(t.Underlying())
		}
		for i := range t.TypeArgs().Len() {
			a.walk(t.TypeArgs().At(i))
		}
	case *types.Alias:
		a.walk(types.Unalias(t))
	case *types.Pointer:
		a.walk(t.Elem())
	case *types.Slice:
		a.walk(t.Elem())
	case *types.Array:
		a.walk(t.Elem())
	case *types.Map:
		a.walk(t.Key())
		a.walk(t.Elem())
	case *types.Chan:
		a.walk(t.Elem())
	case *types.Signature:
		if a.params != nil {
			a.params(a.current, t)
		}
		for i := range t.Params().Len() {
			a.walk(t.Params().At(i).Type())
		}
		for i := range t.Results().Len() {
			a.walk(t.Results().At(i).Type())
		}
	case *types.Struct:
		for i := range t.NumFields() {
			if field := t.Field(i); field.Exported() || field.Embedded() {
				a.walk(field.Type())
			}
		}
	case *types.Interface:
		for i := range t.NumMethods() {
			if method := t.Method(i); method.Exported() {
				a.walk(method.Type())
			}
		}
	}
}

func (a *apiSurface) run() {
	scope := a.pkg.Scope()
	names := scope.Names()
	sort.Strings(names)
	for _, name := range names {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		a.current = name
		a.seen = make(map[types.Type]bool)
		a.walk(obj.Type())
		if named, ok := obj.Type().(*types.Named); ok {
			a.walk(named.Underlying())
			for i := range named.NumMethods() {
				if method := named.Method(i); method.Exported() {
					a.current = name + "." + method.Name()
					a.walk(method.Type())
				}
			}
			for _, recv := range []types.Type{named, types.NewPointer(named)} {
				methods := types.NewMethodSet(recv)
				for i := range methods.Len() {
					if fn := methods.At(i).Obj(); fn.Exported() {
						a.current = name + "." + fn.Name()
						a.walk(fn.Type())
					}
				}
			}
		}
	}
}

// TestNoPublicAPIExposesACLLType: the book embeds CLL, and nothing a caller
// can name returns or accepts a CLL type. The check follows exported
// functions, methods, interface methods, struct fields and type aliases, and
// descends into unexported types of this package that the API reaches.
func TestNoPublicAPIExposesACLLType(t *testing.T) {
	pkg := checkedPackage(t)
	var leaks []string
	surface := &apiSurface{pkg: pkg, visit: func(where string, named *types.Named) {
		if p := named.Obj().Pkg(); p != nil && (p.Path() == cllModule || strings.HasPrefix(p.Path(), cllModule+"/")) {
			leaks = append(leaks, where+" -> "+p.Path()+"."+named.Obj().Name())
		}
	}}
	surface.run()
	if len(leaks) > 0 {
		t.Fatalf("public API exposes CLL types:\n%s", strings.Join(leaks, "\n"))
	}
	book, ok := pkg.Scope().Lookup("Book").Type().Underlying().(*types.Struct)
	if !ok {
		t.Fatal("Book is not a struct")
	}
	for i := range book.NumFields() {
		if field := book.Field(i); field.Name() == "commitments" && field.Exported() {
			t.Fatal("Book.commitments must stay unexported")
		}
	}
}

// TestDiscoveryCandidatesNeverReachProofs: a DiscoveryIndex result is
// approximate and must never become proof input. Enforced by type: no
// exported function or method outside Candidates' own methods accepts a
// Candidates, and Candidates has no exported fields to rebuild one from.
func TestDiscoveryCandidatesNeverReachProofs(t *testing.T) {
	pkg := checkedPackage(t)
	candidates := pkg.Scope().Lookup("Candidates").Type()
	var accepts []string
	var mentions func(t types.Type, seen map[types.Type]bool) bool
	mentions = func(t types.Type, seen map[types.Type]bool) bool {
		if seen[t] {
			return false
		}
		seen[t] = true
		if types.Identical(t, candidates) {
			return true
		}
		switch t := t.(type) {
		case *types.Pointer:
			return mentions(t.Elem(), seen)
		case *types.Slice:
			return mentions(t.Elem(), seen)
		case *types.Map:
			return mentions(t.Key(), seen) || mentions(t.Elem(), seen)
		case *types.Named:
			return t.Obj().Pkg() == pkg && mentions(t.Underlying(), seen)
		case *types.Struct:
			for i := range t.NumFields() {
				if mentions(t.Field(i).Type(), seen) {
					return true
				}
			}
		}
		return false
	}
	surface := &apiSurface{pkg: pkg, visit: func(string, *types.Named) {}, params: func(where string, sig *types.Signature) {
		if strings.HasPrefix(where, "Candidates.") {
			return
		}
		for i := range sig.Params().Len() {
			if mentions(sig.Params().At(i).Type(), map[types.Type]bool{}) {
				accepts = append(accepts, where)
			}
		}
	}}
	surface.run()
	if len(accepts) > 0 {
		t.Fatalf("these accept discovery Candidates and could turn a candidate into proof input:\n%s", strings.Join(accepts, "\n"))
	}
	fields, ok := candidates.Underlying().(*types.Struct)
	if !ok {
		t.Fatal("Candidates is not a struct")
	}
	for i := range fields.NumFields() {
		if fields.Field(i).Exported() {
			t.Fatalf("Candidates.%s is exported; candidates must not be constructible by callers", fields.Field(i).Name())
		}
	}
}
