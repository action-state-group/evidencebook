// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// The full adjudicator copy (evidencebook#861, docs/design/861-four-books.md
// §2.2): a party's answer to an adjudicator's request for the deal carries
// its whole deal book and the copy of the other side's book it holds, so a
// silent side is still read from the copy the other side received. It is an
// adjudicator's copy (rows withheld) whose bundle extension's book also
// carries records and received; the book's facts (BookEvidence) travel beside
// the bundle.

// DerivationAdjudicatorCopy is the derivation token an adjudicator's
// evidence request names to get a party's full adjudicator copy of a deal
// (its subject: correlation, the deal id). A profile token in the request
// draft's name/N form, as AAC's own history_card/2 and standard_history/1.
// The request has no selective form: the responder answers with the full
// copy or refuses.
const DerivationAdjudicatorCopy = "deal_adjudicator_copy/1"

// ReportReceived is the copy of the other side's deal book the holder holds,
// in a full adjudicator copy: the log id, every record held from that log
// for the deal that chains from seq 1 (by record id, as book pairs; both
// branches of a fork the holder holds), and the stamp proofs held from the
// other side for that log whose book checkpoints the records recompute.
// When the holder holds none of the other side's book, LogID is "" and both
// lists are empty, so "not held" stays apart from "not shown". It carries no
// witness receipts: the store keeps none for a received proof (design note
// 461 §12, Q8), so a received record is timed by the holder's own facts.
type ReportReceived struct {
	LogID   string                     `json:"log_id"`
	Records map[string]json.RawMessage `json:"records"`
	Proofs  []evidencebook.StampProof  `json:"proofs"`
}

// FullCopy is what a verified full adjudicator copy shows: the holder's
// role, key, agreement, its whole deal book in seq order,
// and the held copy of the other side's book: its log id ("" for none), its
// records by record id (a fork's branches both), and its stamp proofs,
// verified.
type FullCopy struct {
	Role      string
	Key       string
	Agreement AgreeBody
	Own       []BookRead
	LogID     string
	Received  map[string]BookRead
	Proofs    []evidencebook.VerifiedStampProof
}

