// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// externalCheckInputSchema compiles external-check-input-v0.schema.json as it
// ships beside PROFILE.md.
func externalCheckInputSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("x-deal-v0", "external-check-input-v0.schema.json"))
	require.NoError(t, err)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	require.NoError(t, err)
	c := jsonschema.NewCompiler()
	require.NoError(t, c.AddResource("external-check-input-v0.schema.json", doc))
	s, err := c.Compile("external-check-input-v0.schema.json")
	require.NoError(t, err)
	return s
}

// TestExternalCheckInputRulesPin holds the optional rules_pin member (#230) to
// the acting role's pack and digests: an input without it stays valid, and any
// other shape of it is refused.
func TestExternalCheckInputRulesPin(t *testing.T) {
	s := externalCheckInputSchema(t)
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	input := func(pin string) string {
		in := `{"schema":"external-check-input/v0","party_role":"seller",` +
			`"record":{"capsule_id":"c1","agent_input":{}},"history":[],` +
			`"history_scope":{"days":31,"max_records":1000,"complete":true}`
		if pin != "" {
			in += `,"rules_pin":` + pin
		}
		return in + `}`
	}
	valid := map[string]string{
		"absent":       "",
		"pack only":    `{"pack":"asg/seller"}`,
		"digests only": `{"definition_digests":["` + digestA + `"]}`,
		"both":         `{"pack":"asg/seller","definition_digests":["` + digestA + `","` + digestB + `"]}`,
	}
	invalid := map[string]string{
		"null":             `null`,
		"empty":            `{}`,
		"extra member":     `{"pack":"asg/seller","role":"seller"}`,
		"single digest":    `{"definition_digest":"` + digestA + `"}`,
		"empty pack":       `{"pack":""}`,
		"empty digests":    `{"definition_digests":[]}`,
		"repeated digest":  `{"definition_digests":["` + digestA + `","` + digestA + `"]}`,
		"uppercase digest": `{"definition_digests":["` + strings.Repeat("A", 64) + `"]}`,
		"short digest":     `{"definition_digests":["abc"]}`,
		"digests as one":   `{"definition_digests":"` + digestA + `"}`,
	}
	for name, pin := range valid {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(input(pin)))
		require.NoError(t, err, name)
		assert.NoError(t, s.Validate(doc), name)
	}
	for name, pin := range invalid {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(input(pin)))
		require.NoError(t, err, name)
		assert.Error(t, s.Validate(doc), name)
	}
}
