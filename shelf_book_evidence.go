// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
)

// One book's facts, handed to an adjudicator (evidencebook#861,
// docs/design/861-four-books.md §2.2): every book fact of one book up to a
// stamp, each with its stamp proof, its link opening and the witness
// receipts held for the shelf checkpoint that proves it, and the
// consistency proofs between the book checkpoints the facts carry. It
// carries no status opening (an adjudicator computes from the books, not
// from the holder's statement of the result) and nothing of the shelf's
// other books, which a whole-shelf Handover would.

// BookEvidence is one book's facts up to a stamp.
type BookEvidence struct {
	// BookID is the book's first record id.
	BookID string `json:"book_id"`
	// Facts are the book's facts, in shelf entry order.
	Facts []BookFactEvidence `json:"facts"`
	// Consistency is, for each pair of consecutive facts whose size grew,
	// the proof between their book checkpoints.
	Consistency []HandoverConsistency `json:"consistency"`
}

// BookFactEvidence is one fact of a BookEvidence: its stamp proof under the
// shelf checkpoint of the stamp that wrote it (with no status opening), its
// link opening, and the witness receipts held for that shelf checkpoint.
type BookFactEvidence struct {
	Proof       StampProof       `json:"proof"`
	LinkOpening FactOpening      `json:"link_opening"`
	Receipts    []WitnessReceipt `json:"receipts"`
}

// BookEvidence gathers the book bookID's facts up to stamp n (the newest
// finished stamp when n is 0): each fact proved under the checkpoint of the
// stamp that wrote it, with its link opening and that checkpoint's receipts.
// It is what an adjudicator's full answer carries beside its bundle; a
// journal is never handed over.
func (c *Cadence) BookEvidence(ctx context.Context, bookID string, n uint64) (BookEvidence, error) {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return BookEvidence{}, ErrReleased
	}
	if n == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0`).Scan(&n); err != nil {
			return BookEvidence{}, err
		}
	}
	type fact struct {
		seq, tick uint64
		kind      string
		link      []byte
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_seq, tick, kind, link_opening FROM shelf_entries WHERE book_id = ? AND kind IN (?, ?) AND tick <= ? ORDER BY entry_seq`,
		bookID, FactKindBook, FactKindJournal, n)
	if err != nil {
		return BookEvidence{}, err
	}
	var facts []fact
	for rows.Next() {
		var f fact
		if err := rows.Scan(&f.seq, &f.tick, &f.kind, &f.link); err != nil {
			rows.Close()
			return BookEvidence{}, err
		}
		facts = append(facts, f)
	}
	if err := rows.Close(); err != nil {
		return BookEvidence{}, err
	}
	out := BookEvidence{BookID: bookID, Facts: []BookFactEvidence{}, Consistency: []HandoverConsistency{}}
	var prev *BookFact
	var prevStatement []byte
	for _, f := range facts {
		if f.kind != FactKindBook {
			return BookEvidence{}, fmt.Errorf("%w: the journal's facts are handed to nobody", ErrInvalid)
		}
		p, seq, err := c.stampProofLocked(ctx, bookID, f.tick, false)
		if err != nil {
			return BookEvidence{}, err
		}
		if seq != f.seq {
			return BookEvidence{}, fmt.Errorf("%w: stamp %d proves the fact at %d of %s, not the one at %d", ErrCorrupt, f.tick, seq, bookID, f.seq)
		}
		var e BookFactEvidence
		e.Proof = p
		if f.link == nil {
			return BookEvidence{}, fmt.Errorf("%w: the fact at %d of %s keeps no link opening", ErrCorrupt, f.seq, bookID)
		}
		if err := json.Unmarshal(f.link, &e.LinkOpening); err != nil {
			return BookEvidence{}, fmt.Errorf("%w: the fact at %d's link opening: %v", ErrCorrupt, f.seq, err)
		}
		if e.Receipts, err = c.receiptsLocked(ctx, p.ShelfCheckpoint); err != nil {
			return BookEvidence{}, err
		}
		var bf BookFact
		if err := json.Unmarshal(p.Fact, &bf); err != nil {
			return BookEvidence{}, fmt.Errorf("%w: stored fact %d: %v", ErrCorrupt, f.seq, err)
		}
		if prev != nil && bf.Size > prev.Size {
			older, err := checkpointFromStatement(prevStatement)
			if err != nil {
				return BookEvidence{}, err
			}
			newer, err := checkpointFromStatement(p.BookCheckpoint)
			if err != nil {
				return BookEvidence{}, err
			}
			log, err := s.cadenceLogLocked(ctx, bf.LogID)
			if err != nil {
				return BookEvidence{}, err
			}
			proof, err := log.substrate().ProveConsistency(ctx, older, newer)
			if err != nil {
				return BookEvidence{}, err
			}
			out.Consistency = append(out.Consistency, HandoverConsistency{LogID: bf.LogID, FromSize: prev.Size, ToSize: bf.Size, Proof: proof})
		}
		out.Facts = append(out.Facts, e)
		prev, prevStatement = &bf, p.BookCheckpoint
	}
	return out, nil
}

