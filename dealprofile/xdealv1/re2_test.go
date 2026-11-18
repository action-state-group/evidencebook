// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The validator reads a schema pattern as RE2 (evidencebook#546): the
// platform pattern's \s takes a tab but not a no-break space or a vertical
// tab. The Rust crate (tests/deal_v1_vectors.rs) and the Python package
// (tests/test_deal_v1.py) hold the same answers.
func TestSchemaPatternWhiteSpaceIsRE2(t *testing.T) {
	record := func(platform string) []byte {
		b, err := json.Marshal(map[string]any{
			"x-deal-v1": map[string]any{"profile": "x-deal-v1", "canonicalization": "jcs", "record_type": "sale_unpost",
				"at": "2026-11-01T10:00:00Z"},
			"body": map[string]any{"platform": platform},
		})
		require.NoError(t, err)
		return b
	}
	for _, p := range []string{"shop x.example", "a\vb.example", "market.test"} {
		assert.NoError(t, Check(record(p)), "%q", p)
	}
	var se *StageError
	require.ErrorAs(t, Check(record("a\tb.example")), &se)
	assert.Equal(t, StageSchema, se.Stage)
	require.ErrorAs(t, Check(record("market.test\n")), &se)
	assert.Equal(t, StageSchema, se.Stage, "a pattern's $ is the end of the text")
}
