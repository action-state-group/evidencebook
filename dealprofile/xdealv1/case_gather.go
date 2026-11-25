// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/evidencebook"
)

// Gathering four books into one case (evidencebook#861,
// docs/design/861-four-books.md §2.3, Steven's rulings in §6): each party's
// full answer carries its own deal book and the copy of the other side's
// book it holds, so a side is read from its own book, from the copy the
// other side received, or both. capsulectl-deal's `deal ruling` and a hosted
// adjudicator both call GatherCase, so the two cannot drift.

// CaseAnswer is one party's full answer to the adjudicator's request: its
// full adjudicator copy (VerifyFullCopy) and the evidence of its deal book's
// facts (evidencebook.BookEvidence, TrimBookEvidence).
type CaseAnswer struct {
	Copy     CaseCopy
	Evidence evidencebook.BookEvidence
}

// What timed a record of a side's book read (CaseRecord.TimedBy).
const (
	// TimedByOwnStamp is a time from the side's own stamps: the earliest
	// named registration of a shelf checkpoint proving a fact of its own
	// book that covers the record.
	TimedByOwnStamp = "own_stamp"
	// TimedByHolderLink is a time from the other side's stamps: the
	// earliest named registration of a shelf checkpoint proving a fact of
	// the holder's book whose link value reached the record. A page says the
	// time came from the other side's held proofs.
	TimedByHolderLink = "holder_link"
)

// CaseRecord is one record of a side's book read, signed by that side, with
// its record time (RFC 3339 UTC; "" when no named witness timed it) and what
// timed it ("" for none).
type CaseRecord struct {
	Read    BookRead
	At      string
	TimedBy string
}

// CaseSide is what the adjudicator read of one side's book: the book read
// (every record signed by the side's key, in seq order; an opening copy's
// records alone are the ones it carries), the last record read (null for
// none; for an opening copy alone, the last record it rests on, as CheckCase
// reads it), the sources it was
// read from (the last record id of the side's own book and of the copy the
// other side held, null for a source not shown), the position of a fork
// between them (0 for none), and the records of a branch not read, kept as
// fork evidence.
type CaseSide struct {
	Role   string
	Key    string
	Book   []CaseRecord
	Read   Null[string]
	Own    Null[string]
	Held   Null[string]
	ForkAt uint64
	Forked []BookRead
}

// GatheredCase is a case read from up to four sources: the Case SealRuling
// takes (its Read the last record of each side's book read, its Sources
// set) and each side's book.
type GatheredCase struct {
	Case
	Buyer, Seller CaseSide
}

// Side is the gathered side of role.
func (g GatheredCase) Side(role string) CaseSide {
	if role == RoleBuyer {
		return g.Buyer
	}
	return g.Seller
}

// answerRead is a verified full answer: the copy, the times its facts give
// and the link values they name, and the branch of the held copy read.
type answerRead struct {
	full   FullCopy
	times  evidencebook.HandoverTimes
	keys   []string
	branch []BookRead
	forked []BookRead
	forkAt uint64
}

// openingRead is a case-opening copy's records of its signer, by seq, and
// the last record it rests on.
type openingRead struct {
	records map[uint64]BookRead
	size    uint64
	last    string
	// forkAt is the lowest seq at which the copy itself carries two of its
	// signer's records (0 for none), and forked those records and any after
	// them, which records then leaves out.
	forkAt uint64
	forked []BookRead
}

