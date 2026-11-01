// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

// A staple is a History card the holder attaches to its own message (a
// deal opening, an offer, a commitment), so a counterparty with no way to
// ask back can still read it (evidencebook#275, frame 4 "Stapled"; the
// cross-ecosystem admission design, "The stapled history").
//
// It is built from the evidence request's own objects, so it adds no wire
// member: the holder's request names the derivation, pins the coverage to
// the holder's newest checkpoint (the staple's as-of) and carries a fresh
// nonce; the holder's book answers it with the signed envelope it would
// give any requester. A reader detects a replayed staple by the nonce and
// the anchor against its own min_freshness. Where the staple sits, and the
// witness Receipts that travel beside a card (SH-4), are the carrier's.
//
// A staple never carries more than the standard card: only history_card/1
// (over the checkpoints subject) and standard_history/1 (over full_history)
// are stapled, both recipient-free, so the bytes are the ones every
// requester those cards are answered to gets.
type Staple struct {
	// Request is the holder's own request, as JSON.
	Request Message
	// Reply is the holder's signed envelope answering Request, as JSON.
	Reply Reply
}

// stapleSubjects is the cap: each derivation a staple may carry and the
// one subject it is answered over.
var stapleSubjects = map[string]SubjectKind{
	DerivationHistoryCard:     SubjectCheckpoints,
	DerivationStandardHistory: SubjectFullHistory,
}

// Staple builds a staple of derivation, history_card/1 or standard_history/1,
// as of the book's newest checkpoint (cut now if the book grew). Any other
// derivation is ErrInvalid: a staple is shown to whoever reads the message
// it rides on, so it is capped at the standard card. Whether to staple at
// all is the host's disclosure setting ("attach it when I buy or sell");
// the book has no default.
//
// standard_history/1 needs Config.StandardHistory. Its answer is sealed as
// an evidence_request_answered record, as for any authenticated requester
// (the holder answers itself), so the holder's book records which standard
// card it attached; history_card/1 is public and grows no record. The
// book's own request is not counted by its limiter.
func (b *Book) Staple(ctx context.Context, derivation string) (Staple, error) {
	subject, ok := stapleSubjects[derivation]
	if !ok {
		return Staple{}, fmt.Errorf("%w: a staple carries %s or %s, not %q", ErrInvalid, DerivationHistoryCard, DerivationStandardHistory, derivation)
	}
	checkpoint, err := b.Checkpoint(ctx)
	if err != nil {
		return Staple{}, err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return Staple{}, err
	}
	octets, err := EncodeRequest(EvidenceRequest{
		Subject: Subject{Kind: subject}, Derivation: derivation,
		Coverage: Coverage{ExpectedPin: anchorDigest(checkpoint)},
		Nonce:    hex.EncodeToString(nonce), Profile: ProfileID,
	}, MediaTypeJSON)
	if err != nil {
		return Staple{}, err
	}
	request := Message{Octets: octets, MediaType: MediaTypeJSON}
	// The share policy gates record-naming subjects only; neither card is one.
	opts := RespondOptions{Policy: DefaultSharePolicy()}
	if derivation == DerivationStandardHistory {
		opts.AuthenticatedRequester = KeyID(b.signer)
	}
	reply, _, err := b.respond(ctx, request, opts, true)
	if err != nil {
		return Staple{}, err
	}
	if reply.Envelope == nil {
		reason := ""
		if reply.Refusal != nil {
			reason = reply.Refusal.Reason
		}
		return Staple{}, fmt.Errorf("%w: the book refused its own %s request (%s)", ErrInvalid, derivation, reason)
	}
	return Staple{Request: request, Reply: reply}, nil
}

// StapleCheck is what CheckStaple read from a staple that passed.
type StapleCheck struct {
	Derivation string
	// Anchor is the digest of the holder checkpoint the card is as of.
	Anchor string
	// Nonce is the holder's nonce, for the reader's replay check.
	Nonce    string
	IssuedAt string
	// Artifact is the card, in its canonical octets.
	Artifact []byte
}

