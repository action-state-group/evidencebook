// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"math/rand"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/rowstatus"
	"github.com/stretchr/testify/require"
)

// Invariant A, by a randomized differential test: for generated rows and
// statements, rowstatus.Compute over the statements a fact carries equals
// Compute over them all, at every T at or after the fact, however the
// statements whose time was not known then are timed later (or not at all).
func TestCarriedStatementsKeepTheResult(t *testing.T) {
	rng := rand.New(rand.NewSource(461))
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ts := func(h int) string { return base.Add(time.Duration(h) * time.Hour).Format(time.RFC3339) }
	values := []string{rowstatus.Met, rowstatus.NotMet, rowstatus.CannotTell}
	for iter := 0; iter < 20000; iter++ {
		due, window := rng.Intn(48), 48+rng.Intn(96)
		fact := rng.Intn(240)
		dueAt, windowAt := ts(due), ts(window)
		row := rowstatus.Row{ID: "r", OwedBy: RoleSeller, Due: rowstatus.Date{At: &dueAt}, WindowEnd: rowstatus.Date{At: &windowAt}}
		// Each side's book: statements with times known at the fact (not
		// after it, non-decreasing), then a suffix whose times are not known.
		var stmts []rowstatus.Statement
		for _, side := range []string{RoleBuyer, RoleSeller} {
			n := rng.Intn(5)
			h := 0
			untimedFrom := rng.Intn(n + 1)
			for k := 0; k < n; k++ {
				st := rowstatus.Statement{Side: side, Value: values[rng.Intn(len(values))]}
				if k < untimedFrom && fact > 0 {
					h += rng.Intn(fact/(n+1) + 1)
					if h > fact {
						h = fact
					}
					t := ts(h)
					st.Time = &t
				}
				stmts = append(stmts, st)
			}
		}
		in := rowstatus.Input{At: ts(fact), Parties: []string{RoleBuyer, RoleSeller}, Row: row, Statements: stmts}
		res, err := rowstatus.Compute(in)
		require.NoError(t, err)
		kept := carriedStatements(stmts, res.WindowEnd)

		for trial := 0; trial < 5; trial++ {
			// Time the unknown statements at or after the fact, in book
			// order per side, or leave some untimed.
			resolved := make([]rowstatus.Statement, len(stmts))
			copy(resolved, stmts)
			// The buyer's book is the holder's: its records are timed by the
			// holder's stamps, at or after the fact. The seller's is the
			// counterparty's: a record may be timed by its author's own
			// stamp before the fact, though never before an earlier record
			// of its book (design §4.3; Sherlock2 on the 4c proposal).
			last := map[string]int{RoleBuyer: fact, RoleSeller: 0}
			for i, s := range resolved {
				if s.Time != nil {
					if s.Side == RoleSeller {
						tt, _ := time.Parse(time.RFC3339, *s.Time)
						last[RoleSeller] = int(tt.Sub(base).Hours())
					}
					continue
				}
				if rng.Intn(4) == 0 {
					continue
				}
				h := last[s.Side] + rng.Intn(120)
				last[s.Side] = h
				t := ts(h)
				resolved[i].Time = &t
			}
			// A statement left untimed stays untimed for the rest of its book.
			seenUntimed := map[string]bool{}
			for i, s := range resolved {
				if seenUntimed[s.Side] {
					resolved[i].Time = nil
				}
				if s.Time == nil {
					seenUntimed[s.Side] = true
				}
			}
			var sub []rowstatus.Statement
			for _, i := range kept {
				sub = append(sub, resolved[i])
			}
			at := ts(fact + rng.Intn(300))
			full := in
			full.At, full.Statements = at, resolved
			part := full
			part.Statements = sub
			want, err := rowstatus.Compute(full)
			require.NoError(t, err)
			got, err := rowstatus.Compute(part)
			require.NoError(t, err)
			require.Equal(t, want, got, "iteration %d trial %d: statements %+v kept %v at %s", iter, trial, resolved, kept, at)
		}
	}
}