// GatherCase reads a dispute, the case-opening adjudicator copies (as
// CheckCase takes them; one per side at most) and up to two full answers
// (one per side) as one case for the adjudicator adjudicatorKey, with the
// reader's witness directory:
//
//   - the dispute and, per side, the opening copy if given (else the
//     answer's copy) pass CheckCase; each answer passes VerifyFullCopy, is
//     of the case's deal and the same agreement as that side's opening copy;
//   - each answer's book evidence passes VerifyBookEvidence under its
//     agreement's checkpoint key and shelf log, is of its deal book, and
//     each fact's book checkpoint is recomputed by the answer's records;
//   - every record the answering party's book names as received (each own
//     record's seen, and each fact's link value's last_received) is a
//     record of its held copy at that position, on one branch: the branch
//     read, which the held records extend while they do not fork. Held
//     records off it are the other side's fork, kept as evidence.
//
// Per side, the sources are its own book (its full answer, else its opening
// copy's records) and the copy the other side held. Where two of them hold
// different records at a position, the side's book forked there: the copy
// the other side held governs (Steven's ruling 3, u211), both are kept, and
// fork_at records the lowest position shown to differ. With no held copy
// and a fork between the side's own opening copy and its full answer, no
// branch is shown as received, so the book is read only up to the position
// before it. Otherwise the longer source is read. Each record read is timed
// by the side's own stamps and by the holder's linking facts, the earlier
// of the two, and says which (CaseRecordTimes).
func GatherCase(dispute []byte, opening []CaseCopy, answers []CaseAnswer, adjudicatorKey string, witnesses aacwitness.Directory) (GatheredCase, error) {
	if len(answers) > 2 {
		return GatheredCase{}, caseErr("a case takes at most two full answers, not %d", len(answers))
	}
	if len(opening)+len(answers) == 0 {
		return GatheredCase{}, caseErr("a case takes an adjudicator copy")
	}
	openBy := map[string]CaseCopy{}
	openRead := map[string]openingRead{}
	for _, c := range opening {
		cc, err := readCaseCopy(c, adjudicatorKey)
		if err != nil {
			return GatheredCase{}, err
		}
		if _, dup := openBy[cc.role]; dup {
			return GatheredCase{}, caseErr("two opening copies of the %s", cc.role)
		}
		openBy[cc.role] = c
		if openRead[cc.role], err = readOpening(c, cc); err != nil {
			return GatheredCase{}, err
		}
	}
	ansBy := map[string]answerRead{}
	ansCopy := map[string]CaseCopy{}
	for _, a := range answers {
		cc, err := readCaseCopy(a.Copy, adjudicatorKey)
		if err != nil {
			return GatheredCase{}, err
		}
		if _, dup := ansBy[cc.role]; dup {
			return GatheredCase{}, caseErr("two full answers of the %s", cc.role)
		}
		_, full, err := VerifyFullCopy(a.Copy.Report, a.Copy.Extension)
		if err != nil {
			return GatheredCase{}, err
		}
		ar, err := readAnswer(full, a.Evidence, witnesses)
		if err != nil {
			return GatheredCase{}, err
		}
		ansBy[cc.role], ansCopy[cc.role] = ar, a.Copy
	}
	var copies []CaseCopy
	for _, role := range []string{RoleBuyer, RoleSeller} {
		if c, ok := openBy[role]; ok {
			copies = append(copies, c)
		} else if c, ok := ansCopy[role]; ok {
			copies = append(copies, c)
		}
	}
	c, err := CheckCase(dispute, copies, adjudicatorKey)
	if err != nil {
		return GatheredCase{}, err
	}
	keyOf := map[string]string{RoleBuyer: c.Keys.Buyer, RoleSeller: c.Keys.Seller}
	for role, a := range ansBy {
		switch {
		case a.full.Key != keyOf[role] || a.full.Own[0].Record.Block.DealID != c.DealID:
			return GatheredCase{}, caseErr("the %s's full answer is not of the case's deal and key", role)
		case openBy[role].Report != nil && a.full.Own[0].RecordID != openRead[role].records[1].RecordID:
			return GatheredCase{}, caseErr("the %s's full answer rests on another agreement than its opening copy", role)
		}
	}
	g := GatheredCase{Case: c}
	sources := RulingSources{}
	for _, role := range []string{RoleBuyer, RoleSeller} {
		other := RoleSeller
		if role == RoleSeller {
			other = RoleBuyer
		}
		own, hasOwn := ansBy[role]
		holder, hasHolder := ansBy[other]
		op, hasOpening := openRead[role]
		side := gatherSide(role, keyOf[role], own, hasOwn, holder, hasHolder, op, hasOpening)
		if role == RoleBuyer {
			g.Buyer = side
			g.Read.Buyer = side.Read
			sources.Buyer = BuyerSources{Own: side.Own, HeldBySeller: side.Held, ForkAt: int64(side.ForkAt)}
		} else {
			g.Seller = side
			g.Read.Seller = side.Read
			sources.Seller = SellerSources{Own: side.Own, HeldByBuyer: side.Held, ForkAt: int64(side.ForkAt)}
		}
	}
	g.Sources = &sources
	return g, nil
}

