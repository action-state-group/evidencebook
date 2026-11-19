// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pairNamesVectors = "../../testdata/deal-v1/pair-names/vectors.json"

// pairNameCase is one vector: a sent or book pair and what VerifySent or
// VerifyBook gives, valid or the stage of the refusal.
type pairNameCase struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Pair  string `json:"pair"`
	Valid bool   `json:"valid"`
	Stage string `json:"stage,omitempty"`
}

// respell writes pair's members again, each under the name rename gives
// (the same name to keep it), and the extra members after them.
func respell(t *testing.T, pair []byte, rename map[string]string, extra map[string]json.RawMessage) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(pair, &m))
	out := map[string]json.RawMessage{}
	for k, v := range m {
		if n, ok := rename[k]; ok {
			k = n
		}
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	b, err := json.Marshal(out)
	require.NoError(t, err)
	return b
}

// pairNameCases are a sent and a book pair from one handshake, as sent, and
// each with a member in another case or a second spelling of a member
// beside it, which evidencebook#559 refuses.
func pairNameCases(t *testing.T) []pairNameCase {
	t.Helper()
	h := newHandshake(t, nil)
	var sentIn, bookIn map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(h.offerPair, &sentIn))
	require.NoError(t, json.Unmarshal(h.agreementPair, &bookIn))
	// Another record of the same shape to put beside the verified one.
	otherSent := json.RawMessage(jcsOf(t, fxOfferRecord(t, h)))
	var cases []pairNameCase
	add := func(name, kind string, pair []byte, valid bool) {
		c := pairNameCase{Name: name, Kind: kind, Pair: hex.EncodeToString(pair), Valid: valid}
		if !valid {
			c.Stage = map[string]string{"sent": StageSent, "book": StageBook}[kind]
		}
		cases = append(cases, c)
	}
	add("sent-as-sent", "sent", h.offerPair, true)
	add("sent-record-in-another-case", "sent", respell(t, h.offerPair, map[string]string{"record": "Record"}, nil), false)
	add("sent-agent-input-in-another-case", "sent", respell(t, h.offerPair, map[string]string{"agent_input": "AGENT_INPUT"}, nil), false)
	add("sent-agent-input-twice", "sent", respell(t, h.offerPair, nil, map[string]json.RawMessage{"Agent_Input": otherSent}), false)
	add("sent-record-twice", "sent", respell(t, h.offerPair, nil, map[string]json.RawMessage{"Record": bookIn["record"]}), false)
	add("book-as-sent", "book", h.agreementPair, true)
	add("book-payload-in-another-case", "book", respell(t, h.agreementPair, map[string]string{"payload": "Payload"}, nil), false)
	add("book-header-in-another-case", "book", respell(t, h.agreementPair, map[string]string{"header": "HEADER"}, nil), false)
	add("book-payload-twice", "book", respell(t, h.agreementPair, nil, map[string]json.RawMessage{"Payload": sentIn["agent_input"]}), false)
	add("book-header-twice", "book", respell(t, h.agreementPair, nil, map[string]json.RawMessage{"Header": bookIn["header"]}), false)
	return cases
}

// fxOfferRecord is another offer of the handshake's deal, to put beside the
// one a pair verifies.
func fxOfferRecord(t *testing.T, h *handshake) Record {
	t.Helper()
	r := h.offer
	r.Block.At = "2026-11-01T09:31:00Z"
	require.NoError(t, Check(r))
	return r
}

func TestPairNames(t *testing.T) {
	for _, c := range pairNameCases(t) {
		pair, err := hex.DecodeString(c.Pair)
		require.NoError(t, err)
		if c.Kind == "sent" {
			_, err = VerifySent(pair, nil)
		} else {
			_, err = VerifyBook(pair)
		}
		if c.Valid {
			assert.NoError(t, err, c.Name)
			continue
		}
		var se *StageError
		require.ErrorAs(t, err, &se, c.Name)
		assert.Equal(t, c.Stage, se.Stage, c.Name)
		assert.Contains(t, err.Error(), "its members are not exactly", c.Name)
	}
}

// TestPairNamesVectors writes and checks testdata/deal-v1/pair-names
// (EB_WRITE_DEAL_V1_VECTORS=1), which the Rust and Python ports replay.
func TestPairNamesVectors(t *testing.T) {
	out := map[string]any{
		"description": "VerifySent and VerifyBook on a pair whose members are not exactly its own (evidencebook#559): each case a sent or book pair, as hex, valid or refused at the stage given. Written by dealprofile/xdealv1 TestPairNamesVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       pairNameCases(t),
	}
	b, err := json.MarshalIndent(out, "", "  ")
	require.NoError(t, err)
	b = append(b, '\n')
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(pairNamesVectors), 0o755))
		require.NoError(t, os.WriteFile(pairNamesVectors, b, 0o644))
	}
	want, err := os.ReadFile(pairNamesVectors)
	require.NoError(t, err)
	require.True(t, bytes.Equal(want, b), "%s differs; rerun with EB_WRITE_DEAL_V1_VECTORS=1", pairNamesVectors)
}
