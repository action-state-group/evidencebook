// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// rawJSONVectors is what Go's encoding/json gives on the texts a sent or
// book pair may hold (testdata/deal-v1/rawjson): json.Valid, checkText,
// strictUnmarshal into a pair, json.Unmarshal into a map of raw members and
// into a struct's capsule_id, and json.Marshal of a map holding the text as
// a raw member. Python's reader (python/evidencebook/deal/v1/rawjson.py,
// goscan.py) is held to it.
const rawJSONVectors = "../../testdata/deal-v1/rawjson/vectors.json"

type rawPair struct {
	Record     map[string]json.RawMessage `json:"record"`
	AgentInput json.RawMessage            `json:"agent_input"`
}

type rawCase struct {
	In string `json:"in,omitempty"` // hex
	// Gen is a deep input written out of its description instead:
	// "array" is depth [ then depth ], "pair" that array as the
	// agent_input of {"record":{}, ...}, "after_string" that array after "".
	Gen       *rawGen        `json:"gen,omitempty"`
	Valid     bool           `json:"valid"`
	CheckText bool           `json:"check_text"`
	Pair      *rawPairResult `json:"pair"`
	Map       *rawMapResult  `json:"map"`
	CapsuleID *string        `json:"capsule_id"`
	Marshal   *string        `json:"marshal"` // hex (sha256: for a long one), nil when refused
}

type rawGen struct {
	Kind  string `json:"kind"`
	Depth int    `json:"depth"`
}

func (g rawGen) text() string {
	a := strings.Repeat("[", g.Depth) + strings.Repeat("]", g.Depth)
	switch g.Kind {
	case "pair":
		return `{"record":{},"agent_input":` + a + `}`
	case "after_string":
		return `""` + a
	}
	return a
}

type rawPairResult struct {
	RecordNil  bool              `json:"record_nil"`
	Record     map[string]string `json:"record,omitempty"`
	AgentInput *string           `json:"agent_input"`
}

type rawMapResult struct {
	Nil     bool              `json:"nil"`
	Members map[string]string `json:"members,omitempty"`
}

func rawStrings(m map[string]json.RawMessage) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = string(v)
	}
	return out
}