// CheckStaple checks that a staple is presented by holder (the lowercase
// hex Ed25519 key the reader names for its counterparty) and stays inside
// the cap: the envelope verifies under holder's key and answers this very
// request, the derivation is history_card/1 over checkpoints or
// standard_history/1 over full_history, the anchor and profile are the
// ones the request named, and a standard_history/1 card is of the standard
// card's form, and a history_card/1 artifact has the card's members and
// nothing else (historyCardForm). It does not check the anchor's
// checkpoint, the card's own properties or the nonce's freshness: those
// are the reader's verifier and policy.
func CheckStaple(s Staple, holder string) (StapleCheck, error) {
	if s.Request.MediaType != MediaTypeJSON || s.Reply.MediaType != MediaTypeJSON {
		return StapleCheck{}, fmt.Errorf("%w: a staple's request and reply are JSON", ErrInvalid)
	}
	parsed := evidencerequest.ParseRequest(s.Request)
	if parsed.Reason != "" {
		return StapleCheck{}, fmt.Errorf("%w: a staple's request is malformed: %s", ErrInvalid, parsed.Why)
	}
	req := parsed.Request
	subject, ok := stapleSubjects[req.Derivation]
	if !ok || req.Subject.Kind != subject {
		return StapleCheck{}, fmt.Errorf("%w: a staple carries %s over %s or %s over %s, not %q over %s", ErrInvalid,
			DerivationHistoryCard, SubjectCheckpoints, DerivationStandardHistory, SubjectFullHistory, req.Derivation, req.Subject.Kind)
	}
	if req.Coverage.ExpectedPin == "" || req.Nonce == "" {
		return StapleCheck{}, fmt.Errorf("%w: a staple's request pins its anchor and carries a nonce", ErrInvalid)
	}
	reply, err := ParseReply(s.Reply.Message)
	if err != nil {
		return StapleCheck{}, err
	}
	e := reply.Envelope
	if e == nil {
		return StapleCheck{}, fmt.Errorf("%w: a staple's reply is a refusal", ErrInvalid)
	}
	if err := e.Verify(); err != nil {
		return StapleCheck{}, err
	}
	switch {
	case e.Responder != holder:
		return StapleCheck{}, fmt.Errorf("%w: the staple is not presented by its holder", ErrInvalid)
	case e.RequestDigest != digestBytes(s.Request.Octets):
		return StapleCheck{}, fmt.Errorf("%w: the staple's envelope answers another request", ErrInvalid)
	case e.Derivation != req.Derivation || e.Subject.Kind != req.Subject.Kind:
		return StapleCheck{}, fmt.Errorf("%w: the staple's envelope signs another derivation or subject", ErrInvalid)
	case e.Anchor.Digest != req.Coverage.ExpectedPin:
		return StapleCheck{}, fmt.Errorf("%w: the staple's envelope is as of another anchor", ErrInvalid)
	case e.Profile != cmp.Or(req.Profile, ProfileID):
		// Verify leaves the profile to its caller (profile §3.3 step 5).
		return StapleCheck{}, fmt.Errorf("%w: the staple's envelope names another profile", ErrInvalid)
	}
	if req.Derivation == DerivationHistoryCard {
		if err := historyCardForm(e.Artifact, e.Anchor.Digest); err != nil {
			return StapleCheck{}, err
		}
	}
	if req.Derivation == DerivationStandardHistory {
		properties, err := standardhistory.Check(e.Artifact, nil, nil, nil)
		if err != nil {
			return StapleCheck{}, err
		}
		for _, property := range standardhistory.Properties {
			if properties[property] == standardhistory.Fail {
				return StapleCheck{}, fmt.Errorf("%w: the stapled card fails %s", ErrInvalid, property)
			}
		}
	}
	return StapleCheck{Derivation: req.Derivation, Anchor: e.Anchor.Digest, Nonce: req.Nonce, IssuedAt: e.IssuedAt, Artifact: e.Artifact}, nil
}

// historyCardForm checks that a history_card/1 artifact (ER §8.1) has the
// card's members and nothing else: anchor, derivation, evidence_stream,
// history and subject, each history entry exactly a checkpoint and the
// consistency proof from the one before (null for the first), as lowercase
// hex, the last checkpoint being the anchor. It checks no signature or
// proof: the checkpoint key is the reader's.
func historyCardForm(artifact []byte, anchor string) error {
	bad := func(why string) error { return fmt.Errorf("%w: the stapled history_card/1 %s", ErrInvalid, why) }
	decoded, err := evidencerequest.DecodeJSON(artifact)
	card, ok := decoded.(map[string]any)
	if err != nil || !ok {
		return bad("is not a JSON object")
	}
	if !keysExactly(card, "anchor", "derivation", "evidence_stream", "history", "subject") {
		return bad("has members other than the card's")
	}
	stream, _ := card["evidence_stream"].(string)
	subject, _ := card["subject"].(map[string]any)
	if card["derivation"] != DerivationHistoryCard || card["anchor"] != anchor || stream == "" ||
		len(subject) != 1 || !keysExactly(subject, "checkpoints") || subject["checkpoints"] != nil {
		return bad("does not name its derivation, anchor, stream and subject")
	}
	history, _ := card["history"].([]any)
	if len(history) == 0 {
		return bad("has no history")
	}
	for i, item := range history {
		entry, _ := item.(map[string]any)
		if !keysExactly(entry, "checkpoint", "consistency_from_previous") {
			return bad(fmt.Sprintf("entry %d has members other than a checkpoint and its consistency proof", i))
		}
		checkpoint, _ := entry["checkpoint"].(string)
		proof, isText := entry["consistency_from_previous"].(string)
		if !lowerHex(checkpoint) || (i == 0) != (entry["consistency_from_previous"] == nil) || (i > 0 && (!isText || !lowerHex(proof))) {
			return bad(fmt.Sprintf("entry %d is not a hex checkpoint with its consistency proof", i))
		}
		if i == len(history)-1 {
			raw, _ := hex.DecodeString(checkpoint)
			if digestBytes(raw) != anchor {
				return bad("does not end at its anchor")
			}
		}
	}
	return nil
}

// keysExactly reports whether m has exactly the members names.
func keysExactly(m map[string]any, names ...string) bool {
	if m == nil || len(m) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := m[name]; !ok {
			return false
		}
	}
	return true
}

// lowerHex reports whether s is non-empty lowercase hex of whole octets.
func lowerHex(s string) bool {
	if s == "" || len(s)%2 != 0 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
