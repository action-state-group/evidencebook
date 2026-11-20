// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/action-state-group/evidencebook/rowstatus"
)

// The status value (design §8.1): what a v1 deal book's fact commits to in
// its status opening. It holds inputs, never a computed status, so a quiet
// book gets no new fact when a date passes, and it holds no shared
// identifier: no deal id, no record id or digest, no log id.

// StatusTime is a time in a status value, one of three (design §8.1,
// "Times"): Known, a record's known time (RFC 3339 UTC); None, no such
// record (a named record not written yet); or Stamp, a record whose time is
// not known when the fact is written, n being the holder's first stamp that
// covers it. It encodes as a string, null, or {"stamp": n}.
type StatusTime struct {
	Known string
	Stamp uint64
}

// NoTime is StatusTime's None: no such record.
var NoTime = StatusTime{}

// KnownTime is a record's known time.
func KnownTime(t string) StatusTime { return StatusTime{Known: t} }

// StampTime is a record first covered by stamp n, its time not known yet.
func StampTime(n uint64) StatusTime { return StatusTime{Stamp: n} }

// IsNone reports whether t is None.
func (t StatusTime) IsNone() bool { return t.Known == "" && t.Stamp == 0 }

// MarshalJSON encodes t as a string, null or {"stamp": n}.
func (t StatusTime) MarshalJSON() ([]byte, error) {
	switch {
	case t.Known != "":
		return json.Marshal(t.Known)
	case t.Stamp != 0:
		return json.Marshal(map[string]uint64{"stamp": t.Stamp})
	}
	return []byte("null"), nil
}

// UnmarshalJSON reads a string, null or {"stamp": n} (n >= 1).
func (t *StatusTime) UnmarshalJSON(b []byte) error {
	*t = StatusTime{}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &t.Known)
	}
	var s struct {
		Stamp *uint64 `json:"stamp"`
	}
	if err := strictUnmarshal(b, &s); err != nil || s.Stamp == nil || *s.Stamp == 0 {
		return fmt.Errorf("xdealv1: a status time is a string, null or {\"stamp\": n} with n >= 1")
	}
	t.Stamp = *s.Stamp
	return nil
}

// known is t as rowstatus reads a time: its string, or nil when not known.
func (t StatusTime) known() rowstatus.Time {
	if t.Known == "" {
		return nil
	}
	k := t.Known
	return &k
}

// StatusRecord is a record of a deal book or case book as a status value is
// built from it: the record, its record id, and its time as the fact states
// it.
type StatusRecord struct {
	Record   Record     `json:"record"`
	RecordID string     `json:"record_id"`
	Time     StatusTime `json:"time"`
}

// StatusBooks is what a status value is built from (design §8.1): the offer
// the deal agreed; the holder's deal book, the counted prefix of the
// counterparty's and the adjudicator's case book, each in book order; the
// rows whose case book forked with different first final rulings; each
// payment row's PaymentCheck; and whether the holder holds an accepted stamp
// proof covering the counterparty's agreement.
type StatusBooks struct {
	Offer                   Record                   `json:"offer"`
	Own                     []StatusRecord           `json:"own"`
	Counterpart             []StatusRecord           `json:"counterpart"`
	CaseBook                []StatusRecord           `json:"case_book"`
	ConflictingFinalRulings []string                 `json:"conflicting_final_rulings,omitempty"`
	Payments                map[string]PaymentResult `json:"payments,omitempty"`
	ProofHeld               bool                     `json:"proof_held"`
	// RulingsUnnamed is how many counted rulings of the deal's adjudicator
	// the holder holds that neither book names yet (#527): the receive call
	// names each, so a nonzero count is a naming that failed or waits.
	RulingsUnnamed int `json:"rulings_unnamed,omitempty"`
}