// readOpening is a case-opening copy's records signed by its holder, by seq,
// with the last record it rests on.
func readOpening(c CaseCopy, cc caseCopy) (openingRead, error) {
	var x ReportBundle
	if err := json.Unmarshal(c.Extension, &x); err != nil {
		return openingRead{}, caseErr("the x-deal-v1 extension: %v", err)
	}
	read, err := VerifyReport(c.Report, x.Book.Agreement)
	if err != nil {
		return openingRead{}, err
	}
	out := openingRead{records: map[uint64]BookRead{}, size: read.Report.RestsOn.Own.Size, last: cc.last}
	first, err := VerifyBook(x.Book.Agreement)
	if err != nil {
		return openingRead{}, err
	}
	out.records[1] = first
	for _, id := range slices.Sorted(maps.Keys(x.Book.ReliedOn)) {
		b, err := VerifyBook(x.Book.ReliedOn[id])
		if err != nil {
			return openingRead{}, err
		}
		if b.KeyID != cc.key || b.LogID != first.LogID {
			continue
		}
		if prev, dup := out.records[b.Seq]; dup && prev.RecordID != b.RecordID {
			if out.forkAt == 0 || b.Seq < out.forkAt {
				out.forkAt = b.Seq
			}
			out.forked = append(out.forked, b)
			continue
		}
		out.records[b.Seq] = b
	}
	if out.forkAt > 0 {
		for seq, b := range out.records {
			if seq >= out.forkAt {
				out.forked = append(out.forked, b)
				delete(out.records, seq)
			}
		}
		out.forked = uniqueBySeq(out.forked)
	}
	return out, nil
}

// readAnswer checks an answer's book evidence against its full copy and
// picks the branch of its held copy it names as received.
func readAnswer(full FullCopy, e evidencebook.BookEvidence, witnesses aacwitness.Directory) (answerRead, error) {
	ag := full.Agreement
	if e.BookID != full.Own[0].RecordID {
		return answerRead{}, caseErr("the %s's book evidence is of book %s, not its deal book", full.Role, e.BookID)
	}
	times, err := evidencebook.VerifyBookEvidence(e, ag.CheckpointKey, ag.ShelfLogID, witnesses)
	if err != nil {
		return answerRead{}, err
	}
	out := answerRead{full: full, times: times}
	for _, w := range ag.Witnesses {
		out.keys = append(out.keys, w.KeyID)
	}
	ids := recordIDs(full.Own)
	// What the holder's book names as received, in the order it named it.
	var named []string
	for _, b := range full.Own {
		if s := b.Record.Block.Seen; s.V != nil {
			named = append(named, *s.V)
		}
	}
	for i, f := range e.Facts {
		v, err := evidencebook.VerifyStampProof(f.Proof)
		if err != nil {
			return answerRead{}, err
		}
		if v.Fact.LogID != full.Own[0].LogID {
			return answerRead{}, caseErr("the %s's fact %d is not of its deal book's log", full.Role, i+1)
		}
		ok, err := evidencebook.StampProofRecomputes(v, ids)
		if err != nil {
			return answerRead{}, err
		}
		if !ok {
			return answerRead{}, caseErr("the %s's fact %d is not recomputed by its records", full.Role, i+1)
		}
		var link evidencebook.LinkValue
		if err := json.Unmarshal(f.LinkOpening.Value, &link); err != nil {
			return answerRead{}, caseErr("the %s's fact %d's link value: %v", full.Role, i+1, err)
		}
		if r := link.LastReceived; r != nil {
			held, ok := full.Received[r.RecordDigest]
			if r.LogID != full.LogID || !ok || held.Seq != r.Size {
				return answerRead{}, caseErr("the %s's fact %d names %s at %d of %s as received, which its held copy does not carry there", full.Role, i+1, r.RecordDigest, r.Size, r.LogID)
			}
			named = append(named, r.RecordDigest)
		}
	}
	out.branch, out.forked, out.forkAt, err = heldBranch(full, named)
	return out, err
}

