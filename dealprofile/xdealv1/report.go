// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/rowstatus"
)

// The v1 sealed deal report (design §9, PROFILE §10): a snapshot the holder
// signs of what it computed at as_of from what it held, so a receipt page
// displays each row's status and never recomputes it. It is the same Capsule
// form as v0's (dealprofile.ReportActionID, type deal_report) with profile
// x-deal-v1, always standalone, and nothing reads it as an input.

// Report row_state, counterparty proof and check words (design §9, "The
// closed word sets").
const (
	RowsListed     = "listed"
	RowsNotStarted = "not_started"
	RowsWithheld   = "withheld"

	// ProofHeld: the counterparty runs the layer and the holder holds a stamp
	// proof covering its agreement. ProofNotYet: it runs the layer and no
	// such proof has arrived yet. ProofNoLayer: the counterparty runs no
	// layer, so it sends no stamp proof and the deal is unguarded (u611).
	ProofHeld    = "held"
	ProofNotYet  = "not_yet"
	ProofNoLayer = "no_layer"

	// RemedyNotCounted is a ruled row's remedy when the ruling orders a
	// payment (a refund): rowstatus counts no payment remedy until the
	// profile defines its terms leg (RowInputs), so the report says the
	// remedy is not counted rather than due or not honoured (Sherlock2 and
	// the coordinator, #530 review).
	RemedyNotCounted = "not_counted"

	CheckPassed        = "passed"
	CheckFailed        = "failed"
	CheckNotRun        = "not_run"
	CheckNotRecomputed = "not_recomputed"

	// NotSet is a row date rowstatus leaves unset.
	NotSet = "not_set"
)

// ReportLog is the holder's own book as the report rests on it.
type ReportLog struct {
	LogID string `json:"log_id"`
	Size  uint64 `json:"size"`
	Last  string `json:"last"`
}

// ReportCounterpartLog is the counterparty's counted prefix the report used:
// its log id, how many records it counted, the last one, and the position of
// a fork the holder holds evidence of (0 for none).
type ReportCounterpartLog struct {
	LogID   string `json:"log_id"`
	Counted uint64 `json:"counted"`
	Last    string `json:"last"`
	ForkAt  uint64 `json:"fork_at"`
}

// ReportFact is the book's last status fact against the status the report
// computed: Current is false when the fact's status value is not the one the
// holder computes now (a stamp that could not state the book, TickResult's
// Unstated, or a change no stamp has stated yet), so a page never shows the
// fact as the deal's current status.
type ReportFact struct {
	Current bool `json:"current"`
}

// ReportBooks is what a v1 report is computed from: the status value's books
// (StatusBooks), each {"stamp": n} time resolved as Stamps gives (one it
// does not give is no time yet); the holder's book and the counterparty's
// counted prefix as the report rests on them; the deal checkpoint it is made
// at; and the book's last status fact, nil before the first.
type ReportBooks struct {
	Books       StatusBooks
	Stamps      map[uint64]string
	Own         ReportLog
	Counterpart *ReportCounterpartLog
	Checkpoint  dealprofile.Checkpoint
	Fact        *ReportFact
}

// ReportCopy is what one copy names: its audience, a fresh nonce (64
// lowercase hex), the referee an adjudicator's copy is handed to, the time
// as_of the statuses are computed at (RFC 3339 UTC to the second) and the
// producer's text (as v0's report body; nil is {}).
type ReportCopy struct {
	Audience string
	Nonce    string
	Referee  string
	AsOf     string
	Text     map[string]any
}

// Report is the v1 report payload (design §9).
type Report struct {
	Type           string                 `json:"type"`
	Profile        string                 `json:"profile"`
	DealID         string                 `json:"deal_id"`
	Audience       string                 `json:"audience"`
	Nonce          string                 `json:"nonce"`
	Referee        string                 `json:"referee,omitempty"`
	AsOf           string                 `json:"as_of"`
	DealCheckpoint dealprofile.Checkpoint `json:"deal_checkpoint"`
	PackDigest     string                 `json:"pack_digest"`
	Start          StartResult            `json:"start"`
	Settled        bool                   `json:"settled"`
	RowsState      string                 `json:"rows_state"`
	Rows           []ReportRow            `json:"rows"`
	// Disputes are the status value's, one per dispute record (Steven's #95
	// ruling); absent from a copy whose rows are withheld.
	Disputes []StatusDispute `json:"disputes,omitempty"`
	// RulingsUnnamed is the status value's count of counted rulings held and
	// named by neither book (#527): a page shows "ruling held, unnamed"
	// beside an open outcome. Absent at zero and from a withheld copy.
	RulingsUnnamed int            `json:"rulings_unnamed,omitempty"`
	RestsOn        ReportRestsOn  `json:"rests_on"`
	StatusFact     *ReportFact    `json:"status_fact"`
	Text           map[string]any `json:"report"`
}

