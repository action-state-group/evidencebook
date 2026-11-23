// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/rowstatus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reportDir = "../../testdata/deal-v1/report"

var (
	reportNonceA = strings.Repeat("a", 64)
	reportCP     = dealprofile.Checkpoint{LogID: "deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0", MMRSize: 15, Root: strings.Repeat("c", 64)}
)

// reportBooks is Appendix A's buyer, as a report rests on it.
func reportBooks(t *testing.T, b StatusBooks) ReportBooks {
	t.Helper()
	rb := ReportBooks{Books: b, Checkpoint: reportCP, Own: ReportLog{LogID: reportCP.LogID, Size: uint64(len(b.Own))}}
	if len(b.Own) > 0 {
		rb.Own.Last = b.Own[len(b.Own)-1].RecordID
	}
	if len(b.Counterpart) > 0 {
		rb.Counterpart = &ReportCounterpartLog{LogID: "deal/5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e", Counted: uint64(len(b.Counterpart)), Last: b.Counterpart[len(b.Counterpart)-1].RecordID}
	}
	return rb
}

// heldAt is b as the holder held it at as: every record whose time is after
// as left out (Appendix A's records all carry known times).
func heldAt(b StatusBooks, as string) StatusBooks {
	keep := func(rs []StatusRecord) []StatusRecord {
		var out []StatusRecord
		for _, r := range rs {
			if r.Time.Known != "" && r.Time.Known > as {
				break
			}
			out = append(out, r)
		}
		return out
	}
	b.Own, b.Counterpart, b.CaseBook = keep(b.Own), keep(b.Counterpart), keep(b.CaseBook)
	return b
}

// notStarted is the seller, the acceptor, holding its own agreement only.
func notStarted(t *testing.T) StatusBooks {
	a, b := appendixAStatus(t)
	b.Own, b.Counterpart, b.CaseBook = statusBooksOf(t, a, "appendix-a/seller-book", nil)[:1], nil, nil
	b.ProofHeld = false
	return b
}

func keepCopy(at string) ReportCopy {
	return ReportCopy{Audience: dealprofile.AudienceKeep, Nonce: reportNonceA, AsOf: at}
}

func rowByID(t *testing.T, r Report, id string) ReportRow {
	t.Helper()
	for _, row := range r.Rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("no row %s", id)
	return ReportRow{}
}

// Appendix A through the design §9 times: each row's status as rowstatus
// gives it at as_of, settled, and the ruling and its sources.
func TestBuildReportAppendixA(t *testing.T) {
	_, b := appendixAStatus(t)
	at := func(as string) Report {
		r, err := BuildReport(reportBooks(t, heldAt(b, as)), keepCopy(as))
		require.NoError(t, err)
		return r
	}
	r := at("2026-11-03T12:00:00Z")
	assert.Equal(t, StateStarted, r.Start.State)
	assert.Equal(t, RowsListed, r.RowsState)
	assert.Equal(t, rowstatus.AgreedSoFar, rowByID(t, r, "paid_as_agreed").Status)
	assert.Equal(t, rowstatus.NotDue, rowByID(t, r, "as_described").Status)
	assert.Equal(t, NotSet, rowByID(t, r, "as_described").Due, "an unset date is not_set")
	assert.False(t, r.Settled)

	r = at("2026-11-12T12:00:00Z")
	assert.Equal(t, rowstatus.InDispute, rowByID(t, r, "as_described").Status)

	r = at("2026-11-21T12:00:00Z")
	row := rowByID(t, r, "as_described")
	assert.Equal(t, rowstatus.Ruled, row.Status)
	assert.Equal(t, strp(RemedyNotCounted), row.Remedy, "the ruling orders a refund, a payment rowstatus does not count yet")
	assert.False(t, row.RemedyMade, "no refund held on 21 Nov")
	require.NotNil(t, row.Ruling)
	assert.True(t, row.Ruling.Final)
	assert.Equal(t, strp(RoleSeller), row.Ruling.Against)
	assert.Equal(t, b.CaseBook[1].RecordID, row.Ruling.Record, "the first final ruling")
	assert.Contains(t, row.ReliedOn, ReportSource{Record: b.CaseBook[1].RecordID, Side: SideAdjudicator, Time: b.CaseBook[1].Time.known(), Witnessed: true})
	assert.False(t, r.Settled, "a remedy not counted keeps the deal unsettled")

	// The seller refunded on 25 Nov: the report never says the remedy was
	// not honoured (Sherlock2, the twin and the coordinator, #530 review).
	for _, as := range []string{"2026-11-26T12:00:00Z", "2026-11-28T12:00:00Z"} {
		row := rowByID(t, at(as), "as_described")
		assert.Equal(t, strp(RemedyNotCounted), row.Remedy, as)
		assert.True(t, row.RemedyMade, "%s: the refund is held", as)
		refund := b.Counterpart[len(b.Counterpart)-1]
		assert.Contains(t, row.ReliedOn, ReportSource{Record: refund.RecordID, Side: RoleSeller, Time: refund.Time.known(), Witnessed: true}, as)
	}
	r = at("2026-11-28T12:00:00Z")
	assert.False(t, r.Settled)
	assert.Len(t, r.Disputes, 1, "one dispute record, one entry")
	assert.Equal(t, rowstatus.AgreedSoFar, at("2026-11-01T12:00:00Z").Rows[0].Status)
}

