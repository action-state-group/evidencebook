// SPDX-License-Identifier: Apache-2.0

package xdealv1

import "fmt"

// Deciding a disputed row without a person (Ethan, 2026-11-25; Sherlock2's
// door answers of the same day, buzz 4ec88700 and 319f98a4): a row is decided
// only when the side that owes it has itself stated, in a record it signed,
// that the row was not met, and has not stated otherwise since. Every other
// case needs a person. Nothing is decided from free text (a perform record's
// observed) or from the other side's word. DecideCase only reports: the ruling
// is still sealed by SealRuling (capsulectl deal ruling), and the ruling
// record does not say it was decided this way.

// DecidingRecord is the record that decides a row: the owing side's own
// latest statement on it.
type DecidingRecord struct {
	// Against is the owing side's role, the side the ruling goes against.
	Against string `json:"against"`
	// Value is the row's finding: ValueNotMet.
	Value string `json:"value"`
	// Record is the deciding record's digest and RecordID its record id.
	Record   string `json:"record"`
	RecordID string `json:"record_id"`
	// Seq is its place in the owing side's book; At and TimedBy are its
	// record time and what timed it, as GatherCase read them ("" for none).
	Seq     uint64 `json:"seq"`
	At      string `json:"at"`
	TimedBy string `json:"timed_by"`
}

// Decision is what DecideCase reports for a case's row: the side the dispute
// is raised against, and Decisive when that side's own latest statement
// decides the row, else NeedsPerson says why a person must rule.
type Decision struct {
	Row         string          `json:"row"`
	Against     string          `json:"against"`
	Decisive    *DecidingRecord `json:"decisive,omitempty"`
	NeedsPerson string          `json:"needs_person,omitempty"`
}

// DecideCase reports whether a gathered case's row is decided by the own
// records of the side the dispute is raised against: the side that did not
// sign the dispute, which owes the row (an adjudicator's copy withholds the
// rows, so the dispute names the side). through, when present, is the last
// record id of that side's book to read (a sealed ruling's read for that
// side), so checking a sealed ruling again gives the answer it was sealed on,
// whatever that side stated after; null reads the whole book read.
//
// The owing side's book read by GatherCase holds only records that side
// signed and that verified, in seq order. The statements considered are those
// its records carry on the row (a statement record's, or those a perform or
// settle record carries), and the latest one decides: not_met decides the row
// against the owing side. Any other latest value, no statement, no record of
// the owing side read, a fork in its book (its latest statement is then not
// one statement) or a through record not in the book read needs a person.
func DecideCase(g GatheredCase, through Null[string]) Decision {
	var owedBy string
	switch g.Dispute.KeyID {
	case g.Keys.Buyer:
		owedBy = RoleSeller
	case g.Keys.Seller:
		owedBy = RoleBuyer
	}
	d := Decision{Row: g.Row, Against: owedBy}
	person := func(format string, args ...any) Decision {
		d.NeedsPerson = fmt.Sprintf(format, args...)
		return d
	}
	if owedBy == "" {
		return person("the dispute is signed by neither side's key, so it names no side it is raised against")
	}
	side := g.Side(owedBy)
	if side.ForkAt > 0 {
		return person("the %s's book has two versions after record %d", owedBy, side.ForkAt-1)
	}
	book := side.Book
	if through.Present && through.IsNull() {
		return person("the ruling read no record the %s signed", owedBy)
	}
	if through.Present {
		n := -1
		for i, r := range book {
			if r.Read.RecordID == *through.V {
				n = i
				break
			}
		}
		if n < 0 {
			return person("record %s of the %s, the last one the ruling read, is not in the book read", *through.V, owedBy)
		}
		book = book[:n+1]
	}
	if len(book) == 0 {
		return person("no record the %s signed was read, so its own word on row %s is not known", owedBy, g.Row)
	}
	var last *CaseRecord
	var value string
	for i := range book {
		for _, st := range statementsOf(book[i].Read.Record) {
			if st.Row == g.Row {
				last, value = &book[i], st.Value
			}
		}
	}
	switch {
	case last == nil:
		return person("the %s stated nothing on row %s in a record it signed", owedBy, g.Row)
	case value != ValueNotMet:
		return person("the %s's latest statement on row %s, in a record it signed, says %s", owedBy, g.Row, value)
	}
	d.Decisive = &DecidingRecord{Against: owedBy, Value: ValueNotMet, Record: last.Read.Digest, RecordID: last.Read.RecordID,
		Seq: last.Read.Seq, At: last.At, TimedBy: last.TimedBy}
	return d
}

// statementsOf is the statements a record's body carries: a statement
// record's, or those a perform or settle record carries. Any other body
// carries none.
func statementsOf(r Record) []StatementItem {
	switch b := r.Body.(type) {
	case *StatementBody:
		return b.Statements
	case *PerformBody:
		return b.Statements
	case *SettleBody:
		return b.Statements
	}
	return nil
}
