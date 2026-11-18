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
