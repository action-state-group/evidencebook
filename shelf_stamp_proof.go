// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// StampProof shows that a holder's shelf log carries a fact of one of its
// books (evidencebook#461 §3): the fact, the book checkpoint it carries, the
// fact's inclusion in the shelf log and the shelf checkpoint that includes
// it. One checkpoint key signs both checkpoints: the proof's key. Its wire
// form on the MCP and A2A bindings is the AAC item's (§5); this is the
// library's form.
type StampProof struct {
	// Fact is the fact document's canonical JSON, a book fact.
	Fact []byte `json:"fact"`
	// StatusOpening is the fact's status opening, when the audience the
	// proof was made for may have it.
	StatusOpening *FactOpening `json:"status_opening,omitempty"`
	// BookCheckpoint is the COSE statement of the book checkpoint the fact
	// carries.
	BookCheckpoint []byte `json:"book_checkpoint"`
	// Inclusion is the fact entry's inclusion proof in the shelf log, at
	// its entry position, under ShelfCheckpoint.
	Inclusion InclusionEvidence `json:"inclusion"`
	// ShelfCheckpoint is the COSE statement of the shelf checkpoint.
	ShelfCheckpoint []byte `json:"shelf_checkpoint"`
}

// VerifiedStampProof is what a stamp proof shows once verified.
type VerifiedStampProof struct {
	Fact            BookFact
	BookCheckpoint  Checkpoint
	ShelfCheckpoint Checkpoint
	// KeyID is the checkpoint key that signed both checkpoints.
	KeyID string
}

