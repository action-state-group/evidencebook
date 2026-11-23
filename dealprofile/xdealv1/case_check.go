// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// An adjudicator's rules (evidencebook#779): what makes a dispute and the
// parties' adjudicator copies a case for an adjudicator, what a later
// record of that case must be, and how a ruling is sealed in the case book.
// capsulectl-deal's `deal ruling` and a hosted adjudicator both call these,
// so every adjudicator built on this library applies the same rules.

// StageCase is a dispute, copy or later record read as a case (CheckCase,
// CheckCaseRecord).
const StageCase = "case"

// CaseCopy is one party's adjudicator copy as its bundle carries it: the
// report pair (the bundle's report extension) and the x-deal-v1 extension,
// as VerifyReportBundle takes them. The bundle itself is the host's to
// verify.
type CaseCopy struct {
	Report    []byte
	Extension []byte
}

// Case is a dispute and one or both parties' adjudicator copies read as a
// case for one adjudicator: the deal, the disputed row, both sides' keys,
// what the adjudicator read of each side's book (null for a side it holds
// no copy of), the agreement the ruling is read against, the dispute's
// forum, the agreement's rules digest, and the deal's case log.
type Case struct {
	DealID      string
	Row         string
	Dispute     BookRead
	Keys        RulingKeys
	Read        RulingRead
	Agreement   Record
	OfferDigest string
	Forum       string
	RulesDigest string
	LogID       string
}

// caseCopy is a verified adjudicator copy.
type caseCopy struct {
	role, key, last string
	agreement       Record
	ag              AgreeBody
}

func caseErr(format string, args ...any) error { return stageErr(StageCase, format, args...) }

// readCaseCopy reads one copy: it passes VerifyReportBundle, is an
// adjudicator copy, and its agreement names adjudicatorKey.
func readCaseCopy(c CaseCopy, adjudicatorKey string) (caseCopy, error) {
	read, err := VerifyReportBundle(c.Report, c.Extension)
	if err != nil {
		return caseCopy{}, err
	}
	if read.Report.Audience != dealprofile.AudienceAdjudicator {
		return caseCopy{}, caseErr("a %s copy is not an adjudicator copy", read.Report.Audience)
	}
	var x ReportBundle
	if err := json.Unmarshal(c.Extension, &x); err != nil {
		return caseCopy{}, caseErr("the x-deal-v1 extension: %v", err)
	}
	first, err := VerifyBook(x.Book.Agreement)
	if err != nil {
		return caseCopy{}, err
	}
	ag, ok := asAgree(first.Record.Body)
	if !ok || ag.Kind != KindAgreement {
		return caseCopy{}, caseErr("the copy carries no agreement")
	}
	if ag.Adjudicator.V == nil || ag.Adjudicator.V.KeyID != adjudicatorKey {
		return caseCopy{}, caseErr("the copy's agreement does not name this adjudicator")
	}
	if ag.Role != RoleBuyer && ag.Role != RoleSeller {
		return caseCopy{}, caseErr("the copy's agreement has role %s", ag.Role)
	}
	return caseCopy{role: ag.Role, key: first.KeyID, last: read.Report.RestsOn.Own.Last, agreement: first.Record, ag: ag}, nil
}

// namesCounterparty reports whether ag, when it names a counterparty key,
// names key; an agreement with no layered counterparty names none.
func namesCounterparty(ag AgreeBody, key string) bool {
	if ag.CounterpartyLayer == nil || !*ag.CounterpartyLayer || ag.CounterpartyKey.V == nil {
		return true
	}
	return *ag.CounterpartyKey.V == key
}

