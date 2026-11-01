// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/chain-vectors.json is the reference checker's chain-stage verdict
// (check_profile.py --chain-vectors) over every prefix of each positive chain,
// each negative fixture's chain, and single-member mutations of the positive
// chains. CheckChain must give the same refusal with the same text. CI
// regenerates the file from the checker and fails on a difference, and the
// Rust crate's tests read it too.

type chainCase struct {
	Name  string `json:"name"`
	Stage string `json:"stage"`
	Error string `json:"error"`
	// A mutation case: the positive chain Chain with the member at Path of
	// record Index deleted or set to Value, and every later record re-linked.
	Chain string `json:"chain"`
	Index int    `json:"index"`
	Path  []any  `json:"path"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

func chainFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("x-deal-v0", "fixtures", dir, "*.json"))
	require.NoError(t, err)
	sort.Strings(files)
	return files
}

func readRecord(t *testing.T, path string) json.RawMessage {
	t.Helper()
	var f struct {
		Record json.RawMessage `json:"record"`
	}
	readJSON(t, path, &f)
	return f.Record
}

func chainOf(t *testing.T, name string) []any {
	t.Helper()
	dir, rest, _ := strings.Cut(name, "/")
	var recs []any
	if dir == "negative" {
		var f struct {
			Record       json.RawMessage   `json:"record"`
			ChainDir     string            `json:"chain_dir"`
			ChainPrefix  []string          `json:"chain_prefix"`
			ChainBetween []json.RawMessage `json:"chain_between"`
		}
		readJSON(t, filepath.Join("x-deal-v0", "fixtures", dir, rest), &f)
		if f.ChainDir == "" {
			f.ChainDir = "positive"
		}
		for _, n := range f.ChainPrefix {
			recs = append(recs, readRecord(t, filepath.Join("x-deal-v0", "fixtures", f.ChainDir, n)))
		}
		for _, r := range f.ChainBetween {
			recs = append(recs, r)
		}
		return append(recs, f.Record)
	}
	n, err := strconv.Atoi(rest)
	require.NoError(t, err)
	for _, p := range chainFiles(t, dir)[:n] {
		recs = append(recs, readRecord(t, p))
	}
	return recs
}

// asJSONNumber reads a vector value as a record reads it: numbers kept as
// written.
func asJSONNumber(v any) any {
	if f, ok := v.(float64); ok {
		return json.Number(strconv.FormatInt(int64(f), 10))
	}
	return v
}

// mutated is a mutation case's chain: the mutation applied to one record, and
// each later record re-pointed at the digests the mutation changed (the
// checker's _relink), so the rules after it read it.
func mutated(t *testing.T, c chainCase) []any {
	t.Helper()
	var docs []any
	for _, r := range chainOf(t, c.Chain+"/"+strconv.Itoa(len(chainFiles(t, c.Chain)))) {
		d, err := decode(r)
		require.NoError(t, err)
		docs = append(docs, d)
	}
	old, err := Digest(docs[c.Index])
	require.NoError(t, err)
	node := docs[c.Index]
	for _, step := range c.Path[:len(c.Path)-1] {
		if m, ok := node.(map[string]any); ok {
			node = m[step.(string)]
		} else {
			node = node.([]any)[int(step.(float64))]
		}
	}
	switch last, m := c.Path[len(c.Path)-1], node; {
	case c.Op == "delete":
		delete(m.(map[string]any), last.(string))
	default:
		if mm, ok := m.(map[string]any); ok {
			mm[last.(string)] = asJSONNumber(c.Value)
		} else {
			m.([]any)[int(last.(float64))] = asJSONNumber(c.Value)
		}
	}
	mapping := map[string]string{}
	nd, err := Digest(docs[c.Index])
	require.NoError(t, err)
	mapping[old] = nd
	var sub func(v any) any
	sub = func(v any) any {
		switch x := v.(type) {
		case string:
			if m, ok := mapping[x]; ok {
				return m
			}
		case []any:
			for i := range x {
				x[i] = sub(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = sub(x[k])
			}
		}
		return v
	}
	for i := c.Index + 1; i < len(docs); i++ {
		od, err := Digest(docs[i])
		require.NoError(t, err)
		sub(docs[i])
		mapping[od], err = Digest(docs[i])
		require.NoError(t, err)
	}
	return docs
}

func TestChainVectors(t *testing.T) {
	var v struct {
		Cases []chainCase `json:"cases"`
	}
	readJSON(t, filepath.Join("testdata", "chain-vectors.json"), &v)
	require.NotEmpty(t, v.Cases)
	refused := 0
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var recs []any
			if c.Chain != "" {
				recs = mutated(t, c)
			} else {
				recs = chainOf(t, c.Name)
			}
			err := CheckChain(recs)
			if c.Stage == "" {
				assert.NoError(t, err)
				return
			}
			refused++
			var se *StageError
			require.True(t, errors.As(err, &se), "%v", err)
			assert.Equal(t, c.Stage, se.Stage)
			assert.Equal(t, c.Error, se.Detail)
		})
	}
	assert.Greater(t, refused, 200, "the vectors hold the chain stage's refusals")
}

func TestChainUnreadableRecord(t *testing.T) {
	err := CheckChain([]any{[]byte(`[]`)})
	require.Error(t, err)
	assert.Equal(t, "[chain] seq position 1: a record is a JSON object", err.Error())
}

// An integer written -0 passes the record-level stages and is the integer 0
// to the reference checker's parser, so the chain rules read it as 0: the
// baseline asks max_total_minor -0 and the task authority carries 0.
func TestChainNegativeZeroIsZero(t *testing.T) {
	const dir = "positive-typed"
	var docs []any
	for _, r := range chainOf(t, dir+"/"+strconv.Itoa(len(chainFiles(t, dir)))) {
		d, err := decode(r)
		require.NoError(t, err)
		docs = append(docs, d)
	}
	digests := make([]string, len(docs))
	for i, d := range docs {
		var err error
		digests[i], err = Digest(d)
		require.NoError(t, err)
	}
	docs[0].(map[string]any)["body"].(map[string]any)["intent"].(map[string]any)["max_total_minor"] = json.Number("-0")
	docs[1].(map[string]any)["body"].(map[string]any)["max_total_minor"] = json.Number("0")
	mapping := map[string]string{}
	var sub func(v any)
	sub = func(v any) {
		switch x := v.(type) {
		case []any:
			for i := range x {
				if s, ok := x[i].(string); ok && mapping[s] != "" {
					x[i] = mapping[s]
				}
				sub(x[i])
			}
		case map[string]any:
			for k := range x {
				if s, ok := x[k].(string); ok && mapping[s] != "" {
					x[k] = mapping[s]
				}
				sub(x[k])
			}
		}
	}
	for i := range docs {
		sub(docs[i])
		var err error
		mapping[digests[i]], err = Digest(docs[i])
		require.NoError(t, err)
	}
	assert.NoError(t, CheckChain(docs))
}

func TestChainEdgeInputs(t *testing.T) {
	err := CheckChain(nil)
	require.Error(t, err)
	assert.Equal(t, "[chain] a record the chain rules cannot read: no records", err.Error())
	_, ok := validUTC("0000-01-01T00:00:00Z")
	assert.False(t, ok)
	_, ok = validUTC("2026-02-29T00:00:00Z")
	assert.False(t, ok)
	_, ok = validUTC("2028-02-29T23:59:59Z")
	assert.True(t, ok)
}
