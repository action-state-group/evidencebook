// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/action-state-group/evidencebook"
)

// StatusProvider gives a v1 deal book's profile, link value and status value
// at each stamp (design §8.1, §8.2). It is a BookStatusProvider: the shelf
// hands it the book and the journal, and it reads the received books through
// the shelf's lock-free readers only, so it never takes the shelf's lock a
// stamp holds.
type StatusProvider struct {
	// Shelf is the shelf the provider serves.
	Shelf *evidencebook.Shelf
	// OfferPair returns the sent pair of the offer whose record digest is
	// given, as the host sent or received it: the library keeps no sent
	// Capsule (SealSent hands it to the host). Nil, or a nil pair, leaves a
	// payment row's check not run.
	OfferPair func(ctx context.Context, offerDigest string) ([]byte, error)
}

var _ evidencebook.BookStatusProvider = (*StatusProvider)(nil)

// Values claims no book: a shelf calls BookValues on a BookStatusProvider.
func (p *StatusProvider) Values(context.Context, string, string) (string, evidencebook.LinkValue, json.RawMessage, bool, error) {
	return "", evidencebook.LinkValue{}, nil, false, nil
}

// BookValues gives a v1 deal book's values, ok false for any other book (a
// journal-less shelf, a book not opened by a v1 agreement).
func (p *StatusProvider) BookValues(ctx context.Context, in evidencebook.StatusInput) (string, evidencebook.LinkValue, json.RawMessage, bool, error) {
	none := func(err error) (string, evidencebook.LinkValue, json.RawMessage, bool, error) {
		return "", evidencebook.LinkValue{}, nil, false, err
	}
	if p.Shelf == nil {
		return none(fmt.Errorf("xdealv1: the status provider has no shelf"))
	}
	own, err := p.ownBook(ctx, in)
	if err != nil || len(own) == 0 {
		return none(err)
	}
	ag, _ := asAgree(own[0].rec.Record.Body)
	deal := own[0].rec.Record.Block.DealID
	offer, err := p.offer(ctx, in.Journal, ag.Offer.Digest)
	if err != nil {
		return none(err)
	}
	var keys []string
	for _, w := range ag.Witnesses {
		keys = append(keys, w.KeyID)
	}
	for i := range own {
		if own[i].rec.Time, err = p.ownTime(ctx, in, uint64(i+1), keys); err != nil {
			return none(err)
		}
	}
	held, err := p.Shelf.HeldByDeal(ctx, deal)
	if err != nil {
		return none(err)
	}
	b := StatusBooks{Offer: offer}
	for _, o := range own {
		b.Own = append(b.Own, o.rec)
	}
	link := evidencebook.LinkValue{CheckedOver: []string{}}
	var theirs []heldBookRecord
	var theirLog string
	if ag.CounterpartyKey.V != nil {
		theirs, theirLog, err = counted(ctx, p.Shelf, held, *ag.CounterpartyKey.V, func(r Record) bool {
			a, ok := asAgree(r.Body)
			return ok && a.Kind == KindAgreement
		})
		if err != nil {
			return none(err)
		}
		for i := range theirs {
			if theirs[i].rec.Time, err = p.receivedTime(ctx, in, theirLog, uint64(i+1), keys); err != nil {
				return none(err)
			}
			b.Counterpart = append(b.Counterpart, theirs[i].rec)
		}
		if len(theirs) > 0 {
			last := theirs[len(theirs)-1]
			link.LastReceived = &evidencebook.ReceivedPosition{LogID: theirLog, Size: uint64(len(theirs)), RecordDigest: last.rec.RecordID}
			theirAg, _ := asAgree(theirs[0].rec.Record.Body)
			if b.ProofHeld, err = proofCovers(ctx, p.Shelf, *ag.CounterpartyKey.V, theirLog, theirs[0].rec.RecordID, theirAg); err != nil {
				return none(err)
			}
		}
	}
	if a := ag.Adjudicator.V; a != nil {
		if b.CaseBook, b.ConflictingFinalRulings, err = caseBook(ctx, p.Shelf, held, a.KeyID, b.Own); err != nil {
			return none(err)
		}
	}
	if b.Payments, err = p.payments(ctx, offer, ag, own, theirs); err != nil {
		return none(err)
	}
	for _, r := range b.Payments {
		link.CheckedOver = append(link.CheckedOver, r.CheckedOver...)
	}
	slices.Sort(link.CheckedOver)
	link.CheckedOver = slices.Compact(link.CheckedOver)
	v, err := BuildStatus(b)
	if err != nil {
		return none(err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return none(err)
	}
	return Profile, link, raw, true, nil
}

// ownRecord is a record of the holder's book with its sealed pair.
type ownRecord struct {
	rec  StatusRecord
	pair []byte
}

// ownBook is the holder's book read through its handle, nil when the book is
// not opened by a v1 agreement. A record whose payload is not readable now
// (withheld under retention) ends the book read here, so no later record is
// read past it.
func (p *StatusProvider) ownBook(ctx context.Context, in evidencebook.StatusInput) ([]ownRecord, error) {
	if in.Book == nil || in.Journal == nil {
		return nil, nil
	}
	recs, err := in.Book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: in.Book.Size()})
	if err != nil {
		return nil, err
	}
	var out []ownRecord
	for i, r := range recs {
		rec, kind, err := v1Payload(ctx, in.Book, r)
		if err != nil {
			return nil, err
		}
		if kind != payloadV1 {
			if i == 0 {
				return nil, nil
			}
			break
		}
		if i == 0 {
			if a, ok := asAgree(rec.Body); !ok || a.Kind != KindAgreement {
				return nil, nil
			}
		}
		sealed, err := in.Book.Sealed(ctx, r.RecordID)
		if err != nil {
			return nil, err
		}
		payload, _, err := Encode(rec)
		if err != nil {
			return nil, err
		}
		pair, err := BookPair(sealed, payload)
		if err != nil {
			return nil, err
		}
		out = append(out, ownRecord{rec: StatusRecord{Record: rec, RecordID: r.RecordID}, pair: pair})
	}
	return out, nil
}