// ReportRestsOn is what the whole report rests on (design §9). Counterparty
// and Witnesses are absent from an adjudicator's copy. CounterpartyKind is
// the status value's, null while nothing states it (a page shows "not
// stated"). CounterpartyProof reads counterparty_layer and the held proof
// together, one of ProofHeld, ProofNotYet, ProofNoLayer.
type ReportRestsOn struct {
	Own               ReportLog             `json:"own"`
	Counterparty      *ReportCounterpartLog `json:"counterparty,omitempty"`
	CounterpartyLayer bool                  `json:"counterparty_layer"`
	CounterpartyKind  *string               `json:"counterparty_kind"`
	CounterpartyProof string                `json:"counterparty_proof"`
	Witnesses         []Witness             `json:"witnesses,omitempty"`
}

// ReportRow is one agreed row (design §9, "One row").
type ReportRow struct {
	ID        string          `json:"id"`
	OwedBy    string          `json:"owed_by"`
	Owed      json.RawMessage `json:"owed"`
	Due       string          `json:"due"`
	WindowEnd string          `json:"window_end"`
	Status    string          `json:"status"`
	Value     *string         `json:"value"`
	Remedy    *string         `json:"remedy"`
	// RemedyMade says the owing side's book holds a remedy for the row (a
	// settle or perform whose remedy_for names it), ruled or not (design
	// §8.1, Q5).
	RemedyMade bool           `json:"remedy_made"`
	Local      bool           `json:"local"`
	Check      string         `json:"check"`
	Late       ReportLate     `json:"late"`
	Ruling     *ReportRuling  `json:"ruling"`
	ReliedOn   []ReportSource `json:"relied_on"`
}

// ReportLate are the row's late statements and disputes (rowstatus
// ComputeLate), each by its record.
type ReportLate struct {
	Statements []ReportLateStatement `json:"statements"`
	Disputes   []ReportLateDispute   `json:"disputes"`
}

// ReportLateStatement is a late statement.
type ReportLateStatement struct {
	Side   string `json:"side"`
	Value  string `json:"value"`
	Record string `json:"record"`
}

// ReportLateDispute is a late dispute.
type ReportLateDispute struct {
	Side   string `json:"side"`
	Record string `json:"record"`
}

// ReportRuling is the effective ruling rowstatus applied to a ruled row.
type ReportRuling struct {
	Record    string  `json:"record"`
	Value     string  `json:"value"`
	Against   *string `json:"against"`
	Final     bool    `json:"final"`
	RemedyDue *string `json:"remedy_due"`
}

// ReportSource is a record a row's inputs came from: its side (buyer,
// seller, or adjudicator for a case book record), its record time, and
// whether it has one, every time a status input reads coming from a witness
// the agreement names (design §4.3).
type ReportSource struct {
	Record    string  `json:"record"`
	Side      string  `json:"side"`
	Time      *string `json:"time"`
	Witnessed bool    `json:"witnessed"`
}

// SideAdjudicator is a case book record's side in relied_on.
const SideAdjudicator = "adjudicator"

