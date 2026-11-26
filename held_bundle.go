// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// RecordTypeHeldBundle is a peer bundle a sealed reconcile relied on, held
// in full (evidencebook#116, the custody and record-kind profile v0, item
// 24: "peer bundles held in full when a reconcile relied on them"). Its one
// payload is the bundle's octets as received; its statement is a
// HeldBundleStatement. A Close that cites a peer Close seals one first and
// cites it.
const RecordTypeHeldBundle = "held_bundle"

// HeldBundleReliedByClose is the relied_by of a held_bundle a Close sealed.
const HeldBundleReliedByClose = "close"

// HeldBundleStatement is the statement of a held_bundle record.
//
// BundleDigest is the bundle's own digest (bundle_digest, as VerifyBundle
// reads it: SHA-256 over the bundle's canonical JSON without its
// countersignatures). The record's payload commitment is a different
// digest: SHA-256 of the octets held, which is what Shelf.Held and
// Shelf.HeldBy look a copy up by. The record carries both, so either finds
// the other.
type HeldBundleStatement struct {
	BundleDigest string `json:"bundle_digest"`
	// Peer is the log id the bundle's completeness certificate names.
	Peer string `json:"peer"`
	// ReliedBy is the record type of the record that relies on it.
	ReliedBy string `json:"relied_by"`
}

// heldPeerBundle re-reads a peer bundle from the octets the caller holds, so
// the reconcile a Close seals is computed from exactly the bundle it holds:
// a VerifiedBundle's parsed fields could otherwise describe one bundle while
// its Bytes are another's.
func heldPeerBundle(peer VerifiedBundle) (VerifiedBundle, error) {
	if len(peer.Bytes) == 0 {
		return VerifiedBundle{}, fmt.Errorf("%w: a Close citing a peer Close holds the peer bundle in full, so the peer bundle needs its bytes (VerifyBundle keeps them)", ErrInvalid)
	}
	derived, err := VerifyBundle(peer.Bytes)
	if err != nil {
		return VerifiedBundle{}, fmt.Errorf("%w: the peer bundle's bytes: %v", ErrInvalid, err)
	}
	if derived.Digest == "" || derived.Digest != peer.Digest {
		return VerifiedBundle{}, fmt.Errorf("%w: the peer bundle's bytes are bundle %q, not the bundle %q it names", ErrInvalid, derived.Digest, peer.Digest)
	}
	return derived, nil
}

// holdBundleLocked returns the id of the held_bundle record holding peer for
// a record of type reliedBy: one the log already has, if an earlier attempt
// sealed it and no record cites it yet (a Close that failed after it), or a
// new one appended now.
func (b *Book) holdBundleLocked(ctx context.Context, peer VerifiedBundle, counterparty, reliedBy string) (string, error) {
	payload := digestBytes(peer.Bytes)
	statement := HeldBundleStatement{BundleDigest: peer.Digest, Peer: peer.LogID, ReliedBy: reliedBy}
	for i := len(b.log) - 1; i >= 0; i-- {
		r := b.log[i]
		if r.Header.RecordType != RecordTypeHeldBundle || !slices.Equal(r.Header.PayloadCommitments, []string{payload}) || r.Header.CounterpartyRef != counterparty {
			continue
		}
		var held HeldBundleStatement
		if json.Unmarshal(r.Header.Statement, &held) != nil || held != statement || b.citedLocked(r.RecordID) {
			continue
		}
		if state, _, _, err := b.retentionLocked(r.RecordID); err != nil || state != Available {
			continue
		}
		return r.RecordID, nil
	}
	raw, err := json.Marshal(statement)
	if err != nil {
		return "", err
	}
	record, err := b.appendLocked(ctx, Entry{
		RecordType: RecordTypeHeldBundle, EpistemicType: ObservedEvent, CounterpartyRef: counterparty,
		Payloads: [][]byte{peer.Bytes}, Statement: raw,
	})
	if err != nil {
		return "", fmt.Errorf("holding the peer bundle: %w", err)
	}
	return record.RecordID, nil
}

// citedLocked reports whether any record other than a lifecycle record
// cites id.
func (b *Book) citedLocked(id string) bool {
	return slices.ContainsFunc(b.log, func(r Record) bool {
		return r.Header.RecordType != RecordTypeLifecycle && slices.Contains(r.Header.LinksTo(Cites), id)
	})
}

// keptCiterLocked returns a record that cites the held_bundle record id and
// is kept: any record but a lifecycle record whose own retention is not
// DELETED. A record with no retention state (a Close, a Result) is kept for
// good, so the bundle it relied on is too.
func (b *Book) keptCiterLocked(id string) (string, error) {
	for _, r := range b.log {
		if r.Header.RecordType == RecordTypeLifecycle || !slices.Contains(r.Header.LinksTo(Cites), id) {
			continue
		}
		if len(r.Header.PayloadCommitments) == 0 && len(r.Header.FieldCommitments) == 0 {
			return r.RecordID, nil
		}
		state, _, _, err := b.retentionLocked(r.RecordID)
		if err != nil {
			return "", err
		}
		if state != Deleted {
			return r.RecordID, nil
		}
	}
	return "", nil
}