// offer is the offer whose record digest is digest: the holder's own, in its
// journal, or the one it received sent whole.
func (p *StatusProvider) offer(ctx context.Context, journal *evidencebook.Book, digest string) (Record, error) {
	recs, err := journal.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: journal.Size()})
	if err != nil {
		return Record{}, err
	}
	for _, r := range recs {
		rec, kind, err := v1Payload(ctx, journal, r)
		if err != nil {
			return Record{}, err
		}
		if _, ok := asPropose(rec.Body); kind == payloadV1 && ok {
			if d, err := Digest(rec); err == nil && d == digest {
				return rec, nil
			}
		}
	}
	held, err := p.Shelf.HeldFirst(ctx, SentLogPrefix+digest)
	if err != nil {
		return Record{}, err
	}
	for _, h := range held {
		if s, err := VerifySent(h.Record, nil); err == nil && s.Digest == digest {
			return s.Record, nil
		}
	}
	return Record{}, fmt.Errorf("xdealv1: status: the deal's offer %s is not held", digest)
}

// ownTime is the time of the holder's record at seq: its record time from a
// witness the agreement names, else the first stamp covering it, else this
// stamp, which is the first to (design §8.1, "Times").
func (p *StatusProvider) ownTime(ctx context.Context, in evidencebook.StatusInput, seq uint64, keys []string) (StatusTime, error) {
	if t, ok, err := p.Shelf.RecordTimeByKey(ctx, in.BookID, seq, keys); err != nil || ok {
		return KnownTime(t.UTC().Format(time.RFC3339)), err
	}
	n, ok, err := p.Shelf.RecordStamp(ctx, in.BookID, seq)
	if err != nil || ok {
		return StampTime(n), err
	}
	return StampTime(in.N), nil
}

// receivedTime is the time the holder's stamps give the counterparty's
// record at pos of logID: from a named witness, else the first stamp whose
// link value reached it, else this stamp, whose link value reaches the
// counted prefix (design §8.1; a time from the counterparty's own stamps
// waits for Q8).
func (p *StatusProvider) receivedTime(ctx context.Context, in evidencebook.StatusInput, logID string, pos uint64, keys []string) (StatusTime, error) {
	if t, ok, err := p.Shelf.ReceivedRecordTimeByKey(ctx, in.BookID, logID, pos, keys); err != nil || ok {
		return KnownTime(t.UTC().Format(time.RFC3339)), err
	}
	n, ok, err := p.Shelf.ReceivedRecordStamp(ctx, in.BookID, logID, pos)
	if err != nil || ok {
		return StampTime(n), err
	}
	return StampTime(in.N), nil
}

// heldBookRecord is a received book record, verified, with its pair.
type heldBookRecord struct {
	rec  StatusRecord
	pair []byte
}