// StatusValue is the v1 deal book's status value (design §8.1).
type StatusValue struct {
	V                     int             `json:"v"`
	Role                  string          `json:"role"`
	Side                  string          `json:"side"`
	AgreedAt              string          `json:"agreed_at"`
	CounterpartyLayer     bool            `json:"counterparty_layer"`
	CounterpartyKind      *string         `json:"counterparty_kind"`
	ValueBand             *ValueBand      `json:"value_band"`
	Witnesses             []Witness       `json:"witnesses"`
	Adjudicator           *StatusAdjud    `json:"adjudicator"`
	CounterpartyProofHeld bool            `json:"counterparty_proof_held"`
	Start                 StatusStart     `json:"start"`
	Rows                  []StatusRow     `json:"rows"`
	Disputes              []StatusDispute `json:"disputes"`
	// RulingsUnnamed is StatusBooks.RulingsUnnamed, a count with no ids:
	// a dispute whose outcome reads open may have a ruling held unnamed,
	// so a reader shows it beside the outcome (design §8.1).
	RulingsUnnamed int `json:"rulings_unnamed,omitempty"`
}

// ValueBand is the agreed price's currency and power of ten (AAC
// standard-history §5.3: digits(value) - assetScale - 1), or null for a zero
// or unknown price.
type ValueBand struct {
	Currency string `json:"currency"`
	Decade   int    `json:"decade"`
}

// StatusAdjud is the agreement's adjudicator as a status value names it: its
// key and rules digest, public configuration both books share.
type StatusAdjud struct {
	KeyID       string `json:"key_id"`
	RulesDigest string `json:"rules_digest"`
}

// StatusStart is Start's input without at, its offerer's agreement time as
// a StatusTime.
type StatusStart struct {
	Side                     string     `json:"side"`
	CounterpartyLayer        bool       `json:"counterparty_layer"`
	AgreeBy                  string     `json:"agree_by"`
	CounterpartAgreementHeld bool       `json:"counterpart_agreement_held"`
	SameOfferAndRows         *bool      `json:"same_offer_and_rows"`
	OffererNamesAcceptor     *bool      `json:"offerer_names_acceptor"`
	OffererAgreementTime     StatusTime `json:"offerer_agreement_time"`
	AcceptorNamesOfferer     bool       `json:"acceptor_names_offerer"`
	CancellationNamedFirst   bool       `json:"cancellation_named_first"`
	// FirstAct is the holder's first act with a counterparty that runs no
	// layer; null before it, and when the counterparty runs the layer (#575).
	FirstAct *StatusFirstAct `json:"first_act"`
}

// StatusFirstAct is the holder's first perform or settle record in a status
// value: its time, a StatusTime. None says the record has no time yet, which
// Start reads as a local start.
type StatusFirstAct struct {
	Time StatusTime `json:"time"`
}

// StatusRow is one agreed row: its rowstatus input without at, each time a
// StatusTime and only the statements invariant A keeps; when the other side
// first named each statement kept; the effective ruling's against; and when
// the owing side first made a remedy, ruled or not. CheckReason and
// CheckExcluded say why a payment check did not run or failed, and how many
// settle records it left out, without naming any (no shared identifier).
type StatusRow struct {
	ID                    string         `json:"id"`
	Input                 StatusRowInput `json:"input"`
	StatementNamedByOther []StatusTime   `json:"statement_named_by_other"`
	RulingAgainst         *string        `json:"ruling_against"`
	RemedyMade            StatusTime     `json:"remedy_made"`
	CheckReason           string         `json:"check_reason,omitempty"`
	CheckExcluded         int            `json:"check_excluded,omitempty"`
}

// StatusRowInput is rowstatus.Input without at, its times StatusTimes.
type StatusRowInput struct {
	Parties                 []string               `json:"parties"`
	Row                     rowstatus.Row          `json:"row"`
	Named                   map[string]StatusNamed `json:"named,omitempty"`
	Statements              []StatusStatement      `json:"statements,omitempty"`
	Check                   string                 `json:"check,omitempty"`
	Disputes                []StatusRowDispute     `json:"disputes,omitempty"`
	Withdrawals             []StatusSideTime       `json:"withdrawals,omitempty"`
	Rulings                 []StatusRuling         `json:"rulings,omitempty"`
	ConflictingFinalRulings bool                   `json:"conflicting_final_rulings,omitempty"`
	Remedies                []StatusTimeOnly       `json:"remedies,omitempty"`
}

// StatusNamed is a named record's time and latest date.
type StatusNamed struct {
	Time   StatusTime `json:"time"`
	Latest string     `json:"latest"`
}

// StatusStatement is a statement and its record's time.
type StatusStatement struct {
	Side  string     `json:"side"`
	Value string     `json:"value"`
	Time  StatusTime `json:"time"`
}

