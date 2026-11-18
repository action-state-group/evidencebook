// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/rowstatus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appendixABooks are Appendix A's books as the buyer holds them on 26 Nov,
// each record timed at its own at (as if every stamp were witnessed at
// once): the buyer's book, the seller's, and the adjudicator's case book.
func appendixABooks(t *testing.T, a *appendixA, path string) []TimedRecord {
	t.Helper()
	var out []TimedRecord
	for _, f := range a.list {
		if strings.HasPrefix(f.Path, path+"/") {
			at := f.Record.Block.At
			out = append(out, TimedRecord{Record: f.Record, RecordID: recordID(f.digest(t)), Time: &at})
		}
	}
	return out
}

func statusOf(t *testing.T, ins []rowstatus.Input, row string) rowstatus.Result {
	t.Helper()
	for _, in := range ins {
		if in.Row.ID == row {
			r, err := rowstatus.Compute(in)
			require.NoError(t, err)
			return r
		}
	}
	t.Fatalf("no row %s", row)
	return rowstatus.Result{}
}

// Appendix A, computed from the buyer's books with every record timed: rows
// 1 and 2 agreed met, row 3 ruled not met. The refund is a payment remedy,
// which counts only once the payment check is built (§6.1).
func TestRowInputsAppendixA(t *testing.T) {
	a := buildAppendixA(t)
	b := RowBooks{Offer: a.record("appendix-a/buyer-journal/05-offer.json"),
		Own: appendixABooks(t, a, "appendix-a/buyer-book"), Counterpart: appendixABooks(t, a, "appendix-a/seller-book"),
		CaseBook: appendixABooks(t, a, "appendix-a/case-book")}
	ins, err := RowInputs(b, "2026-11-26T12:00:00Z")
	require.NoError(t, err)
	require.Len(t, ins, 3)
	for _, in := range ins {
		_, err := rowstatus.Compute(in)
		require.NoError(t, err, in.Row.ID)
	}
	paid := statusOf(t, ins, "paid_as_agreed")
	assert.Equal(t, rowstatus.Agreed, paid.Status)
	assert.Equal(t, rowstatus.Met, paid.Value)
	delivered := statusOf(t, ins, "delivered")
	assert.Equal(t, rowstatus.Agreed, delivered.Status)
	described := statusOf(t, ins, "as_described")
	assert.Equal(t, rowstatus.Ruled, described.Status)
	assert.Equal(t, rowstatus.NotMet, described.Value)
	assert.False(t, described.Local)

	// The named records: paid from the buyer's payment, received from its
	// receipt.
	in := ins[1]
	require.NotNil(t, in.Named["paid"].Time)
	assert.Equal(t, "2026-11-01T10:15:00Z", *in.Named["paid"].Time)
	require.NotNil(t, in.Named["received"].Time)
	assert.Equal(t, "2026-11-04T15:00:00Z", *in.Named["received"].Time)

	// The buyer's dispute on row 3: its book named the seller's reply (its
	// statement on the row) before the window ended.
	d := ins[2]
	require.Len(t, d.Disputes, 1)
	assert.True(t, d.Disputes[0].SeenInTime)
	assert.Len(t, d.Rulings, 2)
}

// The settled path: the buyer withdraws after the seller's concession; row 3
// is agreed not met.
func TestRowInputsSettledPath(t *testing.T) {
	a := buildAppendixA(t)
	buyer := appendixABooks(t, a, "appendix-a/buyer-book")[:5]
	seller := appendixABooks(t, a, "appendix-a/seller-book")[:4]
	buyer = append(buyer, appendixABooks(t, a, "settled/buyer-book")...)
	seller = append(seller, appendixABooks(t, a, "settled/seller-book")...)
	ins, err := RowInputs(RowBooks{Offer: a.record("appendix-a/buyer-journal/05-offer.json"), Own: buyer, Counterpart: seller}, "2026-11-26T12:00:00Z")
	require.NoError(t, err)
	r := statusOf(t, ins, "as_described")
	assert.Equal(t, rowstatus.Agreed, r.Status)
	assert.Equal(t, rowstatus.NotMet, r.Value)
}

func timedAt(r Record, id, at string) TimedRecord {
	return TimedRecord{Record: r, RecordID: id, Time: &at}
}

func stmtRecord(seq int64, seen string, items ...StatementItem) Record {
	b := journalBlock(TypeStatement, "2026-11-10T00:00:00Z", true)
	b.Seq = seq
	if seen == "" {
		b.Seen = NullOf[string]()
	} else {
		b.Seen = Some(seen)
	}
	return Record{Block: b, Body: &StatementBody{Statements: statements(items...)}}
}