// counted is sender's counted prefix of its deal book for the deal: the log
// whose first held record from sender is opened (first) as the book's first
// record must be, read back from the prefix's last record through prev.
func counted(ctx context.Context, shelf *evidencebook.Shelf, held []evidencebook.HeldRecord, sender string, first func(Record) bool) ([]heldBookRecord, string, error) {
	byDigest := map[string]evidencebook.HeldRecord{}
	logID := ""
	for _, h := range held {
		if h.SenderKey != sender || strings.HasPrefix(h.LogID, SentLogPrefix) {
			continue
		}
		byDigest[h.Digest] = h
		if h.Position == 1 && logID == "" {
			if b, err := VerifyBook(h.Record); err == nil && first(b.Record) {
				logID = h.LogID
			}
		}
	}
	if logID == "" {
		return nil, "", nil
	}
	prefix, err := shelf.CountedPrefix(ctx, sender, logID)
	if err != nil {
		return nil, "", err
	}
	out := make([]heldBookRecord, prefix.Size)
	d := prefix.Last
	for i := int(prefix.Size) - 1; i >= 0; i-- {
		h, ok := byDigest[d]
		if !ok || h.LogID != logID {
			return nil, "", fmt.Errorf("xdealv1: status: the counted record %s is not held", d)
		}
		b, err := VerifyBook(h.Record)
		if err != nil {
			return nil, "", err
		}
		out[i] = heldBookRecord{rec: StatusRecord{Record: b.Record, RecordID: h.Digest}, pair: h.Record}
		d = h.Prev
	}
	return out, logID, nil
}

// caseBook is the adjudicator's rulings of the deal the holder has named
// arriving (a statement of its own book whose names.ruling cites one), each
// timed by that naming record, in case book order; and the rows whose case
// book forked with different final rulings. A ruling the holder has not
// named yet is not an input: its arrival has no time on the holder's shelf.
func caseBook(ctx context.Context, shelf *evidencebook.Shelf, held []evidencebook.HeldRecord, adjudicator string, own []StatusRecord) ([]StatusRecord, []string, error) {
	namedAt := map[string]StatusTime{}
	for _, r := range own {
		if s, ok := bodyOf[StatementBody](r.Record.Body); ok && s.Names != nil && s.Names.Ruling != nil {
			if _, seen := namedAt[s.Names.Ruling.Digest]; !seen {
				namedAt[s.Names.Ruling.Digest] = r.Time
			}
		}
	}
	recs, logID, err := counted(ctx, shelf, held, adjudicator, func(r Record) bool { return r.Block.RecordType == TypeRuling })
	if err != nil || logID == "" {
		return nil, nil, err
	}
	var out []StatusRecord
	for _, r := range recs {
		d, err := Digest(r.rec.Record)
		if err != nil {
			return nil, nil, err
		}
		if t, ok := namedAt[d]; ok {
			r.rec.Time = t
			out = append(out, r.rec)
		}
	}
	prefix, err := shelf.CountedPrefix(ctx, adjudicator, logID)
	if err != nil {
		return nil, nil, err
	}
	var conflicting []string
	if prefix.ForkAt > 0 {
		finals := map[string]map[string]bool{}
		for _, h := range held {
			if h.SenderKey != adjudicator || h.LogID != logID {
				continue
			}
			b, err := VerifyBook(h.Record)
			if err != nil {
				continue
			}
			if rb, ok := bodyOf[RulingBody](b.Record.Body); ok && rb.Final {
				if finals[rb.Row] == nil {
					finals[rb.Row] = map[string]bool{}
				}
				finals[rb.Row][rb.Value+"/"+fmt.Sprint(rb.Against.V != nil && *rb.Against.V != "")] = true
			}
		}
		for row, vs := range finals {
			if len(vs) > 1 {
				conflicting = append(conflicting, row)
			}
		}
		slices.Sort(conflicting)
	}
	return out, conflicting, nil
}

// payments runs each payment row's check over both books, when the host
// gives the offer's sent pair (design §6.3).
func (p *StatusProvider) payments(ctx context.Context, offer Record, ag AgreeBody, own []ownRecord, theirs []heldBookRecord) (map[string]PaymentResult, error) {
	of, _ := asPropose(offer.Body)
	rows := PaymentRows(of.TermsRows)
	if p.OfferPair == nil || len(rows) == 0 {
		return nil, nil
	}
	od, err := Digest(offer)
	if err != nil {
		return nil, err
	}
	pair, err := p.OfferPair(ctx, od)
	if err != nil || pair == nil {
		return nil, err
	}
	acceptor := ""
	if ag.Side == SideOfferer {
		if ag.CounterpartyKey.V != nil {
			acceptor = *ag.CounterpartyKey.V
		}
	} else if b, err := VerifyBook(own[0].pair); err == nil {
		acceptor = b.KeyID
	}
	var legs [][]byte
	for _, o := range own {
		legs = append(legs, o.pair)
	}
	for _, t := range theirs {
		legs = append(legs, t.pair)
	}
	out := map[string]PaymentResult{}
	for _, row := range rows {
		res, err := PaymentCheck(PaymentInput{Offer: pair, OfferDigest: od, AcceptorKey: acceptor, Row: row, Legs: legs})
		var se *StageError
		if errors.As(err, &se) && acceptor == "" {
			continue // no acceptor key known yet: not run
		}
		if err != nil {
			return nil, err
		}
		out[row] = res
	}
	return out, nil
}
