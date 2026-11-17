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

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// A genesis fact's tie (evidencebook#461, docs/design/461-shelf-facts.md
// §2.7): the old witnessed tick's leaf for the fact's log, its path to that
// tick's tree root, that root's entry in the shelf log before the format
// switch, the cadence checkpoint the tick cut and the receipts held for it,
// the old book checkpoint the leaf commits to, and the consistency proof
// from it to the genesis fact's checkpoint. The tick is the one with the
// largest witnessed size of the log not past the genesis fact's size. A
// genesis fact with no tie is untied: nothing is claimed about the log
// before the coverage start.

// HandoverTie ties one book's genesis fact to the old witnessed tree.
type HandoverTie struct {
	// BookID is the genesis fact's book id.
	BookID string `json:"book_id"`
	// The old tick's leaf: the log, its size and checkpoint digest at the
	// tick, the leaf's salt and position, and its path to the tick's root
	// (CadenceDepth hashes, lowercase hex, from the leaf up).
	LogID            string   `json:"log_id"`
	Size             uint64   `json:"size"`
	CheckpointSHA256 string   `json:"checkpoint_sha256"`
	Salt             string   `json:"salt"`
	Index            uint64   `json:"index"`
	Path             []string `json:"path"`
	// EntrySeq is the position, from 1, of the tick's root in the shelf
	// log, before the format switch.
	EntrySeq uint64 `json:"entry_seq"`
	// CadenceCheckpoint is the COSE statement the tick cut, and Receipts the
	// witness receipts held for it, one per witness in witness URL order.
	CadenceCheckpoint []byte           `json:"cadence_checkpoint"`
	Receipts          []WitnessReceipt `json:"receipts"`
	// OldCheckpoint is the COSE statement of the book checkpoint the leaf
	// commits to.
	OldCheckpoint []byte `json:"old_checkpoint"`
	// Consistency proves the genesis fact's checkpoint extends the old one;
	// absent when the two are of one size (their roots are then equal).
	Consistency *ConsistencyEvidence `json:"consistency,omitempty"`
}

// tieLocked builds the tie of the genesis fact of logID (book bookID) whose
// checkpoint is genesis; ok is false when the log has no witnessed tick at
// or below the genesis size, or its leaf keeps no checkpoint statement (a
// tick a released capsule-cli build stored), and the fact is then untied.
func (c *Cadence) tieLocked(ctx context.Context, bookID, logID string, genesis []byte) (tie HandoverTie, ok bool, err error) {
	s := c.shelf
	cp, err := checkpointFromStatement(genesis)
	if err != nil {
		return HandoverTie{}, false, err
	}
	var n uint64
	err = s.db.QueryRowContext(ctx,
		`SELECT tick FROM cadence_witnessed WHERE log_id = ? AND mmr_size <= ? ORDER BY mmr_size DESC, tick DESC LIMIT 1`, logID, cp.TreeSize).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return HandoverTie{}, false, nil
	}
	if err != nil {
		return HandoverTie{}, false, err
	}
	tick, err := c.tickLocked(ctx, n)
	if err != nil {
		return HandoverTie{}, false, err
	}
	leaf, held := tick.Leaves.Deals[logID]
	if tick.Leaves.Stamp != nil || !held {
		return HandoverTie{}, false, fmt.Errorf("%w: witnessed tick %d holds no tree leaf of %s", ErrCorrupt, n, logID)
	}
	if leaf.Statement == "" {
		return HandoverTie{}, false, nil
	}
	old, err := base64.RawURLEncoding.DecodeString(leaf.Statement)
	if err != nil {
		return HandoverTie{}, false, fmt.Errorf("%w: tick %d's statement of %s: %v", ErrCorrupt, n, logID, err)
	}
	_, path, err := tick.Leaves.Tree(leaf.Index)
	if err != nil {
		return HandoverTie{}, false, err
	}
	tie = HandoverTie{BookID: bookID, LogID: logID, Size: leaf.Size, CheckpointSHA256: leaf.CheckpointSHA256, Salt: leaf.Salt,
		Index: leaf.Index, Path: make([]string, len(path)), EntrySeq: tick.EntrySeq, CadenceCheckpoint: tick.Checkpoint,
		Receipts: []WitnessReceipt{}, OldCheckpoint: old}
	for i := range path {
		tie.Path[i] = hex.EncodeToString(path[i])
	}
	if tie.Receipts, err = c.receiptsLocked(ctx, tick.Checkpoint); err != nil {
		return HandoverTie{}, false, err
	}
	if leaf.Size < cp.TreeSize {
		older, err := checkpointFromStatement(old)
		if err != nil {
			return HandoverTie{}, false, err
		}
		log, err := s.cadenceLogLocked(ctx, logID)
		if err != nil {
			return HandoverTie{}, false, err
		}
		proof, err := log.substrate().ProveConsistency(ctx, older, cp)
		if err != nil {
			return HandoverTie{}, false, err
		}
		tie.Consistency = &proof
	}
	return tie, true, nil
}