// verifyFullCopy checks a full adjudicator copy's records and received
// (VerifyReportBundle), given the verified report r and its agreement first
// (ag). Each record is a verified book pair under its record id, of the
// report's deal:
//
//   - records are signed by the agreement's key, in the log the report rests
//     on, at seqs 1 to rests_on.own.size, each once; seq 1 is the agreement,
//     the last is rests_on.own.last, and each next one cites the one before
//     (its header's prev and its block's prev);
//   - received is carried; when it holds records, they are signed by the
//     agreement's counterparty_key, in its one log, each at seq 1 or citing a
//     record of received at the seq before it; a seq-1 record is an
//     agreement of the same offer, of the other role, naming the holder's
//     key as its layered counterparty;
//   - every own record's seen is a record of received;
//   - relied_on is inside them: the holder's relied-on records are records,
//     the counterparty's are received, and the adjudicator's are neither;
//   - each held proof verifies, is signed by the other side's checkpoint key
//     in its shelf log, proves a fact of a received seq-1 record's book in
//     received's log, and its book checkpoint is recomputed by the received
//     records along the chain ending at seq entries.
func verifyFullCopy(x ReportBundle, r Report, first BookRead, ag AgreeBody) (FullCopy, error) {
	bad := func(format string, args ...any) (FullCopy, error) {
		return FullCopy{}, stageErr(StageReport, "the x-deal-v1 extension: "+format, args...)
	}
	if r.Audience != dealprofile.AudienceAdjudicator {
		return bad("only an adjudicator's copy carries the whole book")
	}
	rc := x.Book.Received
	if rc == nil || rc.Records == nil || rc.Proofs == nil {
		return bad("a full copy carries received, with records and proofs")
	}
	out := FullCopy{Role: ag.Role, Key: first.KeyID, Agreement: ag, Received: map[string]BookRead{}}
	own, err := chain(x.Book.Records, r.DealID)
	if err != nil {
		return FullCopy{}, err
	}
	switch {
	case uint64(len(own)) != r.RestsOn.Own.Size:
		return bad("records holds %d records, the report rests on %d", len(own), r.RestsOn.Own.Size)
	case own[0].RecordID != first.RecordID:
		return bad("records' seq 1 is not the agreement")
	case own[len(own)-1].RecordID != r.RestsOn.Own.Last:
		return bad("records' last record is not rests_on.own.last")
	}
	for _, b := range own {
		if b.KeyID != first.KeyID || b.LogID != r.RestsOn.Own.LogID {
			return bad("records' %s is not in the holder's book the report rests on", b.RecordID)
		}
	}
	out.Own = own
	other := RoleBuyer
	if ag.Role == RoleBuyer {
		other = RoleSeller
	}
	switch {
	case len(rc.Records) == 0 && (rc.LogID != "" || len(rc.Proofs) > 0):
		return bad("received holds no record, so it names no log and no proof")
	case len(rc.Records) > 0 && ag.CounterpartyKey.V == nil:
		return bad("received holds records, and the agreement names no counterparty key")
	}
	for id, pair := range rc.Records {
		b, err := VerifyBook(pair)
		if err != nil {
			return FullCopy{}, err
		}
		switch {
		case b.RecordID != id:
			return bad("received %s holds the record %s", id, b.RecordID)
		case b.Record.Block.DealID != r.DealID:
			return bad("received %s is of deal %q, not the report's", id, b.Record.Block.DealID)
		case b.KeyID != *ag.CounterpartyKey.V || b.LogID != rc.LogID:
			return bad("received %s is not the counterparty's record in log %s", id, rc.LogID)
		}
		out.Received[id] = b
	}
	for _, id := range slices.Sorted(maps.Keys(out.Received)) {
		b := out.Received[id]
		if b.Seq == 1 {
			theirAg, ok := asAgree(b.Record.Body)
			if !ok || theirAg.Kind != KindAgreement || theirAg.Role != other || theirAg.Offer.Digest != ag.Offer.Digest || !namesCounterparty(theirAg, first.KeyID) {
				return bad("received %s at seq 1 is not the %s's agreement of this offer naming the holder", id, other)
			}
			continue
		}
		p, ok := out.Received[b.Prev]
		if !ok || p.Seq != b.Seq-1 || b.Record.Block.Prev == nil || b.Record.Block.Prev.Digest != p.Digest {
			return bad("received %s at seq %d does not cite a received record before it", id, b.Seq)
		}
	}
	for i, p := range rc.Proofs {
		v, err := evidencebook.VerifyStampProof(p)
		if err != nil {
			return bad("received proof %d: %v", i+1, err)
		}
		genesis, ok := out.Received[v.Fact.BookID]
		if !ok || genesis.Seq != 1 || v.Fact.LogID != rc.LogID {
			return bad("received proof %d is not of the received book", i+1)
		}
		theirAg, _ := asAgree(genesis.Record.Body)
		if v.KeyID != theirAg.CheckpointKey || v.ShelfCheckpoint.LogID != theirAg.ShelfLogID {
			return bad("received proof %d is not under the counterparty's checkpoint key and shelf log", i+1)
		}
		if !recomputedBySome(v, out.Received) {
			return bad("received proof %d is not recomputed by the received records", i+1)
		}
		out.Proofs = append(out.Proofs, v)
	}
	out.LogID = rc.LogID
	for _, b := range own {
		if s := b.Record.Block.Seen; s.V != nil {
			if _, ok := out.Received[*s.V]; !ok {
				return bad("records' %s has seen %s, which received does not carry", b.RecordID, *s.V)
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(x.Book.ReliedOn)) {
		if _, isOwn := x.Book.Records[id]; isOwn {
			continue
		}
		if _, held := out.Received[id]; held {
			continue
		}
		b, err := VerifyBook(x.Book.ReliedOn[id])
		if err != nil {
			return FullCopy{}, err
		}
		if ag.Adjudicator.V == nil || b.KeyID != ag.Adjudicator.V.KeyID {
			return bad("relied_on %s is in neither records nor received", id)
		}
	}
	return out, nil
}

// branchTo is the chain of held records from seq 1 to the record id, in seq
// order, nil when id is not held or does not chain to seq 1.
func branchTo(held map[string]BookRead, id string) []BookRead {
	b, ok := held[id]
	if !ok {
		return nil
	}
	out := make([]BookRead, b.Seq)
	for i := b.Seq; i >= 1; i-- {
		if !ok || b.Seq != i {
			return nil
		}
		out[i-1] = b
		b, ok = held[b.Prev]
	}
	return out
}

// recordIDs are the record ids of a chain.
func recordIDs(chain []BookRead) []string {
	out := make([]string, len(chain))
	for i, b := range chain {
		out[i] = b.RecordID
	}
	return out
}

// recomputedBySome reports whether the held records along some chain from
// seq 1 to seq entries recompute the proof's book checkpoint.
func recomputedBySome(v evidencebook.VerifiedStampProof, held map[string]BookRead) bool {
	for _, id := range slices.Sorted(maps.Keys(held)) {
		if held[id].Seq != v.BookCheckpoint.Entries {
			continue
		}
		if ok, err := evidencebook.StampProofRecomputes(v, recordIDs(branchTo(held, id))); err == nil && ok {
			return true
		}
	}
	return false
}

// chain reads a map of book pairs by record id as one book's records from
// seq 1, each verified and of deal, each citing the one before it.
func chain(pairs map[string]json.RawMessage, deal string) ([]BookRead, error) {
	bad := func(format string, args ...any) ([]BookRead, error) {
		return nil, stageErr(StageReport, "the x-deal-v1 extension: records: "+format, args...)
	}
	if len(pairs) == 0 {
		return bad("no record")
	}
	out := make([]BookRead, len(pairs))
	for id, pair := range pairs {
		b, err := VerifyBook(pair)
		if err != nil {
			return nil, err
		}
		switch {
		case b.RecordID != id:
			return bad("%s holds the record %s", id, b.RecordID)
		case b.Record.Block.DealID != deal:
			return bad("%s is of deal %q, not the report's", id, b.Record.Block.DealID)
		case b.Seq < 1 || b.Seq > uint64(len(pairs)) || out[b.Seq-1].RecordID != "":
			return bad("%s is at seq %d, which is not one of 1 to %d once", id, b.Seq, len(pairs))
		}
		out[b.Seq-1] = b
	}
	for i := 1; i < len(out); i++ {
		p, b := out[i-1], out[i]
		if b.LogID != p.LogID || b.Prev != p.RecordID || b.Record.Block.Prev == nil || b.Record.Block.Prev.Digest != p.Digest {
			return bad("%s at seq %d does not cite %s before it", b.RecordID, b.Seq, p.RecordID)
		}
	}
	return out, nil
}

// fullBook gathers a full copy's records and received from the snapshot: the
// holder's own book pairs, every record held from the counterparty's log for
// the deal that chains from seq 1, and the held proofs those records
// recompute.
func fullBook(rb ReportBooks, pairs map[string][]byte, held []evidencebook.HeldRecord, proofs []evidencebook.HeldProof) (map[string]json.RawMessage, *ReportReceived, error) {
	records := map[string]json.RawMessage{}
	for _, r := range rb.Books.Own {
		p, ok := pairs[r.RecordID]
		if !ok {
			return nil, nil, fmt.Errorf("xdealv1: report: no book pair read for record %s", r.RecordID)
		}
		records[r.RecordID] = p
	}
	rc := &ReportReceived{Records: map[string]json.RawMessage{}, Proofs: []evidencebook.StampProof{}}
	ag, _ := asAgree(rb.Books.Own[0].Record.Body)
	if rb.Counterpart == nil || ag.CounterpartyKey.V == nil {
		return records, rc, nil
	}
	rc.LogID = rb.Counterpart.LogID
	reads := map[string]BookRead{}
	raw := map[string][]byte{}
	for _, h := range held {
		if h.SenderKey != *ag.CounterpartyKey.V || h.LogID != rc.LogID {
			continue
		}
		if b, err := VerifyBook(h.Record); err == nil && b.RecordID == h.Digest {
			reads[b.RecordID], raw[b.RecordID] = b, h.Record
		}
	}
	kept := map[string]BookRead{}
	for id, b := range reads {
		if branchTo(reads, id) != nil {
			rc.Records[id], kept[id] = raw[id], b
		}
	}
	for _, hp := range proofs {
		if recomputedBySome(hp.Verified, kept) {
			rc.Proofs = append(rc.Proofs, hp.Proof)
		}
	}
	return records, rc, nil
}