// heldBranch is the branch of a held copy the holder named as received
// (every named record on it, ending at the furthest, extended while the held
// records do not fork), the held records off it, and the lowest seq of one
// of those (0 for none).
func heldBranch(full FullCopy, named []string) ([]BookRead, []BookRead, uint64, error) {
	if len(full.Received) == 0 {
		return nil, nil, 0, nil
	}
	head := ""
	for _, id := range named {
		if b, ok := full.Received[id]; ok && (head == "" || b.Seq > full.Received[head].Seq) {
			head = id
		}
	}
	var branch []BookRead
	if head != "" {
		branch = branchTo(full.Received, head)
		for _, id := range named {
			b := full.Received[id]
			if branch[b.Seq-1].RecordID != id {
				return nil, nil, 0, caseErr("the %s's book names two branches of the other side's book as received", full.Role)
			}
		}
	}
	children := map[string][]BookRead{}
	var roots []BookRead
	for _, id := range slices.Sorted(maps.Keys(full.Received)) {
		b := full.Received[id]
		if b.Seq == 1 {
			roots = append(roots, b)
		} else {
			children[b.Prev] = append(children[b.Prev], b)
		}
	}
	if branch == nil {
		if len(roots) != 1 {
			// The sender forked at its first record and the holder named
			// neither: no branch is read, and every held record is evidence.
			var all []BookRead
			for _, id := range slices.Sorted(maps.Keys(full.Received)) {
				all = append(all, full.Received[id])
			}
			return nil, uniqueBySeq(all), 1, nil
		}
		branch = []BookRead{roots[0]}
	}
	for {
		next := children[branch[len(branch)-1].RecordID]
		if len(next) != 1 {
			break
		}
		branch = append(branch, next[0])
	}
	on := map[string]bool{}
	for _, b := range branch {
		on[b.RecordID] = true
	}
	var forked []BookRead
	var forkAt uint64
	for _, id := range slices.Sorted(maps.Keys(full.Received)) {
		if b := full.Received[id]; !on[id] {
			forked = append(forked, b)
			if forkAt == 0 || b.Seq < forkAt {
				forkAt = b.Seq
			}
		}
	}
	slices.SortFunc(forked, func(a, b BookRead) int { return int(a.Seq) - int(b.Seq) })
	return branch, forked, forkAt, nil
}