// StatusRowDispute is a dispute, its time and seen_in_time.
type StatusRowDispute struct {
	Side       string     `json:"side"`
	Time       StatusTime `json:"time"`
	SeenInTime bool       `json:"seen_in_time"`
}

// StatusSideTime is a withdrawal's side and time.
type StatusSideTime struct {
	Side string     `json:"side"`
	Time StatusTime `json:"time"`
}

// StatusRuling is a ruling, its time and remedy due date.
type StatusRuling struct {
	Final     bool       `json:"final"`
	Value     string     `json:"value"`
	Time      StatusTime `json:"time"`
	RemedyDue *string    `json:"remedy_due,omitempty"`
}

// StatusTimeOnly is a remedy's time.
type StatusTimeOnly struct {
	Time StatusTime `json:"time"`
}

// StatusDispute is one dispute record of either side (design §8.1, "Card
// facts"): its row, its claimant's role, its outcome and its forum. A
// contest is that one record; nothing else counts it again.
type StatusDispute struct {
	Row      string `json:"row"`
	Claimant string `json:"claimant"`
	Outcome  string `json:"outcome"`
	Forum    string `json:"forum"`
}

// Dispute outcomes in a status value (design §8.1).
const (
	OutcomeOpen            = "open"
	OutcomeForClaimant     = "for_claimant"
	OutcomeAgainstClaimant = "against_claimant"
	OutcomeNoFinding       = "no_finding"
	OutcomeWithdrawn       = "withdrawn"
)

