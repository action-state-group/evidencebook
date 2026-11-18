// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"github.com/action-state-group/evidencebook/rowstatus"
)

// carriedStatements are the indexes, in order, of the statements of one row
// a fact carries (design §8.1, invariant A, as PR 4c proposes): a statement
// with a nil time is one whose time is not known when the fact is written,
// and will be at or after it. windowEnd is the row's contest window end as
// of the fact, "" while not known. A statement whose time is not known may
// be timed later before the fact (a counterparty record its author's own
// stamp covered first), never before an earlier record of its book. Per side,
// in book order, it keeps every untimed statement; for each, the earlier timed
// statement of that side with the greatest time (rowstatus reads it to judge
// whether the untimed one is late); the
// latest timed statement not after the window end; and every timed statement
// after it whose value the other side may hold in time (a possible
// concession). While the window end is not known it keeps every statement.
// Nothing dropped can change rowstatus.Compute at the fact's time or later.
func carriedStatements(stmts []rowstatus.Statement, windowEnd string) []int {
	all := make([]int, len(stmts))
	for i := range stmts {
		all[i] = i
	}
	if windowEnd == "" {
		return all
	}
	keep := make([]bool, len(stmts))
	// The values each side may hold in time: its latest timed statement in
	// the window, and any untimed one, which might yet be found in time.
	possible := map[string]map[string]bool{}
	for _, side := range []string{RoleBuyer, RoleSeller} {
		possible[side] = map[string]bool{}
		latestIn := -1
		for i, s := range stmts {
			if s.Side != side {
				continue
			}
			switch {
			case s.Time == nil:
				keep[i] = true
				possible[side][s.Value] = true
				// The lower bound rowstatus reads: the earlier timed
				// statement of this side with the greatest time. A book's
				// record times never decrease (a stamp covering a record
				// covers those before it), so it is also the latest.
				best := -1
				for j := 0; j < i; j++ {
					if stmts[j].Side == side && stmts[j].Time != nil && (best < 0 || *stmts[j].Time >= *stmts[best].Time) {
						best = j
					}
				}
				if best >= 0 {
					keep[best] = true
				}
			case *s.Time <= windowEnd:
				latestIn = i
			}
		}
		if latestIn >= 0 {
			keep[latestIn] = true
			possible[side][stmts[latestIn].Value] = true
		}
	}
	for i, s := range stmts {
		if s.Time == nil || *s.Time <= windowEnd {
			continue
		}
		other := RoleBuyer
		if s.Side == RoleBuyer {
			other = RoleSeller
		}
		if possible[other][s.Value] {
			keep[i] = true
		}
	}
	var out []int
	for i, k := range keep {
		if k {
			out = append(out, i)
		}
	}
	return out
}