// CheckCase reads a dispute (its book pair) and one or both parties'
// adjudicator copies as a case for the adjudicator whose key id is
// adjudicatorKey:
//   - the dispute verifies and is of kind dispute, names adjudicatorKey,
//     and is signed by a party of the copies' deal;
//   - each copy passes VerifyReportBundle, is an adjudicator copy, and its
//     agreement names adjudicatorKey;
//   - the copies are of one deal and one offer, one per side, and with two,
//     each agreement names the other copy's signer as its counterparty and
//     both name the same adjudicator terms and forums;
//   - with one copy, the other side's key is that agreement's
//     counterparty_key, and a copy whose agreement names none is refused;
//   - the dispute is read against its own agreement (CheckDispute) when the
//     disputing side's copy is given, and otherwise its forum against the
//     agreement's forums.
func CheckCase(dispute []byte, copies []CaseCopy, adjudicatorKey string) (Case, error) {
	dread, err := VerifyBook(dispute)
	if err != nil {
		return Case{}, err
	}
	d, ok := bodyOf[DisputeBody](dread.Record.Body)
	switch {
	case !ok || d.Kind != KindDispute:
		return Case{}, caseErr("the record is not a dispute")
	case d.Adjudicator != adjudicatorKey:
		return Case{}, caseErr("the dispute goes to another adjudicator")
	case len(copies) < 1 || len(copies) > 2:
		return Case{}, caseErr("a case takes one or two adjudicator copies, not %d", len(copies))
	}
	var cs []caseCopy
	for _, c := range copies {
		cc, err := readCaseCopy(c, adjudicatorKey)
		if err != nil {
			return Case{}, err
		}
		if cc.agreement.Block.DealID != dread.Record.Block.DealID {
			return Case{}, caseErr("a copy is of deal %s, the dispute of deal %s", cc.agreement.Block.DealID, dread.Record.Block.DealID)
		}
		if len(cs) > 0 {
			o := cs[0]
			if cc.ag.Offer.Digest != o.ag.Offer.Digest {
				return Case{}, caseErr("the two copies answer different offers")
			}
			if cc.role == o.role {
				return Case{}, caseErr("both copies are the %s's: a case takes one copy per side", cc.role)
			}
			// Each side's agreement names the other's key: a copy under a key
			// the other agreement does not name is not of this deal.
			if !namesCounterparty(cc.ag, o.key) || !namesCounterparty(o.ag, cc.key) {
				return Case{}, caseErr("the two copies' agreements do not name each other's keys")
			}
			if !sameAdjudicator(cc.ag.Adjudicator, o.ag.Adjudicator) ||
				!slices.Equal(sortedCopy(cc.ag.Forums), sortedCopy(o.ag.Forums)) {
				return Case{}, caseErr("the two copies' agreements name different adjudicator terms or forums")
			}
		}
		cs = append(cs, cc)
	}
	keys := map[string]string{}
	read := RulingRead{Buyer: NullOf[string](), Seller: NullOf[string]()}
	for _, cc := range cs {
		keys[cc.role] = cc.key
		if cc.role == RoleBuyer {
			read.Buyer = Some(cc.last)
		} else {
			read.Seller = Some(cc.last)
		}
	}
	if len(cs) == 1 {
		cc := cs[0]
		other := RoleBuyer
		if cc.role == RoleBuyer {
			other = RoleSeller
		}
		if cc.ag.CounterpartyLayer == nil || !*cc.ag.CounterpartyLayer || cc.ag.CounterpartyKey.V == nil {
			return Case{}, caseErr("the copy's agreement names no counterparty key, so the other side's key is unknown: give its copy too")
		}
		keys[other] = *cc.ag.CounterpartyKey.V
	}
	if dread.KeyID != keys[RoleBuyer] && dread.KeyID != keys[RoleSeller] {
		return Case{}, caseErr("the dispute is signed by neither party of the deal")
	}
	checked := false
	for _, cc := range cs {
		if cc.key == dread.KeyID {
			if err := CheckDispute(dread.Record, cc.agreement); err != nil {
				return Case{}, err
			}
			checked = true
		}
	}
	// The ruling is read against the disputing side's agreement when its copy
	// is given; the two agreements agree on the adjudicator and forums anyway.
	first := cs[0]
	for _, cc := range cs {
		if cc.key == dread.KeyID {
			first = cc
		}
	}
	if !checked && !slices.Contains(first.ag.Forums, d.Forum) {
		return Case{}, caseErr("the dispute's forum %s is not one the agreement accepts", d.Forum)
	}
	return Case{
		DealID: dread.Record.Block.DealID, Row: d.Row, Dispute: dread,
		Keys: RulingKeys{Buyer: keys[RoleBuyer], Seller: keys[RoleSeller]}, Read: read,
		Agreement: first.agreement, OfferDigest: first.ag.Offer.Digest, Forum: d.Forum,
		RulesDigest: first.ag.Adjudicator.V.RulesDigest, LogID: CaseLogID(first.ag.Offer.Digest, adjudicatorKey),
	}, nil
}

