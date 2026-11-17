// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vectorDir = "../../testdata/deal-v1/records"

func stageOf(err error) string {
	var se *StageError
	if errors.As(err, &se) {
		return se.Stage
	}
	return ""
}

// edit marshals r, applies f to its generic JSON, and returns the bytes.
func edit(t *testing.T, r Record, f func(blk, body map[string]any)) []byte {
	t.Helper()
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	f(doc[Profile].(map[string]any), doc["body"].(map[string]any))
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

// deal-id-presence (design §3.1, review round 3): each record type with
// deal_id as §3.1 places it passes, and with it flipped (dropped where it is
// required, added where it is absent) is refused at the record stage; the
// records whose deal_id depends on when they were sealed pass either way.
// The cases are written to testdata/deal-v1/records/deal-id-presence.json
// (EB_WRITE_DEAL_V1_VECTORS=1), which the Rust and Python ports replay.
func TestDealIDPresence(t *testing.T) {
	type vcase struct {
		Name   string          `json:"name"`
		Record json.RawMessage `json:"record"`
		Valid  bool            `json:"valid"`
		Stage  string          `json:"stage,omitempty"`
	}
	var cases []vcase
	records := validRecords(t)
	names := make([]string, 0, len(records))
	for n := range records {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r := records[name]
		raw, err := json.Marshal(r)
		require.NoError(t, err)
		require.NoError(t, Check(raw), name)
		cases = append(cases, vcase{Name: name, Record: raw, Valid: true})
		flipped := edit(t, r, func(b, _ map[string]any) {
			if _, ok := b["deal_id"]; ok {
				delete(b, "deal_id")
			} else {
				b["deal_id"] = dealID
			}
		})
		err = Check(flipped)
		either := slices.Contains(eitherDealID, r.Block.RecordType) && PlacementOf(r) == PlacementJournal
		if either {
			assert.NoError(t, err, name)
			cases = append(cases, vcase{Name: name + "/deal_id-flipped", Record: flipped, Valid: true})
			continue
		}
		require.Error(t, err, name)
		assert.Equal(t, StageRecord, stageOf(err), "%s: %v", name, err)
		cases = append(cases, vcase{Name: name + "/deal_id-flipped", Record: flipped, Valid: false, Stage: StageRecord})
	}
	got, err := json.MarshalIndent(map[string]any{
		"description": "deal-id-presence (evidencebook#461, docs/design/461-deal-profile.md §3.1): each record passes or is refused at the stage given. deal_id is required on every book record, on propose and every agree, on check and verdict and on an approval that answers a verdict; absent on intent, admit, sale, thread, thread_opened, sale_unpost, counterparty_profile, disclosure and a confirm_limits approval; either on a journal journey record, ask or answer. Written by dealprofile/xdealv1's TestDealIDPresence (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(vectorDir, "deal-id-presence.json")
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(vectorDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "deal-id-presence.json differs from this build's; regenerate only for an intended change")
}

func TestPlacementRules(t *testing.T) {
	records := validRecords(t)
	for name, c := range map[string]struct {
		base string
		f    func(b, body map[string]any)
	}{
		"a journal record with a seq":          {"propose", func(b, _ map[string]any) { b["seq"] = 1 }},
		"a journal record with seen":           {"agree/decline", func(b, _ map[string]any) { b["seen"] = nil }},
		"a journal record with prev":           {"check", func(b, _ map[string]any) { b["prev"] = *ref('1') }},
		"a deal-book record with no seq":       {"perform", func(b, _ map[string]any) { delete(b, "seq") }},
		"a deal-book record with no seen":      {"perform", func(b, _ map[string]any) { delete(b, "seen") }},
		"prev at seq 1":                        {"agree/agreement", func(b, _ map[string]any) { b["prev"] = *ref('1') }},
		"no prev past seq 1":                   {"perform", func(b, _ map[string]any) { delete(b, "prev") }},
		"no agreement_ref past seq 1":          {"perform", func(b, _ map[string]any) { delete(b, "agreement_ref") }},
		"an agreement past seq 1":              {"agree/agreement", func(b, _ map[string]any) { b["seq"], b["prev"], b["agreement_ref"] = 2, *ref('1'), *ref('2') }},
		"a deal book opened by another record": {"perform", func(b, _ map[string]any) { b["seq"] = 1; delete(b, "prev"); delete(b, "agreement_ref") }},
		"a ruling with seen":                   {"ruling", func(b, _ map[string]any) { b["seen"] = nil }},
		"an acceptor's agreement that saw":     {"agree/agreement", func(b, _ map[string]any) { b["seen"] = hex('1') }},
		"no layer and a counterparty key":      {"agree/agreement", func(_, body map[string]any) { body["counterparty_layer"] = false }},
		"a row given twice": {"propose", func(_, body map[string]any) {
			tr := body["terms_rows"].(map[string]any)
			rows := tr["rows"].([]any)
			tr["rows"] = append(rows, rows[0])
		}},
		"a date on a record not named":       {"propose", func(_, body map[string]any) { body["terms_rows"].(map[string]any)["named"] = map[string]any{} }},
		"a check body v0 refuses":            {"check", func(_, body map[string]any) { body["action"] = "fly" }},
		"a decline with an agreement member": {"agree/decline", func(_, body map[string]any) { body["rows_digest"] = hex('1') }},
		"a cancellation with a reason":       {"agree/cancellation", func(_, body map[string]any) { body["reason"] = "other" }},
		"a decline with an unknown reason":   {"agree/decline", func(_, body map[string]any) { body["reason"] = "too_slow" }},
		"a payer leg with no check":          {"settle/payer", func(_, body map[string]any) { delete(body, "check") }},
		"an empty statement naming nothing":  {"statement", func(_, body map[string]any) { body["statements"] = []any{} }},
		"a ruling against a third party":     {"ruling", func(_, body map[string]any) { body["against"] = "referee" }},
		"a withdrawal with dispute members":  {"dispute/withdrawal", func(_, body map[string]any) { body["adjudicator"] = "k" }},
		"another profile in the block":       {"propose", func(b, _ map[string]any) { b["profile"] = "x-deal-v0" }},
		"an unknown block member":            {"propose", func(b, _ map[string]any) { b["baseline_ref"] = *ref('1') }},
		"a float":                            {"admit", func(_, body map[string]any) { body["tier"] = 1.5 }},
		"another canonicalization":           {"admit", func(b, _ map[string]any) { b["canonicalization"] = "none" }},
	} {
		raw := edit(t, records[c.base], c.f)
		err := Check(raw)
		assert.Error(t, err, name)
		assert.NotEmpty(t, stageOf(err), "%s: %v", name, err)
	}
}

func TestCheckAgreement(t *testing.T) {
	offer := record(blk(TypePropose), offerBody())
	acc := acceptorAgreement(offer)
	require.NoError(t, CheckAgreement(acc, offer, ""))
	accID := hex('9')
	offerer := acceptorAgreement(offer)
	ob := offerer.Body.(*AgreeBody)
	ob.Side, ob.Role, ob.CounterpartyKey = SideOfferer, RoleBuyer, Some("seller-key")
	offerer.Block.Seen = Some(accID)
	require.NoError(t, Check(offerer))
	require.NoError(t, CheckAgreement(offerer, offer, accID))

	for name, f := range map[string]func(a *Record, o *Record){
		"another offer":       func(_ *Record, o *Record) { o.Body.(*ProposeBody).PackDigest = hex('0') },
		"another deal":        func(a *Record, _ *Record) { a.Block.DealID = "deal-fedcba9876543210" },
		"another rows digest": func(a *Record, _ *Record) { a.Body.(*AgreeBody).RowsDigest = hex('0') },
		"other witnesses":     func(a *Record, _ *Record) { a.Body.(*AgreeBody).Witnesses = []string{"https://other.test"} },
		"no adjudicator":      func(a *Record, _ *Record) { a.Body.(*AgreeBody).Adjudicator = NullOf[Adjudicator]() },
		"the offer's role":    func(a *Record, _ *Record) { a.Body.(*AgreeBody).Role = RoleBuyer },
		"not citing an offer": func(_ *Record, o *Record) { o.Block.RecordType = TypeCheck; o.Body = &CheckBody{} },
	} {
		a, o := acceptorAgreement(offer), record(blk(TypePropose), offerBody())
		f(&a, &o)
		err := CheckAgreement(a, o, "")
		assert.Equal(t, StageAgreement, stageOf(err), "%s: %v", name, err)
	}
	// The offerer's agreement names the acceptor's agreement it holds.
	assert.Equal(t, StageAgreement, stageOf(CheckAgreement(offerer, offer, hex('8'))))
	offerer.Body.(*AgreeBody).Role = RoleSeller
	assert.Equal(t, StageAgreement, stageOf(CheckAgreement(offerer, offer, accID)), "the offerer's role is the offer's")
}

func TestVersion(t *testing.T) {
	v1, err := json.Marshal(validRecords(t)["propose"])
	require.NoError(t, err)
	for name, c := range map[string]struct {
		raw  string
		want string
	}{
		"v1":             {string(v1), Profile},
		"v0 block":       {`{"x-deal-v0": {"profile": "x-deal-v0"}, "body": {}}`, dealprofile.Profile},
		"typed v0":       {`{"type": "task-authority/v0", "body": {}}`, dealprofile.Profile},
		"a later block":  {`{"x-deal-v7": {"profile": "x-deal-v7"}, "body": {}}`, "x-deal-v7"},
		"two blocks":     {`{"x-deal-v0": {"profile": "x-deal-v0"}, "x-deal-v1": {"profile": "x-deal-v1"}}`, ""},
		"block and type": {`{"x-deal-v1": {"profile": "x-deal-v1"}, "type": "action-record/v0"}`, ""},
		"neither":        {`{"body": {}}`, ""},
		"mismatch":       {`{"x-deal-v1": {"profile": "x-deal-v0"}}`, ""},
		"not an object":  {`[1]`, ""},
		"another type":   {`{"type": "deal_report"}`, ""},
	} {
		got, err := dealprofile.Version([]byte(c.raw))
		if c.want == "" {
			assert.ErrorIs(t, err, dealprofile.ErrUnknownVersion, name)
			continue
		}
		require.NoError(t, err, name)
		assert.Equal(t, c.want, got, name)
	}
}

// A v0 record is never read as v1, and a v1 record never as v0.
func TestVersionsDoNotCross(t *testing.T) {
	v1, err := json.Marshal(validRecords(t)["propose"])
	require.NoError(t, err)
	assert.Error(t, dealprofile.Decode(v1, &dealprofile.Record{}))
	_, err = Decode([]byte(`{"x-deal-v0": {"profile": "x-deal-v0", "canonicalization": "jcs", "deal_id": "deal-0123456789abcdef", "record_type": "intent", "seq": 1, "at": "2026-11-01T10:00:00Z"}, "body": {"verbatim_commitment": "` + hex('a') + `"}}`))
	assert.Error(t, err)
}
