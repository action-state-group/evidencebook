// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	if _, inline := p.Record[evidencebook.RecordHeaderMember]; inline {
		return bad("the Capsule carries its header inline; a book record commits it")
	}
	capsule, id, keyID, err := verifyCapsule(StageBook, p.Record)
	if err != nil {
		return BookRead{}, err
	}
	var c struct {
		Model struct {
			Compute struct {
				AgentInputDigest string `json:"agent_input_digest"`
			} `json:"compute_attestation"`
		} `json:"model_attestation"`
	}
	if err := json.Unmarshal(capsule, &c); err != nil {
		return BookRead{}, err
	}
	hdoc, err := decodeJSON(p.Header)
	if err != nil {
		return bad("the header: %v", err)
	}
	hjcs, err := canonical.JCS(hdoc)
	if err != nil {
		return bad("the header: %v", err)
	}
	if sum := sha256.Sum256(hjcs); c.Model.Compute.AgentInputDigest != hex.EncodeToString(sum[:]) {
		return bad("the Capsule does not commit this header")
	}
	var h evidencebook.Header
	if err := json.Unmarshal(hjcs, &h); err != nil {
		return bad("the header: %v", err)
	}
	digest, err := Digest([]byte(p.Payload))
	if err != nil {
		return BookRead{}, err
	}
	if !slices.Contains(h.PayloadCommitments, digest) {
		return bad("the header does not commit this record")
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
}

func (o ours) offer(dealID string) (ProposeBody, string, bool) {
	for _, r := range o.journal {
		if b, ok := asPropose(r.Body); ok && r.Block.DealID == dealID && b.CounterTo == nil {
			d, _ := Digest(r)
			return b, d, true
		}
	}
	for _, r := range o.journal {
		if b, ok := asPropose(r.Body); ok && r.Block.DealID == dealID {
			d, _ := Digest(r)
			return b, d, true
		}
	}
	return ProposeBody{}, "", false
}

func (o ours) agreement(dealID string) (AgreeBody, bool) {
	for _, r := range o.agreements {
		if a, ok := asAgree(r.Body); ok && r.Block.DealID == dealID {
			return a, true
		}
	}
	return AgreeBody{}, false
}

// v1Payload is the x-deal-v1 record a book record of the holder's carries,
// or ok false for another record.
func v1Payload(ctx context.Context, b *evidencebook.Book, r evidencebook.Record) (Record, bool, error) {
	if len(r.Header.PayloadCommitments) != 1 {
		return Record{}, false, nil
	}
	raw, err := b.ResolvePayload(ctx, r.RecordID, r.Header.PayloadCommitments[0])
	if errors.Is(err, evidencebook.ErrUnavailable) || errors.Is(err, evidencebook.ErrNotFound) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	if v, err := dealprofile.Version(raw); err != nil || v != Profile {
		return Record{}, false, nil
	}
	rec, err := Decode(raw)
	if err != nil {
		return Record{}, false, nil
	}
	return rec, true, nil
}