// CheckCaseRecord reads a later record of an open case: a withdrawal or a
// challenge, which verifies, is of the case's deal, and is signed by one of
// its parties; a withdrawal is of the case's row, and a challenge names a
// ruling whose record digest is among rulings, the case book's.
func CheckCaseRecord(record []byte, c Case, rulings []string) (BookRead, error) {
	read, err := VerifyBook(record)
	if err != nil {
		return BookRead{}, err
	}
	d, ok := bodyOf[DisputeBody](read.Record.Body)
	switch {
	case !ok || (d.Kind != KindWithdrawal && d.Kind != KindChallenge):
		return BookRead{}, caseErr("the record is neither a withdrawal nor a challenge")
	case read.Record.Block.DealID != c.DealID:
		return BookRead{}, caseErr("the record is of deal %s, the case of deal %s", read.Record.Block.DealID, c.DealID)
	case read.KeyID != c.Keys.Buyer && read.KeyID != c.Keys.Seller:
		return BookRead{}, caseErr("the record is signed by neither party of the case")
	case d.Kind == KindWithdrawal && d.Row != c.Row:
		return BookRead{}, caseErr("the withdrawal is of row %s, the case of row %s", d.Row, c.Row)
	case d.Kind == KindChallenge && (d.Ruling == nil || !slices.Contains(rulings, d.Ruling.Digest)):
		return BookRead{}, caseErr("the challenge names a ruling this case book does not hold")
	}
	return read, nil
}

// RulingOf is the ruling of c with the adjudicator's decision: its value,
// against whom (null for neither), its remedy, whether it is final, and its
// forum ("" for the dispute's).
func (c Case) RulingOf(value string, against Null[string], remedy *RulingRemedy, final bool, forum string) RulingBody {
	if forum == "" {
		forum = c.Forum
	}
	return RulingBody{Row: c.Row, Keys: c.Keys, Against: against, Value: value, Remedy: remedy, Read: c.Read,
		Forum: forum, RulesDigest: c.RulesDigest, Final: final}
}

// SealedRuling is a ruling in the case book: its place, record id, record
// digest, record and book pair, the case book's first record id (the book
// id a stamp proof of it names), and whether it was already there.
type SealedRuling struct {
	Seq      uint64
	RecordID string
	Digest   string
	Record   Record
	Pair     []byte
	BookID   string
	Existing bool
}

// ErrRulingConflict refuses a ruling on a row the case book already holds
// a different final ruling on: the first final ruling applies, and its
// adjudicator never replaces it; a challenge of it records disagreement.
var ErrRulingConflict = errors.New("xdealv1: the case book already holds a different final ruling on this row")

// sealTries is how many times SealRuling rebuilds a ruling whose place was
// taken by another write.
const sealTries = 8

