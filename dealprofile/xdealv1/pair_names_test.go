// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	emit "github.com/action-state-group/capsule-emit/go"
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
	// Inside the signed Capsule: a member spelled otherwise than one the
	// profile reads (U+017F for s, or another case) is refused, beside the
	// member or in its place, so no reader can read the other spelling.
	other := mustDigest(t, fxOfferRecord(t, h))
	beside := func(name string, v any) func(c map[string]any) {
		return func(c map[string]any) {
			c["model_attestation"].(map[string]any)["compute_attestation"].(map[string]any)[name] = v
		}
	}
	add("sent-resigned", "sent", resignSent(t, h.buyer, h.offerPair, sentIn["agent_input"], func(map[string]any) {}), true)
	add("sent-capsule-unknown-member", "sent", resignSent(t, h.buyer, h.offerPair, sentIn["agent_input"],
		func(c map[string]any) { c["x_note"] = "a member the profile does not read" }), true)
	for _, v := range []struct{ name, member string }{{"long-s", "agent_input_digeſt"}, {"upper", "Agent_Input_Digest"}} {
		add("sent-capsule-"+v.name+"-digest-beside", "sent",
			resignSent(t, h.buyer, h.offerPair, sentIn["agent_input"], beside(v.member, other)), false)
		add("sent-capsule-"+v.name+"-digest-read", "sent",
			resignSent(t, h.buyer, h.offerPair, otherSent, beside(v.member, other)), false)
	}
	for _, v := range []struct{ name, member string }{{"long-s", "referenceſ"}, {"upper", "References"}} {
		add("sent-capsule-"+v.name+"-references", "sent", resignSent(t, h.buyer, h.offerPair, sentIn["agent_input"],
			func(c map[string]any) { c[v.member] = []any{"x"} }), false)
	}
	add("book-as-sent", "book", h.agreementPair, true)
	// A header correlation, which the schema allows and a host may set on
	// a deal-book entry, read in every language; a second spelling of one of
	// its names is refused.
	add("book-header-correlation", "book", resealHeader(t, "seller", h.agreementPair, func(hd map[string]any) {
		hd["correlation"] = map[string]any{"exchange_id": "ex-1"}
	}), true)
	add("book-header-correlation-twice", "book", resealHeader(t, "seller", h.agreementPair, func(hd map[string]any) {
		hd["correlation"] = map[string]any{"exchange_id": "ex-1", "Exchange_ID": "ex-2"}
	}), false)
	// A correlation member the header does not list is refused, each listed
	// one read (evidencebook#563: the Rust reader holds the names as digests).
	for _, name := range []string{"request_digest", "twin_bracket_id"} {
		add("book-header-correlation-"+name, "book", resealHeader(t, "seller", h.agreementPair, func(hd map[string]any) {
			hd["correlation"] = map[string]any{name: "v-1"}
		}), true)
	}
	add("book-header-correlation-unlisted", "book", resealHeader(t, "seller", h.agreementPair, func(hd map[string]any) {
		hd["correlation"] = map[string]any{"unlisted_key": "v-1"}
	}), false)
	add("book-header-correlation-listed-and-unlisted", "book", resealHeader(t, "seller", h.agreementPair, func(hd map[string]any) {
		hd["correlation"] = map[string]any{"exchange_id": "ex-1", "unlisted_key": "v-1"}
	}), false)
	// A correlation of another type is a type error; a null is none.
	for _, v := range []struct {
		name  string
		value any
		valid bool
	}{
		{"array", []any{}, false}, {"number-member", map[string]any{"exchange_id": 42}, false},
		{"null", nil, true}, {"null-member", map[string]any{"exchange_id": nil}, true},
	} {
		add("book-header-correlation-"+v.name, "book", resealHeader(t, "seller", h.agreementPair, func(hd map[string]any) {
			hd["correlation"] = v.value
		}), v.valid)
	}
	// Inside the signed header: payload_commitmentſ beside
	// payload_commitments is not a member the header has, so the pair is
	// refused whichever payload it carries.
	otherAg := h.agreement
	otherAg.Block.At = "2026-11-01T10:01:00Z"
	require.NoError(t, Check(otherAg))
	commitmentBeside := func(hd map[string]any) { hd["payload_commitmentſ"] = []any{mustDigest(t, otherAg)} }
	add("book-header-long-s-commitments-beside", "book", resealHeader(t, "seller", h.agreementPair, commitmentBeside), false)
	read := resealHeader(t, "seller", h.agreementPair, commitmentBeside)
	var rm map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(read, &rm))
	rm["payload"] = jcsOf(t, otherAg)
	readPair, err := json.Marshal(rm)
	require.NoError(t, err)
	add("book-header-long-s-commitments-read", "book", readPair, false)
	add("book-payload-in-another-case", "book", respell(t, h.agreementPair, map[string]string{"payload": "Payload"}, nil), false)
	add("book-header-in-another-case", "book", respell(t, h.agreementPair, map[string]string{"header": "HEADER"}, nil), false)
	add("book-payload-twice", "book", respell(t, h.agreementPair, nil, map[string]json.RawMessage{"Payload": sentIn["agent_input"]}), false)
	add("book-header-twice", "book", respell(t, h.agreementPair, nil, map[string]json.RawMessage{"Header": bookIn["header"]}), false)
	return cases
}

// resignSent is the sent pair with its Capsule changed by edit, its id
// computed again and signed again under the party's record key, and
// agentInput as its agent_input.
func resignSent(t *testing.T, p *party, pair []byte, agentInput json.RawMessage, edit func(c map[string]any)) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(pair, &m))
	var rec map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(m["record"], &rec))
	delete(rec, "signature")
	delete(rec, "key_id")
	delete(rec, "capsule_id")
	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	doc, err := decodeJSON(raw)
	require.NoError(t, err)
	c := doc.(map[string]any)
	edit(c)
	id, err := canonical.ComputeCapsuleID(c)
	require.NoError(t, err)
	c["capsule_id"] = id
	capsule, err := canonical.JCS(c)
	require.NoError(t, err)
	identity, err := emit.NewEd25519SigningIdentity(seed(p.name + " record key"))
	require.NoError(t, err)
	envelope, err := emit.Sign(emit.BuiltPayload{CapsuleID: id, JSON: capsule}, identity)
	require.NoError(t, err)
	out, err := Pair(capsule, envelope, agentInput)
	require.NoError(t, err)
	return out
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
	}
}

// TestPairNamesVectors writes and checks testdata/deal-v1/pair-names
// (EB_WRITE_DEAL_V1_VECTORS=1), which the Rust and Python ports replay.
func TestPairNamesVectors(t *testing.T) {
	out := map[string]any{
		"description": "VerifySent and VerifyBook on a pair whose members, or whose signed Capsule's members, are spelled otherwise than the members read (evidencebook#559): each case a sent or book pair, as hex, valid or refused at the stage given. Written by dealprofile/xdealv1 TestPairNamesVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
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