// BuildStatus builds the status value from the books (design §8.1). It
// refuses books that RowInputs refuses or that Start's derivation refuses.
func BuildStatus(b StatusBooks) (StatusValue, error) {
	of, ok := asPropose(b.Offer.Body)
	if !ok {
		return StatusValue{}, fmt.Errorf("xdealv1: status: the offer is a %s record", b.Offer.Block.RecordType)
	}
	if len(b.Own) == 0 {
		return StatusValue{}, fmt.Errorf("xdealv1: status: the holder's book is empty")
	}
	own, ok := asAgree(b.Own[0].Record.Body)
	if !ok || own.Kind != KindAgreement {
		return StatusValue{}, fmt.Errorf("xdealv1: status: the holder's book does not open with its agreement")
	}
	timed := func(rs []StatusRecord) []TimedRecord {
		out := make([]TimedRecord, len(rs))
		for i, r := range rs {
			out[i] = TimedRecord{Record: r.Record, RecordID: r.RecordID, Time: r.Time.known()}
		}
		return out
	}
	timeOf := func(ref recRef) StatusTime {
		switch ref.book {
		case bookOwn:
			return b.Own[ref.i].Time
		case bookCounterpart:
			return b.Counterpart[ref.i].Time
		}
		return b.CaseBook[ref.i].Time
	}
	rb := RowBooks{Offer: b.Offer, Own: timed(b.Own), Counterpart: timed(b.Counterpart), CaseBook: timed(b.CaseBook),
		ConflictingFinalRulings: b.ConflictingFinalRulings, Checks: paymentChecks(b.Payments)}
	agreedAt := b.Own[0].Record.Block.At
	// The fact is written after every time it states as known, so the rows
	// are read as of the latest of them: a window end hanging on a named
	// record is known once that record's time is (design §8.1, A).
	asOf := agreedAt
	for _, rs := range [][]StatusRecord{b.Own, b.Counterpart, b.CaseBook} {
		for _, r := range rs {
			if r.Time.Known > asOf {
				asOf = r.Time.Known
			}
		}
	}
	ins, srcs, err := rowInputs(rb, asOf)
	if err != nil {
		return StatusValue{}, err
	}
	layer := own.CounterpartyLayer != nil && *own.CounterpartyLayer
	v := StatusValue{V: 1, Role: own.Role, Side: own.Side, AgreedAt: agreedAt, CounterpartyLayer: layer,
		ValueBand: valueBand(of.Terms), Witnesses: own.Witnesses, CounterpartyProofHeld: b.ProofHeld,
		Rows: []StatusRow{}, Disputes: []StatusDispute{}, RulingsUnnamed: b.RulingsUnnamed}
	if v.Witnesses == nil {
		v.Witnesses = []Witness{}
	}
	if a := own.Adjudicator.V; a != nil {
		v.Adjudicator = &StatusAdjud{KeyID: a.KeyID, RulesDigest: a.RulesDigest}
	}
	if v.Start, err = statusStart(b); err != nil {
		return StatusValue{}, err
	}
	books := map[byte][]StatusRecord{bookOwn: b.Own, bookCounterpart: b.Counterpart}
	ownRole, otherRole := own.Role, RoleBuyer
	if ownRole == RoleBuyer {
		otherRole = RoleSeller
	}
	bookOfRole := map[string]byte{ownRole: bookOwn, otherRole: bookCounterpart}

	for k, in := range ins {
		src := srcs[k]
		res, err := rowstatus.Compute(in)
		if err != nil {
			return StatusValue{}, err
		}
		kept := carriedStatements(in.Statements, res.WindowEnd)
		row := StatusRow{ID: in.Row.ID, StatementNamedByOther: []StatusTime{}, RemedyMade: NoTime,
			Input: StatusRowInput{Parties: in.Parties, Row: in.Row, Check: in.Check, ConflictingFinalRulings: in.ConflictingFinalRulings}}
		if len(in.Named) > 0 {
			row.Input.Named = map[string]StatusNamed{}
			for name, n := range in.Named {
				t := NoTime
				if ref, ok := src.named[name]; ok {
					t = timeOf(ref)
				}
				row.Input.Named[name] = StatusNamed{Time: t, Latest: n.Latest}
			}
		}
		for _, i := range kept {
			s := in.Statements[i]
			ref := src.statements[i]
			row.Input.Statements = append(row.Input.Statements, StatusStatement{Side: s.Side, Value: s.Value, Time: timeOf(ref)})
			row.StatementNamedByOther = append(row.StatementNamedByOther, namedByOther(books, ref))
		}
		for i, d := range in.Disputes {
			row.Input.Disputes = append(row.Input.Disputes, StatusRowDispute{Side: d.Side, Time: timeOf(src.disputes[i]), SeenInTime: d.SeenInTime})
		}
		for i, w := range in.Withdrawals {
			row.Input.Withdrawals = append(row.Input.Withdrawals, StatusSideTime{Side: w.Side, Time: timeOf(src.withdrawals[i])})
		}
		for i, r := range in.Rulings {
			row.Input.Rulings = append(row.Input.Rulings, StatusRuling{Final: r.Final, Value: r.Value, Time: timeOf(src.rulings[i]), RemedyDue: r.RemedyDue})
		}
		for i := range in.Remedies {
			row.Input.Remedies = append(row.Input.Remedies, StatusTimeOnly{Time: timeOf(src.remedies[i])})
		}
		effective := effectiveRuling(b, in.Row.ID, in.ConflictingFinalRulings)
		if effective != nil && effective.Against.V != nil {
			against := *effective.Against.V
			row.RulingAgainst = &against
		}
		row.RemedyMade = remedyMade(books[bookOfRole[in.Row.OwedBy]], in.Row.ID)
		if p, ok := b.Payments[in.Row.ID]; ok {
			row.CheckReason, row.CheckExcluded = p.Reason, len(p.Excluded)
		}
		v.Rows = append(v.Rows, row)
		v.Disputes = append(v.Disputes, disputesOf(books, bookOfRole, in.Row.ID, effective)...)
	}
	return v, nil
}

// valueBand is the agreed price's band: its currency and decade, nil for a
// zero or missing price or a currency with no known scale.
func valueBand(t Terms) *ValueBand {
	if t.PriceMinor == nil || *t.PriceMinor <= 0 || t.Currency == "" {
		return nil
	}
	row := Row{Owed: rawJSON(map[string]any{"amount_minor": *t.PriceMinor, "currency": t.Currency})}
	amount, ok := owedAmount(row)
	if !ok {
		return nil
	}
	value, _ := amount["value"].(string)
	scale, _ := amount["assetScale"].(json.Number).Int64()
	return &ValueBand{Currency: t.Currency, Decade: len(value) - int(scale) - 1}
}

func rawJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

// paymentChecks are the payment rows' checks that ran, by row id: a check
// not run is left out, so RowInputs reads that row as not run.
func paymentChecks(payments map[string]PaymentResult) map[string]string {
	checks := map[string]string{}
	for row, p := range payments {
		if p.Check != "" {
			checks[row] = p.Check
		}
	}
	return checks
}