// VerifyStampProof checks a stamp proof on its own (§3, "the proof"): both
// checkpoints verify and are signed by one key; the fact is a canonical book
// fact whose log and size are the book checkpoint's, and whose
// checkpoint_sha256 is the statement's digest; a status opening given
// recomputes its commitment; and the fact's entry is included under the
// shelf checkpoint. What the proof says against the records held is the
// store's (Receive).
func VerifyStampProof(p StampProof) (VerifiedStampProof, error) {
	bad := func(format string, args ...any) (VerifiedStampProof, error) {
		return VerifiedStampProof{}, fmt.Errorf("%w: stamp proof: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	book, err := checkpointFromStatement(p.BookCheckpoint)
	if err != nil {
		return bad("the book checkpoint: %v", err)
	}
	shelf, err := checkpointFromStatement(p.ShelfCheckpoint)
	if err != nil {
		return bad("the shelf checkpoint: %v", err)
	}
	if book.KeyID != shelf.KeyID {
		return bad("the book checkpoint is signed by %s, the shelf checkpoint by %s", book.KeyID, shelf.KeyID)
	}
	var f BookFact
	if err := validJSONText(p.Fact); err != nil {
		return bad("the fact: %v", err)
	}
	if err := canonicalDocument(p.Fact, &f); err != nil {
		return bad("the fact: %v", err)
	}
	if f.V != 1 || f.Kind != FactKindBook {
		return bad("a %q of version %d, not a book fact", f.Kind, f.V)
	}
	if f.LogID != book.LogID || f.Size != book.TreeSize {
		return bad("the fact names %s at %d, its book checkpoint %s at %d", f.LogID, f.Size, book.LogID, book.TreeSize)
	}
	sum := sha256.Sum256(p.BookCheckpoint)
	if hex.EncodeToString(sum[:]) != f.CheckpointSHA256 {
		return bad("the book checkpoint does not hash to the fact's checkpoint_sha256")
	}
	if p.StatusOpening != nil {
		if err := p.StatusOpening.Verify(f.StatusCommitment); err != nil {
			return bad("the status opening: %v", err)
		}
	}
	if p.Inclusion.Kind != "cll-mmr-inclusion" || !shelf.Covers(p.Inclusion.Seq) {
		return bad("no CLL inclusion proof of an entry the shelf checkpoint covers")
	}
	var wire MMRInclusion
	if err := json.Unmarshal(p.Inclusion.Proof, &wire); err != nil {
		return bad("the inclusion proof: %v", err)
	}
	decode := func(values []string) ([][]byte, bool) {
		out := make([][]byte, len(values))
		for i, v := range values {
			b, err := decodeRecordID(v)
			if err != nil {
				return nil, false
			}
			out[i] = b
		}
		return out, true
	}
	witness, ok1 := decode(wire.Witness)
	left, ok2 := decode(wire.PeaksLeft)
	right, ok3 := decode(wire.PeaksRight)
	root, err := decodeRecordID(shelf.Root)
	if !ok1 || !ok2 || !ok3 || err != nil {
		return bad("the inclusion proof is not hex digests")
	}
	entry := sha256.Sum256(p.Fact)
	proof := mmr.InclusionProof{V: wire.V, Kind: wire.Kind, Size: wire.Size, LeafIndex: wire.LeafIndex, Witness: witness, PeaksLeft: left, PeaksRight: right}
	if !mmr.VerifyInclusion(root, shelf.TreeSize, p.Inclusion.Seq-1, entry[:], proof) {
		return bad("the fact's entry is not included under the shelf checkpoint")
	}
	return VerifiedStampProof{Fact: f, BookCheckpoint: book, ShelfCheckpoint: shelf, KeyID: book.KeyID}, nil
}

// StampProof builds the proof of a book's latest fact at stamp n (the newest
// finished stamp when n is 0), for an audience: the status opening is
// included when OpeningsFor lets that audience have it, and the link opening
// never is. The journal's facts are proved to nobody.
func (c *Cadence) StampProof(ctx context.Context, bookID string, n uint64, audience Audience) (StampProof, error) {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return StampProof{}, ErrReleased
	}
	_, withStatus, err := OpeningsFor(FactKindBook, audience)
	if err != nil {
		return StampProof{}, err
	}
	if n == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0`).Scan(&n); err != nil {
			return StampProof{}, err
		}
	}
	tick, err := c.tickLocked(ctx, n)
	if err != nil {
		return StampProof{}, err
	}
	if tick.Leaves.Stamp == nil || tick.CheckpointSize == 0 {
		return StampProof{}, fmt.Errorf("%w: tick %d is not a finished stamp", ErrInvalid, n)
	}
	anchor, err := checkpointFromStatement(tick.Checkpoint)
	if err != nil {
		return StampProof{}, err
	}
	var seq uint64
	var kind string
	var doc, so []byte
	err = s.db.QueryRowContext(ctx,
		`SELECT entry_seq, kind, doc, status_opening FROM shelf_entries WHERE book_id = ? AND kind IN (?, ?) AND entry_seq <= ? ORDER BY entry_seq DESC LIMIT 1`,
		bookID, FactKindBook, FactKindJournal, anchor.Entries).Scan(&seq, &kind, &doc, &so)
	if errors.Is(err, sql.ErrNoRows) {
		return StampProof{}, fmt.Errorf("%w: no fact of book %s at stamp %d", ErrNotFound, bookID, n)
	}
	if err != nil {
		return StampProof{}, err
	}
	if kind != FactKindBook {
		return StampProof{}, fmt.Errorf("%w: the journal's facts are proved to nobody", ErrInvalid)
	}
	var f BookFact
	if err := json.Unmarshal(doc, &f); err != nil {
		return StampProof{}, fmt.Errorf("%w: stored fact %d: %v", ErrCorrupt, seq, err)
	}
	out := StampProof{Fact: doc, ShelfCheckpoint: tick.Checkpoint}
	if withStatus {
		out.StatusOpening = &FactOpening{}
		if err := json.Unmarshal(so, out.StatusOpening); err != nil {
			return StampProof{}, fmt.Errorf("%w: stored status opening %d: %v", ErrCorrupt, seq, err)
		}
	}
	// The statement is in the leaves of the stamp that cut the checkpoint:
	// the newest one up to n that names it.
	for t := n; t >= 1 && out.BookCheckpoint == nil; t-- {
		got, err := c.tickLocked(ctx, t)
		if err != nil {
			return StampProof{}, err
		}
		if leaf, ok := got.Leaves.Deals[f.LogID]; ok && got.Leaves.Stamp != nil && leaf.CheckpointSHA256 == f.CheckpointSHA256 && leaf.Statement != "" {
			if out.BookCheckpoint, err = base64.RawURLEncoding.DecodeString(leaf.Statement); err != nil {
				return StampProof{}, fmt.Errorf("%w: tick %d's statement of %s: %v", ErrCorrupt, t, f.LogID, err)
			}
		}
	}
	if out.BookCheckpoint == nil {
		return StampProof{}, fmt.Errorf("%w: no stored statement for %s's checkpoint %s", ErrCorrupt, f.LogID, f.CheckpointSHA256)
	}
	if out.Inclusion, err = c.log.ProveInclusion(ctx, seq, anchor); err != nil {
		return StampProof{}, err
	}
	return out, nil
}

// receiveProof handles a Receive call's proof part (§3): it verifies the
// proof, asks the profile whose records it covers, refuses one that
// contradicts a held proof at the same size or the records held (keeping
// the evidence), and keeps every proof accepted, two proofs of one book
// checkpoint under different shelf checkpoints both.
func (s *Shelf) receiveProof(ctx context.Context, profile ReceiveProfile, p StampProof, changes changeSet) (ProofOutcome, error) {
	var out ProofOutcome
	refuse := func(err error) (ProofOutcome, error) {
		out.Refused = err
		return out, nil
	}
	v, err := VerifyStampProof(p)
	if err != nil {
		return refuse(err)
	}
	sender, ok, err := profile.ProofSender(ctx, v.KeyID, v.Fact.LogID)
	if err != nil {
		return ProofOutcome{}, err
	}
	if !ok {
		return refuse(fmt.Errorf("%w: %s may not push a stamp proof of %s", ErrRefused, v.KeyID, v.Fact.LogID))
	}
	key := logKey{sender, v.Fact.LogID}
	shelfSum := sha256.Sum256(p.ShelfCheckpoint)
	shelfDigest := hex.EncodeToString(shelfSum[:])
	inclusion, err := json.Marshal(p.Inclusion)
	if err != nil {
		return ProofOutcome{}, err
	}
	var opening []byte
	if p.StatusOpening != nil {
		if opening, err = canonicalJSON(p.StatusOpening); err != nil {
			return ProofOutcome{}, err
		}
	}
	now := s.receiveNow()
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		prefix, err := receivedPrefix(ctx, tx, key)
		if err != nil {
			return err
		}
		forked := func() {
			for _, d := range prefix.deals {
				changes.forked[d] = true
			}
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT shelf_checkpoint_sha256, book_checkpoint FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? AND size = ? ORDER BY received_at, shelf_checkpoint_sha256`,
			sender, v.Fact.LogID, v.Fact.Size)
		if err != nil {
			return err
		}
		type held struct {
			shelf string
			book  []byte
		}
		var same []held
		for rows.Next() {
			var h held
			if err := rows.Scan(&h.shelf, &h.book); err != nil {
				_ = rows.Close()
				return err
			}
			same = append(same, h)
		}
		if err := firstErr(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, h := range same {
			cp, err := checkpointFromStatement(h.book)
			if err != nil {
				return err
			}
			if cp.Root != v.BookCheckpoint.Root {
				// Refused, and the evidence kept: the transaction commits.
				out.Refused = fmt.Errorf("%w: the book checkpoint of %s at %d contradicts one held from %s", ErrInvalid, v.Fact.LogID, v.Fact.Size, sender)
				forked()
				return noteEvidence(ctx, tx, EvidenceCheckpoint, h.book, p.BookCheckpoint, now)
			}
		}
		if v.BookCheckpoint.Entries <= uint64(len(prefix.digests)) {
			match, over, err := rootRecomputes(v.BookCheckpoint, prefix.digests)
			if err != nil {
				return err
			}
			if !match {
				out.Refused = fmt.Errorf("%w: the records held from %s in %s do not recompute the book checkpoint at %d", ErrInvalid, sender, v.Fact.LogID, v.Fact.Size)
				forked()
				return noteEvidence(ctx, tx, EvidenceCheckpointRecords, p.BookCheckpoint, over, now)
			}
		}
		for _, h := range same {
			if h.shelf == shelfDigest {
				out.Duplicate = true
				return nil
			}
		}
		var largest uint64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(size), 0) FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ?`, sender, v.Fact.LogID).Scan(&largest); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO shelf_received_proofs (sender_key, log_id, size, shelf_checkpoint_sha256, book_checkpoint, fact, status_opening, inclusion_proof, shelf_checkpoint, received_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sender, v.Fact.LogID, v.Fact.Size, shelfDigest, p.BookCheckpoint, p.Fact, opening, inclusion, p.ShelfCheckpoint, now); err != nil {
			return err
		}
		out.Accepted = true
		before, _ := mmr.LeafCount(largest)
		if v.BookCheckpoint.Entries <= before {
			return nil
		}
		// The records the proof newly covers.
		deals, err := tx.QueryContext(ctx,
			`SELECT DISTINCT deal_id FROM shelf_received WHERE sender_key = ? AND log_id = ? AND position > ? AND position <= ?`,
			sender, v.Fact.LogID, before, v.BookCheckpoint.Entries)
		if err != nil {
			return err
		}
		defer deals.Close()
		for deals.Next() {
			var d string
			if err := deals.Scan(&d); err != nil {
				return err
			}
			changes.covered[d] = true
		}
		return deals.Err()
	})
	if err != nil {
		return ProofOutcome{}, err
	}
	return out, nil
}
