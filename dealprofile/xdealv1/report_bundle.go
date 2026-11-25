// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// The x-deal-v1 bundle extension (design §9, "The bundle extension";
// PROFILE §10): what a bundle carrying a v1 report adds so a reader can
// verify the report and read the records its rows rely on, the payment
// legs PaymentCheck read included (Steven's ruling on #588). The host
// builds and seals the bundle; this package defines the extension and
// verifies it.

// ReportBook is the extension's book: the holder's deal book record at seq
// 1, the agreement VerifyReport reads the report against, and every record
// the report's rows rely on, by record id, each as its book pair
// {record, header, payload}. The relied-on records are those of every row
// the holder computed, so a copy whose rows are withheld (an adjudicator's)
// or not started carries the same records as one that lists them.
//
// A full adjudicator copy (evidencebook#861, report_full.go) also carries
// Records, every record of the holder's deal book by record id, and
// Received, the copy of the other side's book it holds (absent when it holds
// none).
type ReportBook struct {
	Agreement json.RawMessage            `json:"agreement"`
	ReliedOn  map[string]json.RawMessage `json:"relied_on"`
	Records   map[string]json.RawMessage `json:"records,omitempty"`
	Received  *ReportReceived            `json:"received,omitempty"`
}

// ReportBundle is the x-deal-v1 bundle extension: the sealed report's
// Capsule id, as x-deal-v0's sealed_report, and the book.
type ReportBundle struct {
	SealedReport string     `json:"sealed_report"`
	Book         ReportBook `json:"book"`
}

// reportWithBook builds the report and gathers its book from pairs, the
// book pair of each record the snapshot read, by record id, and, for a full
// adjudicator copy, what the holder holds of the deal (fullInputs).
func reportWithBook(rb ReportBooks, c ReportCopy, pairs map[string][]byte, in fullInputs) (Report, ReportBook, error) {
	r, rows, err := buildReport(rb, c)
	if err != nil {
		return Report{}, ReportBook{}, err
	}
	pair := func(id string) (json.RawMessage, error) {
		p, ok := pairs[id]
		if !ok {
			return nil, fmt.Errorf("xdealv1: report: no book pair read for record %s", id)
		}
		return p, nil
	}
	book := ReportBook{ReliedOn: map[string]json.RawMessage{}}
	if book.Agreement, err = pair(rb.Books.Own[0].RecordID); err != nil {
		return Report{}, ReportBook{}, err
	}
	for _, row := range rows {
		for _, src := range row.ReliedOn {
			if book.ReliedOn[src.Record], err = pair(src.Record); err != nil {
				return Report{}, ReportBook{}, err
			}
		}
	}
	if c.Full {
		if c.Audience != dealprofile.AudienceAdjudicator {
			return Report{}, ReportBook{}, fmt.Errorf("xdealv1: report: only an adjudicator's copy is full")
		}
		if book.Records, book.Received, err = fullBook(rb, pairs, in.held, in.proofs); err != nil {
			return Report{}, ReportBook{}, err
		}
	}
	return r, book, nil
}

// fullInputs is what a full adjudicator copy reads beyond the snapshot's
// book pairs: every record the holder holds for the deal, and the stamp
// proofs held from the counterparty for its log.
type fullInputs struct {
	held   []evidencebook.HeldRecord
	proofs []evidencebook.HeldProof
}

// VerifyReportBundle checks a sealed v1 report (its pair, as VerifyReport
// reads it) with the x-deal-v1 bundle extension that carries it, ext, so
// every host and page applies one rule. ext is exactly {sealed_report,
// book: {agreement, relied_on}}, relied_on an object (empty when no row
// relies on a record). It is refused unless:
//
//   - the report passes VerifyReport against book.agreement, and
//     sealed_report is the report Capsule's id;
//   - the copy's audience and rows agree: an adjudicator's copy is
//     withheld with no rows, no other copy is withheld, rows appear only
//     when listed, and a keep or counterparty copy is listed exactly when
//     its deal has started;
//   - each relied_on pair passes VerifyBook, is of the report's deal, has
//     its record id as its key, and is signed by a party of the agreement:
//     the agreement's own key (the holder), its counterparty_key, or its
//     adjudicator's key; the holder's sit in rests_on.own's log at a seq no
//     greater than its size, and, where the copy names
//     rests_on.counterparty, the counterparty's sit in that log at a seq no
//     greater than its counted; a keep or counterparty copy that names none
//     carries no counterparty record, and in an adjudicator's copy the
//     counterparty's records sit in one log;
//   - an adjudicator's copy, and only it, names a referee;
//   - when the rows are listed, the keys are exactly the records the rows
//     name in relied_on, each named with the side of its pair's signer.
//
// A bundle that passes shows the records the holder says its rows rest on,
// signed by the deal's parties; like the report, it is not a recomputation.
// In a copy whose rows are not listed nothing ties the keys to rows; an
// adjudicator's records have no log bound, and in an adjudicator's copy,
// which names no counterparty prefix, the counterparty's records are bound
// only to one log.
//
// A full adjudicator copy, one whose book carries records, is also checked
// as VerifyFullCopy says.
func VerifyReportBundle(report, ext []byte) (ReportRead, error) {
	read, _, err := verifyReportBundle(report, ext)
	return read, err
}

// VerifyFullCopy is VerifyReportBundle for a full adjudicator copy
// (evidencebook#861): a copy whose book carries no records is refused, and
// what the copy shows is returned (FullCopy; verifyFullCopy gives the
// checks).
func VerifyFullCopy(report, ext []byte) (ReportRead, FullCopy, error) {
	read, full, err := verifyReportBundle(report, ext)
	if err != nil {
		return ReportRead{}, FullCopy{}, err
	}
	if full == nil {
		return ReportRead{}, FullCopy{}, stageErr(StageReport, "the x-deal-v1 extension: not a full adjudicator copy: its book carries no records")
	}
	return read, *full, nil
}