// What each audience's copy holds (design §9 table): an adjudicator's
// withholds the rows, the counterparty's prefix, the witnesses, disputes and
// the unnamed count, and names its referee; the others do not.
func TestBuildReportAudiences(t *testing.T) {
	_, full := appendixAStatus(t)
	b := heldAt(full, "2026-11-21T12:00:00Z")
	b.RulingsUnnamed = 1
	rb := reportBooks(t, b)
	keep, err := BuildReport(rb, keepCopy("2026-11-21T12:00:00Z"))
	require.NoError(t, err)
	cp := keepCopy("2026-11-21T12:00:00Z")
	cp.Audience = dealprofile.AudienceCounterparty
	other, err := BuildReport(rb, cp)
	require.NoError(t, err)
	assert.Equal(t, keep.Rows, other.Rows)
	assert.NotNil(t, other.RestsOn.Counterparty)
	assert.Equal(t, 1, other.RulingsUnnamed)

	adj := keepCopy("2026-11-21T12:00:00Z")
	adj.Audience = dealprofile.AudienceAdjudicator
	_, err = BuildReport(rb, adj)
	assert.Error(t, err, "an adjudicator's copy names its referee")
	adj.Referee = "  Lamp Court  "
	a, err := BuildReport(rb, adj)
	require.NoError(t, err)
	assert.Equal(t, RowsWithheld, a.RowsState)
	assert.Empty(t, a.Rows)
	assert.Nil(t, a.RestsOn.Counterparty)
	assert.Empty(t, a.RestsOn.Witnesses)
	assert.Empty(t, a.Disputes)
	assert.Zero(t, a.RulingsUnnamed)
	assert.Equal(t, "Lamp Court", a.Referee)
	assert.Equal(t, keep.Start, a.Start)
	assert.Equal(t, keep.Settled, a.Settled)
	assert.Equal(t, keep.RestsOn.Own, a.RestsOn.Own)

	payload, err := ReportPayload(a)
	require.NoError(t, err)
	for _, r := range b.Counterpart {
		assert.NotContains(t, string(payload), r.RecordID, "no counterparty record id in an adjudicator's copy")
	}
}

// Sherlock2's card notes (#526 and #528 reviews): counterparty_kind null
// for a page to show "not stated"; counterparty_proof reads the layer and the
// held proof together (u611's no-layer marking); rulings_unnamed beside an
// open outcome; a status fact that is not current says so.
func TestBuildReportCardMembers(t *testing.T) {
	_, full := appendixAStatus(t)
	at := "2026-11-12T12:00:00Z"
	b := heldAt(full, at)
	r, err := BuildReport(reportBooks(t, b), keepCopy(at))
	require.NoError(t, err)
	assert.Nil(t, r.RestsOn.CounterpartyKind)
	assert.Equal(t, ProofHeld, r.RestsOn.CounterpartyProof)
	assert.Nil(t, r.StatusFact, "no fact yet")
	raw, err := ReportPayload(r)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"counterparty_kind":null`)
	assert.NotContains(t, string(raw), "rulings_unnamed")
	assert.NotContains(t, string(raw), "lifecycle", "a v1 report has no lifecycle")

	b.ProofHeld = false
	r, err = BuildReport(reportBooks(t, b), keepCopy(at))
	require.NoError(t, err)
	assert.Equal(t, ProofNotYet, r.RestsOn.CounterpartyProof, "false alone is a proof not yet arrived")

	noLayer := b
	noLayer.Own = append([]StatusRecord(nil), b.Own...)
	ag := *noLayer.Own[0].Record.Body.(*AgreeBody)
	f := false
	ag.CounterpartyLayer = &f
	noLayer.Own[0].Record.Body = &ag
	r, err = BuildReport(reportBooks(t, noLayer), keepCopy(at))
	require.NoError(t, err)
	assert.Equal(t, ProofNoLayer, r.RestsOn.CounterpartyProof)
	assert.False(t, r.RestsOn.CounterpartyLayer)

	b.ProofHeld = true
	b.RulingsUnnamed = 1
	rb := reportBooks(t, b)
	rb.Fact = &ReportFact{Current: false}
	r, err = BuildReport(rb, keepCopy(at))
	require.NoError(t, err)
	require.Len(t, r.Disputes, 1)
	assert.Equal(t, OutcomeOpen, r.Disputes[0].Outcome)
	assert.Equal(t, 1, r.RulingsUnnamed, "an open outcome beside a ruling held unnamed")
	raw, err = ReportPayload(r)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"rulings_unnamed":1`)
	assert.Contains(t, string(raw), `"status_fact":{"current":false}`)
}

