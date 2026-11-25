// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	emit "github.com/action-state-group/capsule-emit/go"
	evidencebook "github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// Records the holder receives (design §8.3). A deal-book or case-book record
// travels as a book pair: its Capsule with its signature and key_id, the
// record header the Capsule commits as its agent_input_digest, and the
// x-deal-v1 record the header commits as its one payload. An offer, a
// decline or a cancellation travels as a sent pair (sent.go).

// SentLogPrefix begins the one-record log a sent journal record is held in:
// "x-deal-v1/sent/<record digest>". It hides the record's journal position.
const SentLogPrefix = Profile + "/sent/"

// StageBook is a book pair read on its own (VerifyBook).
const StageBook = "book"

// BookPair is a book record as it is sent: sealed is the record as its book
// sealed it (evidencebook.Book.Sealed), and payload the x-deal-v1 record's
// JCS bytes, its one payload.
func BookPair(sealed evidencebook.StoredRecord, payload []byte) ([]byte, error) {
	signed, err := signedCapsule(sealed.Capsule, sealed.Envelope)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]json.RawMessage{"record": signed, "header": sealed.Header, "payload": payload})
}

// signedCapsule is the Capsule's members with signature (the envelope,
// lowercase hex) and key_id (the signer's public key, lowercase hex) added.
func signedCapsule(capsule, envelope []byte) ([]byte, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(capsule, &members); err != nil {
		return nil, fmt.Errorf("xdealv1: the Capsule is not a JSON object: %w", err)
	}
	var id struct {
		CapsuleID string `json:"capsule_id"`
	}
	if err := json.Unmarshal(capsule, &id); err != nil {
		return nil, err
	}
	verified, err := emit.VerifyEnvelope(id.CapsuleID, envelope)
	if err != nil {
		return nil, fmt.Errorf("xdealv1: the envelope: %w", err)
	}
	sig, _ := json.Marshal(hex.EncodeToString(envelope))
	key, _ := json.Marshal(hex.EncodeToString(verified.PublicKey))
	members["signature"], members["key_id"] = sig, key
	return json.Marshal(members)
}

// verifyCapsule checks a signed Capsule's members: the Capsule verifies, and
// its signature verifies under its key_id. It returns the Capsule's JCS bytes
// without signature and key_id, its id and the key.
func verifyCapsule(stage string, members map[string]json.RawMessage) (capsule []byte, id, keyID string, err error) {
	bad := func(format string, args ...any) ([]byte, string, string, error) {
		return nil, "", "", stageErr(stage, format, args...)
	}
	var sigHex string
	if err := errors.Join(json.Unmarshal(members["signature"], &sigHex), json.Unmarshal(members["key_id"], &keyID)); err != nil || sigHex == "" || keyID == "" {
		return bad("the Capsule carries no signature and key_id")
	}
	rest := make(map[string]json.RawMessage, len(members))
	for k, v := range members {
		if k != "signature" && k != "key_id" {
			rest[k] = v
		}
	}
	raw, err := json.Marshal(rest)
	if err != nil {
		return nil, "", "", err
	}
	doc, err := decodeJSON(raw)
	if err != nil {
		return bad("the Capsule: %v", err)
	}
	if capsule, err = canonical.JCS(doc); err != nil {
		return bad("the Capsule: %v", err)
	}
	result, err := emit.VerifyCapsule(capsule)
	if err != nil || !result.OK || result.CapsuleID == nil {
		return bad("the Capsule does not verify: %v", err)
	}
	envelope, err := hex.DecodeString(sigHex)
	if err != nil {
		return bad("the signature is not hex")
	}
	verified, err := emit.VerifyEnvelope(*result.CapsuleID, envelope)
	if err != nil {
		return bad("the signature does not verify: %v", err)
	}
	if hex.EncodeToString(verified.PublicKey) != keyID {
		return bad("the Capsule is signed by another key than its key_id")
	}
	return capsule, *result.CapsuleID, keyID, nil
}

