// SPDX-License-Identifier: Apache-2.0

// Package rowstatus computes the status of one term row of a deal from the
// two sides' statements on it and the dates (evidencebook#461, acceptance 6).
// No record holds a status and nobody declares it: whoever holds the inputs
// computes it, as of any time, and gets the same answer. The Rust crate
// (row_status) and the Python package (evidencebook.row_status) compute the
// same results from the same inputs; testdata/row-status holds the shared
// vectors.
//
// The rule, applied in order at a time T, after the row's dates are known:
//
//  0. A date that hangs on a named record whose time is not known by the
//     record's latest date: not_reached once T is past that latest date.
//  1. The first final ruling of the adjudicator the agreement names: ruled,
//     with its remedy none, due, made (by the remedy's date) or not_honoured
//     (that date passed without one; a remedy made after it is not counted).
//     A date whose named record has no time yet, when no final ruling
//     exists: not_due.
//  2. A dispute that is not late and not withdrawn, or two conflicting final
//     rulings: in_dispute.
//  3. Both sides' in-time statements differ, or the row's recomputed check
//     failed: in_disagreement.
//  4. Both the same: agreed_so_far inside the contest window, agreed after.
//  5. One side only: waiting inside the window, uncontested after.
//  6. Neither: not_due up to and including the due date, no_statement after.
//
// Only in-time statements count: for each side, its latest statement whose
// time is not after the end of the contest window. A statement of
// cannot_tell counts as none. Once the window has ended, a later statement
// that gives its author the other side's in-time value is a concession, and
// only the first concession on a row counts; two at the same time count as
// none. A record with no time still counts, and the result is then local; an
// untimed statement is never a concession, since it cannot be shown to be
// after the window (it is late only when an earlier timed statement of the
// same side already is).
package rowstatus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrInvalid reports inputs the rule cannot be applied to.
var ErrInvalid = errors.New("rowstatus: invalid input")

// Statuses, as the vectors spell them.
const (
	NotReached     = "not_reached"
	NotDue         = "not_due"
	NoStatement    = "no_statement"
	Waiting        = "waiting"
	Uncontested    = "uncontested"
	AgreedSoFar    = "agreed_so_far"
	Agreed         = "agreed"
	InDisagreement = "in_disagreement"
	InDispute      = "in_dispute"
	Ruled          = "ruled"
)

// Statement values.
const (
	Met        = "met"
	NotMet     = "not_met"
	CannotTell = "cannot_tell"
)

// Remedy states of a ruled row.
const (
	RemedyNone         = "none"
	RemedyDue          = "due"
	RemedyMade         = "made"
	RemedyNotHonoured  = "not_honoured"
	checkFail          = "fail"
	checkPass          = "pass"
	maxSecondsInOffset = 100 * 366 * 24 * 3600
)

// Time is an RFC 3339 time in UTC to the second, or null for a record that
// has no time yet.
type Time = *string

// Date is a row date: a fixed time, or a number of seconds after the time of
// a named record.
type Date struct {
	At      *string `json:"at,omitempty"`
	After   string  `json:"after,omitempty"`
	Seconds int64   `json:"seconds,omitempty"`
}

// Named is a record a date hangs on: its time once it has one, and the
// latest time by which it must have one.
type Named struct {
	Time   Time   `json:"time"`
	Latest string `json:"latest"`
}

// Row is one term row: who owes it, its due date and the end of its contest
// window, and whether its terms mark it as recomputed.
type Row struct {
	ID         string `json:"id"`
	OwedBy     string `json:"owed_by"`
	Due        Date   `json:"due"`
	WindowEnd  Date   `json:"window_end"`
	Recomputed bool   `json:"recomputed,omitempty"`
}

// Statement is what one side said about the row, in its book's order.
type Statement struct {
	Side  string `json:"side"`
	Value string `json:"value"`
	Time  Time   `json:"time"`
}

// Dispute is a dispute on the row by its claimant. SeenInTime says whether
// the claimant's own book named the statement it did not contest, in a seen
// field, before the contest window ended.
type Dispute struct {
	Side       string `json:"side"`
	Time       Time   `json:"time"`
	SeenInTime bool   `json:"seen_in_time"`
}

// Withdrawal is a claimant's withdrawal of its disputes on the row.
type Withdrawal struct {
	Side string `json:"side"`
	Time Time   `json:"time"`
}