func rawJSONCase(in []byte) rawCase {
	c := rawCase{In: hex.EncodeToString(in), Valid: json.Valid(in), CheckText: checkText(in) == nil}
	var p rawPair
	if err := strictUnmarshal(in, &p); err == nil {
		r := &rawPairResult{RecordNil: p.Record == nil, Record: rawStrings(p.Record)}
		if p.AgentInput != nil {
			s := string(p.AgentInput)
			r.AgentInput = &s
		}
		c.Pair = r
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(in, &m); err == nil {
		c.Map = &rawMapResult{Nil: m == nil, Members: rawStrings(m)}
	}
	var id struct {
		CapsuleID string `json:"capsule_id"`
	}
	if err := json.Unmarshal(in, &id); err == nil {
		c.CapsuleID = &id.CapsuleID
	}
	if out, err := json.Marshal(map[string]json.RawMessage{"p": in}); err == nil {
		s := hex.EncodeToString(out)
		if len(out) > 512 {
			sum := sha256.Sum256(out)
			s = "sha256:" + hex.EncodeToString(sum[:])
		}
		c.Marshal = &s
	}
	return c
}

// TestRawJSONVectors writes and checks testdata/deal-v1/rawjson/vectors.json
// (EB_WRITE_DEAL_V1_VECTORS=1): hand-picked texts, nesting at Go's bounds,
// then texts mutated from them by a fixed seed.
func TestRawJSONVectors(t *testing.T) {
	inputs := []string{
		`{}`, `[]`, `null`, `""`, `1`, `-0`, `1.5e3`, `1e400`, `-1e400`, `1e308`, `true`, `nul`, ``, ` `, `{"a":1}`,
		`{"a":1,"a":2}`, `{"a":1,"A":2}`, `{"a":1,}`, `[1,]`, `{"a" 1}`, `{"a":1} `, `{"a":1}x`, `{"a":1} {}`, `{"a":1}]`,
		`{"a":1}}`, `"a"x`, `1x`, `1 x`, `{"record":{"capsule_id":"c1","x":[1,2]},"agent_input":{"b":"<&>"}}`,
		`{"record":{"a":1},"Record":{"b":2},"agent_input":1}`, `{"record":{"a":1},"RECORD":null,"agent_input":1}`,
		`{"record":5,"Record":{"a":1},"agent_input":1}`, `{"record":{},"agent_input":null}`, `{"record":{}}`,
		`{"record":{},"agent_input":1,"x":2}`, `{"capsule_id":"c1"}`, `{"capsule_id":5,"capsule_id":"c1"}`,
		`{"capsule_id":"c1","Capsule_ID":null}`, `{"Capsule_ID":"c2"}`, `{"capsule_id":"cé","x":" "}`,
		"{\"s\":\"  \"}", `{"s":"\ud800"}`, `{"s":"\x"}`, `{"s":"\u12"}`, "{\"s\":\"a\tb\"}", "\xef\xbb\xbf{}",
		"{\"s\":\"\xff\"}", `{"a":[{"b":[]},{}],"c":{"d":{}}}`, `  {"a" : [ 1 , 2 ] }  `, `{"a":01}`, `{"a":1.}`, `{"a":.5}`,
		`{"a":1e}`, `{"a":-}`, `{"a":tru}`, `{"a":"x"`, `{"a":`, `[[[`, `]`, `{"a":1}{"b":2}`,
	}
	alphabet := []byte("{}[]\":,0123456789.eE+-tfnul \\\"aR<&>\xe2\x80\xa8\xff")
	r := rand.New(rand.NewPCG(552, 461))
	bases := inputs
	for i := 0; i < 800; i++ {
		s := []byte(bases[r.IntN(len(bases))])
		for n := r.IntN(4); n > 0; n-- {
			j := r.IntN(len(s) + 1)
			switch op := r.Float64(); {
			case op < 0.4 && len(s) > 0:
				s[min(j, len(s)-1)] = alphabet[r.IntN(len(alphabet))]
			case op < 0.7:
				s = append(s[:j], append([]byte{alphabet[r.IntN(len(alphabet))]}, s[j:]...)...)
			case len(s) > 0:
				k := min(j, len(s)-1)
				s = append(s[:k], s[k+1:]...)
			}
		}
		inputs = append(inputs, string(s))
	}
	var cases []rawCase
	for _, in := range inputs {
		cases = append(cases, rawJSONCase([]byte(in)))
	}
	for _, kind := range []string{"array", "pair", "after_string"} {
		for _, n := range []int{9998, 9999, 10000, 10001} {
			g := rawGen{Kind: kind, Depth: n}
			c := rawJSONCase([]byte(g.text()))
			c.In, c.Gen = "", &g
			cases = append(cases, c)
		}
	}
	out, err := json.MarshalIndent(map[string]any{
		"description": "Go's encoding/json on texts a sent or book pair may hold (evidencebook#461): each input (hex) with json.Valid, checkText, strictUnmarshal into {record map[string]json.RawMessage, agent_input json.RawMessage} (null when refused), json.Unmarshal into map[string]json.RawMessage (null when refused) and into struct{CapsuleID string} (null when refused), and json.Marshal(map[string]json.RawMessage{\"p\": in}) as hex (null when refused). Hand-picked texts, nesting at Go's bounds, then texts mutated by a fixed seed. Written by dealprofile/xdealv1's TestRawJSONVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", " ")
	require.NoError(t, err)
	out = append(out, '\n')
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll("../../testdata/deal-v1/rawjson", 0o755))
		require.NoError(t, os.WriteFile(rawJSONVectors, out, 0o644))
	}
	want, err := os.ReadFile(rawJSONVectors)
	require.NoError(t, err)
	require.True(t, bytes.Equal(want, out), "%s differs; rerun with EB_WRITE_DEAL_V1_VECTORS=1", rawJSONVectors)
}