// BookRead is what a verified book pair shows.
type BookRead struct {
	Record Record
	// Digest is the record digest; RecordID the Capsule id (the record's
	// entry in its book log); KeyID the key that signed it.
	Digest, RecordID, KeyID string
	// LogID, Seq and Prev place it in its book: the header's book log id,
	// its seq, and the record id it cites before it ("" at seq 1).
	LogID string
	Seq   uint64
	Prev  string
}

// VerifyBook checks a book pair on its own: the Capsule verifies under its
// key_id and commits the header as its agent_input_digest; the header
// commits the record's digest as a payload and names its book log, seq and,
// past seq 1, exactly one record it cites; and the record passes Check, is
// sealed in a deal book or a case book, and is at the header's seq.
func VerifyBook(pair []byte) (BookRead, error) {
	bad := func(format string, args ...any) (BookRead, error) {
		return BookRead{}, stageErr(StageBook, format, args...)
	}
	if err := checkText(pair); err != nil {
		return BookRead{}, err
	}
	var p struct {
		Record  map[string]json.RawMessage `json:"record"`
		Header  json.RawMessage            `json:"header"`
		Payload json.RawMessage            `json:"payload"`
	}
	if err := strictUnmarshal(pair, &p); err != nil || p.Record == nil || p.Header == nil || p.Payload == nil {
		return bad("not a book pair {record, header, payload}: %v", err)
	}
	if err := exactObject(pair, reflect.TypeOf(p), false, "the pair"); err != nil {
		return bad("not a book pair {record, header, payload}: %v", err)
	}
	if _, inline := p.Record[evidencebook.RecordHeaderMember]; inline {
		return bad("the Capsule carries its header inline; a book record commits it")
	}
	capsule, id, keyID, err := verifyCapsule(StageBook, p.Record)
	if err != nil {
		return BookRead{}, err
	}
	c, err := readCapsule(capsule)
	if err != nil {
		return bad("%v", err)
	}
	hdoc, err := decodeJSON(p.Header)
	if err != nil {
		return bad("the header: %v", err)
	}
	hjcs, err := canonical.JCS(hdoc)
	if err != nil {
		return bad("the header: %v", err)
	}
	if sum := sha256.Sum256(hjcs); c.AgentInputDigest != hex.EncodeToString(sum[:]) {
		return bad("the Capsule does not commit this header")
	}
	if err := exactObject(hjcs, reflect.TypeFor[evidencebook.Header](), false, "the header"); err != nil {
		return bad("%v", err)
	}
	var h evidencebook.Header
	if err := json.Unmarshal(hjcs, &h); err != nil {
		return bad("the header: %v", err)
	}
	digest, err := Digest([]byte(p.Payload))
	if err != nil {
		return BookRead{}, err
	}
	// One payload, this record: a header committing two would let one
	// record id be read as either.
	if len(h.PayloadCommitments) != 1 || h.PayloadCommitments[0] != digest {
		return bad("the header does not commit this record as its one payload")
	}
	if err := Check([]byte(p.Payload)); err != nil {
		return BookRead{}, err
	}
	r, err := Decode(p.Payload)
	if err != nil {
		return BookRead{}, err
	}
	if place := PlacementOf(r); place == PlacementJournal {
		return bad("a %s is sealed in the journal, not sent from a book", r.Block.RecordType)
	}
	if h.BookID == "" || h.Seq == 0 || uint64(r.Block.Seq) != h.Seq {
		return bad("the record's seq %d is not its header's %d", r.Block.Seq, h.Seq)
	}
	var cites []string
	for _, l := range h.Links {
		if l.Type == evidencebook.Cites {
			cites = append(cites, l.Target)
		}
	}
	out := BookRead{Record: r, Digest: digest, RecordID: id, KeyID: keyID, LogID: h.BookID, Seq: h.Seq}
	switch {
	case h.Seq == 1 && len(cites) != 0:
		return bad("the first record of a book cites none before it")
	case h.Seq > 1 && len(cites) != 1:
		return bad("a book record cites exactly the record before it")
	case h.Seq > 1:
		out.Prev = cites[0]
	}
	return out, nil
}