// gatherSide reads one side's book from its sources (GatherCase). A fork
// at a position the copy the other side held reaches is settled by that copy
// (Steven's ruling 3, u211): the held branch is read there. A fork past it
// (the holder's copy stops at a fork it received both branches of, or the
// side's own copies differ beyond what the holder holds) is settled by
// nothing, so no branch is read from that position on, and what lies past
// it is kept as fork evidence.
func gatherSide(role, key string, own answerRead, hasOwn bool, holder answerRead, hasHolder bool, op openingRead, hasOpening bool) CaseSide {
	side := CaseSide{Role: role, Key: key, Own: NullOf[string](), Held: NullOf[string]()}
	var forks []uint64
	fork := func(at uint64, kept []BookRead) {
		forks = append(forks, at)
		side.Forked = append(side.Forked, kept...)
	}
	var held []BookRead
	if hasHolder {
		held = holder.branch
		if len(held) > 0 {
			side.Held = Some(held[len(held)-1].RecordID)
		}
		if holder.forkAt > 0 {
			fork(holder.forkAt, holder.forked)
		}
	}
	var full []BookRead
	if hasOwn {
		full = own.full.Own
		side.Own = Some(full[len(full)-1].RecordID)
	} else if hasOpening {
		side.Own = Some(op.last)
	}
	// The own book against the held copy, and the opening copy against both.
	heldGoverns := false
	if at := firstDiff(full, held); at > 0 {
		fork(at, full[at-1:])
		heldGoverns = true
	}
	if hasOpening {
		if op.forkAt > 0 {
			fork(op.forkAt, op.forked)
		}
		for _, against := range [][]BookRead{full, held} {
			if at := openingDiff(op, against); at > 0 {
				// The opening copy's branch from there is kept beside the other.
				var kept []BookRead
				for seq, b := range op.records {
					if seq >= at {
						kept = append(kept, b)
					}
				}
				fork(at, kept)
			}
		}
	}
	var book []BookRead
	switch {
	case heldGoverns:
		book = held
	case len(full) > 0 || len(held) > 0:
		book = full
		if len(held) > len(full) {
			book = held
		}
	case hasOpening:
		for _, s := range slices.Sorted(maps.Keys(op.records)) {
			book = append(book, op.records[s])
		}
	}
	// The lowest fork the held copy does not reach caps the book.
	var unsettled uint64
	for _, at := range forks {
		if at > uint64(len(held)) && (unsettled == 0 || at < unsettled) {
			unsettled = at
		}
	}
	if unsettled > 0 {
		for i, b := range book {
			if b.Seq >= unsettled {
				side.Forked = append(side.Forked, book[i:]...)
				book = book[:i]
				break
			}
		}
	}
	if len(forks) > 0 {
		side.ForkAt = slices.Min(forks)
	}
	side.Forked = uniqueBySeq(side.Forked)
	var ownTimes, holderTimes *SideTimes
	if hasOwn {
		ownTimes = &SideTimes{Times: own.times, BookID: full[0].RecordID, Keys: own.keys, Chain: full}
	}
	if hasHolder && len(held) > 0 {
		holderTimes = &SideTimes{Times: holder.times, BookID: holder.full.Own[0].RecordID, Keys: holder.keys, Chain: held, LogID: holder.full.LogID}
	}
	side.Book = CaseRecordTimes(book, ownTimes, holderTimes)
	side.Read = NullOf[string]()
	switch {
	case len(held) == 0 && len(full) == 0 && hasOpening && unsettled == 0:
		side.Read = Some(op.last)
	case len(book) > 0:
		side.Read = Some(book[len(book)-1].RecordID)
	}
	return side
}

// uniqueBySeq is records once each, in seq then record id order.
func uniqueBySeq(rs []BookRead) []BookRead {
	seen := map[string]bool{}
	var out []BookRead
	for _, r := range rs {
		if !seen[r.RecordID] {
			seen[r.RecordID] = true
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b BookRead) int {
		if a.Seq != b.Seq {
			return int(a.Seq) - int(b.Seq)
		}
		return strings.Compare(a.RecordID, b.RecordID)
	})
	return out
}

// firstDiff is the lowest position (from 1) at which two chains hold
// different records, 0 when one is a prefix of the other.
func firstDiff(a, b []BookRead) uint64 {
	for i := range min(len(a), len(b)) {
		if a[i].RecordID != b[i].RecordID {
			return uint64(i + 1)
		}
	}
	return 0
}

