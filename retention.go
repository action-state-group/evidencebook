// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// LifecycleStatement is the body of a lifecycle record: a later, separately
// committed statement that one payload of the cited record changed retention
// state. The record that committed the payload is never rewritten.
type LifecycleStatement struct {
	PayloadCommitment string         `json:"payload_commitment"`
	RetentionState    RetentionState `json:"retention_state"`
	Reason            string         `json:"reason,omitempty"`
}

// Retention returns the current retention state of one payload of one
// record: the state the record committed, folded forward through every
// lifecycle record that cites that record for that payload, in log order.
// State belongs to the (record, payload) pair, so two records that commit
// identical bytes have independent retention.
func (b *Book) Retention(_ context.Context, recordID, digest string) (RetentionState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, _, err := b.retentionLocked(recordID, digest)
	return state, err
}

// retentionLocked also returns the id of the newest lifecycle record for the
// pair, or the record itself when there is none.
func (b *Book) retentionLocked(recordID, digest string) (RetentionState, string, error) {
	record, ok := b.records[recordID]
	if !ok || !slices.Contains(record.Header.PayloadCommitments, digest) {
		return "", "", fmt.Errorf("%w: record %s does not commit payload %s", ErrNotFound, recordID, digest)
	}
	state, latest := record.Header.RetentionState, recordID
	for _, id := range b.order[record.Seq:] {
		h := b.records[id].Header
		if h.RecordType != RecordTypeLifecycle || !slices.Contains(h.LinksTo(Cites), recordID) {
			continue
		}
		var statement LifecycleStatement
		if err := json.Unmarshal(h.Statement, &statement); err != nil {
			return "", "", fmt.Errorf("%w: lifecycle record %s: %v", ErrCorrupt, id, err)
		}
		if statement.PayloadCommitment == digest {
			state, latest = statement.RetentionState, id
		}
	}
	return state, latest, nil
}

// SetRetention appends a lifecycle record moving one payload of recordID to
// state. DELETED is permanent and is blocked while a legal hold stands. The
// bytes are destroyed after the lifecycle record is committed, and only when
// no other record still holds the same bytes in a resolvable state. Asking
// for DELETED again on a deleted payload retries a failed byte deletion and
// returns the existing lifecycle record.
func (b *Book) SetRetention(ctx context.Context, recordID, digest string, state RetentionState, reason string) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !state.Valid() {
		return Record{}, fmt.Errorf("%w: retention state %q", ErrInvalid, state)
	}
	current, latest, err := b.retentionLocked(recordID, digest)
	if err != nil {
		return Record{}, err
	}
	switch {
	case current == Deleted && state == Deleted:
		return b.records[latest], b.deleteBytesLocked(ctx, digest)
	case current == Deleted:
		return Record{}, fmt.Errorf("%w: payload %s of %s is already deleted", ErrInvalid, digest, recordID)
	case current == LegalHold && state == Deleted:
		return Record{}, fmt.Errorf("%w: payload %s of %s", ErrLegalHold, digest, recordID)
	}
	statement, err := json.Marshal(LifecycleStatement{PayloadCommitment: digest, RetentionState: state, Reason: reason})
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
		if err := b.deleteBytesLocked(ctx, digest); err != nil {
			return record, fmt.Errorf("lifecycle committed but payload bytes remain; call SetRetention with DELETED again to retry: %w", err)
		}
	}
	return record, nil
}

// deleteBytesLocked removes payload bytes unless another record still
// commits the same digest in a state that must resolve.
func (b *Book) deleteBytesLocked(ctx context.Context, digest string) error {
	for _, id := range b.order {
		if !slices.Contains(b.records[id].Header.PayloadCommitments, digest) {
			continue
		}
		state, _, err := b.retentionLocked(id, digest)
		if err != nil {
			return err
		}
		if state != Deleted {
			return nil
		}
	}
	return b.payloads.Delete(ctx, digest)
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

// ResolvePayload returns one payload of one record when its retention state
// allows it.
func (b *Book) ResolvePayload(ctx context.Context, recordID, digest string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resolveLocked(ctx, recordID, digest)
}

func (b *Book) resolveLocked(ctx context.Context, recordID, digest string) ([]byte, error) {
	state, _, err := b.retentionLocked(recordID, digest)
	if err != nil {
		return nil, err
	}
	if state == Withheld || state == Deleted {
		return nil, &UnavailableError{RecordID: recordID, Digest: digest, State: state}
	}
	data, err := b.payloads.Resolve(ctx, digest)
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%w: payload %s of %s is %s but its bytes are missing", ErrCorrupt, digest, recordID, state)
	}
	return data, err
}
