// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/action-state-group/capsule-emit/go/artifact"
)

// LifecycleStatement is the body of a lifecycle record: a later, separately
// committed statement that the cited record's retention state changed. The
// state belongs to the record as a whole, never to one of its payloads, and
// the record that committed the payloads is never rewritten.
//
// A lifecycle record written before retention was kept per record also names
// a payload_commitment; it is read as applying to the whole record.
type LifecycleStatement struct {
	RetentionState RetentionState `json:"retention_state"`
	Reason         string         `json:"reason,omitempty"`
}

// Retention returns the record's current retention state (Evidence Layer
// §6.1): the state its header committed, folded forward through every
// lifecycle record that cites it, in log order. While a retention commitment
// this book issued covers the record and has not expired (CommitRetention),
// an AVAILABLE or PARTIAL record reads as LEGAL_HOLD: the hold blocks
// deletion and changes no resolvability. A WITHHELD record stays WITHHELD
// under a hold, which still blocks its deletion. A record that commits no
// payload and no field has no retention state and is refused.
func (b *Book) Retention(_ context.Context, recordID string) (RetentionState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, _, _, err := b.retentionLocked(recordID)
	return state, err
}

// retentionLocked also returns the id of the newest lifecycle record for the
// record (the record itself when there is none) and whether deletion is
// blocked by a hold.
func (b *Book) retentionLocked(recordID string) (state RetentionState, latest string, held bool, err error) {
	return retentionOf(b.records, b.order, recordID, b.now())
}

// retentionOf is a record's retention among a book's records (order is
// their ids in seq order) at now: retentionLocked over any such view, so a
// shelf can read it without opening the book (Shelf.Records).
func retentionOf(records map[string]Record, order []string, recordID string, now time.Time) (state RetentionState, latest string, held bool, err error) {
	record, ok := records[recordID]
	if !ok {
		return "", "", false, fmt.Errorf("%w: record %s", ErrNotFound, recordID)
	}
	if len(record.Header.PayloadCommitments) == 0 && len(record.Header.FieldCommitments) == 0 {
		return "", "", false, fmt.Errorf("%w: record %s commits no payload or field, so it has no retention state", ErrNotFound, recordID)
	}
	state, latest = record.Header.RetentionState, recordID
	for _, id := range order[record.Seq:] {
		h := records[id].Header
		if h.RecordType != RecordTypeLifecycle || !slices.Contains(h.LinksTo(Cites), recordID) {
			continue
		}
		var statement LifecycleStatement
		if err := json.Unmarshal(h.Statement, &statement); err != nil {
			return "", "", false, fmt.Errorf("%w: lifecycle record %s: %v", ErrCorrupt, id, err)
		}
		state, latest = statement.RetentionState, id
	}
	if state == Deleted {
		return state, latest, false, nil
	}
	held = state == LegalHold || heldBy(records, order, record, now)
	if held && (state == Available || state == Partial) {
		state = LegalHold
	}
	return state, latest, held, nil
}

// heldBy reports whether a retention commitment covers record and has
// not expired at now. A commitment covers a record it names by id, a range
// of positions containing it, or an exchange it cites.
func heldBy(records map[string]Record, order []string, record Record, now time.Time) bool {
	for _, id := range order {
		h := records[id].Header
		if h.RecordType != RecordTypeRetentionCommitment {
			continue
		}
		commitment, ok := readRetentionCommitment(h.Statement)
		if !ok || now.After(commitment.until) {
			continue
		}
		subject := commitment.subject
		switch subject.Kind {
		case SubjectRecord:
			if subject.Digest == record.RecordID {
				return true
			}
		case SubjectRange:
			// A range names leaf indexes, from 0; a record's seq is its
			// leaf index plus one.
			if leaf := record.Seq - 1; leaf >= subject.Range[0] && leaf <= subject.Range[1] {
				return true
			}
		case SubjectExchange:
			if slices.Contains(record.Header.LinksTo(Cites), subject.Digest) {
				return true
			}
		}
	}
	return false
}