// receiptsLocked is every receipt shelf_receipts holds for the checkpoint
// statement, in witness URL order.
func (c *Cadence) receiptsLocked(ctx context.Context, statement []byte) ([]WitnessReceipt, error) {
	sum := sha256.Sum256(statement)
	rows, err := c.shelf.db.QueryContext(ctx, `SELECT receipt FROM shelf_receipts WHERE checkpoint_sha256 = ? ORDER BY witness_id`, hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, err
	}
	out := []WitnessReceipt{}
	for rows.Next() {
		var raw string
		var r WitnessReceipt
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: a stored receipt of checkpoint %x: %v", ErrCorrupt, sum, err)
		}
		out = append(out, r)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

// verifyTie checks a genesis fact's tie (§2.7) against the handover's
// entries (entries, the tree of them) before the format switch at marker,
// under the holder's key and the reader's witness directory.
func verifyTie(t HandoverTie, f BookFact, genesis Checkpoint, h Handover, tree *mmr.Tree, marker uint64, pinnedKey string, witnesses aacwitness.Directory) error {
	if t.LogID != f.LogID || t.Size > f.Size {
		return fmt.Errorf("the tie names %s at %d, the genesis fact %s at %d", t.LogID, t.Size, f.LogID, f.Size)
	}
	// The old book checkpoint: the holder's, of the log at the leaf's size,
	// hashing to the leaf's digest, and a prefix of the genesis checkpoint.
	old, err := checkpointFromStatement(t.OldCheckpoint)
	if err != nil {
		return fmt.Errorf("the old checkpoint does not verify: %v", err)
	}
	sum := sha256.Sum256(t.OldCheckpoint)
	if old.KeyID != pinnedKey || old.LogID != t.LogID || old.TreeSize != t.Size || hex.EncodeToString(sum[:]) != t.CheckpointSHA256 {
		return fmt.Errorf("the old checkpoint is not the leaf's")
	}
	switch {
	case t.Size == f.Size:
		if t.Consistency != nil || old.Root != genesis.Root {
			return fmt.Errorf("the old checkpoint is not the genesis checkpoint's root")
		}
	case t.Consistency == nil:
		return fmt.Errorf("no consistency proof from the old checkpoint")
	default:
		if err := verifyCLLConsistency(old, genesis, *t.Consistency); err != nil {
			return fmt.Errorf("the genesis checkpoint does not extend the old one: %v", err)
		}
	}
	// The leaf and its path recompute the tick's root.
	if t.Index >= 1<<CadenceDepth || t.Salt == "" || len(t.Path) != CadenceDepth {
		return fmt.Errorf("a malformed leaf")
	}
	path := make([][]byte, len(t.Path))
	for i, p := range t.Path {
		if path[i], err = hex.DecodeString(p); err != nil || len(path[i]) != sha256.Size || hex.EncodeToString(path[i]) != p {
			return fmt.Errorf("a malformed path")
		}
	}
	leaf, err := cadenceLeaf(t.LogID, t.Size, t.CheckpointSHA256, t.Salt)
	if err != nil {
		return err
	}
	root := cadenceFold(leaf, t.Index, path)
	// The root is a handed-over entry before the format switch, and the
	// cadence checkpoint is the holder's checkpoint of the shelf log the
	// handed-over entries recompute at its size, so the receipts over it
	// witnessed that root.
	if t.EntrySeq < 1 || t.EntrySeq >= marker || h.Entries[t.EntrySeq-1] != hex.EncodeToString(root) {
		return fmt.Errorf("the tick's root is not the shelf log's entry at %d", t.EntrySeq)
	}
	cadence, err := checkpointFromStatement(t.CadenceCheckpoint)
	if err != nil {
		return fmt.Errorf("the cadence checkpoint does not verify: %v", err)
	}
	if cadence.KeyID != pinnedKey || cadence.LogID != h.LogID || cadence.Entries < t.EntrySeq {
		return fmt.Errorf("the cadence checkpoint is not the holder's shelf log at the tick's root")
	}
	peaks, err := tree.PeakHashesAt(cadence.TreeSize)
	if err != nil || hex.EncodeToString(mmr.RootFromPeaks(peaks)) != cadence.Root {
		return fmt.Errorf("the handed-over entries do not recompute the cadence checkpoint")
	}
	for _, r := range t.Receipts {
		if check, _, err := verifyWitnessReceipt(t.CadenceCheckpoint, r, witnesses); err == nil && check.State == WitnessVerified {
			return nil
		}
	}
	return fmt.Errorf("no receipt from a named witness verifies for the tick's checkpoint")
}