// ReceiveProfile is x-deal-v1's evidencebook.ReceiveProfile (design §8.3).
// Shelf is the holder's shelf, set once it is open: the profile reads the
// holder's journal and deal books, and the store, through it. One profile
// serves one shelf.
//
// Each side recomputes the other's book checkpoints from the records it
// receives, so a deal book's log must hold the deal's records only: the
// shelf gives deal books no authenticated indexes
// (evidencebook.ShelfConfig.AuthenticatedIndexes returning an empty list),
// whose index_root records the counterparty never receives.
type ReceiveProfile struct {
	Shelf *evidencebook.Shelf

	// The store asks Admit about the record Read just read, before it holds
	// it; Receive takes one record at a time, so the last one read is it.
	mu   sync.Mutex
	last struct {
		digest string
		rec    Record
	}
	cache holderCache
}

var _ evidencebook.ReceiveProfile = (*ReceiveProfile)(nil)

// read is a received record read as either pair.
type read struct {
	book *BookRead
	sent *Sent
}

func (rd read) record() Record {
	if rd.book != nil {
		return rd.book.Record
	}
	return rd.sent.Record
}

func readPair(record []byte) (read, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(record, &members); err != nil {
		return read{}, stageErr(StageBook, "not a pair: %v", err)
	}
	if _, ok := members["agent_input"]; ok {
		s, err := VerifySent(record, nil)
		return read{sent: &s}, err
	}
	b, err := VerifyBook(record)
	return read{book: &b}, err
}

// Read verifies a received pair and returns what the store needs (design
// §8.3): a book record is placed at its book log id and seq, with the
// record id it cites before it; a sent record is the only record of
// x-deal-v1/sent/<record digest>, and an offer is marked so.
func (p *ReceiveProfile) Read(_ context.Context, record []byte) (evidencebook.ReceivedRecord, error) {
	rd, err := readPair(record)
	if err != nil {
		return evidencebook.ReceivedRecord{}, err
	}
	p.mu.Lock()
	p.last.rec = rd.record()
	if rd.book != nil {
		p.last.digest = rd.book.RecordID
	} else {
		p.last.digest = rd.sent.CapsuleID
	}
	p.mu.Unlock()
	if b := rd.book; b != nil {
		return evidencebook.ReceivedRecord{Digest: b.RecordID, SenderKey: b.KeyID, LogID: b.LogID, Position: b.Seq,
			Prev: b.Prev, DealID: b.Record.Block.DealID}, nil
	}
	s := rd.sent
	return evidencebook.ReceivedRecord{Digest: s.CapsuleID, SenderKey: s.KeyID, LogID: SentLogPrefix + s.Digest, Position: 1,
		DealID: s.Record.Block.DealID, Offer: s.Record.Block.RecordType == TypePropose}, nil
}

// ours is the holder's own x-deal-v1 records: its journal's, in order, and
// the agreement opening each of its deal books.
type ours struct {
	journal    []Record
	agreements []Record
	// cases are the rulings opening each of our case books, when the
	// holder is an adjudicator, by their log id.
	cases map[string]Record
}

// caseParty reports whether sender is a party of a deal whose agreement
// cites offerDigest and names adjudicatorKey, by our case book of that deal:
// the case log of that offer and adjudicator is one of ours, and its first
// ruling, of the same deal, names sender among its keys. An adjudicator then
// takes the parties' book records after its ruling, to check each entered it.
func (o ours) caseParty(dealID, offerDigest, adjudicatorKey, sender string) bool {
	r, ok := o.cases[CaseLogID(offerDigest, adjudicatorKey)]
	if !ok || r.Block.DealID != dealID {
		return false
	}
	rb, ok := bodyOf[RulingBody](r.Body)
	return ok && (rb.Keys.Buyer == sender || rb.Keys.Seller == sender)
}

