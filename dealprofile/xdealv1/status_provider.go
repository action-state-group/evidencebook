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

var (
	_ evidencebook.BookStatusProvider  = (*StatusProvider)(nil)
	_ evidencebook.BeforeStampProvider = (*StatusProvider)(nil)
)

// BeforeStamp names, before each stamp, every counted ruling the holder
// holds that its deal book does not name yet (NameAllRulings): a naming a
// Receive failed, or left for a book not readable whole then, is sealed by
// the next stamp, whose status value then reads it (PROFILE §5;
// evidencebook#527).
func (p *StatusProvider) BeforeStamp(ctx context.Context, at time.Time) error {
	if p.Shelf == nil {
		return fmt.Errorf("xdealv1: the status provider has no shelf")
	}
	_, err := NameAllRulings(ctx, p.Shelf, at)
	return err
}

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
	b, link, _, _, ok, err := p.statusBooks(ctx, in)
	if err != nil || !ok {
		return none(err)
	}
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

// statusBooks is what a v1 deal book's status value is built from, read
// through in's handles and the shelf's lock-free readers, with its link value
// and the counterparty's counted prefix as a report rests on it, from the one
// prefix read the status walked, and the book pair of each record read, by
// record id (the holder's own records and every record held for the deal);
// ok false for a book that is not a v1 deal book.
func (p *StatusProvider) statusBooks(ctx context.Context, in evidencebook.StatusInput) (StatusBooks, evidencebook.LinkValue, *ReportCounterpartLog, map[string][]byte, bool, error) {
	fail := func(err error) (StatusBooks, evidencebook.LinkValue, *ReportCounterpartLog, map[string][]byte, bool, error) {
		return StatusBooks{}, evidencebook.LinkValue{}, nil, nil, false, err
	}
	own, err := p.ownBook(ctx, in)
	if err != nil || len(own) == 0 {
		return fail(err)
	}
	ag, _ := asAgree(own[0].rec.Record.Body)
	deal := own[0].rec.Record.Block.DealID
	offer, err := p.offer(ctx, journalOf(in), ag.Offer.Digest)
	if err != nil {
		return fail(err)
	}
	var keys []string
	for _, w := range ag.Witnesses {
		keys = append(keys, w.KeyID)
	}
	for i := range own {
		if own[i].rec.Time, err = p.ownTime(ctx, in, uint64(i+1), keys); err != nil {
			return fail(err)
		}
	}
	held, err := p.Shelf.HeldByDeal(ctx, deal)
	if err != nil {
		return fail(err)
	}
	b := StatusBooks{Offer: offer}
	for _, o := range own {
		b.Own = append(b.Own, o.rec)
	}
	link := evidencebook.LinkValue{CheckedOver: []string{}}
	var theirs []heldBookRecord
	var theirLog string
	var prefix evidencebook.CountedPrefix
	if ag.CounterpartyKey.V != nil {
		if logs := logsOf(held, *ag.CounterpartyKey.V, func(r Record) bool {
			a, ok := asAgree(r.Body)
			return ok && a.Kind == KindAgreement
		}); len(logs) > 0 {
			theirLog = logs[0]
			if theirs, prefix, err = countedWith(ctx, p.Shelf, held, *ag.CounterpartyKey.V, theirLog); err != nil {
				return fail(err)
			}
		}
		for i := range theirs {
			if theirs[i].rec.Time, err = p.receivedTime(ctx, in, theirLog, uint64(i+1), keys); err != nil {
				return fail(err)
			}
			b.Counterpart = append(b.Counterpart, theirs[i].rec)
		}
		if len(theirs) > 0 {
			last := theirs[len(theirs)-1]
			link.LastReceived = &evidencebook.ReceivedPosition{LogID: theirLog, Size: uint64(len(theirs)), RecordDigest: last.rec.RecordID}
			theirAg, _ := asAgree(theirs[0].rec.Record.Body)
			if b.ProofHeld, err = proofCovers(ctx, p.Shelf, *ag.CounterpartyKey.V, theirLog, theirs[0].rec.RecordID, theirAg); err != nil {
				return fail(err)
			}
		}
	}
	if a := ag.Adjudicator.V; a != nil {
		if b.CaseBook, b.ConflictingFinalRulings, b.RulingsUnnamed, err = caseBook(ctx, p.Shelf, held, a.KeyID, b.Own, b.Counterpart); err != nil {
			return fail(err)
		}
	}
	if b.Payments, err = p.payments(ctx, offer, ag, own, theirs); err != nil {
		return fail(err)
	}
	for _, r := range b.Payments {
		link.CheckedOver = append(link.CheckedOver, r.CheckedOver...)
	}
	slices.Sort(link.CheckedOver)
	link.CheckedOver = slices.Compact(link.CheckedOver)
	var cp *ReportCounterpartLog
	if len(theirs) > 0 {
		cp = &ReportCounterpartLog{LogID: theirLog, Counted: uint64(len(theirs)), Last: theirs[len(theirs)-1].rec.RecordID, ForkAt: prefix.ForkAt}
	}
	pairs := map[string][]byte{}
	for _, o := range own {
		pairs[o.rec.RecordID] = o.pair
	}
	for _, h := range held {
		if _, ok := pairs[h.Digest]; !ok {
			pairs[h.Digest] = h.Record
		}
	}
	return b, link, cp, pairs, true, nil
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
	if in.Book == nil || journalOf(in) == nil {
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
func (p *StatusProvider) offer(ctx context.Context, journal evidencebook.JournalReader, digest string) (Record, error) {
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

// logsOf are the logs of sender's records held for the deal whose first
// held record is opened (first) as such a book's first record must be, in
// log id order.
func logsOf(held []evidencebook.HeldRecord, sender string, first func(Record) bool) []string {
	var out []string
	for _, h := range held {
		if h.SenderKey != sender || h.Position != 1 || strings.HasPrefix(h.LogID, SentLogPrefix) || slices.Contains(out, h.LogID) {
			continue
		}
		if b, err := VerifyBook(h.Record); err == nil && first(b.Record) {
			out = append(out, h.LogID)
		}
	}
	slices.Sort(out)
	return out
}

// counted is sender's counted prefix of logID, as far as this deal's held
// records reach it: from position 1, each record the one held record that
// follows the last, up to the prefix's size and before any fork. It stops,
// never fails, at a counted record not held for the deal: one of another
// deal in the same log, one received after the held records were read, or
// one forgotten under retention. Data another party or retention controls
// must not stop the stamp.
func counted(ctx context.Context, shelf *evidencebook.Shelf, held []evidencebook.HeldRecord, sender, logID string) ([]heldBookRecord, error) {
	out, _, err := countedWith(ctx, shelf, held, sender, logID)
	return out, err
}

// countedWith is counted with the counted prefix it walked.
func countedWith(ctx context.Context, shelf *evidencebook.Shelf, held []evidencebook.HeldRecord, sender, logID string) ([]heldBookRecord, evidencebook.CountedPrefix, error) {
	prefix, err := shelf.CountedPrefix(ctx, sender, logID)
	if err != nil {
		return nil, prefix, err
	}
	limit := prefix.Size
	if prefix.ForkAt > 0 && prefix.ForkAt-1 < limit {
		limit = prefix.ForkAt - 1
	}
	next := map[string][]evidencebook.HeldRecord{} // prev -> held records that follow it
	for _, h := range held {
		if h.SenderKey == sender && h.LogID == logID {
			next[h.Prev] = append(next[h.Prev], h)
		}
	}
	var out []heldBookRecord
	prev := ""
	for pos := uint64(1); pos <= limit; pos++ {
		cands := next[prev]
		if len(cands) != 1 || cands[0].Position != pos {
			break
		}
		h := cands[0]
		b, err := VerifyBook(h.Record)
		if err != nil {
			break
		}
		out = append(out, heldBookRecord{rec: StatusRecord{Record: b.Record, RecordID: h.Digest}, pair: h.Record})
		prev = h.Digest
	}
	return out, prefix, nil
}

// caseBook is the adjudicator's rulings of the deal that either side has
// named arriving (a statement whose names.ruling cites one, in the holder's
// book or the counterparty's counted prefix), each timed by the earliest
// such naming, in case book order; and the rows whose case book forked with
// different first final rulings; and how many counted rulings neither book
// names. The side a ruling goes against cannot keep it out by not naming it
// while the other side names it (Sherlock2's twin, PR 4c-2b-2 review); a
// ruling neither side has named is not an input yet, and the receive call is
// to name it (#527). Until it does, the count says one is held, so an open
// outcome is never shown as unresolved.
func caseBook(ctx context.Context, shelf *evidencebook.Shelf, held []evidencebook.HeldRecord, adjudicator string, own, theirs []StatusRecord) ([]StatusRecord, []string, int, error) {
	var rulings []StatusRecord
	var conflicting []string
	// Every held log of the adjudicator: receive keeps a ruling only on the
	// deal's one case log (CaseLogID), and this reader stays general.
	for _, logID := range logsOf(held, adjudicator, func(r Record) bool { return r.Block.RecordType == TypeRuling }) {
		recs, err := counted(ctx, shelf, held, adjudicator, logID)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, r := range recs {
			rulings = append(rulings, r.rec)
		}
		rows, err := forkedFinals(held, adjudicator, logID)
		if err != nil {
			return nil, nil, 0, err
		}
		conflicting = append(conflicting, rows...)
	}
	slices.Sort(conflicting)
	conflicting = slices.Compact(conflicting)
	out, err := namedRulings(rulings, own, theirs)
	if err != nil {
		return nil, nil, 0, err
	}
	return out, conflicting, len(rulings) - len(out), nil
}

// namedRulings is the rulings either book names arriving, in case book
// order, each timed by the earliest naming.
func namedRulings(rulings, own, theirs []StatusRecord) ([]StatusRecord, error) {
	namedAt := map[string]StatusTime{}
	for _, book := range [][]StatusRecord{own, theirs} {
		for _, r := range book {
			if s, ok := bodyOf[StatementBody](r.Record.Body); ok && s.Names != nil && s.Names.Ruling != nil {
				d := s.Names.Ruling.Digest
				if t, seen := namedAt[d]; !seen || earlier(r.Time, t) {
					namedAt[d] = r.Time
				}
			}
		}
	}
	var out []StatusRecord
	for _, r := range rulings {
		d, err := Digest(r.Record)
		if err != nil {
			return nil, err
		}
		if t, ok := namedAt[d]; ok {
			r.Time = t
			out = append(out, r)
		}
	}
	return out, nil
}

// earlier reports whether a is an earlier naming time than b: a known time
// before a stamp (a stamp's time is not known yet), else the earlier known
// time or the lower stamp. Every stamp here is the holder's own.
func earlier(a, b StatusTime) bool {
	switch {
	case a.Known != "" && b.Known != "":
		return a.Known < b.Known
	case a.Known != "" || b.Known != "":
		return a.Known != ""
	}
	return a.Stamp < b.Stamp
}

// forkedFinals is the rows whose first final ruling differs between two
// branches of the adjudicator's case book the holder holds (design §8, "A
// forked case book"): each branch, from position 1 to a record no other
// held record follows, unbroken (a run after a gap is no branch), applies
// its own first final ruling per row, as
// rowstatus does, and two branches conflict on a row when both have one and
// their decisions (value, against, remedy) differ.
func forkedFinals(held []evidencebook.HeldRecord, adjudicator, logID string) ([]string, error) {
	type node struct {
		prev   string
		ruling *RulingBody
	}
	nodes := map[string]node{}
	followed := map[string]bool{}
	for _, h := range held {
		if h.SenderKey != adjudicator || h.LogID != logID {
			continue
		}
		b, err := VerifyBook(h.Record)
		if err != nil {
			continue
		}
		rb, _ := bodyOf[RulingBody](b.Record.Body)
		nodes[h.Digest] = node{prev: h.Prev, ruling: rb}
		followed[h.Prev] = true
	}
	decision := func(rb *RulingBody) string {
		raw, _ := json.Marshal(struct {
			Value   string        `json:"value"`
			Against Null[string]  `json:"against"`
			Remedy  *RulingRemedy `json:"remedy"`
		}{rb.Value, rb.Against, rb.Remedy})
		return string(raw)
	}
	firsts := map[string]map[string]bool{} // row -> the first final decisions across branches
	for leaf := range nodes {
		if followed[leaf] {
			continue
		}
		var chain []node
		whole := false
		for d := leaf; ; {
			n, ok := nodes[d]
			if !ok {
				break // a gap: this chain does not reach position 1
			}
			chain = append(chain, n)
			if n.prev == "" {
				whole = true
				break
			}
			d = n.prev
		}
		// Only a branch from position 1 applies its own first final: a run
		// after a gap (records received out of order, or forgotten) is the
		// tail of a branch whose first final it may not hold (Codex and the
		// Claude stand-in, PR 4c-2b-2 review round 2).
		if !whole {
			continue
		}
		seen := map[string]bool{}
		for k := len(chain) - 1; k >= 0; k-- {
			rb := chain[k].ruling
			if rb == nil || !rb.Final || seen[rb.Row] {
				continue
			}
			seen[rb.Row] = true
			if firsts[rb.Row] == nil {
				firsts[rb.Row] = map[string]bool{}
			}
			firsts[rb.Row][decision(rb)] = true
		}
	}
	var out []string
	for row, ds := range firsts {
		if len(ds) > 1 {
			out = append(out, row)
		}
	}
	slices.Sort(out)
	return out, nil
}

// payments runs each payment row's check over both books, when the host
// gives the offer's sent pair (design §6.3). It returns nil only when no
// check could run at all (no pair, or no payment rows), and a map, empty or
// not, otherwise, with a row left out when no acceptor key is known yet:
// paymentsNotRun tells the two apart by exactly that.
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

// journalOf is the journal a status input hands over: read across its
// windows (Journals), or the one journal book of an input that sets only
// Journal.
func journalOf(in evidencebook.StatusInput) evidencebook.JournalReader {
	if in.Journals != nil {
		return in.Journals
	}
	if in.Journal != nil {
		return in.Journal
	}
	return nil
}