// SealRuling seals body in the case book of c on shelf, at time at, once: a
// ruling of the deal with the same body already there is returned as it is
// (Existing), and any other ruling on a row the book already holds a final
// ruling on is refused (ErrRulingConflict). The body must be c's: its row,
// keys, read and rules digest (Case.RulingOf builds one). The ruling is
// checked (Check, CheckRuling against c.Agreement, the case-book placement)
// before it is appended at the book's next place.
func SealRuling(ctx context.Context, shelf *evidencebook.Shelf, c Case, body RulingBody, at time.Time) (SealedRuling, error) {
	book, err := shelf.Open(ctx, c.LogID)
	if err != nil {
		return SealedRuling{}, err
	}
	switch {
	case body.Row != c.Row:
		return SealedRuling{}, caseErr("the ruling is on row %s, the case on row %s", body.Row, c.Row)
	case body.Keys != c.Keys:
		return SealedRuling{}, caseErr("the ruling names other keys than the case's parties")
	case !sameRead(body.Read, c.Read):
		return SealedRuling{}, caseErr("the ruling's read is not what the case read")
	case body.RulesDigest != c.RulesDigest:
		return SealedRuling{}, caseErr("the ruling names other rules than the agreement's")
	}
	wantBody, err := bodyBytes(body)
	if err != nil {
		return SealedRuling{}, err
	}
	for try := 0; try < sealTries; try++ {
		size := book.Size()
		var rows []evidencebook.Record
		if size > 0 {
			if rows, err = book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: size}); err != nil {
				return SealedRuling{}, err
			}
		}
		var bookID, prevID string
		for i, r := range rows {
			if i == 0 {
				bookID = r.RecordID
			}
			prevID = r.RecordID
			rec, kind, err := v1Payload(ctx, book, r)
			if err != nil {
				return SealedRuling{}, err
			}
			if kind != payloadV1 {
				return SealedRuling{}, fmt.Errorf("xdealv1: the case book's record %d is not readable", i+1)
			}
			rb, ok := bodyOf[RulingBody](rec.Body)
			if !ok || rec.Block.DealID != c.DealID {
				continue
			}
			got, err := bodyBytes(*rb)
			if err != nil {
				return SealedRuling{}, err
			}
			if bytes.Equal(got, wantBody) {
				return sealedAt(ctx, book, r, rec, uint64(i+1), bookID, true)
			}
			if rb.Final && rb.Row == body.Row {
				return SealedRuling{}, ErrRulingConflict
			}
		}
		block := Block{Profile: Profile, Canonicalization: Canonicalization, DealID: c.DealID,
			RecordType: TypeRuling, At: at.UTC().Format(time.RFC3339), Seq: int64(size) + 1}
		if size > 0 {
			last := rows[len(rows)-1]
			if len(last.Header.PayloadCommitments) != 1 {
				return SealedRuling{}, fmt.Errorf("xdealv1: the case book's last record has no record digest")
			}
			prev := RefTo(last.Header.PayloadCommitments[0])
			block.Prev = &prev
		}
		rec := Record{Block: block, Body: &body}
		if err := Check(rec); err != nil {
			return SealedRuling{}, err
		}
		if err := CheckRuling(rec, c.Agreement); err != nil {
			return SealedRuling{}, err
		}
		entry, err := BookEntry(rec, prevID)
		if err != nil {
			return SealedRuling{}, err
		}
		stored, err := book.AppendAt(ctx, size+1, entry)
		if errors.Is(err, evidencebook.ErrBusy) {
			continue
		}
		if err != nil {
			return SealedRuling{}, err
		}
		if bookID == "" {
			bookID = stored.RecordID
		}
		return sealedAt(ctx, book, stored, rec, size+1, bookID, false)
	}
	return SealedRuling{}, fmt.Errorf("%w: the case book kept changing while the ruling was sealed", evidencebook.ErrBusy)
}

// sameRead reports whether two reads name the same records.
func sameRead(a, b RulingRead) bool {
	eq := func(x, y Null[string]) bool {
		return (x.V == nil) == (y.V == nil) && (x.V == nil || *x.V == *y.V)
	}
	return eq(a.Buyer, b.Buyer) && eq(a.Seller, b.Seller)
}

// sealedAt is the SealedRuling of a case book record.
func sealedAt(ctx context.Context, book *evidencebook.Book, r evidencebook.Record, rec Record, seq uint64, bookID string, existing bool) (SealedRuling, error) {
	payload, digest, err := Encode(rec)
	if err != nil {
		return SealedRuling{}, err
	}
	sealed, err := book.Sealed(ctx, r.RecordID)
	if err != nil {
		return SealedRuling{}, err
	}
	pair, err := BookPair(sealed, payload)
	if err != nil {
		return SealedRuling{}, err
	}
	return SealedRuling{Seq: seq, RecordID: r.RecordID, Digest: digest, Record: rec, Pair: pair, BookID: bookID, Existing: existing}, nil
}

// bodyBytes is a ruling body's canonical bytes: two rulings are the same
// when these are.
func bodyBytes(body RulingBody) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return canonical.JCS(doc)
}