// offerByDigest is our journal's propose whose record digest is d: an offer
// or a counter-offer, whichever a reply cites.
func (o ours) offerByDigest(d string) (ProposeBody, Record, bool) {
	for _, r := range o.journal {
		if b, ok := asPropose(r.Body); ok {
			if rd, err := Digest(r); err == nil && rd == d {
				return b, r, true
			}
		}
	}
	return ProposeBody{}, Record{}, false
}

// agreementRecord is the holder's own agreement of a deal, as sealed.
func (o ours) agreementRecord(dealID string) (Record, bool) {
	for _, r := range o.agreements {
		if a, ok := asAgree(r.Body); ok && a.Kind == KindAgreement && r.Block.DealID == dealID {
			return r, true
		}
	}
	return Record{}, false
}

func (o ours) agreement(dealID string) (AgreeBody, bool) {
	for _, r := range o.agreements {
		if a, ok := asAgree(r.Body); ok && r.Block.DealID == dealID {
			return a, true
		}
	}
	return AgreeBody{}, false
}

// payloadKind is what a holder's book record carries: an x-deal-v1 record,
// something else, or a payload not readable now (withheld under retention,
// which may make it available again).
type payloadKind int

const (
	payloadV1 payloadKind = iota
	payloadOther
	payloadUnavailable
)

// payloadResolver is a book, or the journal read across its windows
// (evidencebook.JournalReader).
type payloadResolver interface {
	ResolvePayload(ctx context.Context, recordID, digest string) ([]byte, error)
}

// v1Payload is the x-deal-v1 record a book record of the holder's carries.
func v1Payload(ctx context.Context, b payloadResolver, r evidencebook.Record) (Record, payloadKind, error) {
	if len(r.Header.PayloadCommitments) != 1 {
		return Record{}, payloadOther, nil
	}
	raw, err := b.ResolvePayload(ctx, r.RecordID, r.Header.PayloadCommitments[0])
	switch {
	case errors.Is(err, evidencebook.ErrUnavailable):
		return Record{}, payloadUnavailable, nil
	case errors.Is(err, evidencebook.ErrNotFound):
		return Record{}, payloadOther, nil
	case err != nil:
		return Record{}, payloadOther, err
	}
	if v, err := dealprofile.Version(raw); err != nil || v != Profile {
		return Record{}, payloadOther, nil
	}
	rec, err := Decode(raw)
	if err != nil {
		return Record{}, payloadOther, nil
	}
	return rec, payloadV1, nil
}

// holderCache keeps what ours read, so a call reads only what is new: the
// journal records past those read, and the first record of a deal book not
// read yet. A book's first record never changes once sealed. A payload not
// readable now is read again on the next call, so a record withheld and made
// available again counts once it is.
type holderCache struct {
	journalSeen uint64
	journal     []Record // the v1 journal records read, by journal seq
	journalSeq  []uint64
	retry       []uint64           // journal seqs whose payload was not readable
	firsts      map[string]*Record // by log id; nil for a book that opens with no v1 agreement
}

