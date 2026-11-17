// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
)

// A sent journal record (evidencebook#461, docs/design/461-deal-profile.md
// §3.1): a journal record that leaves the shelf whole, such as an offer, must
// not show its place in the journal, which a book record's Capsule does
// (recordLocator). So it is sealed twice, in one call: as a journal record
// whose one payload commitment is the SHA-256 of the record's bytes, and as a
// standalone Capsule, appended to no log, that carries the same bytes as its
// payload (its agent_input_digest is the bytes' SHA-256, the record digest)
// under the caller's action_id, fyi, with no chain, signed by the journal's
// key. The standalone Capsule is what travels. The call is
// profile-neutral: the bytes are the profile's, and sending them is the
// host's.

// SentCapsule is a sent record's standalone Capsule: its id, its bytes, its
// Producer Envelope (which carries the signature) and the payload it commits,
// the record's bytes, which travel with it.
type SentCapsule struct {
	CapsuleID string
	Capsule   []byte
	Envelope  []byte
	Payload   []byte
}

// SealSent appends entry to the shelf's journal and seals its standalone
// Capsule under actionID. entry carries exactly one payload, the record's
// canonical JSON (JCS) bytes, so the journal record's payload commitment and
// the Capsule's payload are the same bytes. The Capsule is sealed first and
// the journal record appended under the journal's lock. An error with an
// empty Record left no journal record, and the call may be retried. An error
// with a Record (the record was stored and its log append is pending, or it
// was committed and a later step failed) comes with its Capsule: the record
// is in the journal, so the caller sends that Capsule and does not seal the
// record again.
func (s *Shelf) SealSent(ctx context.Context, entry Entry, actionID string) (Record, SentCapsule, error) {
	if actionID == "" {
		return Record{}, SentCapsule{}, fmt.Errorf("%w: a sent record needs an action_id", ErrInvalid)
	}
	if len(entry.Payloads) != 1 {
		return Record{}, SentCapsule{}, fmt.Errorf("%w: a sent record carries exactly one payload, its record", ErrInvalid)
	}
	payload := entry.Payloads[0]
	decoded, err := decodeJSON(payload)
	if err != nil {
		return Record{}, SentCapsule{}, fmt.Errorf("%w: a sent record's payload is JSON: %v", ErrInvalid, err)
	}
	if canon, err := canonicalJSON(decoded); err != nil || !bytes.Equal(canon, payload) {
		return Record{}, SentCapsule{}, fmt.Errorf("%w: a sent record's payload is its canonical JSON (JCS) bytes", ErrInvalid)
	}
	journal, err := s.Journal(ctx)
	if err != nil {
		return Record{}, SentCapsule{}, err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.released {
		return Record{}, SentCapsule{}, ErrReleased
	}
	identity, err := signingIdentity(ctx, journal.signer)
	if err != nil {
		return Record{}, SentCapsule{}, err
	}
	sealed, err := emit.Seal(emit.SealInput{
		Capsule: emit.Input{
			ActionID: actionID, ActionType: emit.ActionTypeFYI,
			Operator: journal.operator, Developer: journal.developer,
			Timestamp: journal.now().UTC().Truncate(time.Second),
		},
		Payload:  json.RawMessage(payload),
		Identity: identity,
	})
	if err != nil {
		return Record{}, SentCapsule{}, fmt.Errorf("seal the sent record: %w", err)
	}
	entry.Payloads = [][]byte{bytes.Clone(payload)}
	pendingBefore := journal.pending
	record, err := journal.appendLocked(ctx, entry)
	if record.RecordID == "" && journal.pending != nil && journal.pending != pendingBefore {
		// Stored, but its log append failed: the book keeps it pending and
		// commits it first on its next write or open, so it is in the
		// journal as surely as a committed one.
		record = journal.pending.detached()
	}
	if record.RecordID == "" {
		return Record{}, SentCapsule{}, err
	}
	// A record in the journal with an error (stored and pending, or
	// committed and its automatic checkpoint failed) is returned with its
	// Capsule: the caller sends it and must not seal it again.
	return record, SentCapsule{CapsuleID: sealed.CapsuleID, Capsule: sealed.Payload, Envelope: sealed.Envelope, Payload: bytes.Clone(payload)}, err
}
