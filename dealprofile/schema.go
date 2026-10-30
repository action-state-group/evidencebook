// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"bytes"
	"embed"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The profile's schemas, as they ship in x-deal-v0/.
//
//go:embed x-deal-v0/x-deal-v0.schema.json x-deal-v0/records/*.schema.json
var schemaFiles embed.FS

const (
	profileSchemaPath = "x-deal-v0/x-deal-v0.schema.json"
	recordsDir        = "x-deal-v0/records"
	recordsBase       = "https://agentactioncapsule.org/records/"
)

var (
	profileSchemaOnce sync.Once
	profileSchemaC    *jsonschema.Schema
	profileSchemaErr  error

	recordSchemasOnce sync.Once
	recordSchemas     map[string]*jsonschema.Schema
	recordSchemasErr  error
)

// recordSchemaTypes are the typed records and contract types the records
// schemas define.
var recordSchemaTypes = []string{
	TypeTaskAuthority, TypeProposedAction, TypeActionEvaluation, TypeActionApproval, TypeActionRecord,
	TypeActionOutcome, TypeActionReport, TypeCheckRequest, TypeCheckResponse, TypeCommercialBounds,
}

// ProfileSchema is the raw x-deal-v0 record schema.
func ProfileSchema() []byte {
	raw, _ := schemaFiles.ReadFile(profileSchemaPath) // embedded, always present
	return raw
}

func profileSchema() (*jsonschema.Schema, error) {
	profileSchemaOnce.Do(func() { profileSchemaC, profileSchemaErr = ProfileSchemaAt("") })
	return profileSchemaC, profileSchemaErr
}

// ProfileSchemaAt compiles the x-deal-v0 schema at fragment ("" for the
// record schema itself, "#/$defs/<name>" for one definition).
func ProfileSchemaAt(fragment string) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(ProfileSchema()))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	const id = "x-deal-v0.schema.json"
	if err = c.AddResource(id, doc); err != nil {
		return nil, err
	}
	return c.Compile(id + fragment)
}

// RecordSchema is the compiled schema of a typed record or contract type
// (PROFILE.md §9), such as "task-authority/v0".
func RecordSchema(typeName string) (*jsonschema.Schema, error) {
	recordSchemasOnce.Do(func() { recordSchemas, recordSchemasErr = compileRecordSchemas() })
	if recordSchemasErr != nil {
		return nil, recordSchemasErr
	}
	s, ok := recordSchemas[typeName]
	if !ok {
		return nil, fmt.Errorf("unknown record type %q", typeName)
	}
	return s, nil
}

func compileRecordSchemas() (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	entries, err := schemaFiles.ReadDir(recordsDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		raw, err := schemaFiles.ReadFile(path.Join(recordsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if err = c.AddResource(recordsBase+e.Name(), doc); err != nil {
			return nil, err
		}
	}
	out := map[string]*jsonschema.Schema{}
	for _, t := range recordSchemaTypes {
		s, err := c.Compile(recordsBase + strings.ReplaceAll(t, "/", "-") + ".schema.json")
		if err != nil {
			return nil, err
		}
		out[t] = s
	}
	return out, nil
}
