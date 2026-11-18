// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
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

// NameRulings seals, in the holder's deal book of each deal given, a
// statement naming each counted ruling of that deal's adjudicator the book
// does not name yet (PROFILE §5, "statement": a receiving library MUST;
// evidencebook#527). A host calls it after each Receive with the deals
// whose counted records grew (ReceiveResult.Changes.Grew), so a holder's own
// status reads a ruling whichever way it went. It returns the record ids it
// sealed, in order. A deal the holder has no v1 deal book for, or whose
// agreement names no adjudicator, is passed over. It takes the shelf's
// locks, so never call it from a StatusProvider or ReceiveProfile.
func NameRulings(ctx context.Context, shelf *evidencebook.Shelf, dealIDs []string, at time.Time) ([]string, error) {
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
			return nil, err
		}
		recs, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: book.Size()})
		if err != nil {
			return nil, err
		}
		var own []StatusRecord
		for _, r := range recs {
			rec, kind, err := v1Payload(ctx, book, r)
			if err != nil {
				return nil, err
			}
			if kind != payloadV1 {
				break
			}
			own = append(own, StatusRecord{Record: rec, RecordID: r.RecordID})
		}
		if len(own) == 0 || !want[own[0].Record.Block.DealID] {
			continue
		}
		ag, ok := asAgree(own[0].Record.Body)
		if !ok || ag.Kind != KindAgreement || ag.Adjudicator.V == nil {
			continue
		}
		ids, err := nameRulingsIn(ctx, shelf, book, own, ag, at)
		if err != nil {
			return sealed, err
		}
		sealed = append(sealed, ids...)
	}
	return sealed, nil
}

// nameRulingsIn seals the statements one deal book needs.
func nameRulingsIn(ctx context.Context, shelf *evidencebook.Shelf, book *evidencebook.Book, own []StatusRecord, ag AgreeBody, at time.Time) ([]string, error) {
	deal := own[0].Record.Block.DealID
	held, err := shelf.HeldByDeal(ctx, deal)
	if err != nil {
		return nil, err
	}
	named := map[string]bool{}
	for _, r := range own {
		if s, ok := bodyOf[StatementBody](r.Record.Body); ok && s.Names != nil && s.Names.Ruling != nil {
			named[s.Names.Ruling.Digest] = true
		}
	}
	var rulings []string
	adj := ag.Adjudicator.V.KeyID
	for _, logID := range logsOf(held, adj, func(r Record) bool { return r.Block.RecordType == TypeRuling }) {
		recs, err := counted(ctx, shelf, held, adj, logID)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if r.rec.Record.Block.DealID != deal {
				continue
			}
			d, err := Digest(r.rec.Record)
			if err != nil {
				return nil, err
			}
			if !named[d] {
				rulings = append(rulings, d)
				named[d] = true
			}
		}
	}
	if len(rulings) == 0 {
		return nil, nil
	}
	// seen: the last record of the counterparty's counted prefix held.
	seen := NullOf[string]()
	if ag.CounterpartyKey.V != nil {
		if logs := logsOf(held, *ag.CounterpartyKey.V, func(r Record) bool {
			a, ok := asAgree(r.Body)
			return ok && a.Kind == KindAgreement
		}); len(logs) > 0 {
			theirs, err := counted(ctx, shelf, held, *ag.CounterpartyKey.V, logs[0])
			if err != nil {
				return nil, err
			}
			if len(theirs) > 0 {
				seen = Some(theirs[len(theirs)-1].rec.RecordID)
			}
		}
	}
	agDigest, err := Digest(own[0].Record)
	if err != nil {
		return nil, err
	}
	agRef := RefTo(agDigest)
	last := own[len(own)-1]
	var out []string
	for _, d := range rulings {
		lastDigest, err := Digest(last.Record)
		if err != nil {
			return out, err
		}
		prev := RefTo(lastDigest)
		b := Block{Profile: Profile, Canonicalization: Canonicalization, RecordType: TypeStatement, DealID: deal,
			At: at.UTC().Truncate(time.Second).Format(time.RFC3339), Seq: last.Record.Block.Seq + 1,
			Prev: &prev, AgreementRef: &agRef, Seen: seen}
		r := Record{Block: b, Body: &StatementBody{Statements: []StatementItem{}, Names: &StatementNames{Ruling: &Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: d}}}}
		if err := Check(r); err != nil {
			return out, err
		}
		e, err := BookEntry(r, last.RecordID)
		if err != nil {
			return out, err
		}
		sealed, err := book.Append(ctx, e)
		if err != nil {
			return out, err
		}
		out = append(out, sealed.RecordID)
		last = StatusRecord{Record: r, RecordID: sealed.RecordID}
	}
	return out, nil
}