var reportNonce = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BuildReport computes one copy of the v1 report from b at c.AsOf (design
// §9): start and each row as Start and rowstatus give them, settled, and the
// members c.Audience may have. It refuses what BuildStatus refuses, an
// unknown audience, a nonce that is not 64 lowercase hex, a referee on a copy
// that is not an adjudicator's or none on one that is, an as_of that is not
// RFC 3339 UTC to the second, and books holding a record timed after as_of.
func BuildReport(b ReportBooks, c ReportCopy) (Report, error) {
	r, _, err := buildReport(b, c)
	return r, err
}

// buildReport is BuildReport with every row it computed, listed in the copy
// or not: the records they rely on are the ones the x-deal-v1 bundle
// extension carries for every copy (ReportWithBook).
func buildReport(b ReportBooks, c ReportCopy) (Report, []ReportRow, error) {
	bad := func(format string, args ...any) (Report, []ReportRow, error) {
		return Report{}, nil, fmt.Errorf("xdealv1: report: "+format, args...)
	}
	switch c.Audience {
	case dealprofile.AudienceKeep, dealprofile.AudienceCounterparty, dealprofile.AudienceAdjudicator:
	default:
		return bad("audience %q is not a copy audience", c.Audience)
	}
	if !reportNonce.MatchString(c.Nonce) {
		return bad("the nonce must be 64 lowercase hex")
	}
	referee := dealprofile.NormalizeReferee(c.Referee)
	switch {
	case c.Audience == dealprofile.AudienceAdjudicator && referee == "":
		return bad("an adjudicator's copy names the referee it is handed to")
	case c.Audience != dealprofile.AudienceAdjudicator && referee != "":
		return bad("only an adjudicator's copy names a referee")
	}
	if t, err := time.Parse(time.RFC3339, c.AsOf); err != nil || t.UTC().Format(time.RFC3339) != c.AsOf {
		return bad("as_of %q is not RFC 3339 UTC to the second", c.AsOf)
	}
	v, err := BuildStatus(b.Books)
	if err != nil {
		return Report{}, nil, err
	}
	of, _ := asPropose(b.Books.Offer.Body)
	own, _ := asAgree(b.Books.Own[0].Record.Body)

	resolve := func(rs []StatusRecord) []TimedRecord {
		out := make([]TimedRecord, len(rs))
		for i, r := range rs {
			out[i] = TimedRecord{Record: r.Record, RecordID: r.RecordID, Time: resolveTime(r.Time, b.Stamps)}
		}
		return out
	}
	rb := RowBooks{Offer: b.Books.Offer, Own: resolve(b.Books.Own), Counterpart: resolve(b.Books.Counterpart),
		CaseBook: resolve(b.Books.CaseBook), ConflictingFinalRulings: b.Books.ConflictingFinalRulings, Checks: paymentChecks(b.Books.Payments)}
	// A report is what the holder computed at as_of from what it held then:
	// a record timed after as_of was not held, and the status value's
	// disputes and unnamed count read every record given (Codex and the
	// stand-in, #530 review).
	for _, rs := range [][]TimedRecord{rb.Own, rb.Counterpart, rb.CaseBook} {
		for _, r := range rs {
			if r.Time != nil && *r.Time > c.AsOf {
				return bad("record %s is timed %s, after as_of %s", r.RecordID, *r.Time, c.AsOf)
			}
		}
	}
	ins, srcs, err := rowInputs(rb, c.AsOf)
	if err != nil {
		return Report{}, nil, err
	}
	st := v.Start
	start, err := Start(StartInput{At: c.AsOf, Side: st.Side, CounterpartyLayer: st.CounterpartyLayer, AgreeBy: st.AgreeBy,
		CounterpartAgreementHeld: st.CounterpartAgreementHeld, SameOfferAndRows: st.SameOfferAndRows,
		OffererNamesAcceptor: st.OffererNamesAcceptor, OffererAgreementTime: resolveTime(st.OffererAgreementTime, b.Stamps),
		AcceptorNamesOfferer: st.AcceptorNamesOfferer, CancellationNamedFirst: st.CancellationNamedFirst,
		FirstAct: resolveFirstAct(st.FirstAct, b.Stamps)})
	if err != nil {
		return Report{}, nil, err
	}

	r := Report{Type: dealprofile.ReportType, Profile: Profile, DealID: b.Books.Offer.Block.DealID, Audience: c.Audience,
		Nonce: c.Nonce, Referee: referee, AsOf: c.AsOf, DealCheckpoint: b.Checkpoint, PackDigest: of.PackDigest,
		Start: start, Rows: []ReportRow{}, StatusFact: b.Fact, Text: c.Text,
		RestsOn: ReportRestsOn{Own: b.Own, CounterpartyLayer: v.CounterpartyLayer, CounterpartyKind: v.CounterpartyKind,
			CounterpartyProof: counterpartyProof(v)}}
	if r.Text == nil {
		r.Text = map[string]any{}
	}
	legs, err := paymentLegs(b.Books, rb)
	if err != nil {
		return Report{}, nil, err
	}
	rows := []ReportRow{}
	for i, in := range ins {
		row, err := reportRow(rb, of.TermsRows.Rows[i], in, srcs[i], legs[of.TermsRows.Rows[i].ID])
		if err != nil {
			return Report{}, nil, err
		}
		rows = append(rows, row)
	}
	r.Settled = settled(start, rows)
	switch {
	case c.Audience == dealprofile.AudienceAdjudicator:
		r.RowsState = RowsWithheld
	case start.State != StateStarted:
		r.RowsState = RowsNotStarted
	default:
		r.RowsState = RowsListed
		r.Rows = rows
	}
	if c.Audience != dealprofile.AudienceAdjudicator {
		r.RestsOn.Counterparty = b.Counterpart
		r.RestsOn.Witnesses = own.Witnesses
		if r.RestsOn.Witnesses == nil {
			r.RestsOn.Witnesses = []Witness{}
		}
		r.Disputes = v.Disputes
		r.RulingsUnnamed = v.RulingsUnnamed
	}
	return r, rows, nil
}

// ReportPayload is the report's payload bytes: its JCS form.
func ReportPayload(r Report) ([]byte, error) { return jcsJSON(r) }

// resolveTime is a status time as a reader resolves it: a known time, a
// stamp's time from stamps, or no time.
func resolveTime(t StatusTime, stamps map[uint64]string) *string {
	switch {
	case t.Known != "":
		k := t.Known
		return &k
	case t.Stamp != 0:
		if s, ok := stamps[t.Stamp]; ok {
			return &s
		}
	}
	return nil
}

// resolveFirstAct is the status value's first act as Start reads it: its
// time resolved from the stamps, nil time when it is not known.
func resolveFirstAct(a *StatusFirstAct, stamps map[uint64]string) *FirstAct {
	if a == nil {
		return nil
	}
	return &FirstAct{Time: resolveTime(a.Time, stamps)}
}

func counterpartyProof(v StatusValue) string {
	switch {
	case !v.CounterpartyLayer:
		return ProofNoLayer
	case v.CounterpartyProofHeld:
		return ProofHeld
	default:
		return ProofNotYet
	}
}

// reportRow is one row: rowstatus's result and late records at in.At, the
// effective ruling, and the records the input came from.
// paymentLegs maps each payment row's PaymentResult to the book records it
// read: CheckedOver, whose first entry is the offer's digest (a journal
// record, not a book record) and whose others are the digests of settle
// legs of the holder's or the counterparty's book. It refuses a result
// whose first entry is not the offer's digest, or that names a record the
// books given do not hold: a report rests only on what the holder held.
func paymentLegs(b StatusBooks, rb RowBooks) (map[string][]recRef, error) {
	if len(b.Payments) == 0 {
		return nil, nil
	}
	od, err := Digest(b.Offer)
	if err != nil {
		return nil, err
	}
	held := map[string]recRef{}
	for _, bk := range []struct {
		book byte
		recs []TimedRecord
	}{{bookOwn, rb.Own}, {bookCounterpart, rb.Counterpart}} {
		for i, r := range bk.recs {
			d, err := Digest(r.Record)
			if err != nil {
				return nil, err
			}
			if _, ok := held[d]; !ok {
				held[d] = recRef{bk.book, i}
			}
		}
	}
	out := map[string][]recRef{}
	for _, row := range slices.Sorted(maps.Keys(b.Payments)) {
		over := b.Payments[row].CheckedOver
		if len(over) == 0 || over[0] != od {
			return nil, fmt.Errorf("xdealv1: report: row %s's payment check does not begin with the offer's digest", row)
		}
		for _, d := range over[1:] {
			ref, ok := held[d]
			if !ok {
				return nil, fmt.Errorf("xdealv1: report: row %s's payment check read record %s, which the books given do not hold", row, d)
			}
			out[row] = append(out[row], ref)
		}
	}
	return out, nil
}

func reportRow(b RowBooks, agreed Row, in rowstatus.Input, src rowSources, legs []recRef) (ReportRow, error) {
	res, err := rowstatus.Compute(in)
	if err != nil {
		return ReportRow{}, err
	}
	late, err := rowstatus.ComputeLate(in)
	if err != nil {
		return ReportRow{}, err
	}
	rec := func(ref recRef) (TimedRecord, string) {
		switch ref.book {
		case bookOwn:
			own, _ := asAgree(b.Own[0].Record.Body)
			return b.Own[ref.i], own.Role
		case bookCounterpart:
			own, _ := asAgree(b.Own[0].Record.Body)
			if own.Role == RoleBuyer {
				return b.Counterpart[ref.i], RoleSeller
			}
			return b.Counterpart[ref.i], RoleBuyer
		default:
			return b.CaseBook[ref.i], SideAdjudicator
		}
	}
	orNotSet := func(s string) string {
		if s == "" {
			return NotSet
		}
		return s
	}
	row := ReportRow{ID: agreed.ID, OwedBy: agreed.OwedBy, Owed: agreed.Owed, Due: orNotSet(res.Due), WindowEnd: orNotSet(res.WindowEnd),
		Status: res.Status, Local: res.Local, Check: reportCheck(agreed, in.Check),
		Late: ReportLate{Statements: []ReportLateStatement{}, Disputes: []ReportLateDispute{}}, ReliedOn: []ReportSource{}}
	if res.Value != "" {
		row.Value = &res.Value
	}
	if res.Remedy != "" {
		row.Remedy = &res.Remedy
	}
	for _, i := range late.Statements {
		r, _ := rec(src.statements[i])
		row.Late.Statements = append(row.Late.Statements, ReportLateStatement{Side: in.Statements[i].Side, Value: in.Statements[i].Value, Record: r.RecordID})
	}
	for _, i := range late.Disputes {
		r, _ := rec(src.disputes[i])
		row.Late.Disputes = append(row.Late.Disputes, ReportLateDispute{Side: in.Disputes[i].Side, Record: r.RecordID})
	}
	if res.Status == rowstatus.Ruled {
		for k, ru := range in.Rulings {
			if !ru.Final {
				continue
			}
			r, _ := rec(src.rulings[k])
			body, _ := bodyOf[RulingBody](r.Record.Body)
			row.Ruling = &ReportRuling{Record: r.RecordID, Value: ru.Value, Against: body.Against.V, Final: true, RemedyDue: ru.RemedyDue}
			if body.Remedy != nil && body.Remedy.AmountMinor != nil {
				nc := RemedyNotCounted
				row.Remedy = &nc
			}
			break
		}
	}
	// A remedy held for the row, ruled or not (Q5), is one of the records
	// the row relied on.
	var remedyRef *recRef
	owing, owingBook := b.Counterpart, bookCounterpart
	if own, _ := asAgree(b.Own[0].Record.Body); own.Role == agreed.OwedBy {
		owing, owingBook = b.Own, bookOwn
	}
	if i := firstRemedy(len(owing), func(i int) Record { return owing[i].Record }, agreed.ID); i >= 0 {
		row.RemedyMade = true
		remedyRef = &recRef{owingBook, i}
	}
	var refs []recRef
	for _, n := range slices.Sorted(maps.Keys(src.named)) {
		refs = append(refs, src.named[n])
	}
	for _, list := range [][]recRef{src.statements, src.disputes, src.withdrawals, src.rulings, src.remedies} {
		refs = append(refs, list...)
	}
	if remedyRef != nil {
		refs = append(refs, *remedyRef)
	}
	// The settle legs the payment check read (Steven's ruling on #588).
	refs = append(refs, legs...)
	seen := map[recRef]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		r, side := rec(ref)
		row.ReliedOn = append(row.ReliedOn, ReportSource{Record: r.RecordID, Side: side, Time: r.Time, Witnessed: r.Time != nil})
	}
	return row, nil
}