// Ruling is a ruling on the row by the adjudicator the agreement names, in
// the order of that adjudicator's case book. RemedyDue is the date of the
// remedy it orders, if any.
type Ruling struct {
	Final     bool    `json:"final"`
	Value     string  `json:"value"`
	Time      Time    `json:"time"`
	RemedyDue *string `json:"remedy_due,omitempty"`
}

// Remedy is a record that carries out the ruling's remedy.
type Remedy struct {
	Time Time `json:"time"`
}

// Input is everything the status of one row is computed from.
type Input struct {
	// At is the time T the status is computed as of.
	At string `json:"at"`
	// Parties are the deal's two sides, as Statement.Side names them.
	Parties []string `json:"parties"`
	Row     Row      `json:"row"`
	// Named holds the records the row's dates hang on, by name.
	Named map[string]Named `json:"named,omitempty"`
	// NoConcession turns concessions off (a metered service, whose exchange
	// records never change).
	NoConcession bool        `json:"no_concession,omitempty"`
	Statements   []Statement `json:"statements,omitempty"`
	// Check is the result of the row's recomputed check: "pass", "fail",
	// or empty when the row is not recomputed or not checked.
	Check       string       `json:"check,omitempty"`
	Disputes    []Dispute    `json:"disputes,omitempty"`
	Withdrawals []Withdrawal `json:"withdrawals,omitempty"`
	Rulings     []Ruling     `json:"rulings,omitempty"`
	// ConflictingFinalRulings says the reader holds two forks of the
	// adjudicator's case book with different first final rulings.
	ConflictingFinalRulings bool     `json:"conflicting_final_rulings,omitempty"`
	Remedies                []Remedy `json:"remedies,omitempty"`
}

// Result is a row's status and the value stated, read together: agreed can
// be agreement that the row was not met.
type Result struct {
	Status string `json:"status"`
	// Value is met or not_met where the status carries one (agreed,
	// agreed_so_far, waiting, uncontested, ruled), else empty.
	Value string `json:"value,omitempty"`
	// Remedy is the remedy state of a ruled row.
	Remedy string `json:"remedy,omitempty"`
	// Local says the result rests on a record with no time.
	Local bool `json:"local"`
	// Due and WindowEnd are the row's dates as of T, empty while unset.
	Due       string `json:"due,omitempty"`
	WindowEnd string `json:"window_end,omitempty"`
}

