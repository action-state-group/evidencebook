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
// agreement names, each in book order. ConflictingFinalRulings says the
// holder holds two forks of that case book with different first final
// rulings.
type RowBooks struct {
	Offer                   Record        `json:"offer"`
	Own                     []TimedRecord `json:"own"`
	Counterpart             []TimedRecord `json:"counterpart"`
	CaseBook                []TimedRecord `json:"case_book"`
	ConflictingFinalRulings bool          `json:"conflicting_final_rulings"`
}

// RowInputs projects the two books onto one rowstatus.Input per agreed row,
// as of at (design §4.2, §4.4, §6.1). Only the records given count, so the
// caller passes the counterparty's counted prefix only.
//
// The recomputed check of a payment row (§6.3) and a remedy that is a
// payment (§6.1) need the payment check, which is not built yet: such a row's
// check is not run, and a payment remedy is not counted. A non-payment
// remedy counts when its act is the effective ruling's remedy kind.
func RowInputs(b RowBooks, at string) ([]rowstatus.Input, error) {
	of, ok := asPropose(b.Offer.Body)
	if !ok {
		return nil, fmt.Errorf("xdealv1: row inputs: the offer is a %s record", b.Offer.Block.RecordType)
	}
	if len(b.Own) == 0 {
		return nil, fmt.Errorf("xdealv1: row inputs: the holder's book is empty")
	}
	own, ok := asAgree(b.Own[0].Record.Body)
	if !ok || own.Kind != KindAgreement {
		return nil, fmt.Errorf("xdealv1: row inputs: the holder's book does not open with its agreement")
	}
	ownRole, otherRole := own.Role, RoleBuyer
	if ownRole == RoleBuyer {
		otherRole = RoleSeller
	}
	books := map[string][]TimedRecord{ownRole: b.Own, otherRole: b.Counterpart}
	tr := of.TermsRows

	// Named records (§4.4): the first record of the named side's book that
	// carries the mark.
	named := map[string]rowstatus.Named{}
	for name, n := range tr.Named {
		var t rowstatus.Time
		for _, r := range books[n.Side] {
			if slices.Contains(r.Record.Block.Marks, name) {
				t = r.Time
				break
			}
		}
		named[name] = rowstatus.Named{Time: t, Latest: n.Latest}
	}

	// The effective ruling (§8.1): the adjudicator's first final ruling, or
	// none when its case book forked.
	var rulings []TimedRecord
	if adj := own.Adjudicator; adj.V != nil {
		for _, r := range b.CaseBook {
			if r.Record.Block.RecordType == TypeRuling && r.Record.Block.DealID == b.Offer.Block.DealID {
				rulings = append(rulings, r)
			}
		}
	}

	var out []rowstatus.Input
	for _, row := range tr.Rows {
		in := rowstatus.Input{At: at, Parties: []string{RoleBuyer, RoleSeller}, Named: named,
			Row:                     rowstatus.Row{ID: row.ID, OwedBy: row.OwedBy, Due: projectDate(row.Due), WindowEnd: projectDate(row.WindowEnd), Recomputed: row.Recomputed},
			ConflictingFinalRulings: b.ConflictingFinalRulings}
		for _, side := range []string{RoleBuyer, RoleSeller} {
			for _, r := range books[side] {
				for _, it := range statementItems(r.Record) {
					if it.Row == row.ID {
						in.Statements = append(in.Statements, rowstatus.Statement{Side: side, Value: it.Value, Time: r.Time})
					}
				}
				if d, ok := bodyOf[DisputeBody](r.Record.Body); ok && d.Row == row.ID {
					switch d.Kind {
					case KindDispute:
						in.Disputes = append(in.Disputes, rowstatus.Dispute{Side: side, Time: r.Time})
					case KindWithdrawal:
						in.Withdrawals = append(in.Withdrawals, rowstatus.Withdrawal{Side: side, Time: r.Time})
					}
				}
			}
		}
		var effective *RulingBody
		for _, r := range rulings {
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
			if rb.Final && effective == nil && !b.ConflictingFinalRulings {
				effective = rb
			}
		}
		if effective != nil && effective.Against.V != nil && effective.Remedy != nil {
			for _, r := range books[*effective.Against.V] {
				if p, ok := bodyOf[PerformBody](r.Record.Body); ok && p.RemedyFor != nil && p.RemedyFor.Row == row.ID && p.Act == effective.Remedy.Kind {
					in.Remedies = append(in.Remedies, rowstatus.Remedy{Time: r.Time})
				}
			}
		}
		if err := seenInTime(&in, books, row.ID); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
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

// statementItems are the statement items a record carries.
func statementItems(r Record) []StatementItem {
	switch b := r.Body.(type) {
	case *PerformBody:
		return b.Statements
	case *SettleBody:
		return b.Statements
	case *StatementBody:
		return b.Statements
	}
	return nil
}

// seenInTime sets each dispute's seen_in_time (design §6.2): a record of the
// claimant's own book, whose seen reaches the other side's statement on the
// row, has a time not after the row's window end.
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
	for i, d := range in.Disputes {
		other := RoleBuyer
		if d.Side == RoleBuyer {
			other = RoleSeller
		}
		// The other side's statements on the row, by the record id each sits in.
		var stated []int
		for j, r := range books[other] {
			for _, it := range statementItems(r.Record) {
				if it.Row == rowID {
					stated = append(stated, j)
				}
			}
		}
		if len(stated) == 0 || res.WindowEnd == "" {
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
			if j, ok := ids[*s.V]; ok && j >= stated[0] {
				in.Disputes[i].SeenInTime = true
				break
			}
		}
	}
	return nil
}