// statusStart is Start's input from the books, without at, its offerer's
// agreement time as the books state it.
func statusStart(b StatusBooks) (StatusStart, error) {
	conv := func(rs []StatusRecord) []BookRecord {
		out := make([]BookRecord, len(rs))
		for i, r := range rs {
			out[i] = BookRecord{Record: r.Record, RecordID: r.RecordID}
		}
		return out
	}
	in, err := startInput(StartBooks{At: b.Own[0].Record.Block.At, Offer: b.Offer, Own: conv(b.Own), Counterpart: conv(b.Counterpart)})
	if err != nil {
		return StatusStart{}, err
	}
	out := StatusStart{Side: in.Side, CounterpartyLayer: in.CounterpartyLayer, AgreeBy: in.AgreeBy,
		CounterpartAgreementHeld: in.CounterpartAgreementHeld, SameOfferAndRows: in.SameOfferAndRows,
		OffererNamesAcceptor: in.OffererNamesAcceptor, AcceptorNamesOfferer: in.AcceptorNamesOfferer,
		CancellationNamedFirst: in.CancellationNamedFirst, OffererAgreementTime: NoTime}
	// The offerer's agreement opens the offerer's book: the holder's own when
	// it offered, else the counterparty's held first record.
	if in.Side == SideOfferer {
		out.OffererAgreementTime = b.Own[0].Time
	} else if len(b.Counterpart) > 0 {
		out.OffererAgreementTime = b.Counterpart[0].Time
	}
	if !in.CounterpartyLayer {
		for _, r := range b.Own {
			if _, _, isAct := actCheck(r.Record); isAct {
				out.FirstAct = &StatusFirstAct{Time: r.Time}
				break
			}
		}
	}
	return out, nil
}

// namedByOther is when the other side's book first named the record at ref:
// the time of its first record whose seen is that record or a later one of
// ref's book; None when no record names it.
func namedByOther(books map[byte][]StatusRecord, ref recRef) StatusTime {
	mine, other := books[ref.book], books[bookCounterpart]
	if ref.book == bookCounterpart {
		other = books[bookOwn]
	}
	at := map[string]int{}
	for i, r := range mine {
		at[r.RecordID] = i
	}
	for _, r := range other {
		if s := r.Record.Block.Seen; s.V != nil {
			if i, ok := at[*s.V]; ok && i >= ref.i {
				return r.Time
			}
		}
	}
	return NoTime
}

// effectiveRuling is the adjudicator's first final ruling on the row in the
// case book, none when its case book forked with different ones.
func effectiveRuling(b StatusBooks, row string, conflicting bool) *RulingBody {
	if conflicting {
		return nil
	}
	for _, r := range b.CaseBook {
		if rb, ok := bodyOf[RulingBody](r.Record.Body); ok && rb.Row == row && rb.Final && r.Record.Block.DealID == b.Offer.Block.DealID {
			return rb
		}
	}
	return nil
}

// remedyMade is the time of the owing side's first settle or perform whose
// remedy_for is the row, ruled or not (design §8.1, Q5); None for none.
func remedyMade(book []StatusRecord, row string) StatusTime {
	if i := firstRemedy(len(book), func(i int) Record { return book[i].Record }, row); i >= 0 {
		return book[i].Time
	}
	return NoTime
}

// firstRemedy is the index of the first of n records (record(i)) that is a
// settle or perform whose remedy_for is the row, -1 for none: the one rule
// the status value and the report both read a remedy made by.
func firstRemedy(n int, record func(i int) Record, row string) int {
	for i := 0; i < n; i++ {
		r := record(i)
		if p, ok := bodyOf[PerformBody](r.Body); ok && p.RemedyFor != nil && p.RemedyFor.Row == row {
			return i
		}
		if s, ok := bodyOf[SettleBody](r.Body); ok && s.RemedyFor != nil && s.RemedyFor.Row == row {
			return i
		}
	}
	return -1
}