// VerifyBookEvidence checks one book's facts under the holder's checkpoint
// key checkpointKey and shelf log shelfLogID, then reads the record times
// they give under the reader's witness directory:
//
//   - each stamp proof verifies (VerifyStampProof), is signed by
//     checkpointKey, proves an entry of the shelf log shelfLogID, is a fact
//     of the book e.BookID, and carries no status opening;
//   - each link opening recomputes its fact's link commitment and holds a
//     link value;
//   - the facts sit at increasing shelf entry positions and chain as
//     VerifyHandover checks a book's facts: the first names no previous
//     size, and each next one names its predecessor's size, never shrinks,
//     keeps its checkpoint at the same size, and grows by a consistency
//     proof given.
//
// A receipt that does not verify, or gives no registration time, gives no
// time. A record's time is the earliest named registration of the shelf
// checkpoint of a given fact that covers it, which is never earlier than the
// shelf's own RecordTime: a fact left out can only make a time later.
func VerifyBookEvidence(e BookEvidence, checkpointKey, shelfLogID string, witnesses aacwitness.Directory) (HandoverTimes, error) {
	bad := func(format string, args ...any) (HandoverTimes, error) {
		return HandoverTimes{}, fmt.Errorf("%w: book evidence: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	statements := map[string]Checkpoint{}
	proofs := map[string]ConsistencyEvidence{}
	for _, p := range e.Consistency {
		proofs[fmt.Sprintf("%s %d %d", p.LogID, p.FromSize, p.ToSize)] = p.Proof
	}
	var out HandoverTimes
	var prev *BookFact
	var prevSeq uint64
	for i, fe := range e.Facts {
		v, err := VerifyStampProof(fe.Proof)
		if err != nil {
			return bad("fact %d: %v", i+1, err)
		}
		f := v.Fact
		switch {
		case v.KeyID != checkpointKey:
			return bad("fact %d is signed by %s, not the holder's checkpoint key", i+1, v.KeyID)
		case v.ShelfCheckpoint.LogID != shelfLogID:
			return bad("fact %d is proved in the shelf log %s, not %s", i+1, v.ShelfCheckpoint.LogID, shelfLogID)
		case f.BookID != e.BookID:
			return bad("fact %d is of the book %s, not %s", i+1, f.BookID, e.BookID)
		case fe.Proof.StatusOpening != nil:
			return bad("fact %d carries a status opening", i+1)
		case fe.Proof.Inclusion.Seq <= prevSeq:
			return bad("fact %d sits at shelf entry %d, not after %d", i+1, fe.Proof.Inclusion.Seq, prevSeq)
		}
		if err := fe.LinkOpening.Verify(f.LinkCommitment); err != nil {
			return bad("fact %d's link opening: %v", i+1, err)
		}
		var link LinkValue
		if err := canonicalDocument(fe.LinkOpening.Value, &link); err != nil {
			return bad("fact %d's link value: %v", i+1, err)
		}
		sum := sha256.Sum256(fe.Proof.BookCheckpoint)
		statements[hex.EncodeToString(sum[:])] = v.BookCheckpoint
		if prev == nil {
			if f.PrevFactSize != 0 {
				return bad("the first fact names a previous size")
			}
		} else if err := checkFactChain(*prev, f, statements, proofs); err != nil {
			return bad("fact %d: %v", i+1, err)
		}
		reg := handoverRegistration{entries: v.ShelfCheckpoint.Entries, proves: len(out.facts), at: map[string]int64{}, atKey: map[string]int64{}}
		for _, r := range fe.Receipts {
			check, verified, err := verifyWitnessReceipt(fe.Proof.ShelfCheckpoint, r, witnesses)
			if err != nil || check.State != WitnessVerified || verified.registeredAt == nil {
				continue
			}
			keepEarliest(reg.at, r.TSURL, *verified.registeredAt)
			keepEarliest(reg.atKey, verified.keyID, *verified.registeredAt)
		}
		out.facts = append(out.facts, handoverFact{seq: fe.Proof.Inclusion.Seq, bookID: f.BookID, size: f.Size, lastReceived: link.LastReceived})
		out.checkpoints = append(out.checkpoints, reg)
		prev, prevSeq = &f, fe.Proof.Inclusion.Seq
	}
	return out, nil
}
