// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/require"
)

const forkedFinalsVectors = "../../testdata/deal-v1/forked-finals/vectors.json"

type forkedHeld struct {
	Digest   string `json:"digest"`
	Sender   string `json:"sender"`
	LogID    string `json:"log_id"`
	Position uint64 `json:"position"`
	Prev     string `json:"prev"`
	Pair     string `json:"pair"`
}

type forkedCase struct {
	Name string       `json:"name"`
	Key  string       `json:"key"`
	Log  string       `json:"log"`
	Held []forkedHeld `json:"held"`
	Want []string     `json:"want"`
}

// TestForkedFinalsVectors writes and checks testdata/deal-v1/forked-finals
// (EB_WRITE_DEAL_V1_VECTORS=1): TestForkedFinals's case books as held, each
// with the rows forkedFinals gives, for the Python port to replay.
func TestForkedFinalsVectors(t *testing.T) {
	refund := &RulingRemedy{Kind: "refund", Due: "2026-11-20T23:59:59Z"}
	replace := &RulingRemedy{Kind: "replace", Due: "2026-11-20T23:59:59Z"}
	type spec struct {
		name                string
		common, left, right []*RulingBody
		drop                uint64 // a position not held (0 for none)
	}
	specs := []spec{
		{"a-gap-in-a-straight-book", []*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil),
			ruling("delivered", "", ValueMet, false, nil), ruling("as_described", RoleBuyer, ValueMet, true, nil)}, nil, nil, 2},
		{"a-linear-book", []*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil)}, nil, nil, 0},
		{"against-differs", nil, []*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil)},
			[]*RulingBody{ruling("as_described", RoleBuyer, ValueNotMet, true, nil)}, 0},
		{"remedy-differs", nil, []*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, refund)},
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, replace)}, 0},
		{"a-common-first-final", []*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil)},
			[]*RulingBody{ruling("as_described", RoleBuyer, ValueMet, true, nil)},
			[]*RulingBody{ruling("as_described", "", ValueMet, true, nil)}, 0},
		{"one-branch-has-no-final", nil, []*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil)},
			[]*RulingBody{ruling("as_described", RoleBuyer, ValueMet, false, nil)}, 0},
		{"the-same-decision", nil,
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, refund), ruling("delivered", "", ValueMet, false, nil)},
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, refund)}, 0},
	}
	var cases []forkedCase
	for _, s := range specs {
		held, key, log := forkedCaseBook(t, s.common, s.left, s.right)
		var kept []evidencebook.HeldRecord
		for _, h := range held {
			if h.Position != s.drop {
				kept = append(kept, h)
			}
		}
		want, err := forkedFinals(kept, key, log)
		require.NoError(t, err)
		if want == nil {
			want = []string{}
		}
		c := forkedCase{Name: s.name, Key: key, Log: log, Want: want}
		for _, h := range kept {
			c.Held = append(c.Held, forkedHeld{Digest: h.Digest, Sender: h.SenderKey, LogID: h.LogID, Position: h.Position,
				Prev: h.Prev, Pair: hex.EncodeToString(h.Record)})
		}
		cases = append(cases, c)
	}
	out := map[string]any{
		"description": "forkedFinals (design §8, \"A forked case book\"): each case an adjudicator's case book as held (each record's digest, sender, log, position, prev and book pair, as hex) and the rows whose first final rulings conflict. Written by dealprofile/xdealv1 TestForkedFinalsVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	require.NoError(t, err)
	b = append(b, '\n')
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(forkedFinalsVectors), 0o755))
		require.NoError(t, os.WriteFile(forkedFinalsVectors, b, 0o644))
	}
	want, err := os.ReadFile(forkedFinalsVectors)
	require.NoError(t, err)
	require.True(t, bytes.Equal(want, b), "%s differs; rerun with EB_WRITE_DEAL_V1_VECTORS=1", forkedFinalsVectors)
}