// seen_in_time names the other side's standing statement (§6.2): the
// seller states met, then not_met, both in time; the buyer's book named only
// the first before the window ended, so a late dispute is not set aside.
func TestSeenInTimeNamesTheStandingStatement(t *testing.T) {
	a := buildAppendixA(t)
	offer := a.record("appendix-a/buyer-journal/05-offer.json")
	buyer := appendixABooks(t, a, "appendix-a/buyer-book")[:3] // agreement, payment, receipt (4 Nov)
	seller := appendixABooks(t, a, "appendix-a/seller-book")[:3]
	seller = append(seller,
		timedAt(stmtRecord(4, "", item("as_described", ValueMet)), label("seller met"), "2026-11-10T14:00:00Z"),
		timedAt(stmtRecord(5, "", item("as_described", ValueNotMet)), label("seller not met"), "2026-11-11T14:00:00Z"))
	buyer = append(buyer,
		timedAt(stmtRecord(4, label("seller met"), item("delivered", ValueMet)), label("buyer names met"), "2026-11-12T09:00:00Z"))
	db := journalBlock(TypeDispute, "2026-11-20T09:00:00Z", true)
	db.Seq, db.Seen = 5, Some(label("seller met"))
	buyer = append(buyer, timedAt(Record{Block: db, Body: &DisputeBody{Kind: KindDispute, Row: "as_described"}}, label("buyer dispute"), "2026-11-20T09:00:00Z"))
	ins, err := RowInputs(RowBooks{Offer: offer, Own: buyer, Counterpart: seller}, "2026-11-26T12:00:00Z")
	require.NoError(t, err)
	d := ins[2]
	require.Len(t, d.Disputes, 1)
	assert.False(t, d.Disputes[0].SeenInTime, "the buyer never named the seller's standing not_met")

	// Naming the standing record in time sets it.
	buyer[3] = timedAt(stmtRecord(4, label("seller not met"), item("delivered", ValueMet)), label("buyer names not met"), "2026-11-12T09:00:00Z")
	ins, err = RowInputs(RowBooks{Offer: offer, Own: buyer, Counterpart: seller}, "2026-11-26T12:00:00Z")
	require.NoError(t, err)
	assert.True(t, ins[2].Disputes[0].SeenInTime)
}

// A forked case book on one row marks that row only.
func TestConflictingFinalRulingsPerRow(t *testing.T) {
	a := buildAppendixA(t)
	b := RowBooks{Offer: a.record("appendix-a/buyer-journal/05-offer.json"),
		Own: appendixABooks(t, a, "appendix-a/buyer-book"), Counterpart: appendixABooks(t, a, "appendix-a/seller-book"),
		CaseBook: appendixABooks(t, a, "appendix-a/case-book"), ConflictingFinalRulings: []string{"as_described"}}
	ins, err := RowInputs(b, "2026-11-26T12:00:00Z")
	require.NoError(t, err)
	assert.False(t, ins[1].ConflictingFinalRulings)
	assert.True(t, ins[2].ConflictingFinalRulings)
	assert.Equal(t, rowstatus.Agreed, statusOf(t, ins, "delivered").Status)
	assert.Equal(t, rowstatus.InDispute, statusOf(t, ins, "as_described").Status)
}

// A body held as a value projects as the same body held as a pointer.
func TestRowInputsTakeValueBodies(t *testing.T) {
	a := buildAppendixA(t)
	offer := a.record("appendix-a/buyer-journal/05-offer.json")
	own, cp := appendixABooks(t, a, "appendix-a/buyer-book"), appendixABooks(t, a, "appendix-a/seller-book")
	want, err := RowInputs(RowBooks{Offer: offer, Own: own, Counterpart: cp}, "2026-11-26T12:00:00Z")
	require.NoError(t, err)
	vals := make([]TimedRecord, len(own))
	for i, r := range own {
		vals[i] = r
		switch b := r.Record.Body.(type) {
		case *StatementBody:
			vals[i].Record.Body = *b
		case *SettleBody:
			vals[i].Record.Body = *b
		case *DisputeBody:
			vals[i].Record.Body = *b
		}
	}
	got, err := RowInputs(RowBooks{Offer: offer, Own: vals, Counterpart: cp}, "2026-11-26T12:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// A remedy that is not a payment counts when its act is the effective
// ruling's remedy kind, by the side ruled against.
func TestNonPaymentRemedyCounts(t *testing.T) {
	a := buildAppendixA(t)
	cases := appendixABooks(t, a, "appendix-a/case-book")
	for i := range cases {
		rb := *cases[i].Record.Body.(*RulingBody)
		rem := *rb.Remedy
		rem.Kind, rem.AmountMinor, rem.Currency = "replace", nil, ""
		rb.Remedy = &rem
		cases[i].Record.Body = &rb
	}
	seller := appendixABooks(t, a, "appendix-a/seller-book")[:6]
	pb := journalBlock(TypePerform, "2026-11-25T10:00:00Z", true)
	pb.Seq = 7
	replace := Record{Block: pb, Body: &PerformBody{Act: "replace", Observed: "carrier_scan", Statements: statements(), RemedyFor: &RemedyFor{Row: "as_described"}}}
	seller = append(seller, timedAt(replace, label("replacement"), "2026-11-25T10:00:00Z"))
	ins, err := RowInputs(RowBooks{Offer: a.record("appendix-a/buyer-journal/05-offer.json"), Own: appendixABooks(t, a, "appendix-a/buyer-book"),
		Counterpart: seller, CaseBook: cases}, "2026-11-28T12:00:00Z")
	require.NoError(t, err)
	require.Len(t, ins[2].Remedies, 1)
	r := statusOf(t, ins, "as_described")
	assert.Equal(t, rowstatus.Ruled, r.Status)
	assert.Equal(t, rowstatus.RemedyMade, r.Remedy)
}