func (p *ReceiveProfile) ours(ctx context.Context) (ours, error) {
	var o ours
	if p.Shelf == nil {
		return o, fmt.Errorf("xdealv1: the receive profile has no shelf")
	}
	journal, err := p.Shelf.Journal(ctx)
	if err != nil {
		return o, err
	}
	records, err := journal.Query(ctx, evidencebook.Filter{})
	if err != nil {
		return o, err
	}
	for _, r := range records {
		rec, ok, err := v1Payload(ctx, journal, r)
		if err != nil {
			return o, err
		}
		if ok {
			o.journal = append(o.journal, rec)
		}
	}
	logs, err := p.Shelf.Books(ctx)
	if err != nil {
		return o, err
	}
	for _, logID := range logs {
		book, err := p.Shelf.Open(ctx, logID)
		if err != nil {
			return o, err
		}
		first, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: 1})
		if err != nil {
			return o, err
		}
		if len(first) == 1 {
			rec, ok, err := v1Payload(ctx, book, first[0])
			if err != nil {
				return o, err
			}
			if a, isAgree := asAgree(rec.Body); ok && isAgree && a.Kind == KindAgreement {
				o.agreements = append(o.agreements, rec)
			}
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

// Admit is the push rule (design §8.3; evidencebook ReceiveProfile.Admit):
// an offer from any key; a decline from the key an offer of ours was sent
// to; a cancellation from the signer of the offer it cancels; a deal-book
// record from our agreement's counterparty_key, or, at an offerer holding
// only its offer, from the key the offer was sent to; a ruling from our
// agreement's adjudicator.
func (p *ReceiveProfile) Admit(ctx context.Context, r evidencebook.ReceivedRecord) (bool, error) {
	if r.Offer {
		return true, nil
	}
	o, err := p.ours(ctx)
	if err != nil {
		return false, err
	}
	if strings.HasPrefix(r.LogID, SentLogPrefix) {
		rec, _, ok, err := p.heldOrPending(ctx, r)
		if err != nil || !ok {
			return false, err
		}
		ag, isAgree := asAgree(rec.Body)
		if !isAgree {
			return false, nil
		}
		switch ag.Kind {
		case KindDecline:
			of, digest, ok := o.offer(rec.Block.DealID)
			return ok && digest == ag.Offer.Digest && of.To.V != nil && *of.To.V == r.SenderKey, nil
		case KindCancellation:
			_, signer, held, err := p.heldSent(ctx, ag.Offer.Digest)
			return held && signer == r.SenderKey, err
		}
		return false, nil
	}
	rec, _, ok, err := p.heldOrPending(ctx, r)
	if err != nil || !ok {
		return false, err
	}
	if rec.Block.RecordType == TypeRuling {
		a, held := o.agreement(rec.Block.DealID)
		return held && a.Adjudicator.V != nil && a.Adjudicator.V.KeyID == r.SenderKey, nil
	}
	if a, held := o.agreement(rec.Block.DealID); held && a.CounterpartyKey.V != nil && *a.CounterpartyKey.V == r.SenderKey {
		return true, nil
	}
	// At an offerer holding only its offer: the acceptor's book, from the
	// key the offer was sent to; its agreement must cite that offer.
	of, digest, ok := o.offer(r.DealID)
	if !ok || of.To.V == nil || *of.To.V != r.SenderKey {
		return false, nil
	}
	if r.Position == 1 {
		ag, isAgree := asAgree(rec.Body)
		return isAgree && ag.Kind == KindAgreement && ag.Offer.Digest == digest, nil
	}
	return true, nil
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
// offer's to, or our agreement's counterparty_key). Exactly one candidate
// gives its signer; none or more refuses the proof.
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
		ag, ok := asAgree(b.Record.Body)
		if !ok || ag.Kind != KindAgreement || ag.CheckpointKey != checkpointKeyID {
			continue
		}
		deal := b.Record.Block.DealID
		var expected string
		if a, ours := o.agreement(deal); ours && a.CounterpartyKey.V != nil {
			expected = *a.CounterpartyKey.V
		} else if of, _, ok := o.offer(deal); ok && of.To.V != nil {
			expected = *of.To.V
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

// CanAct is the coverage gate before the holder's first act of a deal (design
// §7.2, gate B): the counterparty's agreement, whose record id is given, is
// in its sender's counted prefix as the store computes it now, and a held
// stamp proof bound to it covers it: its fact's book id is that record id,
// its checkpoints are signed by the agreement's checkpoint_key, its shelf log
// is the agreement's shelf_log_id, and it covers the agreement among the
// records counted now.
func (p *ReceiveProfile) CanAct(ctx context.Context, counterpartAgreement string) (bool, error) {
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
	if !ok || ag.Kind != KindAgreement {
		return false, nil
	}
	prefix, err := p.Shelf.CountedPrefix(ctx, h.SenderKey, h.LogID)
	if err != nil {
		return false, err
	}
	if prefix.Size < 1 {
		return false, nil
	}
	proofs, err := p.Shelf.HeldProofs(ctx, h.SenderKey, h.LogID)
	if err != nil {
		return false, err
	}
	for _, hp := range proofs {
		v := hp.Verified
		if v.Fact.BookID == counterpartAgreement && v.KeyID == ag.CheckpointKey &&
			v.ShelfCheckpoint.LogID == ag.ShelfLogID && hp.Covers >= 1 {
			return true, nil
		}
	}
	return false, nil
}