func (p *ReceiveProfile) ours(ctx context.Context) (ours, error) {
	if p.Shelf == nil {
		return ours{}, fmt.Errorf("xdealv1: the receive profile has no shelf")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	c := &p.cache
	if c.firsts == nil {
		c.firsts = map[string]*Record{}
	}
	// The whole journal, across its windows on a windowed shelf
	// (evidencebook#797): its seqs never move, so journalSeen stays valid.
	journal, err := p.Shelf.JournalView(ctx)
	if err != nil {
		return ours{}, err
	}
	// Read the new journal records, and those not readable before, into a
	// batch committed only once every read succeeded.
	var seqs []uint64
	seqs = append(seqs, c.retry...)
	size := journal.Size()
	for n := c.journalSeen + 1; n <= size; n++ {
		seqs = append(seqs, n)
	}
	var (
		added    []Record
		addedSeq []uint64
		retry    []uint64
	)
	for _, n := range seqs {
		records, err := journal.Query(ctx, evidencebook.Filter{FromSeq: n, ToSeq: n})
		if err != nil {
			return ours{}, err
		}
		for _, r := range records {
			rec, kind, err := v1Payload(ctx, journal, r)
			if err != nil {
				return ours{}, err
			}
			switch kind {
			case payloadV1:
				added, addedSeq = append(added, rec), append(addedSeq, n)
			case payloadUnavailable:
				retry = append(retry, n)
			}
		}
	}
	if len(seqs) > 0 {
		for i, n := range addedSeq {
			at, _ := slices.BinarySearch(c.journalSeq, n)
			c.journalSeq = slices.Insert(c.journalSeq, at, n)
			c.journal = slices.Insert(c.journal, at, added[i])
		}
		c.journalSeen, c.retry = size, retry
	}
	logs, err := p.Shelf.Books(ctx)
	if err != nil {
		return ours{}, err
	}
	// The caller reads its own copy after the lock is released; a later call
	// may insert into the cache's slice in place.
	o := ours{journal: slices.Clone(c.journal)}
	for _, logID := range logs {
		first, read := c.firsts[logID]
		if !read {
			book, err := p.Shelf.Open(ctx, logID)
			if err != nil {
				return ours{}, err
			}
			recs, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: 1})
			if err != nil {
				return ours{}, err
			}
			unavailable := false
			if len(recs) == 1 {
				rec, kind, err := v1Payload(ctx, book, recs[0])
				if err != nil {
					return ours{}, err
				}
				if a, isAgree := asAgree(rec.Body); kind == payloadV1 && isAgree && a.Kind == KindAgreement {
					first = &rec
				}
				if kind == payloadV1 && rec.Block.RecordType == TypeRuling && strings.HasPrefix(logID, CaseLogPrefix) {
					first = &rec
				}
				unavailable = kind == payloadUnavailable
			}
			if !unavailable {
				c.firsts[logID] = first
			}
		}
		switch {
		case first == nil:
		case first.Block.RecordType == TypeRuling:
			if o.cases == nil {
				o.cases = map[string]Record{}
			}
			o.cases[logID] = *first
		default:
			o.agreements = append(o.agreements, *first)
		}
	}
	return o, nil
}

// heldSent is the record held in the one-record log of the sent record whose
// record digest is digest, and its sender, or ok false when none is held.
func (p *ReceiveProfile) heldSent(ctx context.Context, digest string) (Record, string, bool, error) {
	held, err := p.Shelf.HeldFirst(ctx, SentLogPrefix+digest)
	if err != nil {
		return Record{}, "", false, err
	}
	for _, h := range held {
		if s, err := VerifySent(h.Record, nil); err == nil && s.Digest == digest {
			return s.Record, h.SenderKey, true, nil
		}
	}
	return Record{}, "", false, nil
}