// A deal not started lists no rows; an untimed book's rows are local.
func TestBuildReportNotStartedAndLocal(t *testing.T) {
	_, b := appendixAStatus(t)
	r, err := BuildReport(reportBooks(t, notStarted(t)), keepCopy("2026-11-05T12:00:00Z"))
	require.NoError(t, err)
	assert.NotEqual(t, StateStarted, r.Start.State)
	assert.Equal(t, RowsNotStarted, r.RowsState)
	assert.Empty(t, r.Rows)
	assert.False(t, r.Settled)

	untimed := b
	for _, book := range []*[]StatusRecord{&untimed.Own, &untimed.Counterpart, &untimed.CaseBook} {
		rs := append([]StatusRecord(nil), (*book)...)
		for i := range rs {
			rs[i].Time = StampTime(3)
		}
		*book = rs
	}
	rb := reportBooks(t, untimed)
	r, err = BuildReport(rb, keepCopy("2026-11-05T12:00:00Z"))
	require.NoError(t, err)
	for _, row := range r.Rows {
		for _, s := range row.ReliedOn {
			assert.Nil(t, s.Time, "a stamp whose time is not known is no time")
			assert.False(t, s.Witnessed)
		}
	}
	assert.True(t, slices.ContainsFunc(r.Rows, func(row ReportRow) bool { return row.Local }), "a row resting on an untimed record is local")
	rb.Stamps = map[uint64]string{3: "2026-11-01T10:00:00Z"}
	r, err = BuildReport(rb, keepCopy("2026-11-05T12:00:00Z"))
	require.NoError(t, err)
	assert.Equal(t, strp("2026-11-01T10:00:00Z"), r.Rows[0].ReliedOn[0].Time, "resolved from the stamp's time")
}

// settledWithoutRuling is Appendix A's settled-without-ruling path (design
// §9, §10; lines s1 to s12) from the buyer's side: the buyer states row 3
// not met, the seller concedes not met and refunds with no ruling. Row 3 is
// agreed not_met with its remedy made, and the deal is settled.
func settledWithoutRuling(t *testing.T) StatusBooks {
	a, b := appendixAStatus(t)
	b.Own, b.Counterpart, b.CaseBook = b.Own[:4], b.Counterpart[:3], nil
	concede := stmtRecord(4, b.Own[3].RecordID, item("as_described", ValueNotMet))
	concede.Block.At = "2026-11-10T14:00:00Z"
	refund := a.record("appendix-a/seller-book/07-refund.json")
	body := *refund.Body.(*SettleBody)
	rf := *body.RemedyFor
	rf.Ruling = nil
	body.RemedyFor = &rf
	refund.Body = &body
	refund.Block.Seq, refund.Block.At = 5, "2026-11-11T10:00:00Z"
	b.Counterpart = append(append([]StatusRecord(nil), b.Counterpart...),
		StatusRecord{Record: concede, RecordID: label("seller concedes row 3"), Time: KnownTime("2026-11-10T14:00:00Z")},
		StatusRecord{Record: refund, RecordID: label("seller refunds without a ruling"), Time: KnownTime("2026-11-11T10:00:00Z")})
	return b
}

// noWitnesses is Appendix A's buyer with an empty witness list: no record
// has a time, since only a witness the agreement names gives one (design
// §4.3), so every row is local.
func noWitnesses(t *testing.T, as string) StatusBooks {
	_, full := appendixAStatus(t)
	b := heldAt(full, as)
	b.Own = append([]StatusRecord(nil), b.Own...)
	ag := *b.Own[0].Record.Body.(*AgreeBody)
	ag.Witnesses = []Witness{}
	b.Own[0].Record.Body = &ag
	for _, book := range []*[]StatusRecord{&b.Own, &b.Counterpart, &b.CaseBook} {
		rs := append([]StatusRecord(nil), (*book)...)
		for i := range rs {
			rs[i].Time = NoTime
		}
		*book = rs
	}
	return b
}