func reportCheck(agreed Row, check string) string {
	switch {
	case !agreed.Recomputed:
		return CheckNotRecomputed
	case check == PaymentPass:
		return CheckPassed
	case check == PaymentFail:
		return CheckFailed
	default:
		return CheckNotRun
	}
}

// settled is design §9's: the deal has started, and every row that is not
// not_reached is agreed (met, or not met with a remedy made), uncontested, or
// ruled with remedy none or made. A remedy not counted keeps the deal
// unsettled.
func settled(start StartResult, rows []ReportRow) bool {
	if start.State != StateStarted {
		return false
	}
	for _, r := range rows {
		switch r.Status {
		case rowstatus.NotReached, rowstatus.Uncontested:
		case rowstatus.Agreed:
			// A row both sides agree was not met is remedied only by a
			// remedy made (Steven's SH-16 ruling, design Q5; Sherlock2, #530
			// review): the concession alone does not settle it.
			if r.Value != nil && *r.Value == ValueNotMet && !r.RemedyMade {
				return false
			}
		case rowstatus.Ruled:
			if r.Remedy == nil || (*r.Remedy != rowstatus.RemedyNone && *r.Remedy != rowstatus.RemedyMade) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Report is a copy of the v1 report of the holder's deal book logID (design
// §9), computed from what the shelf holds now, as of c.AsOf: the books a
// stamp's status value reads (each time a named witness gives, no time
// otherwise), at the book's latest checkpoint, which must cover the whole
// book (a stamp checkpoints it; Report writes nothing), and whether the
// book's last status fact is the status computed now. The host seals
// ReportPayload with dealprofile.ReportCapsule as a standalone Capsule. It
// refuses a book that is not a v1 deal book or holds a record that is not
// readable as one, a shelf with no journal (it never creates one), and a book
// that grew past its checkpoint or kept changing while it was read. Each shelf call takes the
// shelf's lock, so it is never called from a stamp or a Receive.
func (p *StatusProvider) Report(ctx context.Context, logID string, c ReportCopy) (Report, error) {
	r, _, err := p.ReportWithBook(ctx, logID, c)
	return r, err
}

// ReportWithBook is Report with the book pairs the report rests on, read in
// the same snapshot (design §9, "The bundle extension"): the holder's
// agreement at seq 1 and every record the report's rows rely on, whether
// the copy lists its rows or not. The host carries them as the x-deal-v1
// bundle extension's book (ReportBundle) beside the sealed report.
func (p *StatusProvider) ReportWithBook(ctx context.Context, logID string, c ReportCopy) (Report, ReportBook, error) {
	s, err := p.ReportSnapshot(ctx, logID, c)
	return s.Report, s.Book, err
}

// ReportSnapshot is a report, the book pairs it rests on, and each payment
// row's payment check, all from one read of the shelf (ReportWithBook), so a
// host that shows the checks beside the report never shows a check the
// report did not read.
type ReportSnapshot struct {
	Report Report
	Book   ReportBook
	// Payments are the payment checks the status read, by row, exactly as
	// the status input carries them (PaymentCheck's results).
	Payments map[string]PaymentResult
	// PaymentsNotRun are the rows whose owed is an amount but whose payment
	// check was not run, with why: CheckNotRecomputed (the row is not
	// recomputed, so it is no payment row; the report row's check says the
	// same), NotRunNoOfferPair (the host gave no sent pair of the offer) or
	// NotRunNoAcceptorKey (no acceptor key is known yet).
	PaymentsNotRun map[string]string
}

// Why a payment row's check was not run (ReportSnapshot.PaymentsNotRun).
const (
	NotRunNoOfferPair   = "no_offer_pair"
	NotRunNoAcceptorKey = "no_acceptor_key"
)

// ReportSnapshot is ReportWithBook with the payment checks read in the same
// snapshot (ReportSnapshot, the type).
func (p *StatusProvider) ReportSnapshot(ctx context.Context, logID string, c ReportCopy) (ReportSnapshot, error) {
	if p.Shelf == nil {
		return ReportSnapshot{}, fmt.Errorf("xdealv1: the status provider has no shelf")
	}
	book, err := p.Shelf.Open(ctx, logID)
	if err != nil {
		return ReportSnapshot{}, err
	}
	first, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: 1})
	if err != nil {
		return ReportSnapshot{}, err
	}
	notV1 := fmt.Errorf("xdealv1: report: book %s is not an x-deal-v1 deal book", logID)
	if len(first) != 1 {
		return ReportSnapshot{}, notV1
	}
	if rec, kind, err := v1Payload(ctx, book, first[0]); err != nil {
		return ReportSnapshot{}, err
	} else if a, ok := asAgree(rec.Body); kind != payloadV1 || !ok || a.Kind != KindAgreement {
		return ReportSnapshot{}, notV1
	}
	journal, err := p.Shelf.ExistingJournalView(ctx)
	if err != nil {
		return ReportSnapshot{}, err
	}
	if journal == nil {
		return ReportSnapshot{}, fmt.Errorf("xdealv1: report: the shelf has no journal, which holds the deal's offer")
	}
	bookID := first[0].RecordID
	for attempt := 0; attempt < 3; attempt++ {
		rb, pairs, stable, err := p.reportBooks(ctx, logID, bookID, book, journal)
		if err != nil {
			return ReportSnapshot{}, err
		}
		if stable {
			r, b, err := reportWithBook(rb, c, pairs)
			if err != nil {
				return ReportSnapshot{}, err
			}
			return ReportSnapshot{Report: r, Book: b, Payments: rb.Books.Payments, PaymentsNotRun: paymentsNotRun(rb.Books)}, nil
		}
	}
	return ReportSnapshot{}, fmt.Errorf("%w: report: book %s kept changing while it was read", evidencebook.ErrBusy, logID)
}

// paymentsNotRun are the rows whose owed is an amount and whose payment check
// the status did not run, with why (ReportSnapshot.PaymentsNotRun).
func paymentsNotRun(b StatusBooks) map[string]string {
	of, ok := asPropose(b.Offer.Body)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, row := range of.TermsRows.Rows {
		if _, isAmount := owedAmount(row); !isAmount {
			continue
		}
		switch _, checked := b.Payments[row.ID]; {
		case !row.Recomputed:
			out[row.ID] = CheckNotRecomputed
		case checked:
		case b.Payments == nil:
			out[row.ID] = NotRunNoOfferPair
		default:
			out[row.ID] = NotRunNoAcceptorKey
		}
	}
	return out
}

// reportBooks reads one snapshot of what a report rests on, with the book
// pair of each record read by record id; stable false when the book moved
// during the read. The counterparty's prefix is the one
// read the status walked.
func (p *StatusProvider) reportBooks(ctx context.Context, logID, bookID string, book *evidencebook.Book, journal evidencebook.JournalReader) (ReportBooks, map[string][]byte, bool, error) {
	cps, err := book.Checkpoints(ctx)
	if err != nil {
		return ReportBooks{}, nil, false, err
	}
	size := book.Size()
	if len(cps) == 0 || cps[len(cps)-1].Entries != size {
		return ReportBooks{}, nil, false, fmt.Errorf("xdealv1: report: book %s has no checkpoint at its size %d; a stamp checkpoints it", logID, size)
	}
	cp := cps[len(cps)-1]
	b, _, counterpart, pairs, ok, err := p.statusBooks(ctx, evidencebook.StatusInput{BookID: bookID, LogID: logID, Book: book, Journals: journal})
	if err != nil {
		return ReportBooks{}, nil, false, err
	}
	if !ok {
		return ReportBooks{}, nil, false, fmt.Errorf("xdealv1: report: book %s is not an x-deal-v1 deal book", logID)
	}
	if book.Size() != size {
		return ReportBooks{}, nil, false, nil
	}
	if uint64(len(b.Own)) != size {
		return ReportBooks{}, nil, false, fmt.Errorf("xdealv1: report: book %s holds a record past seq %d that is not a readable x-deal-v1 record", logID, len(b.Own))
	}
	rb := ReportBooks{Books: b, Counterpart: counterpart,
		Own:        ReportLog{LogID: logID, Size: size, Last: b.Own[len(b.Own)-1].RecordID},
		Checkpoint: dealprofile.Checkpoint{LogID: logID, MMRSize: cp.TreeSize, Root: cp.Root}}
	if rb.Fact, err = p.factCurrent(ctx, bookID, b); err != nil {
		return ReportBooks{}, nil, false, err
	}
	return rb, pairs, true, nil
}

// factCurrent is the book's last status fact against the status value b
// builds now, compared in JCS form; nil before the book's first fact.
func (p *StatusProvider) factCurrent(ctx context.Context, bookID string, b StatusBooks) (*ReportFact, error) {
	facts, err := p.Shelf.Openings(ctx, bookID, evidencebook.AudienceOwner)
	if err != nil {
		return nil, err
	}
	var last *evidencebook.FactOpening
	for _, f := range facts {
		if f.StatusOpening != nil {
			last = f.StatusOpening
		}
	}
	if last == nil {
		return nil, nil
	}
	v, err := BuildStatus(b)
	if err != nil {
		return nil, err
	}
	now, err := jcsJSON(v)
	if err != nil {
		return nil, err
	}
	held, err := decodeJSON(last.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: book %s's last status opening: %v", evidencebook.ErrCorrupt, bookID, err)
	}
	was, err := canonical.JCS(held)
	if err != nil {
		return nil, err
	}
	return &ReportFact{Current: bytes.Equal(now, was)}, nil
}

// jcsJSON is v's JSON in JCS form.
func jcsJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	doc, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	return canonical.JCS(doc)
}
