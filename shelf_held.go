// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Lock-free readers a profile calls from a stamp or a Receive
// (evidencebook#461, docs/design/461-deal-profile.md §7.2 and §8.3). The
// store keeps a log id per sender, not across senders, so these keep the
// sender dimension and leave every choice between candidates to the profile.

// HeldFirst is every received record held at position 1 of a log with id
// logID: across senders, and both records of a fork at position 1 from one
// sender, which the store keeps. They come ordered by sender key, then
// digest. A profile matching a stamp proof to its sender reads them to
// refuse a sender whose log forks at its genesis before it filters by
// anything else. It takes no lock.
func (s *Shelf) HeldFirst(ctx context.Context, logID string) ([]HeldRecord, error) {
	return s.held(ctx, `log_id = ? AND position = 1 ORDER BY sender_key, digest`, logID)
}

// HeldByDeal is every received record held for the deal dealID, the deal
// id its profile's Read gave it: across senders and logs, forks included,
// ordered by sender key, log id, position and digest. A profile reading a
// deal's received books at a stamp reads them here, and the counted prefix
// of each with CountedPrefix. It takes no lock.
func (s *Shelf) HeldByDeal(ctx context.Context, dealID string) ([]HeldRecord, error) {
	return s.held(ctx, `deal_id = ? ORDER BY sender_key, log_id, position, digest`, dealID)
}

// held reads the received records the condition where selects; where is one
// of the constant clauses above, never input.
func (s *Shelf) held(ctx context.Context, where string, arg string) ([]HeldRecord, error) {
	if err := s.live(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at FROM shelf_received WHERE `+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeldRecord{}
	for rows.Next() {
		var h HeldRecord
		var at string
		if err := rows.Scan(&h.Digest, &h.SenderKey, &h.LogID, &h.Position, &h.Prev, &h.DealID, &h.Offer, &h.Record, &at); err != nil {
			return nil, err
		}
		if h.ReceivedAt, err = time.Parse(receivedTimeFormat, at); err != nil {
			return nil, fmt.Errorf("%w: received record %s's time: %v", ErrCorrupt, h.Digest, err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HeldProof is a stamp proof the store holds from a sender, as it stands
// against that sender's counted prefix now.
type HeldProof struct {
	// Proof is the proof as received, and Verified what it shows: the fact
	// (its book id), both checkpoints (the shelf log's id) and the
	// checkpoint key that signed them.
	Proof    StampProof
	Verified VerifiedStampProof
	// Recomputed says the records held recomputed the book checkpoint's root,
	// and Contradicted that they did not; neither, that the records held do
	// not reach its size yet.
	Recomputed, Contradicted bool
	// Covers is how many of the sender's currently counted records the proof
	// covers: its book checkpoint's entries, capped to the counted prefix as
	// the store computes it now, once recomputed; 0 otherwise. A fork found
	// after the proof was recomputed lowers it, though the proof stays
	// recomputed.
	Covers     uint64
	ReceivedAt time.Time
}

// HeldProofs is every stamp proof held from senderKey for logID, by size and
// then shelf checkpoint, each re-verified from the bytes kept and set
// against the sender's counted prefix in one read, so a profile can check
// every held proof's bindings (its book id, checkpoint key and shelf log) at
// the moment it acts, never from one Receive call's events or a stored flag
// alone. It takes no lock.
func (s *Shelf) HeldProofs(ctx context.Context, senderKey, logID string) ([]HeldProof, error) {
	if err := s.live(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	prefix, err := receivedPrefix(ctx, tx, logKey{senderKey, logID})
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT book_checkpoint, fact, status_opening, inclusion_proof, shelf_checkpoint, received_at, verified FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? ORDER BY size, shelf_checkpoint_sha256`,
		senderKey, logID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeldProof{}
	for rows.Next() {
		var h HeldProof
		var opening, inclusion []byte
		var at string
		var state int
		if err := rows.Scan(&h.Proof.BookCheckpoint, &h.Proof.Fact, &opening, &inclusion, &h.Proof.ShelfCheckpoint, &at, &state); err != nil {
			return nil, err
		}
		corrupt := func(what string, err error) error {
			return fmt.Errorf("%w: a held stamp proof of %s from %s: %s: %v", ErrCorrupt, logID, senderKey, what, err)
		}
		if opening != nil {
			h.Proof.StatusOpening = &FactOpening{}
			if err := json.Unmarshal(opening, h.Proof.StatusOpening); err != nil {
				return nil, corrupt("its status opening", err)
			}
		}
		if err := json.Unmarshal(inclusion, &h.Proof.Inclusion); err != nil {
			return nil, corrupt("its inclusion proof", err)
		}
		if h.Verified, err = VerifyStampProof(h.Proof); err != nil {
			return nil, corrupt("it no longer verifies", err)
		}
		if h.ReceivedAt, err = time.Parse(receivedTimeFormat, at); err != nil {
			return nil, corrupt("its time", err)
		}
		switch state {
		case proofUnchecked:
		case proofRecomputed:
			h.Recomputed = true
			h.Covers = min(h.Verified.BookCheckpoint.Entries, uint64(len(prefix.counted)))
		case proofContradicted:
			h.Contradicted = true
		default:
			return nil, corrupt("its state", errors.New(fmt.Sprint(state)))
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