func TestBuildReportSettledWithoutRuling(t *testing.T) {
	r, err := BuildReport(reportBooks(t, settledWithoutRuling(t)), keepCopy("2026-12-01T12:00:00Z"))
	require.NoError(t, err)
	row := rowByID(t, r, "as_described")
	assert.Equal(t, rowstatus.Agreed, row.Status)
	assert.Equal(t, strp(ValueNotMet), row.Value)
	assert.Nil(t, row.Remedy, "not ruled")
	assert.True(t, row.RemedyMade, "the refund outside a ruling (Q5)")
	assert.Nil(t, row.Ruling)
	assert.True(t, r.Settled)
	b := settledWithoutRuling(t)
	refund := b.Counterpart[len(b.Counterpart)-1]
	assert.Contains(t, row.ReliedOn, ReportSource{Record: refund.RecordID, Side: RoleSeller, Time: refund.Time.known(), Witnessed: true}, "the remedy made is a record the row relied on")

	// The concession alone does not settle (Sherlock2, #530 review).
	conceded := settledWithoutRuling(t)
	conceded.Counterpart = conceded.Counterpart[:len(conceded.Counterpart)-1]
	r, err = BuildReport(reportBooks(t, conceded), keepCopy("2026-12-01T12:00:00Z"))
	require.NoError(t, err)
	row = rowByID(t, r, "as_described")
	assert.Equal(t, rowstatus.Agreed, row.Status)
	assert.False(t, row.RemedyMade)
	assert.False(t, r.Settled)
}

func TestBuildReportNoWitnesses(t *testing.T) {
	r, err := BuildReport(reportBooks(t, noWitnesses(t, "2026-11-09T12:00:00Z")), keepCopy("2026-11-09T12:00:00Z"))
	require.NoError(t, err)
	assert.Empty(t, r.RestsOn.Witnesses)
	require.NotEmpty(t, r.Rows)
	for _, row := range r.Rows {
		for _, src := range row.ReliedOn {
			assert.Nil(t, src.Time)
		}
	}
	assert.True(t, rowByID(t, r, "paid_as_agreed").Local, "agreed on untimed statements: local")
	assert.Equal(t, rowstatus.NotReached, rowByID(t, r, "delivered").Status, "a named record with no time past its latest date")
}

func TestBuildReportRefuses(t *testing.T) {
	_, b := appendixAStatus(t)
	rb := reportBooks(t, b)
	for name, c := range map[string]ReportCopy{
		"audience": {Audience: "anyone", Nonce: reportNonceA, AsOf: "2026-11-05T12:00:00Z"},
		"nonce":    {Audience: dealprofile.AudienceKeep, Nonce: "ab", AsOf: "2026-11-05T12:00:00Z"},
		"referee":  {Audience: dealprofile.AudienceKeep, Nonce: reportNonceA, AsOf: "2026-11-05T12:00:00Z", Referee: "x"},
		"as_of":    {Audience: dealprofile.AudienceKeep, Nonce: reportNonceA, AsOf: "2026-11-05T12:00:00.5Z"},
		"zone":     {Audience: dealprofile.AudienceKeep, Nonce: reportNonceA, AsOf: "2026-11-05T12:00:00+01:00"},
	} {
		_, err := BuildReport(rb, c)
		assert.Error(t, err, name)
	}
	_, err := BuildReport(rb, keepCopy("2026-11-05T12:00:00Z"))
	assert.ErrorContains(t, err, "after as_of", "a record timed after as_of was not held then")
	empty := rb
	empty.Books.Own = nil
	_, err = BuildReport(empty, keepCopy("2026-11-05T12:00:00Z"))
	assert.Error(t, err)
}