func verifyReportBundle(report, ext []byte) (ReportRead, *FullCopy, error) {
	bad := func(format string, args ...any) (ReportRead, *FullCopy, error) {
		return ReportRead{}, nil, stageErr(StageReport, "the x-deal-v1 extension: "+format, args...)
	}
	if err := checkText(ext); err != nil {
		return ReportRead{}, nil, err
	}
	var x ReportBundle
	if err := strictUnmarshal(ext, &x); err != nil {
		return bad("%v", err)
	}
	if err := exactObject(ext, reflect.TypeOf(x), false, "the extension"); err != nil {
		return bad("%v", err)
	}
	if x.Book.Agreement == nil || x.Book.ReliedOn == nil {
		return bad("the book carries an agreement and a relied_on object")
	}
	if x.Book.Received != nil && x.Book.Records == nil {
		return bad("received is carried only with records, in a full adjudicator copy")
	}
	read, err := VerifyReport(report, x.Book.Agreement)
	if err != nil {
		return ReportRead{}, nil, err
	}
	r := read.Report
	if x.SealedReport != read.CapsuleID {
		return bad("sealed_report %q is not the report Capsule's id %s", x.SealedReport, read.CapsuleID)
	}
	adj := r.Audience == dealprofile.AudienceAdjudicator
	switch {
	case r.Audience != dealprofile.AudienceKeep && r.Audience != dealprofile.AudienceCounterparty && !adj:
		return bad("audience %q is not a copy audience", r.Audience)
	case adj != (r.Referee != ""):
		return bad("an adjudicator's copy, and only it, names a referee")
	case adj != (r.RowsState == RowsWithheld):
		return bad("an adjudicator's copy, and only it, is withheld; this %s copy is %q", r.Audience, r.RowsState)
	case r.RowsState != RowsListed && len(r.Rows) > 0:
		return bad("a %s copy lists no rows", r.RowsState)
	case !adj && (r.RowsState == RowsListed) != (r.Start.State == StateStarted):
		return bad("a keep or counterparty copy is listed exactly when its deal has started; this one is %q at start %q", r.RowsState, r.Start.State)
	case !adj && r.RowsState != RowsListed && r.RowsState != RowsNotStarted:
		return bad("rows_state %q is not one of listed, not_started, withheld", r.RowsState)
	}

	first, err := VerifyBook(x.Book.Agreement)
	if err != nil {
		return ReportRead{}, nil, err
	}
	ag, _ := asAgree(first.Record.Body) // VerifyReport checked it is the agreement
	other := RoleBuyer
	if ag.Role == RoleBuyer {
		other = RoleSeller
	}
	sideOf := map[string]string{first.KeyID: ag.Role}
	if ag.CounterpartyKey.V != nil {
		sideOf[*ag.CounterpartyKey.V] = other
	}
	if a := ag.Adjudicator.V; a != nil {
		sideOf[a.KeyID] = SideAdjudicator
	}
	sides := map[string]string{}
	counterpartLog := ""
	for _, id := range slices.Sorted(maps.Keys(x.Book.ReliedOn)) {
		b, err := VerifyBook(x.Book.ReliedOn[id])
		if err != nil {
			return ReportRead{}, nil, err
		}
		if b.RecordID != id {
			return bad("relied_on %s holds the record %s", id, b.RecordID)
		}
		if b.Record.Block.DealID != r.DealID {
			return bad("relied_on %s is of deal %q, not the report's", id, b.Record.Block.DealID)
		}
		side, ok := sideOf[b.KeyID]
		if !ok {
			return bad("relied_on %s is signed by no party of the agreement", id)
		}
		switch {
		case side == ag.Role && (b.LogID != r.RestsOn.Own.LogID || b.Seq > r.RestsOn.Own.Size):
			return bad("relied_on %s is not in the holder's book the report rests on", id)
		case side == other && r.RestsOn.Counterparty != nil &&
			(b.LogID != r.RestsOn.Counterparty.LogID || b.Seq > r.RestsOn.Counterparty.Counted):
			return bad("relied_on %s is not in the counterparty's prefix the report rests on", id)
		case side == other && r.RestsOn.Counterparty == nil && !adj:
			return bad("relied_on %s is the counterparty's, and the report counted none of its book", id)
		case side == other && counterpartLog != "" && b.LogID != counterpartLog:
			// An adjudicator's copy names no counterparty prefix: its
			// records sit in one book at least.
			return bad("relied_on %s is in another counterparty book than %s", id, counterpartLog)
		}
		if side == other {
			counterpartLog = b.LogID
		}
		sides[id] = side
	}
	var full *FullCopy
	if x.Book.Records != nil {
		f, err := verifyFullCopy(x, r, first, ag)
		if err != nil {
			return ReportRead{}, nil, err
		}
		full = &f
	}
	if r.RowsState != RowsListed {
		return read, full, nil
	}
	named := map[string]bool{}
	for _, row := range r.Rows {
		for _, src := range row.ReliedOn {
			side, ok := sides[src.Record]
			if !ok {
				return bad("row %q relies on %s, which relied_on does not carry", row.ID, src.Record)
			}
			if side != src.Side {
				return bad("row %q names %s as the %s's, and it is signed by the %s", row.ID, src.Record, src.Side, side)
			}
			named[src.Record] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(sides)) {
		if !named[id] {
			return bad("relied_on carries %s, on which no row relies", id)
		}
	}
	return read, full, nil
}
