// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/require"
)

// typesTable is the vendored copy of TestTypesTable's output: each struct of
// an x-deal-v1 record in field order, with each member's JSON name, Go kind
// and omitzero tag. The Python package reads a record as encoding/json
// reads it into these types.
const typesTable = "../../python/evidencebook/deal/_data/x-deal-v1-types.json"

// TestTypesTable checks the vendored types table is this package's types;
// EB_WRITE_TYPES_TABLE=1 rewrites it.
func TestTypesTable(t *testing.T) {
	tableStructs = map[string][]typeField{}
	bodies := map[string]any{}
	for _, rt := range recordTypes {
		bodies[rt] = typeRef(reflect.TypeOf(newBody(rt)).Elem())
	}
	out := map[string]any{
		"block":      typeRef(reflect.TypeFor[Block]()),
		"terms_rows": typeRef(reflect.TypeFor[TermsRows]()),
		"bodies":     bodies,
		// The books a status value and a report are built from, which
		// json.Unmarshal reads (not strictly): Python reads them as it does.
		"status_books": typeRef(reflect.TypeFor[StatusBooks]()),
		"row_books":    typeRef(reflect.TypeFor[RowBooks]()),
		"report_books": typeRef(reflect.TypeFor[ReportBooks]()),
		// The report payload, which VerifyReport reads strictly and writes
		// back: Python checks a payload's shape against it.
		"report": typeRef(reflect.TypeFor[Report]()),
		// A book record's header, which VerifyBook reads with json.Unmarshal.
		"header":  typeRef(reflect.TypeFor[evidencebook.Header]()),
		"structs": tableStructs,
	}
	got, err := json.MarshalIndent(out, "", " ")
	require.NoError(t, err)
	got = append(got, '\n')
	if os.Getenv("EB_WRITE_TYPES_TABLE") != "" {
		require.NoError(t, os.WriteFile(typesTable, got, 0o644))
	}
	want, err := os.ReadFile(typesTable)
	require.NoError(t, err)
	require.True(t, bytes.Equal(want, got), "%s is stale: rerun with EB_WRITE_TYPES_TABLE=1", typesTable)
}

type typeField struct {
	Name     string `json:"name"`
	Type     any    `json:"type"`
	OmitZero bool   `json:"omitzero,omitempty"`
	// OmitEmpty is on the books' fields, which Python reads and never
	// writes, and on the report's.
	OmitEmpty bool `json:"omitempty,omitempty"`
}

var (
	tableStructs map[string][]typeField
	rawType      = reflect.TypeFor[json.RawMessage]()
	nullName     = "Null["
)

func goTypeName(t reflect.Type) string {
	return strings.TrimPrefix(t.PkgPath(), "github.com/action-state-group/evidencebook/") + "." + t.Name()
}

// customKinds are the types with their own UnmarshalJSON a reader of the
// table implements by name: a record, read strictly (Decode), and a status
// time (string, null or {"stamp": n}).
var customKinds = map[reflect.Type]string{
	reflect.TypeFor[Record]():     "record",
	reflect.TypeFor[StatusTime](): "status_time",
}

func typeRef(t reflect.Type) any {
	if k, ok := customKinds[t]; ok {
		return k
	}
	if t.Kind() == reflect.Interface && t.NumMethod() == 0 {
		return "raw"
	}
	if t == rawType {
		return "raw"
	}
	if strings.HasPrefix(t.Name(), nullName) {
		v, _ := t.FieldByName("V")
		return map[string]any{"null": typeRef(v.Type.Elem())}
	}
	// A type with its own JSON, text or zero methods reads and writes
	// otherwise than its kind; the table cannot hold it.
	for _, m := range []string{"MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText", "IsZero"} {
		if _, ok := reflect.PointerTo(t).MethodByName(m); ok {
			panic(fmt.Sprintf("%s has its own %s", t, m))
		}
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int64, reflect.Int:
		return "int64"
	case reflect.Uint64:
		return "uint64"
	case reflect.Pointer:
		return map[string]any{"ptr": typeRef(t.Elem())}
	case reflect.Slice:
		return map[string]any{"slice": typeRef(t.Elem())}
	case reflect.Map:
		switch t.Key().Kind() {
		case reflect.String:
			return map[string]any{"map": typeRef(t.Elem())}
		case reflect.Uint64:
			return map[string]any{"map": typeRef(t.Elem()), "key": "uint64"}
		}
		panic(fmt.Sprintf("a map keyed by %s", t.Key()))
	case reflect.Struct:
		name := goTypeName(t)
		if _, done := tableStructs[name]; !done {
			tableStructs[name] = nil
			tableStructs[name] = typeFields(t)
		}
		return map[string]any{"struct": name}
	}
	panic(fmt.Sprintf("a member of kind %s (%s)", t.Kind(), t))
}

// fields are a struct's JSON members in field order, an embedded struct's
// in its place (Go promotes them). A name given twice is refused: Go's
// dominance rules are not reproduced.
func typeFields(t reflect.Type) []typeField {
	out := []typeField{}
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || !f.IsExported() && !f.Anonymous {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			out = append(out, typeFields(f.Type)...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		for _, o := range strings.Split(opts, ",") {
			if o != "" && o != "omitzero" && o != "omitempty" {
				panic(fmt.Sprintf("%s.%s: tag option %q", t, f.Name, o))
			}
		}
		out = append(out, typeField{Name: name, Type: typeRef(f.Type), OmitZero: strings.Contains(opts, "omitzero"),
			OmitEmpty: strings.Contains(opts, "omitempty")})
	}
	seen := map[string]bool{}
	for _, f := range out {
		if seen[f.Name] {
			panic(fmt.Sprintf("%s: member %q given twice", t, f.Name))
		}
		seen[f.Name] = true
	}
	return out
}
