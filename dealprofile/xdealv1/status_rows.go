// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"fmt"
	"slices"

	"github.com/action-state-group/evidencebook/rowstatus"
)

// TimedRecord is a record of a deal book or case book as the holder holds
// it, with its record time (design §4.3): the earliest time a witness the
// agreement names gave a checkpoint covering it, or nil for none yet.
type TimedRecord struct {
	Record   Record  `json:"record"`
	RecordID string  `json:"record_id"`
	Time     *string `json:"time"`
}

// RowBooks is what the row inputs are built from (design §6.1): the offer
// whose terms_rows the deal agreed, the holder's deal book, the counted
// prefix of the counterparty's, and the case book of the adjudicator the
// agreement names, each in book order. ConflictingFinalRulings are the rows
// for which the holder holds two forks of that case book with different
// first final rulings. Checks are the recomputed rows' check results by row
// id: a payment row's PaymentCheck Check ("pass", "fail", or "" for not
// run).
type RowBooks struct {
	Offer                   Record            `json:"offer"`
	Own                     []TimedRecord     `json:"own"`
	Counterpart             []TimedRecord     `json:"counterpart"`
	CaseBook                []TimedRecord     `json:"case_book"`
	ConflictingFinalRulings []string          `json:"conflicting_final_rulings,omitempty"`
	Checks                  map[string]string `json:"checks,omitempty"`
}

// RowInputs projects the two books onto one rowstatus.Input per agreed row,
// as of at (design §4.2, §4.4, §6.1). Only the records given count, so the
// caller passes the counterparty's counted prefix only.
//
// A recomputed row's check is the caller's, from b.Checks (a payment row's
// PaymentCheck, §6.3); a recomputed row it holds none for is not run, and a
// check given for a row that is not recomputed, or not in the terms, is refused. A remedy that is
// a payment (§6.1) is not counted: it needs its own terms leg, which the
// profile does not define yet. A non-payment remedy counts when its act is
// the effective ruling's remedy kind.
func RowInputs(b RowBooks, at string) ([]rowstatus.Input, error) {
	ins, _, err := rowInputs(b, at)
	return ins, err
}

// recRef names a record rowInputs read: its book (bookOwn, bookCounterpart,
// bookCase) and its index there.
type recRef struct {
	book byte
	i    int
}

const (
	bookOwn         byte = 'o'
	bookCounterpart byte = 'c'
	bookCase        byte = 'k'
)

// rowSources are, for one row's input, the record each element came from, in
// the input's order, so a status value can give each its own time encoding
// (design §8.1). named maps a name to the first marked record, when one is.
type rowSources struct {
	statements, disputes, withdrawals, rulings, remedies []recRef
	named                                                map[string]recRef
}

func rowInputs(b RowBooks, at string) ([]rowstatus.Input, []rowSources, error) {
	of, ok := asPropose(b.Offer.Body)
	if !ok {
		return nil, nil, fmt.Errorf("xdealv1: row inputs: the offer is a %s record", b.Offer.Block.RecordType)
	}
	if len(b.Own) == 0 {
		return nil, nil, fmt.Errorf("xdealv1: row inputs: the holder's book is empty")
	}
	own, ok := asAgree(b.Own[0].Record.Body)
	if !ok || own.Kind != KindAgreement {
		return nil, nil, fmt.Errorf("xdealv1: row inputs: the holder's book does not open with its agreement")
	}
	ownRole, otherRole := own.Role, RoleBuyer
	if ownRole == RoleBuyer {
		otherRole = RoleSeller
	}
	books := map[string][]TimedRecord{ownRole: b.Own, otherRole: b.Counterpart}
	bookOf := map[string]byte{ownRole: bookOwn, otherRole: bookCounterpart}
	tr := of.TermsRows

	// Named records (§4.4): the first record of the named side's book that
	// carries the mark.
	named := map[string]rowstatus.Named{}
	namedSrc := map[string]recRef{}
	for name, n := range tr.Named {
		var t rowstatus.Time
		for i, r := range books[n.Side] {
			if slices.Contains(r.Record.Block.Marks, name) {
				t = r.Time
				namedSrc[name] = recRef{bookOf[n.Side], i}
				break
			}
		}
		named[name] = rowstatus.Named{Time: t, Latest: n.Latest}
	}

	// The effective ruling (§8.1): the adjudicator's first final ruling, or
	// none when its case book forked.
	var rulings []TimedRecord
	var rulingAt []int
	if adj := own.Adjudicator; adj.V != nil {
		for i, r := range b.CaseBook {
			if r.Record.Block.RecordType == TypeRuling && r.Record.Block.DealID == b.Offer.Block.DealID {
				rulings = append(rulings, r)
				rulingAt = append(rulingAt, i)
			}
		}
	}

	for id := range b.Checks {
		if !slices.ContainsFunc(tr.Rows, func(r Row) bool { return r.ID == id }) {
			return nil, nil, fmt.Errorf("xdealv1: row inputs: a check given for row %q, which the terms do not have", id)
		}
	}
	var out []rowstatus.Input
	var srcs []rowSources
	for _, row := range tr.Rows {
		src := rowSources{named: namedSrc}
		in := rowstatus.Input{At: at, Parties: []string{RoleBuyer, RoleSeller}, Named: named,
			Row:                     rowstatus.Row{ID: row.ID, OwedBy: row.OwedBy, Due: projectDate(row.Due), WindowEnd: projectDate(row.WindowEnd), Recomputed: row.Recomputed},
			ConflictingFinalRulings: slices.Contains(b.ConflictingFinalRulings, row.ID)}
		if c, given := b.Checks[row.ID]; given {
			if !row.Recomputed {
				return nil, nil, fmt.Errorf("xdealv1: row inputs: a check given for row %q, which is not recomputed", row.ID)
			}
			in.Check = c
		}
		for _, side := range []string{RoleBuyer, RoleSeller} {
			for i, r := range books[side] {
				ref := recRef{bookOf[side], i}
				for _, it := range statementItems(r.Record) {
					if it.Row == row.ID {
						in.Statements = append(in.Statements, rowstatus.Statement{Side: side, Value: it.Value, Time: r.Time})
						src.statements = append(src.statements, ref)
					}
				}
				if d, ok := bodyOf[DisputeBody](r.Record.Body); ok && d.Row == row.ID {
					switch d.Kind {
					case KindDispute:
						in.Disputes = append(in.Disputes, rowstatus.Dispute{Side: side, Time: r.Time})
						src.disputes = append(src.disputes, ref)
					case KindWithdrawal:
						in.Withdrawals = append(in.Withdrawals, rowstatus.Withdrawal{Side: side, Time: r.Time})
						src.withdrawals = append(src.withdrawals, ref)
					}
				}
			}
		}
		var effective *RulingBody
		for k, r := range rulings {
			rb, _ := bodyOf[RulingBody](r.Record.Body)
			if rb == nil || rb.Row != row.ID {
				continue
			}
			ru := rowstatus.Ruling{Final: rb.Final, Value: rb.Value, Time: r.Time}
			if rb.Remedy != nil {
				due := rb.Remedy.Due
				ru.RemedyDue = &due
			}
			in.Rulings = append(in.Rulings, ru)
			src.rulings = append(src.rulings, recRef{bookCase, rulingAt[k]})
			if rb.Final && effective == nil && !in.ConflictingFinalRulings {
				effective = rb
			}
		}
		if effective != nil && effective.Against.V != nil && effective.Remedy != nil {
			for i, r := range books[*effective.Against.V] {
				if p, ok := bodyOf[PerformBody](r.Record.Body); ok && p.RemedyFor != nil && p.RemedyFor.Row == row.ID && p.Act == effective.Remedy.Kind {
					in.Remedies = append(in.Remedies, rowstatus.Remedy{Time: r.Time})
					src.remedies = append(src.remedies, recRef{bookOf[*effective.Against.V], i})
				}
			}
		}
		if err := seenInTime(&in, books, row.ID); err != nil {
			return nil, nil, err
		}
		out = append(out, in)
		srcs = append(srcs, src)
	}
	return out, srcs, nil
}

