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

// TestExternalCheckResultNotice holds the optional notice member to one line
// of text: a result without it stays valid.
func TestExternalCheckResultNotice(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("x-deal-v0", "external-check-result-v0.schema.json"))
	require.NoError(t, err)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	require.NoError(t, err)
	c := jsonschema.NewCompiler()
	require.NoError(t, c.AddResource("external-check-result-v0.schema.json", doc))
	s, err := c.Compile("external-check-result-v0.schema.json")
	require.NoError(t, err)
	result := func(notice string) string {
		r := `{"schema":"external-check-result/v0","ruleset_id":"asg/x","definition_digest":"` + strings.Repeat("a", 64) + `","verdict":"allow","findings":[]`
		if notice != "" {
			r += `,"notice":` + notice
		}
		return r + `}`
	}
	valid := map[string]string{
		"absent":    "",
		"a line":    `{"line":"ASK: the card names a different counterparty"}`,
		"500 chars": `{"line":"` + strings.Repeat("a", 500) + `"}`,
		"non-ASCII": `{"line":"Prüfung: kein Treffer"}`,
	}
	invalid := map[string]string{
		"null":                `null`,
		"a string":            `"ASK"`,
		"no line":             `{}`,
		"empty line":          `{"line":""}`,
		"line break":          `{"line":"a\nb"}`,
		"carriage return":     `{"line":"a\rb"}`,
		"escape":              `{"line":"a\u001b[31mb"}`,
		"C1 control":          `{"line":"a\u0085b"}`,
		"line separator":      `{"line":"a\u2028b"}`,
		"paragraph separator": `{"line":"a\u2029b"}`,
		"extra member":        `{"line":"a","decision":"ask"}`,
		"too long":            `{"line":"` + strings.Repeat("a", 501) + `"}`,
	}
	for name, notice := range valid {
		d, err := jsonschema.UnmarshalJSON(strings.NewReader(result(notice)))
		require.NoError(t, err, name)
		assert.NoError(t, s.Validate(d), name)
	}
	for name, notice := range invalid {
		d, err := jsonschema.UnmarshalJSON(strings.NewReader(result(notice)))
		require.NoError(t, err, name)
		assert.Error(t, s.Validate(d), name)
	}
}