// disputesOf is each dispute record of kind dispute on the row, of either
// side, with its outcome (design §8.1): withdrawn after its claimant's
// withdrawal on the row; for or against the claimant when the effective
// ruling is against the other side or the claimant; no finding when it is
// against neither; open otherwise.
func disputesOf(books map[byte][]StatusRecord, bookOfRole map[string]byte, row string, effective *RulingBody) []StatusDispute {
	var out []StatusDispute
	for _, side := range []string{RoleBuyer, RoleSeller} {
		book := books[bookOfRole[side]]
		for i, r := range book {
			d, ok := bodyOf[DisputeBody](r.Record.Body)
			if !ok || d.Kind != KindDispute || d.Row != row {
				continue
			}
			outcome := OutcomeOpen
			withdrawn := slices.ContainsFunc(book[i+1:], func(w StatusRecord) bool {
				wd, ok := bodyOf[DisputeBody](w.Record.Body)
				return ok && wd.Kind == KindWithdrawal && wd.Row == row
			})
			switch {
			case withdrawn:
				outcome = OutcomeWithdrawn
			case effective != nil && effective.Against.V == nil:
				outcome = OutcomeNoFinding
			case effective != nil && *effective.Against.V == side:
				outcome = OutcomeAgainstClaimant
			case effective != nil:
				outcome = OutcomeForClaimant
			}
			out = append(out, StatusDispute{Row: row, Claimant: side, Outcome: outcome, Forum: d.Forum})
		}
	}
	return out
}

// ResolveRow is a status row's input as a reader computes it at at, once
// each {"stamp": n} it can resolve has a time (stamps; an unresolved one
// stays untimed): every time resolved, and each dispute's seen_in_time
// recomputed from those times, since it rests on them (design §8.1, A). A
// dispute is seen in time when the claimant's book named the other side's
// standing statement (its latest statement rowstatus does not hold late) at
// a time not after the row's window end: that statement's
// statement_named_by_other, the first such naming, its book's times never
// decreasing. While the window end is not known the fact's value stands.
func ResolveRow(row StatusRow, at string, stamps map[uint64]string) (rowstatus.Input, error) {
	r := func(st StatusTime) rowstatus.Time { return resolveTime(st, stamps) }
	in := row.Input
	out := rowstatus.Input{At: at, Parties: in.Parties, Row: in.Row, Check: in.Check, ConflictingFinalRulings: in.ConflictingFinalRulings}
	if in.Named != nil {
		out.Named = map[string]rowstatus.Named{}
		for k, n := range in.Named {
			out.Named[k] = rowstatus.Named{Time: r(n.Time), Latest: n.Latest}
		}
	}
	for _, s := range in.Statements {
		out.Statements = append(out.Statements, rowstatus.Statement{Side: s.Side, Value: s.Value, Time: r(s.Time)})
	}
	for _, w := range in.Withdrawals {
		out.Withdrawals = append(out.Withdrawals, rowstatus.Withdrawal{Side: w.Side, Time: r(w.Time)})
	}
	for _, ru := range in.Rulings {
		out.Rulings = append(out.Rulings, rowstatus.Ruling{Final: ru.Final, Value: ru.Value, Time: r(ru.Time), RemedyDue: ru.RemedyDue})
	}
	for _, m := range in.Remedies {
		out.Remedies = append(out.Remedies, rowstatus.Remedy{Time: r(m.Time)})
	}
	if len(in.Disputes) == 0 {
		return out, nil
	}
	plain := out
	plain.Withdrawals, plain.Rulings, plain.Remedies = nil, nil, nil
	res, err := rowstatus.Compute(plain)
	if err != nil {
		return rowstatus.Input{}, err
	}
	end := res.WindowEnd
	for _, d := range in.Disputes {
		seen := d.SeenInTime
		if end != "" {
			seen = false
			standing := -1
			var floor *string
			for i, s := range out.Statements {
				if s.Side == d.Side {
					continue
				}
				late := (s.Time != nil && *s.Time > end) || (s.Time == nil && floor != nil && *floor > end)
				if !late {
					standing = i
				}
				if s.Time != nil && (floor == nil || *s.Time > *floor) {
					floor = s.Time
				}
			}
			if standing >= 0 && standing < len(row.StatementNamedByOther) {
				if t := r(row.StatementNamedByOther[standing]); t != nil && *t <= end {
					seen = true
				}
			}
		}
		out.Disputes = append(out.Disputes, rowstatus.Dispute{Side: d.Side, Time: r(d.Time), SeenInTime: seen})
	}
	return out, nil
}