func projectDate(d Date) rowstatus.Date {
	out := rowstatus.Date{After: d.After}
	if d.At != "" {
		at := d.At
		out.At = &at
	}
	if d.Seconds != nil {
		out.Seconds = *d.Seconds
	}
	return out
}

// statementItems are the statement items a record carries, its body held as
// a value or a pointer.
func statementItems(r Record) []StatementItem {
	if b, ok := bodyOf[PerformBody](r.Body); ok {
		return b.Statements
	}
	if b, ok := bodyOf[SettleBody](r.Body); ok {
		return b.Statements
	}
	if b, ok := bodyOf[StatementBody](r.Body); ok {
		return b.Statements
	}
	return nil
}

// seenInTime sets each dispute's seen_in_time (design §6.2): a record of the
// claimant's own book, timed not after the row's window end, whose seen
// reaches the record of the statement the dispute did not contest: the other
// side's standing statement, its latest statement on the row rowstatus does
// not hold late (timed not after the window end, or untimed with no earlier
// statement of its side timed after it; rowstatus.go:449-467).
func seenInTime(in *rowstatus.Input, books map[string][]TimedRecord, rowID string) error {
	if len(in.Disputes) == 0 {
		return nil
	}
	plain := *in
	plain.Disputes, plain.Withdrawals, plain.Rulings, plain.Remedies = nil, nil, nil, nil
	res, err := rowstatus.Compute(plain)
	if err != nil {
		return err
	}
	if res.WindowEnd == "" {
		return nil
	}
	for i, d := range in.Disputes {
		other := RoleBuyer
		if d.Side == RoleBuyer {
			other = RoleSeller
		}
		standing := -1
		var floor *string
		for j, r := range books[other] {
			if len(rowItems(r.Record, rowID)) == 0 {
				continue
			}
			late := (r.Time != nil && *r.Time > res.WindowEnd) || (r.Time == nil && floor != nil && *floor > res.WindowEnd)
			if !late {
				standing = j
			}
			if r.Time != nil && (floor == nil || *r.Time > *floor) {
				floor = r.Time
			}
		}
		if standing < 0 {
			continue
		}
		ids := map[string]int{}
		for j, r := range books[other] {
			ids[r.RecordID] = j
		}
		for _, r := range books[d.Side] {
			s := r.Record.Block.Seen
			if s.V == nil || r.Time == nil || *r.Time > res.WindowEnd {
				continue
			}
			if j, ok := ids[*s.V]; ok && j >= standing {
				in.Disputes[i].SeenInTime = true
				break
			}
		}
	}
	return nil
}

// rowItems are a record's statement items on one row.
func rowItems(r Record, rowID string) []StatementItem {
	var out []StatementItem
	for _, it := range statementItems(r) {
		if it.Row == rowID {
			out = append(out, it)
		}
	}
	return out
}
