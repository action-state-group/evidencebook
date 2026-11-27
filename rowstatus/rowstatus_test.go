// SPDX-License-Identifier: Apache-2.0

package rowstatus

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type vectors struct {
	Cases []struct {
		Name   string          `json:"name"`
		Input  json.RawMessage `json:"input"`
		Expect Result          `json:"expect"`
	} `json:"cases"`
	Invalid []struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"invalid"`
}

func load(t *testing.T) vectors {
	t.Helper()
	b, err := os.ReadFile("../testdata/row-status/vectors.json")
	require.NoError(t, err)
	var v vectors
	require.NoError(t, json.Unmarshal(b, &v))
	require.NotEmpty(t, v.Cases)
	return v
}

func TestInvalidVectors(t *testing.T) {
	v := load(t)
	require.NotEmpty(t, v.Invalid)
	for _, c := range v.Invalid {
		t.Run(c.Name, func(t *testing.T) {
			in, err := ParseInput(c.Input)
			if err == nil {
				_, err = Compute(in)
			}
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestRepeatedMemberRefused(t *testing.T) {
	_, err := ParseInput([]byte(`{"at": "2026-11-02T00:00:00Z", "at": "2026-11-03T00:00:00Z", "parties": ["a", "b"], "row": {}}`))
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = ParseInput([]byte(`{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}} {}`))
	assert.ErrorIs(t, err, ErrInvalid, "trailing data")
}

func TestRawRefused(t *testing.T) {
	for _, raw := range []string{
		`{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}}]`,
		`{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}}}`,
		`{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}, "named": {"x": {"latest": "2026-11-02T00:00:00Z"}, "x": {"latest": "2026-11-03T00:00:00Z"}}}`,
		`{"at": "2026-11-02T00:00:00Z", "parties": ["\ud800", "b"], "row": {}}`,
		`{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {"id": "\udc00"}}`,
		"{\"at\": \"2026-11-02T00:00:00Z\", \"parties\": [\"a\xff\", \"b\"], \"row\": {}}",
		`{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}, "named": {"\ud800": {}}}`,
	} {
		_, err := ParseInput([]byte(raw))
		assert.ErrorIs(t, err, ErrInvalid, raw)
	}
	_, err := ParseInput([]byte(`{"at": "2026-11-02T00:00:00Z", "parties": ["\ud83d\ude00", "b"], "row": {}}`))
	assert.NoError(t, err, "a surrogate pair is a character")
}

func TestLateVectors(t *testing.T) {
	b, err := os.ReadFile("../testdata/row-status/late.json")
	require.NoError(t, err)
	var v struct {
		Cases []struct {
			Name   string          `json:"name"`
			Input  json.RawMessage `json:"input"`
			Expect Late            `json:"expect"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(b, &v))
	require.NotEmpty(t, v.Cases)
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			in, err := ParseInput(c.Input)
			require.NoError(t, err)
			got, err := ComputeLate(in)
			require.NoError(t, err)
			assert.Equal(t, c.Expect, got)
		})
	}
}

// TestLateLeavesResults checks that the late output is beside Compute's: every
// status vector's result is the same, and ComputeLate refuses what Compute
// refuses.
func TestLateLeavesResults(t *testing.T) {
	v := load(t)
	for _, c := range v.Cases {
		in, err := ParseInput(c.Input)
		require.NoError(t, err)
		_, err = ComputeLate(in)
		require.NoError(t, err, c.Name)
		got, err := Compute(in)
		require.NoError(t, err)
		assert.Equal(t, c.Expect, got, c.Name)
	}
	for _, c := range v.Invalid {
		in, err := ParseInput(c.Input)
		if err == nil {
			_, err = ComputeLate(in)
		}
		assert.ErrorIs(t, err, ErrInvalid, c.Name)
	}
}