// Admit is EBL's current push rule, which is not ruled yet (Forms SF-6, AAC
// spec/shelf-forms-v0.md; design §8.3; evidencebook ReceiveProfile.Admit):
// an offer from any key; a decline of an offer of ours from the key it was
// sent to; a cancellation of an offer of this deal from that offer's signer;
// a ruling from our agreement's adjudicator, on the deal's case log
// (CaseLogID), whoever carries it; at an adjudicator holding a deal's case
// book, a deal-book record of either party it names; and a deal-book record from the
// counterparty of the deal: our agreement's counterparty_key, or, at an
// offerer holding only its offer, the key the offer was sent to. A deal-book
// record is admitted only on a log whose first record from that sender is
// the counterparty's agreement of this deal, citing the offer it answers.
func (p *ReceiveProfile) Admit(ctx context.Context, r evidencebook.ReceivedRecord) (bool, error) {
	if r.Offer {
		return true, nil
	}
	rec, _, ok, err := p.heldOrPending(ctx, r)
	if err != nil || !ok {
		return false, err
	}
	o, err := p.ours(ctx)
	if err != nil {
		return false, err
	}
	if strings.HasPrefix(r.LogID, SentLogPrefix) {
		ag, isAgree := asAgree(rec.Body)
		if !isAgree {
			return false, nil
		}
		switch ag.Kind {
		case KindDecline:
			of, offer, ok := o.offerByDigest(ag.Offer.Digest)
			return ok && offer.Block.DealID == rec.Block.DealID && of.To.V != nil && *of.To.V == r.SenderKey, nil
		case KindCancellation:
			offer, signer, held, err := p.heldSent(ctx, ag.Offer.Digest)
			return held && signer == r.SenderKey && offer.Block.RecordType == TypePropose &&
				offer.Block.DealID == rec.Block.DealID, err
		}
		return false, nil
	}
	if rec.Block.RecordType == TypeRuling {
		// From our agreement's adjudicator, on the deal's one case book
		// (CaseLogID), whoever carried it: a party forwards the ruling it
		// received, so a judge that gave each side another ruling shows as a
		// fork of that log.
		a, held := o.agreement(rec.Block.DealID)
		return held && a.Adjudicator.V != nil && a.Adjudicator.V.KeyID == r.SenderKey &&
			r.LogID == CaseLogID(a.Offer.Digest, r.SenderKey), nil
	}
	// The log's first record from this sender: this record, or the one held.
	genesis := rec
	if r.Position > 1 {
		g, found, err := p.heldGenesis(ctx, r.SenderKey, r.LogID)
		if err != nil || !found {
			return false, err
		}
		genesis = g
	}
	gag, isAgree := asAgree(genesis.Body)
	if !isAgree || gag.Kind != KindAgreement || genesis.Block.DealID != r.DealID {
		return false, nil
	}
	if gag.Adjudicator.V != nil && o.caseParty(r.DealID, gag.Offer.Digest, gag.Adjudicator.V.KeyID, r.SenderKey) {
		// We are the deal's adjudicator and ruled on it: a party's book.
		return true, nil
	}
	if a, held := o.agreement(r.DealID); held {
		// The counterparty of our deal, on a book that answers the same offer.
		return a.CounterpartyKey.V != nil && *a.CounterpartyKey.V == r.SenderKey && gag.Offer.Digest == a.Offer.Digest, nil
	}
	// At an offerer holding only its offer: the acceptor's book answers it,
	// from the key it was sent to.
	of, offer, ok := o.offerByDigest(gag.Offer.Digest)
	return ok && offer.Block.DealID == r.DealID && of.To.V != nil && *of.To.V == r.SenderKey, nil
}

// heldGenesis is the record held at position 1 of logID from senderKey, read
// as a book pair, or found false when none (or a fork of two) is held.
func (p *ReceiveProfile) heldGenesis(ctx context.Context, senderKey, logID string) (Record, bool, error) {
	held, err := p.Shelf.HeldFirst(ctx, logID)
	if err != nil {
		return Record{}, false, err
	}
	var out *Record
	for _, h := range held {
		if h.SenderKey != senderKey {
			continue
		}
		if out != nil {
			return Record{}, false, nil
		}
		b, err := VerifyBook(h.Record)
		if err != nil {
			return Record{}, false, nil
		}
		out = &b.Record
	}
	if out == nil {
		return Record{}, false, nil
	}
	return *out, true, nil
}

// heldOrPending is the record r names: the one Read last read, which the
// store does not hold yet when it asks Admit, or the one it holds.
func (p *ReceiveProfile) heldOrPending(ctx context.Context, r evidencebook.ReceivedRecord) (Record, string, bool, error) {
	p.mu.Lock()
	last := p.last
	p.mu.Unlock()
	if last.digest == r.Digest {
		return last.rec, r.SenderKey, true, nil
	}
	h, err := p.Shelf.ReceivedRecordOf(ctx, r.Digest)
	if errors.Is(err, evidencebook.ErrNotFound) || errors.Is(err, evidencebook.ErrDeleted) {
		return Record{}, "", false, nil
	}
	if err != nil {
		return Record{}, "", false, err
	}
	rd, err := readPair(h.Record)
	if err != nil {
		return Record{}, "", false, nil
	}
	return rd.record(), h.SenderKey, true, nil
}