// openingDiff is the lowest position at which an opening copy's records, or
// the last record it rests on, differ from a chain that reaches them; 0 for
// none shown.
func openingDiff(op openingRead, chain []BookRead) uint64 {
	var at uint64
	for s, b := range op.records {
		if s <= uint64(len(chain)) && chain[s-1].RecordID != b.RecordID && (at == 0 || s < at) {
			at = s
		}
	}
	if op.size >= 1 && op.size <= uint64(len(chain)) && chain[op.size-1].RecordID != op.last && (at == 0 || op.size < at) {
		at = op.size
	}
	return at
}

// SideTimes is one party's record times as its book evidence gives them
// (evidencebook.VerifyBookEvidence): its deal book's id, the witnesses its
// agreement names (key ids), and the chain they time. For the party's own
// book, Chain is its records; for the copy it holds of the other side's,
// Chain is the branch read and LogID that log.
type SideTimes struct {
	Times  evidencebook.HandoverTimes
	BookID string
	Keys   []string
	Chain  []BookRead
	LogID  string
}

// CaseRecordTimes times a side's book read: a record its own book holds at
// that position by its own stamps (own, TimedByOwnStamp), a record the
// other side's held copy holds there by the holder's linking facts (holder,
// TimedByHolderLink), the earlier of the two, the side's own on a tie. Either
// may be nil. A record neither times has no time.
func CaseRecordTimes(book []BookRead, own, holder *SideTimes) []CaseRecord {
	out := make([]CaseRecord, len(book))
	for i, b := range book {
		out[i] = CaseRecord{Read: b}
		var best time.Time
		at := func(t time.Time, ok bool, err error, by string) {
			if err == nil && ok && (out[i].TimedBy == "" || t.Before(best)) {
				best, out[i].TimedBy = t, by
			}
		}
		if on(own, b) {
			t, ok, err := own.Times.RecordTimeByKey(own.BookID, b.Seq, own.Keys)
			at(t, ok, err, TimedByOwnStamp)
		}
		if on(holder, b) {
			t, ok, err := holder.Times.ReceivedRecordTimeByKey(holder.BookID, holder.LogID, b.Seq, holder.Keys)
			at(t, ok, err, TimedByHolderLink)
		}
		if out[i].TimedBy != "" {
			out[i].At = best.UTC().Format(time.RFC3339)
		}
	}
	return out
}

// on reports whether the chain s times holds b at its position.
func on(s *SideTimes, b BookRead) bool {
	return s != nil && b.Seq >= 1 && b.Seq <= uint64(len(s.Chain)) && s.Chain[b.Seq-1].RecordID == b.RecordID
}

// TrimBookEvidence is the longest prefix of a party's book evidence that its
// full copy reaches: each fact's book size within the copy's records, and
// each link value naming a record of its held copy. A host builds the full
// copy and the evidence in two reads; a fact stamped between them is left
// out, which can only make a time later.
func TrimBookEvidence(e evidencebook.BookEvidence, f FullCopy) evidencebook.BookEvidence {
	out := evidencebook.BookEvidence{BookID: e.BookID, Facts: []evidencebook.BookFactEvidence{}, Consistency: []evidencebook.HandoverConsistency{}}
	var last uint64
	for _, fe := range e.Facts {
		v, err := evidencebook.VerifyStampProof(fe.Proof)
		if err != nil || v.BookCheckpoint.Entries > uint64(len(f.Own)) {
			break
		}
		var link evidencebook.LinkValue
		if err := json.Unmarshal(fe.LinkOpening.Value, &link); err != nil {
			break
		}
		if r := link.LastReceived; r != nil {
			if b, ok := f.Received[r.RecordDigest]; !ok || b.Seq != r.Size || r.LogID != f.LogID {
				break
			}
		}
		out.Facts = append(out.Facts, fe)
		last = v.Fact.Size
	}
	for _, c := range e.Consistency {
		if c.ToSize <= last {
			out.Consistency = append(out.Consistency, c)
		}
	}
	return out
}
