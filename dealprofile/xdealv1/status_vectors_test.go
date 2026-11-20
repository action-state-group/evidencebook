// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/evidencebook/rowstatus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const statusDir = "../../testdata/deal-v1/status"

// resolve is ResolveRow, failing the test on an error.
func resolve(t *testing.T, row StatusRow, at string, stamps map[uint64]string) rowstatus.Input {
	t.Helper()
	in, err := ResolveRow(row, at, stamps)
	require.NoError(t, err)
	return in
}

// lowerBoundBooks is carry-forward-lower-bound (design §8.1, REQUIRED): past
// the as_described window end, with the buyer met and the seller not_met in
// time, the seller states cannot_tell with a time, then met with no time
// yet (first covered by stamp 9).
func lowerBoundBooks(t *testing.T) StatusBooks {
	t.Helper()
	a := buildAppendixA(t)
	own := statusBooksOf(t, a, "appendix-a/buyer-book", nil)[:3]
	their := statusBooksOf(t, a, "appendix-a/seller-book", nil)[:3]
	stmt := func(seq int64, at string, value string) Record {
		r := stmtRecord(seq, "", item("as_described", value))
		r.Block.At = at
		return r
	}
	own = append(own, StatusRecord{Record: stmt(4, "2026-11-10T09:00:00Z", ValueMet), RecordID: label("buyer met"), Time: KnownTime("2026-11-10T09:00:00Z")})
	their = append(their,
		StatusRecord{Record: stmt(4, "2026-11-11T09:00:00Z", ValueNotMet), RecordID: label("seller not met"), Time: KnownTime("2026-11-11T09:00:00Z")},
		StatusRecord{Record: stmt(5, "2026-11-20T09:00:00Z", "cannot_tell"), RecordID: label("seller cannot tell"), Time: KnownTime("2026-11-20T09:00:00Z")},
		StatusRecord{Record: stmt(6, "2026-11-21T09:00:00Z", ValueMet), RecordID: label("seller met late"), Time: StampTime(9)})
	return StatusBooks{Offer: a.record("appendix-a/buyer-journal/05-offer.json"), Own: own, Counterpart: their, ProofHeld: true}
}