// Answered says whether our side answered a held offer (design §8.3): an
// agreement opening one of our deal books cites it, or a decline or a
// counter-offer in our journal does.
func (p *ReceiveProfile) Answered(ctx context.Context, offer evidencebook.ReceivedRecord) (bool, error) {
	h, err := p.Shelf.ReceivedRecordOf(ctx, offer.Digest)
	if err != nil {
		return false, err
	}
	s, err := VerifySent(h.Record, nil)
	if err != nil {
		return false, nil
	}
	o, err := p.ours(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range o.agreements {
		if a, _ := asAgree(r.Body); a.Offer.Digest == s.Digest {
			return true, nil
		}
	}
	for _, r := range o.journal {
		if a, ok := asAgree(r.Body); ok && a.Kind == KindDecline && a.Offer.Digest == s.Digest {
			return true, nil
		}
		if b, ok := asPropose(r.Body); ok && b.CounterTo != nil && b.CounterTo.Digest == s.Digest {
			return true, nil
		}
	}
	return false, nil
}

// ProofSender relates a stamp proof to the counterparty whose agreement opens
// the book log it names (design §8.3): among every record held at position 1
// of that log, across senders, it refuses a sender holding two different
// ones (a fork at the genesis), then keeps an x-deal-v1 agreement under this
// checkpoint key, of a deal of ours, signed by the key that deal expects (our
// offer's to, or our agreement's counterparty_key, or at an adjudicator a
// party its case book names). A case book's first ruling binds the proof
// when its checkpoint key is the adjudicator key our agreement names and the
// log is the deal's case log. Exactly one candidate gives its signer; none or
// more refuses the proof.
func (p *ReceiveProfile) ProofSender(ctx context.Context, checkpointKeyID, logID string) (string, bool, error) {
	held, err := p.Shelf.HeldFirst(ctx, logID)
	if err != nil {
		return "", false, err
	}
	first := map[string]string{}
	forked := map[string]bool{}
	for _, h := range held {
		if d, ok := first[h.SenderKey]; ok && d != h.Digest {
			forked[h.SenderKey] = true
		}
		first[h.SenderKey] = h.Digest
	}
	o, err := p.ours(ctx)
	if err != nil {
		return "", false, err
	}
	var candidates []string
	for _, h := range held {
		if forked[h.SenderKey] || slices.Contains(candidates, h.SenderKey) {
			continue
		}
		b, err := VerifyBook(h.Record)
		if err != nil {
			continue
		}
		if b.Record.Block.RecordType == TypeRuling {
			// A case book: the adjudicator checkpoints it with the key our
			// agreement names it by, so the proof binds to that key.
			a, held := o.agreement(b.Record.Block.DealID)
			if held && a.Adjudicator.V != nil && a.Adjudicator.V.KeyID == h.SenderKey &&
				checkpointKeyID == h.SenderKey && logID == CaseLogID(a.Offer.Digest, h.SenderKey) {
				candidates = append(candidates, h.SenderKey)
			}
			continue
		}
		ag, ok := asAgree(b.Record.Body)
		if !ok || ag.Kind != KindAgreement || ag.CheckpointKey != checkpointKeyID {
			continue
		}
		deal := b.Record.Block.DealID
		var expected string
		if a, ours := o.agreement(deal); ours && a.CounterpartyKey.V != nil {
			expected = *a.CounterpartyKey.V
		} else if of, offer, ok := o.offerByDigest(ag.Offer.Digest); ok && offer.Block.DealID == deal && of.To.V != nil {
			expected = *of.To.V
		}
		if expected == "" && ag.Adjudicator.V != nil && o.caseParty(deal, ag.Offer.Digest, ag.Adjudicator.V.KeyID, h.SenderKey) {
			expected = h.SenderKey
		}
		if expected != "" && expected == h.SenderKey {
			candidates = append(candidates, h.SenderKey)
		}
	}
	if len(candidates) != 1 {
		return "", false, nil
	}
	return candidates[0], true, nil
}

// CanActInput names the deal a first act is of, the record id of the
// counterparty's agreement for it (as the holder's index records it, design
// §3.1), and, when the counterparty has published an evidence commitment, the
// shelf log id that commitment names ("" when none is held).
type CanActInput struct {
	DealID               string
	CounterpartAgreement string
	CommitmentShelfLog   string
}

// A holder refuses its first perform or payer settle until CanAct is true
// (Steven, design 461 §7.2); GateAct applies that. CanAct does not apply to a deal whose
// counterparty runs no layer (design §4.7), which holds no counterpart
// agreement, so CanAct is false there and GateAct marks the act unguarded. A
// stamp proof shows the counterparty stamped its book; only a named
// witness's receipt shows anyone independent saw it.
//
// CanAct is the coverage gate before the holder's first act of a deal (design
// §7.2, gate B). The counterparty's agreement must be of this deal, signed by
// the key the deal expects (our agreement's counterparty_key, or the key our
// offer was sent to) and citing the offer the deal answers; it must be in its
// sender's counted prefix as the store computes it now; and a held stamp proof
// bound to it must cover it: its fact's book id is the agreement's record id,
// its checkpoints are signed by the agreement's checkpoint_key, and its shelf
// log is the agreement's shelf_log_id and, when given, the log the
// counterparty's evidence commitment names.
func (p *ReceiveProfile) CanAct(ctx context.Context, in CanActInput) (bool, error) {
	counterpartAgreement := in.CounterpartAgreement
	h, err := p.Shelf.ReceivedRecordOf(ctx, counterpartAgreement)
	if errors.Is(err, evidencebook.ErrNotFound) || errors.Is(err, evidencebook.ErrDeleted) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	b, err := VerifyBook(h.Record)
	if err != nil || h.Position != 1 {
		return false, nil
	}
	ag, ok := asAgree(b.Record.Body)
	if !ok || ag.Kind != KindAgreement || b.Record.Block.DealID != in.DealID {
		return false, nil
	}
	o, err := p.ours(ctx)
	if err != nil {
		return false, err
	}
	if a, held := o.agreement(in.DealID); held {
		if a.CounterpartyKey.V == nil || *a.CounterpartyKey.V != h.SenderKey || ag.Offer.Digest != a.Offer.Digest {
			return false, nil
		}
	} else if of, offer, ok := o.offerByDigest(ag.Offer.Digest); !ok || offer.Block.DealID != in.DealID || of.To.V == nil || *of.To.V != h.SenderKey {
		return false, nil
	}
	if in.CommitmentShelfLog != "" && in.CommitmentShelfLog != ag.ShelfLogID {
		return false, nil
	}
	prefix, err := p.Shelf.CountedPrefix(ctx, h.SenderKey, h.LogID)
	if err != nil {
		return false, err
	}
	if prefix.Size < 1 {
		return false, nil
	}
	return proofCovers(ctx, p.Shelf, h.SenderKey, h.LogID, counterpartAgreement, ag)
}

// proofCovers reports whether a stamp proof held from sender for logID
// covers the counterparty's agreement agreementID (ag): its fact's book id is
// the agreement's record id, its checkpoints are signed by the agreement's
// checkpoint_key, its shelf log is the agreement's shelf_log_id, and it
// covers the agreement in the sender's counted prefix now. It takes no lock.
func proofCovers(ctx context.Context, shelf *evidencebook.Shelf, sender, logID, agreementID string, ag AgreeBody) (bool, error) {
	proofs, err := shelf.HeldProofs(ctx, sender, logID)
	if err != nil {
		return false, err
	}
	for _, hp := range proofs {
		v := hp.Verified
		if v.Fact.BookID == agreementID && v.KeyID == ag.CheckpointKey &&
			v.ShelfCheckpoint.LogID == ag.ShelfLogID && hp.Covers >= 1 {
			return true, nil
		}
	}
	return false, nil
}