// ParseInput reads one input as the vectors write it, refusing unknown,
// differently cased and repeated members, as the Rust and Python readers do
// (Go's decoder alone matches member names without case and keeps the last of
// a repeated one).
func ParseInput(b []byte) (Input, error) {
	var in Input
	if err := exactMembers(b, reflect.TypeOf(in)); err != nil {
		return Input{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return Input{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return in, nil
}

type stmt struct {
	side  int
	value string
	t     *time.Time
	// floor is the latest time of an earlier timed record of the same side:
	// a record with no time of its own is no earlier than that.
	floor *time.Time
}

// Compute applies the rule to one row as of in.At.
func Compute(in Input) (Result, error) {
	c, err := newCalc(in)
	if err != nil {
		return Result{}, err
	}
	return c.compute(), nil
}

type calc struct {
	in        Input
	at        time.Time
	due, end  *time.Time
	unset     bool
	reached   bool
	local     bool
	stmts     []stmt
	disputes  []Dispute
	dTimes    []*time.Time
	rulings   []Ruling
	rTimes    []*time.Time
	remedies  []*time.Time
	withdrawn [2]bool
	// wTimed says a side has a withdrawal with a time; one with none makes
	// a result that rests on it local.
	wTimed [2]bool
}

func newCalc(in Input) (*calc, error) {
	at, err := parseTime(in.At)
	if err != nil {
		return nil, err
	}
	if len(in.Parties) != 2 || in.Parties[0] == "" || in.Parties[1] == "" || in.Parties[0] == in.Parties[1] {
		return nil, fmt.Errorf("%w: two distinct parties are required", ErrInvalid)
	}
	c := &calc{in: in, at: at, reached: true}
	if _, err := c.side(in.Row.OwedBy); err != nil {
		return nil, err
	}
	if in.Check != "" && in.Check != checkPass && in.Check != checkFail {
		return nil, fmt.Errorf("%w: check is pass, fail or empty", ErrInvalid)
	}
	if in.Check != "" && !in.Row.Recomputed {
		return nil, fmt.Errorf("%w: a check result on a row its terms do not mark recomputed", ErrInvalid)
	}
	if c.due, err = c.date(in.Row.Due); err != nil {
		return nil, err
	}
	if c.end, err = c.date(in.Row.WindowEnd); err != nil {
		return nil, err
	}
	if c.due != nil && c.end != nil && c.end.Before(*c.due) {
		return nil, fmt.Errorf("%w: the contest window ends before the due date", ErrInvalid)
	}
	var floors [2]*time.Time
	for _, s := range in.Statements {
		i, err := c.side(s.Side)
		if err != nil {
			return nil, err
		}
		switch s.Value {
		case Met, NotMet, CannotTell:
		default:
			return nil, fmt.Errorf("%w: statement value %q", ErrInvalid, s.Value)
		}
		// The floor counts every earlier timed statement of the side, held
		// as of T or not: an untimed one after a statement dated after T is
		// itself after T, so it is not held yet.
		t, err := optTime(s.Time)
		if err != nil {
			return nil, err
		}
		floor := floors[i]
		if t != nil && (floors[i] == nil || t.After(*floors[i])) {
			floors[i] = t
		}
		if (t != nil && t.After(at)) || (t == nil && floor != nil && floor.After(at)) {
			continue
		}
		c.stmts = append(c.stmts, stmt{side: i, value: s.Value, t: t, floor: floor})
	}
	for _, w := range in.Withdrawals {
		i, err := c.side(w.Side)
		if err != nil {
			return nil, err
		}
		if t, ok, err := c.held(w.Time); err != nil {
			return nil, err
		} else if ok {
			c.withdrawn[i] = true
			c.wTimed[i] = c.wTimed[i] || t != nil
		}
	}
	for _, d := range in.Disputes {
		if _, err := c.side(d.Side); err != nil {
			return nil, err
		}
		t, ok, err := c.held(d.Time)
		if err != nil {
			return nil, err
		}
		if ok {
			c.disputes = append(c.disputes, d)
			c.dTimes = append(c.dTimes, t)
		}
	}
	for _, r := range in.Rulings {
		if r.Value != Met && r.Value != NotMet {
			return nil, fmt.Errorf("%w: a ruling's value is met or not_met", ErrInvalid)
		}
		if r.RemedyDue != nil {
			if _, err := parseTime(*r.RemedyDue); err != nil {
				return nil, err
			}
		}
		t, ok, err := c.held(r.Time)
		if err != nil {
			return nil, err
		}
		if ok {
			c.rulings = append(c.rulings, r)
			c.rTimes = append(c.rTimes, t)
		}
	}
	for _, r := range in.Remedies {
		t, ok, err := c.held(r.Time)
		if err != nil {
			return nil, err
		}
		if ok {
			c.remedies = append(c.remedies, t)
		}
	}
	return c, nil
}

func (c *calc) side(s string) (int, error) {
	switch s {
	case c.in.Parties[0]:
		return 0, nil
	case c.in.Parties[1]:
		return 1, nil
	}
	return 0, fmt.Errorf("%w: %q is not a party", ErrInvalid, s)
}

// held parses a record's time and says whether the record exists as of T: a
// record whose time is after T does not yet. A record with no time exists,
// and makes the result local if it is used.
func (c *calc) held(s Time) (*time.Time, bool, error) {
	t, err := optTime(s)
	if err != nil || t == nil {
		return nil, err == nil, err
	}
	if t.After(c.at) {
		return nil, false, nil
	}
	return t, true, nil
}

func optTime(s Time) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := parseTime(*s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// date resolves a row date as of T. A date on a named record that has no
// time by T is unset; one whose record has no time by its latest date, once
// T is past that date, makes the row not reached.
func (c *calc) date(d Date) (*time.Time, error) {
	switch {
	case d.At != nil && d.After == "" && d.Seconds == 0:
		t, err := parseTime(*d.At)
		return &t, err
	case d.At == nil && d.After != "":
	default:
		return nil, fmt.Errorf("%w: a date is either at or after", ErrInvalid)
	}
	if d.Seconds < 0 || d.Seconds > maxSecondsInOffset {
		return nil, fmt.Errorf("%w: a date's offset is out of range", ErrInvalid)
	}
	n, ok := c.in.Named[d.After]
	if !ok {
		return nil, fmt.Errorf("%w: the date hangs on %q, which is not named", ErrInvalid, d.After)
	}
	latest, err := parseTime(n.Latest)
	if err != nil {
		return nil, err
	}
	var t *time.Time
	if n.Time != nil {
		v, err := parseTime(*n.Time)
		if err != nil {
			return nil, err
		}
		if !v.After(latest) && !v.After(c.at) {
			t = &v
		}
	}
	if t == nil {
		if c.at.After(latest) {
			c.reached = false
		}
		c.unset = true
		return nil, nil
	}
	r := t.Add(time.Duration(d.Seconds) * time.Second)
	if r.Year() > 9999 {
		return nil, fmt.Errorf("%w: a date past year 9999", ErrInvalid)
	}
	return &r, nil
}

func (c *calc) use(t *time.Time) {
	if t == nil {
		c.local = true
	}
}

// late reports whether a statement is after the end of the contest window:
// its time is, or an earlier record of the same side already was.
func (c *calc) late(s stmt) bool {
	if s.t != nil {
		return s.t.After(*c.end)
	}
	return s.floor != nil && s.floor.After(*c.end)
}

// inTime is each side's latest in-time statement, or -1.
func (c *calc) inTime() [2]int {
	idx := [2]int{-1, -1}
	for i, s := range c.stmts {
		if !c.late(s) {
			idx[s.side] = i
		}
	}
	return idx
}

func (c *calc) values(idx [2]int) [2]string {
	var v [2]string
	for side, i := range idx {
		if i < 0 {
			continue
		}
		// A cannot_tell that clears an earlier value is relied on as much as
		// a value is.
		c.use(c.stmts[i].t)
		if c.stmts[i].value != CannotTell {
			v[side] = c.stmts[i].value
		}
	}
	return v
}

// concede applies the first concession, if any counts.
func (c *calc) concede(v [2]string) [2]string {
	if c.in.NoConcession || !c.at.After(*c.end) {
		return v
	}
	first := [2]int{-1, -1}
	for i, s := range c.stmts {
		other := v[1-s.side]
		if first[s.side] >= 0 || s.t == nil || !c.late(s) || other == "" {
			continue
		}
		if s.value == other && v[s.side] != other {
			first[s.side] = i
		}
	}
	a, b := first[0], first[1]
	switch {
	case a < 0 && b < 0:
		return v
	case a >= 0 && b >= 0:
		ta, tb := c.stmts[a].t, c.stmts[b].t
		if ta.Equal(*tb) {
			return v
		}
		if tb.Before(*ta) {
			a = -1
		} else {
			b = -1
		}
	}
	if a >= 0 {
		v[0] = c.stmts[a].value
	} else {
		v[1] = c.stmts[b].value
	}
	return v
}

func (c *calc) compute() Result {
	res := Result{}
	if c.due != nil {
		res.Due = c.due.UTC().Format(time.RFC3339)
	}
	if c.end != nil {
		res.WindowEnd = c.end.UTC().Format(time.RFC3339)
	}
	finish := func(status, value string) Result {
		res.Status, res.Value, res.Local = status, value, c.local
		return res
	}
	if !c.reached {
		return finish(NotReached, "")
	}

	// Rule 1: the first final ruling. It is applied before an unset date,
	// so a final ruling shows even while the record a date hangs on has no
	// time yet.
	if !c.in.ConflictingFinalRulings {
		for i, r := range c.rulings {
			if !r.Final {
				continue
			}
			c.use(c.rTimes[i])
			res.Remedy = c.remedy(r)
			return finish(Ruled, r.Value)
		}
	}

	if c.unset {
		return finish(NotDue, "")
	}

	inTime := c.values(c.inTime())
	stated := c.concede(inTime)
	failed := c.in.Check == checkFail
	after := c.at.After(*c.end)

	// Rule 2: a dispute that counts, or two conflicting final rulings.
	if c.in.ConflictingFinalRulings {
		return finish(InDispute, "")
	}
	// A withdrawal with no time is relied on only when no other dispute
	// counts: it is what keeps the row out of dispute.
	restsOnUntimedWithdrawal := false
	for i, d := range c.disputes {
		if c.disputeLate(d, c.dTimes[i], inTime, failed) {
			continue
		}
		side, _ := c.side(d.Side)
		if c.withdrawn[side] {
			restsOnUntimedWithdrawal = restsOnUntimedWithdrawal || !c.wTimed[side]
			continue
		}
		c.use(c.dTimes[i])
		return finish(InDispute, "")
	}
	if restsOnUntimedWithdrawal {
		c.local = true
	}

	// Rules 3 to 6.
	switch {
	case failed || (stated[0] != "" && stated[1] != "" && stated[0] != stated[1]):
		return finish(InDisagreement, "")
	case stated[0] != "" && stated[1] != "":
		if after {
			return finish(Agreed, stated[0])
		}
		return finish(AgreedSoFar, stated[0])
	case stated[0] != "" || stated[1] != "":
		v := stated[0] + stated[1]
		if after {
			return finish(Uncontested, v)
		}
		return finish(Waiting, v)
	case c.at.After(*c.due):
		return finish(NoStatement, "")
	default:
		return finish(NotDue, "")
	}
}

// disputeLate reports a late dispute: its time is after the contest window;
// the in-time statements alone make the row agreed or uncontested and no
// recomputed check failed; and the claimant had named the statement it did
// not contest before the window ended. A dispute with no time is not late.
func (c *calc) disputeLate(d Dispute, t *time.Time, inTime [2]string, failed bool) bool {
	if t == nil || !t.After(*c.end) || failed || !d.SeenInTime {
		return false
	}
	a, b := inTime[0], inTime[1]
	agreed := a != "" && b != "" && a == b
	uncontested := (a == "") != (b == "")
	return agreed || uncontested
}

func (c *calc) remedy(r Ruling) string {
	if r.RemedyDue == nil {
		return RemedyNone
	}
	due, _ := parseTime(*r.RemedyDue)
	for _, t := range c.remedies {
		if t == nil {
			c.local = true
			return RemedyMade
		}
		if !t.After(due) {
			return RemedyMade
		}
	}
	if c.at.After(due) {
		return RemedyNotHonoured
	}
	return RemedyDue
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: time %q: %v", ErrInvalid, s, err)
	}
	// One form only, so the three implementations agree on what they read:
	// UTC, to the second, written with Z.
	if t.Format(time.RFC3339) != s || t.Location() != time.UTC || t.Year() < 1 {
		return time.Time{}, fmt.Errorf("%w: time %q is not YYYY-MM-DDTHH:MM:SSZ", ErrInvalid, s)
	}
	return t, nil
}

// exactMembers checks that every object in b names only members of t (by
// their JSON names, exactly) and none twice.
func exactMembers(b []byte, t reflect.Type) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := walk(dec, t); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data")
	}
	return validUnicode(b)
}

func walk(dec *json.Decoder, t reflect.Type) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil && t != nil && t.Kind() != reflect.Pointer {
		return errors.New("null where a value is required")
	}
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '[':
		var elem reflect.Type
		if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
			elem = t.Elem()
		}
		for dec.More() {
			if err := walk(dec, elem); err != nil {
				return err
			}
		}
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			k := kt.(string)
			if seen[k] {
				return fmt.Errorf("member %q repeated", k)
			}
			seen[k] = true
			var next reflect.Type
			switch {
			case t == nil:
			case t.Kind() == reflect.Map:
				next = t.Elem()
			case t.Kind() == reflect.Struct:
				f, ok := field(t, k)
				if !ok {
					return fmt.Errorf("unknown member %q", k)
				}
				next = f
			}
			if err := walk(dec, next); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // the closing delimiter
	return err
}

func field(t reflect.Type, name string) (reflect.Type, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == name {
			return f.Type, true
		}
	}
	return nil, false
}

// validUnicode refuses invalid UTF-8 and an escaped surrogate that is not
// part of a pair, which Go's decoder would replace with U+FFFD where Rust
// and Python refuse or keep it.
func validUnicode(b []byte) error {
	if !utf8.Valid(b) {
		return errors.New("invalid UTF-8")
	}
	inString := false
	for i := 0; i < len(b); i++ {
		switch {
		case !inString:
			inString = b[i] == '"'
		case b[i] == '"':
			inString = false
		case b[i] == '\\' && i+1 < len(b):
			i++
			if b[i] != 'u' || i+4 >= len(b) {
				continue
			}
			r := hex4(b[i+1 : i+5])
			i += 4
			if !utf16.IsSurrogate(r) {
				continue
			}
			if r >= 0xdc00 || i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return errors.New("an unpaired surrogate")
			}
			if lo := hex4(b[i+3 : i+7]); lo < 0xdc00 || lo > 0xdfff {
				return errors.New("an unpaired surrogate")
			}
			i += 6
		}
	}
	return nil
}

func hex4(b []byte) rune {
	var r rune
	for _, c := range b {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		}
	}
	return r
}
