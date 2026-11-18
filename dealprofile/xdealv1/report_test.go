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
	assert.Equal(t, strp(rowstatus.RemedyDue), row.Remedy)
	require.NotNil(t, row.Ruling)
	assert.True(t, row.Ruling.Final)
	assert.Equal(t, strp(RoleSeller), row.Ruling.Against)
	assert.Equal(t, b.CaseBook[1].RecordID, row.Ruling.Record, "the first final ruling")
	assert.Contains(t, row.ReliedOn, ReportSource{Record: b.CaseBook[1].RecordID, Side: SideAdjudicator, Time: b.CaseBook[1].Time.known(), Witnessed: true})
	assert.False(t, r.Settled, "a ruled row with remedy due keeps the deal unsettled")

	r = at("2026-11-28T12:00:00Z")
	assert.Equal(t, strp(rowstatus.RemedyNotHonoured), rowByID(t, r, "as_described").Remedy,
		"the refund is a payment remedy, which rowstatus does not count yet (RowInputs), so it reads not honoured")
	assert.Len(t, r.Disputes, 1, "one dispute record, one entry")
	assert.Equal(t, rowstatus.AgreedSoFar, at("2026-11-01T12:00:00Z").Rows[0].Status)
}

// What each audience's copy holds (design §9 table): an adjudicator's
// withholds the rows, the counterparty's prefix, the witnesses, disputes and
// the unnamed count, and names its referee; the others do not.
func TestBuildReportAudiences(t *testing.T) {
	_, b := appendixAStatus(t)
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
	empty := rb
	empty.Books.Own = nil
	_, err := BuildReport(empty, keepCopy("2026-11-05T12:00:00Z"))
	assert.Error(t, err)
}

// settled (design §9, the settled vectors).
func TestSettled(t *testing.T) {
	started := StartResult{State: StateStarted}
	row := func(status, remedy string) ReportRow {
		r := ReportRow{Status: status}
		if remedy != "" {
			r.Remedy = &remedy
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
	}
	for _, c := range cases {
		var rows []ReportRow
		for _, s := range c.Rows {
			status, remedy, _ := strings.Cut(s, ":")
			rows = append(rows, row(status, remedy))
		}
		st := StartResult{State: StateNotStarted}
		if c.Started {
			st = started
		}
		assert.Equal(t, c.Settled, settled(st, rows), c.Name)
	}
	writeOrCompare(t, filepath.Join("../../testdata/deal-v1/settled", "vectors.json"), map[string]any{
		"description": "x-deal-v1 settled (docs/design/461-deal-profile.md §9): a deal is settled when it has started and every row that is not not_reached is agreed, uncontested, or ruled with remedy none or made. Each row is \"status\" or \"ruled:remedy\". Written by dealprofile/xdealv1's TestSettled (EB_WRITE_DEAL_V1_VECTORS=1).",
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

	unnamed := heldAt(b, "2026-11-21T12:00:00Z")
	unnamed.CaseBook, unnamed.RulingsUnnamed = nil, 2
	rb := reportBooks(t, unnamed)
	rb.Fact = &ReportFact{Current: false}
	add("ruling-held-unnamed", rb, keepCopy("2026-11-21T12:00:00Z"))

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

// The report from a real shelf (StatusProvider.Report): it rests on the
// holder's book at its checkpoint and the seller's counted prefix, and says
// whether the book's last status fact is the status computed now: none
// before the first stamp, current right after it, and not current once a
// received record changes the status no stamp has stated yet.
func TestStatusProviderReport(t *testing.T) {
	ctx := context.Background()
	d := newPaymentDeal(t, func(o *ProposeBody) { o.Witnesses = []Witness{} }, true)
	d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
	d.buyer.status = &StatusProvider{OfferPair: func(_ context.Context, digest string) ([]byte, error) {
		if digest == d.offerDigest {
			return d.offerPair, nil
		}
		return nil, nil
	}}
	d.buyer.reopen()
	proof := d.seller.stamp(d.sellerAgID)
	d.buyer.receive(d.sellerAgPair, &proof)
	at := d.buyer.now.UTC().Truncate(time.Second).Format(time.RFC3339)

	r, err := d.buyer.status.Report(ctx, d.buyerLog, keepCopy(at))
	require.NoError(t, err)
	assert.Nil(t, r.StatusFact, "no fact before the first stamp")
	assert.Equal(t, d.buyerLog, r.RestsOn.Own.LogID)
	assert.Equal(t, d.buyerLog, r.DealCheckpoint.LogID)
	assert.NotEmpty(t, r.DealCheckpoint.Root)
	require.NotNil(t, r.RestsOn.Counterparty)
	assert.Equal(t, d.sellerLog, r.RestsOn.Counterparty.LogID)

	d.buyer.tick()
	_, err = d.buyer.cadence.Stamp(ctx, d.buyer.now)
	require.NoError(t, err)
	r, err = d.buyer.status.Report(ctx, d.buyerLog, keepCopy(at))
	require.NoError(t, err)
	require.NotNil(t, r.StatusFact)
	assert.True(t, r.StatusFact.Current, "the last fact is the status computed now")
	assert.Equal(t, ProofNotYet, r.RestsOn.CounterpartyProof, "the seller's proof covers two records and one is held")

	d.buyer.receive(payee, nil)
	r, err = d.buyer.status.Report(ctx, d.buyerLog, keepCopy(at))
	require.NoError(t, err)
	require.NotNil(t, r.StatusFact)
	assert.False(t, r.StatusFact.Current, "a received record no stamp has stated yet: the fact is not current")
	assert.Equal(t, uint64(2), r.RestsOn.Counterparty.Counted)
	assert.Equal(t, ProofHeld, r.RestsOn.CounterpartyProof, "the proof covers once both records are held")

	_, err = d.buyer.status.Report(ctx, "deal/ffffffffffffffffffffffffffffffff", keepCopy(at))
	assert.Error(t, err, "no such book")
}