// settled (design §9, the settled vectors).
func TestSettled(t *testing.T) {
	started := StartResult{State: StateStarted}
	// A row is status, then ":" and its remedy (ruled) or value (agreed),
	// then "+made" for a remedy held.
	row := func(s string) ReportRow {
		s, made := strings.CutSuffix(s, "+made")
		status, extra, _ := strings.Cut(s, ":")
		r := ReportRow{Status: status, RemedyMade: made}
		switch {
		case extra == "":
		case status == rowstatus.Ruled:
			r.Remedy = &extra
		default:
			r.Value = &extra
		}
		return r
	}
	type vcase struct {
		Name    string   `json:"name"`
		Started bool     `json:"started"`
		Rows    []string `json:"rows"`
		Settled bool     `json:"settled"`
	}
	cases := []vcase{
		{"not-started", false, []string{"agreed"}, false},
		{"one-not-reached-rest-agreed", true, []string{"not_reached", "agreed", "uncontested"}, true},
		{"ruled-remedy-none", true, []string{"agreed", "ruled:none"}, true},
		{"ruled-remedy-due", true, []string{"agreed", "ruled:due"}, false},
		{"ruled-remedy-made", true, []string{"agreed", "ruled:made"}, true},
		{"ruled-remedy-not-honoured", true, []string{"agreed", "ruled:not_honoured"}, false},
		{"every-row-not-reached", true, []string{"not_reached", "not_reached"}, true},
		{"in-dispute", true, []string{"agreed", "in_dispute"}, false},
		{"ruled-remedy-not-counted", true, []string{"agreed", "ruled:not_counted+made"}, false},
		{"conceded-not-met-without-remedy", true, []string{"agreed:met", "agreed:not_met"}, false},
		{"conceded-not-met-remedy-made", true, []string{"agreed:met", "agreed:not_met+made"}, true},
	}
	for _, c := range cases {
		var rows []ReportRow
		for _, s := range c.Rows {
			rows = append(rows, row(s))
		}
		st := StartResult{State: StateNotStarted}
		if c.Started {
			st = started
		}
		assert.Equal(t, c.Settled, settled(st, rows), c.Name)
	}
	writeOrCompare(t, filepath.Join("../../testdata/deal-v1/settled", "vectors.json"), map[string]any{
		"description": "x-deal-v1 settled (docs/design/461-deal-profile.md §9): a deal is settled when it has started and every row that is not not_reached is agreed (met, or not met with a remedy made), uncontested, or ruled with remedy none or made. Each row is \"status\", \"ruled:remedy\" or \"agreed:value\", with \"+made\" for a remedy held. Written by dealprofile/xdealv1's TestSettled (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	})
}

// The report vectors (design §9): Appendix A's buyer through the design's
// times, each copy audience, a not-started book, an untimed book and a
// counterparty without the layer, each at a fixed nonce. Replayed from the
// file alone by TestReportVectorsReplay.
func TestReportVectors(t *testing.T) {
	type vcase struct {
		Name   string      `json:"name"`
		Books  ReportBooks `json:"books"`
		Copy   reportCopy  `json:"copy"`
		Report Report      `json:"report"`
	}
	_, b := appendixAStatus(t)
	var cases []vcase
	add := func(name string, rb ReportBooks, c ReportCopy) {
		t.Helper()
		r, err := BuildReport(rb, c)
		require.NoError(t, err, name)
		cases = append(cases, vcase{Name: name, Books: rb, Copy: reportCopy(c), Report: r})
	}
	for _, at := range []struct{ name, at string }{
		{"appendix-a-1-nov", "2026-11-01T12:00:00Z"}, {"appendix-a-3-nov", "2026-11-03T12:00:00Z"},
		{"appendix-a-9-nov", "2026-11-09T12:00:00Z"}, {"appendix-a-12-nov", "2026-11-12T12:00:00Z"},
		{"appendix-a-21-nov", "2026-11-21T12:00:00Z"}, {"appendix-a-26-nov", "2026-11-26T12:00:00Z"},
		{"appendix-a-28-nov", "2026-11-28T12:00:00Z"},
	} {
		add(at.name, reportBooks(t, heldAt(b, at.at)), keepCopy(at.at))
	}
	cp := keepCopy("2026-11-21T12:00:00Z")
	cp.Audience = dealprofile.AudienceCounterparty
	add("appendix-a-counterparty-copy", reportBooks(t, heldAt(b, cp.AsOf)), cp)
	adj := keepCopy("2026-11-21T12:00:00Z")
	adj.Audience, adj.Referee = dealprofile.AudienceAdjudicator, "Lamp Court"
	add("appendix-a-adjudicator-copy", reportBooks(t, heldAt(b, adj.AsOf)), adj)

	add("not-started", reportBooks(t, notStarted(t)), keepCopy("2026-11-05T12:00:00Z"))

	// Both agreed and nothing more: the offerer's agreement has no time and the
	// acceptor never named it, T after agree_by. The deal counts (u608): rows
	// are listed and the start is local.
	silent := heldAt(b, "2026-11-05T12:00:00Z")
	silent.Own = []StatusRecord{silent.Own[0]}
	silent.Own[0].Time = NoTime
	silent.Counterpart, silent.CaseBook, silent.ProofHeld = silent.Counterpart[:1], nil, false
	add("agree-then-silent", reportBooks(t, silent), keepCopy("2026-11-05T12:00:00Z"))

	untimed := heldAt(b, "2026-11-09T12:00:00Z")
	untimed.Own = append([]StatusRecord(nil), untimed.Own...)
	for i := range untimed.Own {
		untimed.Own[i].Time = NoTime
	}
	add("untimed-own-book", reportBooks(t, untimed), keepCopy("2026-11-09T12:00:00Z"))

	noLayer := heldAt(b, "2026-11-09T12:00:00Z")
	noLayer.Own = append([]StatusRecord(nil), noLayer.Own...)
	ag := *noLayer.Own[0].Record.Body.(*AgreeBody)
	f := false
	ag.CounterpartyLayer = &f
	noLayer.Own[0].Record.Body = &ag
	noLayer.ProofHeld = false
	add("counterparty-without-layer", reportBooks(t, noLayer), keepCopy("2026-11-09T12:00:00Z"))

	// The first act's time as the status value carries it (#575): untimed,
	// first covered by a stamp the holder holds, and by one it does not.
	// The deal counts in each; only a resolved time is not local.
	for _, c := range []struct {
		name   string
		time   StatusTime
		stamps map[uint64]string
	}{
		{"counterparty-without-layer-untimed-act", NoTime, nil},
		{"counterparty-without-layer-stamped-act", StampTime(5), map[uint64]string{5: "2026-11-01T10:20:00Z"}},
		{"counterparty-without-layer-unresolved-stamp", StampTime(5), nil},
	} {
		nl := noLayer
		nl.Own = append([]StatusRecord(nil), noLayer.Own...)
		nl.Own[1].Time = c.time
		rb := reportBooks(t, nl)
		rb.Stamps = c.stamps
		add(c.name, rb, keepCopy("2026-11-09T12:00:00Z"))
	}

	add("settled-without-ruling", reportBooks(t, settledWithoutRuling(t)), keepCopy("2026-12-01T12:00:00Z"))
	conceded := settledWithoutRuling(t)
	conceded.Counterpart = conceded.Counterpart[:len(conceded.Counterpart)-1]
	add("conceded-without-refund", reportBooks(t, conceded), keepCopy("2026-12-01T12:00:00Z"))
	add("no-witnesses", reportBooks(t, noWitnesses(t, "2026-11-09T12:00:00Z")), keepCopy("2026-11-09T12:00:00Z"))

	unnamed := heldAt(b, "2026-11-21T12:00:00Z")
	unnamed.CaseBook, unnamed.RulingsUnnamed = nil, 2
	rb := reportBooks(t, unnamed)
	rb.Fact = &ReportFact{Current: false}
	add("ruling-held-unnamed", rb, keepCopy("2026-11-21T12:00:00Z"))

	// The settle legs the payment check read are in the payment row's
	// relied_on (Steven's ruling on #588): with both legs stating the row
	// they are there already, and the check passes; with a payee leg that
	// states nothing, the leg is there through the check alone; with a
	// check not run over legs (several payment rows), no
	// leg is added.
	paid := heldAt(b, "2026-11-09T12:00:00Z")
	paid.Payments = paymentsOver(t, paid, PaymentPass, "", paid.Own[1], paid.Counterpart[1])
	add("appendix-a-payment-passed", reportBooks(t, paid), keepCopy("2026-11-09T12:00:00Z"))
	unstated := payeeLegUnstated(t, heldAt(b, "2026-11-09T12:00:00Z"))
	unstated.Payments = paymentsOver(t, unstated, PaymentPass, "", unstated.Own[1], unstated.Counterpart[1])
	add("appendix-a-payee-leg-unstated", reportBooks(t, unstated), keepCopy("2026-11-09T12:00:00Z"))
	ucp := keepCopy("2026-11-09T12:00:00Z")
	ucp.Audience = dealprofile.AudienceCounterparty
	add("appendix-a-payee-leg-unstated-counterparty-copy", reportBooks(t, unstated), ucp)
	notRun := payeeLegUnstated(t, heldAt(b, "2026-11-09T12:00:00Z"))
	notRun.Payments = paymentsOver(t, notRun, "", ReasonSeveralPayments)
	add("appendix-a-payment-not-run", reportBooks(t, notRun), keepCopy("2026-11-09T12:00:00Z"))

	writeOrCompare(t, filepath.Join(reportDir, "vectors.json"), map[string]any{
		"description": "x-deal-v1 reports (evidencebook#461, docs/design/461-deal-profile.md §9): BuildReport(books, copy) must give report. Written by dealprofile/xdealv1's TestReportVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	})
}

// reportCopy is ReportCopy as a vector file holds it.
type reportCopy struct {
	Audience string         `json:"audience"`
	Nonce    string         `json:"nonce"`
	Referee  string         `json:"referee,omitempty"`
	AsOf     string         `json:"as_of"`
	Text     map[string]any `json:"report,omitempty"`
}

func TestReportVectorsReplay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(reportDir, "vectors.json"))
	require.NoError(t, err)
	var v struct {
		Cases []struct {
			Name   string          `json:"name"`
			Books  ReportBooks     `json:"books"`
			Copy   reportCopy      `json:"copy"`
			Report json.RawMessage `json:"report"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NotEmpty(t, v.Cases)
	for _, c := range v.Cases {
		got, err := BuildReport(c.Books, ReportCopy(c.Copy))
		require.NoError(t, err, c.Name)
		g, err := json.Marshal(got)
		require.NoError(t, err)
		assert.JSONEq(t, string(c.Report), string(g), c.Name)
	}
}

// writeOrCompare writes v as indented JSON under EB_WRITE_DEAL_V1_VECTORS=1,
// else compares it with the file byte for byte.
func writeOrCompare(t *testing.T, path string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "%s differs from this build's; regenerate only for an intended change", path)
}

// The report from a real shelf (StatusProvider.Report): it writes nothing,
// rests on the holder's book at its latest checkpoint, which must cover the
// whole book, and on the seller's counted prefix, and says whether the
// book's last status fact is the status computed now: current right after a
// stamp, not current once a received record changes the status no stamp has
// stated yet.
func TestStatusProviderReport(t *testing.T) {
	ctx := context.Background()
	d := newPaymentDeal(t, func(o *ProposeBody) { o.Witnesses = []Witness{} }, true)
	d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
	d.buyer.recUseStatus(map[string][]byte{d.offerDigest: d.offerPair})
	proof := d.seller.stamp(d.sellerAgID)
	d.buyer.receive(d.sellerAgPair, &proof)
	at := d.buyer.now.UTC().Truncate(time.Second).Format(time.RFC3339)
	book, err := d.buyer.shelf.Open(ctx, d.buyerLog)
	require.NoError(t, err)
	size := book.Size()

	_, err = d.buyer.recReport(d.buyerLog, keepCopy(at))
	require.ErrorContains(t, err, "no checkpoint at its size", "before a stamp checkpoints the book")
	assert.Equal(t, size, book.Size(), "a report writes nothing")

	_, err = d.buyer.recStampTick()
	require.NoError(t, err)
	r, err := d.buyer.recReport(d.buyerLog, keepCopy(at))
	require.NoError(t, err)
	assert.Equal(t, size, book.Size(), "a report writes nothing")
	cps, err := book.Checkpoints(ctx)
	require.NoError(t, err)
	last := cps[len(cps)-1]
	assert.Equal(t, dealprofile.Checkpoint{LogID: d.buyerLog, MMRSize: last.TreeSize, Root: last.Root}, r.DealCheckpoint)
	assert.Equal(t, size, r.RestsOn.Own.Size)
	assert.Equal(t, last.Entries, r.RestsOn.Own.Size, "the checkpoint covers exactly the records the rows read")
	require.NotNil(t, r.StatusFact)
	assert.True(t, r.StatusFact.Current, "the last fact is the status computed now")
	require.NotNil(t, r.RestsOn.Counterparty)
	assert.Equal(t, d.sellerLog, r.RestsOn.Counterparty.LogID)
	assert.Zero(t, r.RestsOn.Counterparty.ForkAt)
	assert.Equal(t, ProofNotYet, r.RestsOn.CounterpartyProof, "the seller's proof covers two records and one is held")

	d.buyer.receive(payee, nil)
	r, err = d.buyer.recReport(d.buyerLog, keepCopy(at))
	require.NoError(t, err)
	require.NotNil(t, r.StatusFact)
	assert.False(t, r.StatusFact.Current, "a received record no stamp has stated yet: the fact is not current")
	assert.Equal(t, uint64(2), r.RestsOn.Counterparty.Counted)
	assert.Equal(t, ProofHeld, r.RestsOn.CounterpartyProof, "the proof covers once both records are held")

	_, err = d.buyer.recReport("deal/ffffffffffffffffffffffffffffffff", keepCopy(at))
	assert.Error(t, err, "no such book")

	// A record not readable as x-deal-v1 (a retention change's lifecycle
	// record) is a permanent refusal, not ErrBusy (the stand-in, #530 review).
	d.buyer.recRetentionIn(d.buyerLog, 2, evidencebook.Withheld)
	_, err = d.buyer.recStampTick()
	require.NoError(t, err)
	_, err = d.buyer.recReport(d.buyerLog, keepCopy(at))
	require.ErrorContains(t, err, "not a readable x-deal-v1 record")
	assert.NotErrorIs(t, err, evidencebook.ErrBusy)
}

// paymentsOver is the payment row's PaymentCheck result over legs: the
// offer's digest, then each leg's record digest.
func paymentsOver(t *testing.T, b StatusBooks, check, reason string, legs ...StatusRecord) map[string]PaymentResult {
	t.Helper()
	od, err := Digest(b.Offer)
	require.NoError(t, err)
	over := []string{od}
	for _, l := range legs {
		d, err := Digest(l.Record)
		require.NoError(t, err)
		over = append(over, d)
	}
	return map[string]PaymentResult{"paid_as_agreed": {Check: check, Reason: reason, CheckedOver: over}}
}

// payeeLegUnstated is b with the seller's payee leg (its seq 2) stating
// nothing, so no statement puts it in a row's relied_on.
func payeeLegUnstated(t *testing.T, b StatusBooks) StatusBooks {
	t.Helper()
	b.Counterpart = append([]StatusRecord(nil), b.Counterpart...)
	leg := *b.Counterpart[1].Record.Body.(*SettleBody)
	require.Equal(t, LegPayee, leg.Leg)
	leg.Statements = statements()
	b.Counterpart[1].Record.Body = &leg
	return b
}

// The legs the payment check read join the payment row's relied_on, after
// the records it listed already and only once; a check not run over legs
// adds none; and a result that does not begin with the offer's digest, or
// names a record the books do not hold, is refused.
func TestBuildReportPaymentLegs(t *testing.T) {
	_, b := appendixAStatus(t)
	at := "2026-11-09T12:00:00Z"
	ids := func(r Report) []string {
		var out []string
		for _, s := range rowByID(t, r, "paid_as_agreed").ReliedOn {
			out = append(out, s.Record+"/"+s.Side)
		}
		return out
	}
	base, err := BuildReport(reportBooks(t, heldAt(b, at)), keepCopy(at))
	require.NoError(t, err)

	paid := heldAt(b, at)
	paid.Payments = paymentsOver(t, paid, PaymentPass, "", paid.Own[1], paid.Counterpart[1])
	r, err := BuildReport(reportBooks(t, paid), keepCopy(at))
	require.NoError(t, err)
	assert.Equal(t, ids(base), ids(r), "both legs state the row: already relied on, not listed twice")
	assert.Equal(t, CheckPassed, rowByID(t, r, "paid_as_agreed").Check)

	unstated := payeeLegUnstated(t, heldAt(b, at))
	r, err = BuildReport(reportBooks(t, unstated), keepCopy(at))
	require.NoError(t, err)
	before := ids(r)
	assert.NotContains(t, before, unstated.Counterpart[1].RecordID+"/seller", "a leg stating nothing is no statement source")
	unstated.Payments = paymentsOver(t, unstated, PaymentPass, "", unstated.Own[1], unstated.Counterpart[1])
	r, err = BuildReport(reportBooks(t, unstated), keepCopy(at))
	require.NoError(t, err)
	assert.Equal(t, append(before, unstated.Counterpart[1].RecordID+"/seller"), ids(r), "the payee leg joins through the check, last")
	for _, id := range []string{"delivered", "as_described"} {
		assert.NotContains(t, func() []string {
			var out []string
			for _, s := range rowByID(t, r, id).ReliedOn {
				out = append(out, s.Record)
			}
			return out
		}(), unstated.Counterpart[1].RecordID, "only the payment row relies on its legs")
	}

	unstated.Payments = paymentsOver(t, unstated, "", ReasonSeveralPayments)
	r, err = BuildReport(reportBooks(t, unstated), keepCopy(at))
	require.NoError(t, err)
	assert.Equal(t, before, ids(r), "a check not run over legs adds none")

	for name, p := range map[string]map[string]PaymentResult{
		"empty":         {"paid_as_agreed": {}},
		"not the offer": {"paid_as_agreed": {CheckedOver: []string{strings.Repeat("0", 64)}}},
		"not held": func() map[string]PaymentResult {
			m := paymentsOver(t, unstated, PaymentPass, "", unstated.Own[1])
			res := m["paid_as_agreed"]
			res.CheckedOver = append(res.CheckedOver, strings.Repeat("e", 64))
			m["paid_as_agreed"] = res
			return m
		}(),
	} {
		bad := unstated
		bad.Payments = p
		_, err := BuildReport(reportBooks(t, bad), keepCopy(at))
		assert.ErrorContains(t, err, "payment check", name)
	}
	// A leg the books held at as_of no longer: the result names a record
	// the report does not rest on.
	early := heldAt(b, "2026-11-01T10:05:00Z")
	early.Payments = paymentsOver(t, heldAt(b, at), PaymentPass, "", paid.Own[1], paid.Counterpart[1])
	_, err = BuildReport(reportBooks(t, early), keepCopy("2026-11-01T10:05:00Z"))
	assert.ErrorContains(t, err, "do not hold")
}
