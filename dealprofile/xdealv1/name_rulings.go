// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/action-state-group/evidencebook"
)

// BookEntry is the entry that seals r in its deal book (design §3.1): its
// JCS bytes as the one payload, whose digest is the record digest; its
// record type; the deal id as the subject; and, past seq 1, a cites link to
// prevRecordID, the record before it, which VerifyBook requires.
func BookEntry(r Record, prevRecordID string) (evidencebook.Entry, error) {
	payload, _, err := Encode(r)
	if err != nil {
		return evidencebook.Entry{}, err
	}
	e := evidencebook.Entry{RecordType: r.Block.RecordType, EpistemicType: evidencebook.ProducerClaim,
		Payloads: [][]byte{payload}, SubjectRef: r.Block.DealID}
	if r.Block.Seq > 1 {
		if prevRecordID == "" {
			return evidencebook.Entry{}, fmt.Errorf("xdealv1: a deal-book record past seq 1 cites the record before it")
		}
		e.Links = []evidencebook.Link{{Type: evidencebook.Cites, Target: prevRecordID}}
	}
	return e, nil
}

var _ evidencebook.AfterReceiveProfile = (*ReceiveProfile)(nil)

// AfterReceive names, before Receive returns, each counted ruling of the
// deals whose counted records grew (NameRulings): the receive call writes
// the naming statement itself, so a holder ruled against cannot skip it
// (PROFILE §5; evidencebook#527).
func (p *ReceiveProfile) AfterReceive(ctx context.Context, changes evidencebook.ReceiveChanges, at time.Time) error {
	_, err := NameRulings(ctx, p.Shelf, changes.Grew, at)
	return err
}

// NameRulings seals, in the holder's deal book of each deal given, a
// statement naming each counted ruling of that deal's adjudicator the book
// does not name yet (PROFILE §5, "statement": a receiving library MUST;
// evidencebook#527). The ReceiveProfile calls it from every Receive
// (AfterReceive) with the deals whose counted records grew, so a holder's own
// status reads a ruling whichever way it went. It returns the record ids it
// sealed, in order. A deal the holder has no v1 deal book for, or whose
// agreement names no adjudicator, is passed over. It takes the shelf's
// locks, so never call it from a StatusProvider or ReceiveProfile.
func NameRulings(ctx context.Context, shelf *evidencebook.Shelf, dealIDs []string, at time.Time) ([]string, error) {
	if len(dealIDs) == 0 {
		return nil, nil
	}
	logs, err := shelf.Books(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, d := range dealIDs {
		want[d] = true
	}
	var sealed []string
	for _, logID := range logs {
		book, err := shelf.Open(ctx, logID)
		if err != nil {
			return sealed, err
		}
		// The first record decides whether the book is one of these deals'.
		first, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: 1})
		if err != nil || len(first) != 1 {
			if err != nil {
				return sealed, err
			}
			continue
		}
		rec, kind, err := v1Payload(ctx, book, first[0])
		if err != nil {
			return sealed, err
		}
		ag, ok := asAgree(rec.Body)
		if kind != payloadV1 || !ok || ag.Kind != KindAgreement || !want[rec.Block.DealID] || ag.Adjudicator.V == nil {
			continue
		}
		ids, err := nameRulingsIn(ctx, shelf, book, ag, rec.Block.DealID, at)
		sealed = append(sealed, ids...)
		if err != nil {
			return sealed, err
		}
	}
	return sealed, nil
}

// nameRulingsBeforeAppend, when set (tests), runs after a naming statement is
// built and before it is appended.
var nameRulingsBeforeAppend func()

// nameRulingsIn seals the statements one deal book needs, each from a fresh
// read of the book and of the counterparty's counted prefix, appended at the
// place it was built for (Book.AppendAt): a write in between makes it read
// again, up to three times. A book with a record that is not readable now as
// x-deal-v1 (a payload withheld, a lifecycle record) is left for a later
// call, since its tail and its names cannot be read whole.
func nameRulingsIn(ctx context.Context, shelf *evidencebook.Shelf, book *evidencebook.Book, ag AgreeBody, deal string, at time.Time) ([]string, error) {
	var out []string
	for attempt := 0; ; attempt++ {
		own, whole, err := readOwnBook(ctx, book)
		if err != nil || !whole {
			return out, err
		}
		held, err := shelf.HeldByDeal(ctx, deal)
		if err != nil {
			return out, err
		}
		ruling, err := firstUnnamed(ctx, shelf, held, ag.Adjudicator.V.KeyID, deal, own)
		if err != nil || ruling == "" {
			return out, err
		}
		r, prev, err := namingStatement(ctx, shelf, held, ag, deal, own, ruling, at)
		if err != nil {
			return out, err
		}
		e, err := BookEntry(r, prev)
		if err != nil {
			return out, err
		}
		if nameRulingsBeforeAppend != nil {
			nameRulingsBeforeAppend()
		}
		sealed, err := book.AppendAt(ctx, uint64(r.Block.Seq), e)
		if sealed.RecordID != "" {
			out = append(out, sealed.RecordID)
		}
		if errors.Is(err, evidencebook.ErrBusy) && attempt < 3 {
			continue // the book moved: read it again
		}
		if err != nil {
			return out, err
		}
		attempt = -1
	}
}