// The status vectors (design §8.1): each case's books, the status value they
// build, and for each row the result a reader computes from the fact at the
// times given, which must equal the result over the full books.
func TestStatusVectors(t *testing.T) {
	type vcase struct {
		Name   string            `json:"name"`
		Books  StatusBooks       `json:"books"`
		Status StatusValue       `json:"status"`
		At     string            `json:"at"`
		Stamps map[uint64]string `json:"stamps,omitempty"`
		Rows   map[string]string `json:"rows"`
	}
	a, base := appendixAStatus(t)
	var cases []vcase
	add := func(name string, b StatusBooks, at string, stamps map[uint64]string) {
		t.Helper()
		v, err := BuildStatus(b)
		require.NoError(t, err, name)
		// What the full books give, every untimed record timed as stamps say.
		full := b
		retime := func(rs []StatusRecord) []TimedRecord {
			out := make([]TimedRecord, len(rs))
			for i, r := range rs {
				out[i] = TimedRecord{Record: r.Record, RecordID: r.RecordID, Time: r.Time.known()}
				if r.Time.Stamp != 0 {
					if s, ok := stamps[r.Time.Stamp]; ok {
						out[i].Time = &s
					}
				}
			}
			return out
		}
		ins, err := RowInputs(RowBooks{Offer: full.Offer, Own: retime(full.Own), Counterpart: retime(full.Counterpart), CaseBook: retime(full.CaseBook),
			ConflictingFinalRulings: full.ConflictingFinalRulings}, at)
		require.NoError(t, err, name)
		rows := map[string]string{}
		for i, row := range v.Rows {
			want, err := rowstatus.Compute(ins[i])
			require.NoError(t, err, name)
			got, err := rowstatus.Compute(resolve(t, row, at, stamps))
			require.NoError(t, err, name)
			require.Equal(t, want, got, "%s, row %s: the fact's input resolves to the full books' result", name, row.ID)
			rows[row.ID] = got.Status
		}
		cases = append(cases, vcase{Name: name, Books: b, Status: v, At: at, Stamps: stamps, Rows: rows})
	}

	add("appendix-a", base, "2026-11-26T12:00:00Z", nil)

	// times-three-cases: a known time, a record first covered by stamp 4
	// (resolved to its time), and a named record not yet written (its row's
	// dates stay unset).
	three := base
	three.Own = statusBooksOf(t, a, "appendix-a/buyer-book", map[int]StatusTime{1: StampTime(4)})[:2]
	three.Counterpart = statusBooksOf(t, a, "appendix-a/seller-book", nil)[:2]
	three.CaseBook = nil
	add("times-three-cases", three, "2026-11-05T12:00:00Z", map[uint64]string{4: "2026-11-01T10:15:00Z"})

	// unwitnessed-then-witnessed: the same record first covered at stamp 1,
	// whose checkpoint got no named receipt; the fact of stamp 2 names it
	// stamp 1 still, and a reader resolves it from stamp 2's receipt.
	unw := three
	unw.Own = statusBooksOf(t, a, "appendix-a/buyer-book", map[int]StatusTime{1: StampTime(1)})[:2]
	add("unwitnessed-then-witnessed", unw, "2026-11-05T12:00:00Z", map[uint64]string{1: "2026-11-01T11:00:00Z"})

	// carry-forward: a statement whose time is not yet known stays carried
	// until it is known.
	cf := base
	cf.Counterpart = statusBooksOf(t, a, "appendix-a/seller-book", map[int]StatusTime{3: StampTime(7)})
	add("carry-forward", cf, "2026-11-26T12:00:00Z", map[uint64]string{7: "2026-11-10T00:00:00Z"})

	// seen-in-time-resolves (Codex, PR 4c-2b-2 review): the seller states
	// met again in time; the buyer names that standing statement in a
	// record first covered by stamp 5, then
	// disputes in one first covered by stamp 6. Resolved, the naming is in
	// time and the dispute late, so seen_in_time is true over the full
	// books though the fact could not know it.
	sit := base
	sit.Own = statusBooksOf(t, a, "appendix-a/buyer-book", nil)[:3]
	sit.Counterpart = statusBooksOf(t, a, "appendix-a/seller-book", nil)[:3]
	sit.CaseBook = nil
	again := stmtRecord(4, "", item("as_described", ValueMet))
	again.Block.At = "2026-11-12T09:00:00Z"
	sit.Counterpart = append(sit.Counterpart, StatusRecord{Record: again, RecordID: label("seller met again"), Time: KnownTime("2026-11-12T09:00:00Z")})
	standing := label("seller met again")
	naming := stmtRecord(4, standing, item("delivered", ValueMet))
	naming.Block.At = "2026-11-13T09:00:00Z"
	db := journalBlock(TypeDispute, "2026-11-20T09:00:00Z", true)
	db.Seq, db.Seen = 5, Some(standing)
	sit.Own = append(sit.Own,
		StatusRecord{Record: naming, RecordID: label("buyer names delivery"), Time: StampTime(5)},
		StatusRecord{Record: Record{Block: db, Body: &DisputeBody{Kind: KindDispute, Row: "as_described", Forum: fxForum}}, RecordID: label("buyer late dispute"), Time: StampTime(6)})
	add("seen-in-time-resolves", sit, "2026-11-26T12:00:00Z", map[uint64]string{5: "2026-11-13T09:00:00Z", 6: "2026-11-20T09:00:00Z"})
	sitCase := cases[len(cases)-1]
	for _, r := range sitCase.Status.Rows {
		if r.ID == "as_described" {
			require.Len(t, r.Input.Disputes, 1)
			assert.False(t, r.Input.Disputes[0].SeenInTime, "the fact cannot know it")
			in := resolve(t, r, sitCase.At, sitCase.Stamps)
			assert.True(t, in.Disputes[0].SeenInTime, "the reader recomputes it from the resolved times")
		}
	}

	// carry-forward-lower-bound (REQUIRED).
	lb := lowerBoundBooks(t)
	add("carry-forward-lower-bound", lb, "2026-11-26T12:00:00Z", nil)
	lbCase := cases[len(cases)-1]
	assert.Equal(t, rowstatus.InDisagreement, lbCase.Rows["as_described"], "in_disagreement over the full books")
	var asDescribed StatusRow
	for _, r := range lbCase.Status.Rows {
		if r.ID == "as_described" {
			asDescribed = r
		}
	}
	values := []string{}
	for _, s := range asDescribed.Input.Statements {
		values = append(values, s.Side+":"+s.Value)
	}
	assert.Contains(t, values, "seller:cannot_tell", "the earlier timed statement is carried: rowstatus reads it as the untimed one's lower bound")
	res, err := rowstatus.Compute(resolve(t, asDescribed, lbCase.At, nil))
	require.NoError(t, err)
	assert.False(t, res.Local, "local false, as over the full input")

	// A counterparty with no layer (#575): the status value carries the
	// holder's first act, so a reader computes Start from the fact. Before
	// the act it is null; after it, its time is known, first covered by a
	// stamp, or none yet.
	noLayer := func(upto int, at StatusTime) StatusBooks {
		nb := base
		nb.Own = append([]StatusRecord(nil), base.Own[:upto]...)
		ag := *nb.Own[0].Record.Body.(*AgreeBody)
		f := false
		ag.CounterpartyLayer = &f
		nb.Own[0].Record.Body = &ag
		if at != (StatusTime{}) || upto > 1 {
			nb.Own[upto-1].Time = at
		}
		nb.Counterpart, nb.CaseBook = nil, nil
		return nb
	}
	firstAct := func(name string) *StatusFirstAct {
		for _, c := range cases {
			if c.Name == name {
				return c.Status.Start.FirstAct
			}
		}
		return nil
	}
	add("no-layer-before-act", noLayer(1, StatusTime{}), "2026-11-05T12:00:00Z", nil)
	add("no-layer-timed-act", noLayer(2, KnownTime("2026-11-01T10:15:00Z")), "2026-11-05T12:00:00Z", nil)
	add("no-layer-stamped-act", noLayer(2, StampTime(5)), "2026-11-05T12:00:00Z", map[uint64]string{5: "2026-11-01T10:20:00Z"})
	add("no-layer-untimed-act", noLayer(2, NoTime), "2026-11-05T12:00:00Z", nil)
	assert.Nil(t, firstAct("no-layer-before-act"))
	assert.Equal(t, &StatusFirstAct{Time: KnownTime("2026-11-01T10:15:00Z")}, firstAct("no-layer-timed-act"))
	assert.Equal(t, &StatusFirstAct{Time: StampTime(5)}, firstAct("no-layer-stamped-act"))
	assert.Equal(t, &StatusFirstAct{Time: NoTime}, firstAct("no-layer-untimed-act"))
	assert.Nil(t, firstAct("appendix-a"), "a counterparty with the layer has no first act")

	got, err := json.MarshalIndent(map[string]any{
		"description": "x-deal-v1 status values (evidencebook#461, docs/design/461-deal-profile.md §8.1): BuildStatus(books) must give status; for each row, rowstatus over the status row's input, with each {\"stamp\": n} resolved as stamps gives (untimed otherwise) at at, gives rows. Written by dealprofile/xdealv1's TestStatusVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(statusDir, "vectors.json")
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(statusDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "status/vectors.json differs from this build's; regenerate only for an intended change")
}

// The vectors replay from the file alone: BuildStatus over each case's books
// gives its status, byte for byte.
func TestStatusVectorsReplay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(statusDir, "vectors.json"))
	require.NoError(t, err)
	var v struct {
		Cases []struct {
			Name   string          `json:"name"`
			Books  json.RawMessage `json:"books"`
			Status json.RawMessage `json:"status"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NotEmpty(t, v.Cases)
	for _, c := range v.Cases {
		var b StatusBooks
		require.NoError(t, json.Unmarshal(c.Books, &b), c.Name)
		got, err := BuildStatus(b)
		require.NoError(t, err, c.Name)
		g, err := json.Marshal(got)
		require.NoError(t, err)
		assert.JSONEq(t, string(c.Status), string(g), c.Name)
	}
}