// SetRetention appends a lifecycle record moving recordID to state. DELETED
// is permanent and is refused while the record is held: under LEGAL_HOLD set
// by a lifecycle record, or covered by a live retention commitment. Deleting purges
// the record's originals from the artifact store after the lifecycle record
// is committed; the record's own commitments, and every other record's
// originals, are untouched. Asking for DELETED again on a deleted record
// retries a failed purge and returns the existing lifecycle record.
func (b *Book) SetRetention(ctx context.Context, recordID string, state RetentionState, reason string) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return Record{}, ErrReleased
	}
	if !state.Valid() {
		return Record{}, fmt.Errorf("%w: retention state %q", ErrInvalid, state)
	}
	current, latest, held, err := b.retentionLocked(recordID)
	if err != nil {
		return Record{}, err
	}
	switch {
	case current == Deleted && state == Deleted:
		return b.records[latest].detached(), b.purgeLocked(ctx, recordID)
	case current == Deleted:
		return Record{}, fmt.Errorf("%w: record %s is already deleted", ErrInvalid, recordID)
	case held && state == Deleted:
		return Record{}, fmt.Errorf("%w: record %s", ErrLegalHold, recordID)
	}
	statement, err := json.Marshal(LifecycleStatement{RetentionState: state, Reason: reason})
	if err != nil {
		return Record{}, err
	}
	record, err := b.appendLocked(ctx, Entry{
		RecordType: RecordTypeLifecycle, EpistemicType: ObservedEvent,
		Links: []Link{{Type: Cites, Target: recordID}}, Statement: statement,
	})
	if err != nil {
		return record, err
	}
	if state == Deleted {
		if err := b.purgeLocked(ctx, recordID); err != nil {
			return record, fmt.Errorf("lifecycle committed but the originals remain; call SetRetention with DELETED again to retry: %w", err)
		}
	}
	return record, nil
}

// purgeLocked destroys one record's originals in the artifact store. A
// record whose originals were never stored there has nothing to destroy.
func (b *Book) purgeLocked(ctx context.Context, recordID string) error {
	if err := b.originals.Purge(ctx, recordID); err != nil && !errors.Is(err, artifact.ErrNotFound) {
		return err
	}
	return nil
}

// UnavailableError reports why a payload yields no bytes. WITHHELD and
// DELETED both yield none; only DELETED is permanent, so callers read State.
type UnavailableError struct {
	RecordID string
	Digest   string
	State    RetentionState
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("payload %s of record %s is %s", e.Digest, e.RecordID, e.State)
}

func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

// ResolvePayload returns one payload of one record, read from the artifact
// store, when the record's retention state allows it.
func (b *Book) ResolvePayload(ctx context.Context, recordID, digest string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resolveLocked(ctx, recordID, digest)
}

func (b *Book) resolveLocked(ctx context.Context, recordID, digest string) ([]byte, error) {
	record, ok := b.records[recordID]
	if !ok || !slices.Contains(record.Header.PayloadCommitments, digest) {
		return nil, fmt.Errorf("%w: record %s does not commit payload %s", ErrNotFound, recordID, digest)
	}
	state, _, _, err := b.retentionLocked(recordID)
	if err != nil {
		return nil, err
	}
	if state == Withheld || state == Deleted {
		return nil, &UnavailableError{RecordID: recordID, Digest: digest, State: state}
	}
	held, err := b.originals.Get(ctx, recordID)
	if errors.Is(err, artifact.ErrNotFound) {
		return nil, fmt.Errorf("%w: record %s is %s but the artifact store holds none of its originals", ErrCorrupt, recordID, state)
	}
	if err != nil {
		return nil, err
	}
	data, found, present := originalBytes(held, digest)
	if !present {
		what := "does not hold it"
		if found {
			what = "holds it purged"
		}
		return nil, fmt.Errorf("%w: payload %s of %s is %s but the artifact store %s", ErrCorrupt, digest, recordID, state, what)
	}
	return data, nil
}
