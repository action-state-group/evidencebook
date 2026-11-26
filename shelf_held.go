// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/action-state-group/capsule-emit/go/artifact"
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

// The held store (evidencebook#116, the custody and record-kind profile v0,
// item 24): every foreign item the profile keeps, found by its digest. A
// record's originals live in the profile's artifact store under the record
// that commits them, and identical bytes under two records are two copies
// with their own retention, so a digest alone names no one copy: Held reads
// one copy by its citing record, and HeldBy lists every copy. The digest is
// the SHA-256 of the octets held (a payload commitment), which for a
// held_bundle record is not its statement's bundle_digest.

// HeldCopy is one copy of a held item, as HeldBy lists it.
type HeldCopy struct {
	// BookID and RecordID name the record whose original it is; both are
	// empty for the received store's copy (Received).
	BookID, RecordID string
	// Received marks the copy the received store holds under the digest
	// (Shelf.Receive), found by the record's own digest.
	Received bool
	// State is the copy's retention state: the citing record's, as
	// Book.Retention reads it; for the received store's copy, AVAILABLE, or
	// DELETED when only its tombstone is left.
	State RetentionState
	// Present says the store still holds the bytes.
	Present bool
}

// Held returns the original that the record citingRecordID commits as
// digest, from the profile's artifact store. A copy whose record is WITHHELD
// or DELETED yields an *UnavailableError naming that state (a DELETED
// copy's tombstone): that copy's state only, never the digest's, since
// another record may hold the same bytes (HeldBy). A record that does not
// commit digest is ErrNotFound. It reads any of the shelf's logs, its
// journal included, and opens no book.
func (s *Shelf) Held(ctx context.Context, citingRecordID, digest string) ([]byte, error) {
	if err := s.live(); err != nil {
		return nil, err
	}
	var bookID string
	switch err := s.db.QueryRowContext(ctx, `SELECT book_id FROM book_records WHERE record_id = ?`, citingRecordID).Scan(&bookID); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("%w: record %s", ErrNotFound, citingRecordID)
	case err != nil:
		return nil, err
	}
	listed, err := s.Records(ctx, bookID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(listed.Records, func(r ShelfRecord) bool { return r.RecordID == citingRecordID })
	if i < 0 {
		return nil, fmt.Errorf("%w: record %s is not committed", ErrNotFound, citingRecordID)
	}
	record := listed.Records[i]
	if !slices.Contains(record.Header.PayloadCommitments, digest) {
		return nil, fmt.Errorf("%w: record %s does not commit payload %s", ErrNotFound, citingRecordID, digest)
	}
	if record.Retention == Withheld || record.Retention == Deleted {
		return nil, &UnavailableError{RecordID: citingRecordID, Digest: digest, State: record.Retention}
	}
	data, present, err := s.original(ctx, citingRecordID, digest)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("%w: payload %s of %s is %s but the artifact store does not hold it", ErrCorrupt, digest, citingRecordID, record.Retention)
	}
	return data, nil
}

// HeldBy lists every copy of digest the shelf holds: one per committed
// record of any of its logs that commits it as a payload, in book id then
// seq order, each with its own state, and then the received store's copy
// when the store holds a record, or its tombstone, under that digest. No
// copy's deletion is reported as another's. An empty list means the shelf
// holds no copy.
func (s *Shelf) HeldBy(ctx context.Context, digest string) ([]HeldCopy, error) {
	if err := s.live(); err != nil {
		return nil, err
	}
	if !hex64.MatchString(digest) {
		return nil, fmt.Errorf("%w: %q is not a SHA-256 digest", ErrInvalid, digest)
	}
	// Only books whose stored headers mention the digest can commit it; the
	// decoded headers decide.
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT book_id FROM book_records WHERE instr(header, ?) > 0 ORDER BY book_id`, digest)
	if err != nil {
		return nil, err
	}
	var books []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		books = append(books, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	out := []HeldCopy{}
	if len(books) > 0 {
		listed, err := s.Records(ctx, books...)
		if err != nil {
			return nil, err
		}
		for _, r := range listed.Records {
			if !slices.Contains(r.Header.PayloadCommitments, digest) {
				continue
			}
			_, present, err := s.original(ctx, r.RecordID, digest)
			if err != nil {
				return nil, err
			}
			out = append(out, HeldCopy{BookID: r.BookID, RecordID: r.RecordID, State: r.Retention, Present: present})
		}
	}
	switch _, err := s.ReceivedRecordOf(ctx, digest); {
	case err == nil:
		out = append(out, HeldCopy{Received: true, State: Available, Present: true})
	case errors.Is(err, ErrDeleted):
		out = append(out, HeldCopy{Received: true, State: Deleted})
	case !errors.Is(err, ErrNotFound):
		return nil, err
	}
	return out, nil
}

// original reads the original that recordID commits as digest from the
// artifact store: present is false when the store holds no such original or
// holds it purged.
func (s *Shelf) original(ctx context.Context, recordID, digest string) (data []byte, present bool, err error) {
	held, err := s.cfg.Originals.Get(ctx, recordID)
	if errors.Is(err, artifact.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	data, _, present = originalBytes(held, digest)
	return data, present, nil
}