// readOwnBook is the holder's book as status records, and whether every
// record read as x-deal-v1.
func readOwnBook(ctx context.Context, book *evidencebook.Book) ([]StatusRecord, bool, error) {
	recs, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: book.Size()})
	if err != nil {
		return nil, false, err
	}
	var own []StatusRecord
	for _, r := range recs {
		rec, kind, err := v1Payload(ctx, book, r)
		if err != nil {
			return nil, false, err
		}
		if kind != payloadV1 {
			return nil, false, nil
		}
		own = append(own, StatusRecord{Record: rec, RecordID: r.RecordID})
	}
	return own, len(own) > 0, nil
}

// firstUnnamed is the record digest of the first counted ruling of the
// deal by the adjudicator, over every case book, that own does not name;
// "" for none.
func firstUnnamed(ctx context.Context, shelf *evidencebook.Shelf, held []evidencebook.HeldRecord, adjudicator, deal string, own []StatusRecord) (string, error) {
	named := map[string]bool{}
	for _, r := range own {
		if s, ok := bodyOf[StatementBody](r.Record.Body); ok && s.Names != nil && s.Names.Ruling != nil {
			named[s.Names.Ruling.Digest] = true
		}
	}
	for _, logID := range logsOf(held, adjudicator, func(r Record) bool { return r.Block.RecordType == TypeRuling }) {
		recs, err := counted(ctx, shelf, held, adjudicator, logID)
		if err != nil {
			return "", err
		}
		for _, r := range recs {
			if r.rec.Record.Block.DealID != deal {
				continue
			}
			d, err := Digest(r.rec.Record)
			if err != nil {
				return "", err
			}
			if !named[d] {
				return d, nil
			}
		}
	}
	return "", nil
}

// namingStatement is the statement naming ruling as own's next record, and
// the record id it cites: seq after the book's last, prev that record's
// digest, agreement_ref the agreement's, and seen the last record of the
// counterparty's counted prefix held now (PROFILE §6).
func namingStatement(ctx context.Context, shelf *evidencebook.Shelf, held []evidencebook.HeldRecord, ag AgreeBody, deal string, own []StatusRecord, ruling string, at time.Time) (Record, string, error) {
	seen := NullOf[string]()
	if ag.CounterpartyKey.V != nil {
		if logs := logsOf(held, *ag.CounterpartyKey.V, func(r Record) bool {
			a, ok := asAgree(r.Body)
			return ok && a.Kind == KindAgreement
		}); len(logs) > 0 {
			theirs, err := counted(ctx, shelf, held, *ag.CounterpartyKey.V, logs[0])
			if err != nil {
				return Record{}, "", err
			}
			if len(theirs) > 0 {
				seen = Some(theirs[len(theirs)-1].rec.RecordID)
			}
		}
	}
	agDigest, err := Digest(own[0].Record)
	if err != nil {
		return Record{}, "", err
	}
	last := own[len(own)-1]
	lastDigest, err := Digest(last.Record)
	if err != nil {
		return Record{}, "", err
	}
	prev, agRef := RefTo(lastDigest), RefTo(agDigest)
	b := Block{Profile: Profile, Canonicalization: Canonicalization, RecordType: TypeStatement, DealID: deal,
		At: at.UTC().Truncate(time.Second).Format(time.RFC3339), Seq: int64(len(own)) + 1,
		Prev: &prev, AgreementRef: &agRef, Seen: seen}
	r := Record{Block: b, Body: &StatementBody{Statements: []StatementItem{}, Names: &StatementNames{Ruling: &Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: ruling}}}}
	if err := Check(r); err != nil {
		return Record{}, "", err
	}
	return r, last.RecordID, nil
}
