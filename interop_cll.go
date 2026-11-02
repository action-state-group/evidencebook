// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// cllMaterialSource is implemented by a substrate whose evidence stream is a
// Checkpointed Local Log. Only such a substrate's anchors and proofs are
// served under the profile; every other substrate answers no request whose
// answer would need them (FR-20: a receipt held but never verified is not
// presented as a checkpoint or a proof).
type cllMaterialSource interface {
	// cllProver restores the log's MMR once, checked against at, for every
	// proof one answer needs. A restore reads the whole log under the log
	// store's lock, which the book's own appends also take, so an answer
	// restores it once rather than once per proof (evidencebook#208).
	cllProver(ctx context.Context, at Checkpoint) (*cllProver, error)
}

var _ cllMaterialSource = (*CLLSubstrate)(nil)

// cllProver builds the profile's CLL proofs under one anchor from one
// restored MMR.
type cllProver struct {
	at   Checkpoint
	tree *mmr.Tree
}

func (s *CLLSubstrate) cllProver(ctx context.Context, at Checkpoint) (*cllProver, error) {
	tree, err := s.tree(ctx, at)
	if err != nil {
		return nil, err
	}
	return &cllProver{at: at, tree: tree}, nil
}

// inclusionProof is the cll-inclusion-proof octets for the record at seq:
// deterministic CBOR of [index, inclusion-path].
func (p *cllProver) inclusionProof(seq uint64) ([]byte, error) {
	if !p.at.Covers(seq) {
		return nil, fmt.Errorf("%w: seq %d under checkpoint %s", ErrNotCovered, seq, p.at.ID)
	}
	proof, err := p.tree.InclusionProofAt(seq-1, p.at.TreeSize)
	if err != nil {
		return nil, err
	}
	return evidencerequest.EncodeCLLInclusionProof(seq-1, proof.Witness)
}

// consistencyProof is the cll-consistency-proof octets proving the tree at
// newSize extends the tree at oldSize: deterministic CBOR of [tree-size-1,
// tree-size-2, consistency-paths, right-peaks].
func (p *cllProver) consistencyProof(oldSize, newSize uint64) ([]byte, error) {
	at, tree := p.at, p.tree
	if oldSize == 0 || oldSize > newSize || newSize > at.TreeSize {
		return nil, fmt.Errorf("%w: consistency from %d to %d under %s", ErrInvalid, oldSize, newSize, at.ID)
	}
	proof, err := tree.ConsistencyProofAt(oldSize, newSize)
	if err != nil {
		return nil, err
	}
	roots := evidencerequest.MMRConsistentRoots(oldSize-1, proof.OldPeaks, proof.Witness)
	if len(roots) > len(proof.NewPeaks) {
		return nil, fmt.Errorf("%w: consistency roots exceed the new peaks", ErrCorrupt)
	}
	right := proof.NewPeaks[len(roots):]
	if !slices.EqualFunc(append(slices.Clone(roots), right...), proof.NewPeaks, bytes.Equal) {
		return nil, fmt.Errorf("%w: consistency proof from %d to %d does not reach the new peaks", ErrCorrupt, oldSize, newSize)
	}
	return evidencerequest.EncodeCLLConsistencyProof(oldSize, newSize, proof.Witness, right)
}

// anchorDigest identifies a checkpoint as a coverage anchor: SHA-256 over
// its signed statement octets (profile §6.2.1, P-31). It is the value a
// requester pins in expected_pin.
func anchorDigest(cp Checkpoint) string {
	return digestBytes(cp.Statement)
}

// cllMaterial builds the §6.2.1 material for the record at seq under the
// prover's anchor: the anchor checkpoint, the record's inclusion proof, and,
// when the checkpoint extends an earlier one, the proof that it does.
func cllMaterial(prover *cllProver, seq uint64) ([]VerificationEntry, [][]byte, error) {
	at := prover.at
	signed, err := checkpoint.ParseRecord(at.Statement)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: anchor checkpoint: %v", ErrCorrupt, err)
	}
	inclusion, err := prover.inclusionProof(seq)
	if err != nil {
		return nil, nil, err
	}
	items := [][]byte{at.Statement, inclusion}
	kinds := []string{KindCLLCheckpoint, KindCLLInclusionProof}
	if signed.PrevSize > 0 {
		consistency, err := prover.consistencyProof(signed.PrevSize, signed.MMRSize)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, consistency)
		kinds = append(kinds, KindCLLConsistencyProof)
	}
	entries := make([]VerificationEntry, len(items))
	for i, item := range items {
		entries[i] = VerificationEntry{Kind: kinds[i], Digest: digestBytes(item)}
	}
	return entries, items, nil
}

// signedCheckpoint is a CLL checkpoint read from its signed statement, with
// the peak lists the anchor_inclusion procedure needs.
type signedCheckpoint struct {
	Checkpoint
	prevSize       uint64
	prevRoot       string
	commitment     [][]byte
	prevCommitment [][]byte
}

// readSignedCheckpoint parses a cll-checkpoint item, checks its signature,
// and requires the pinned checkpoint key.
func readSignedCheckpoint(statement []byte, pinnedKey string) (signedCheckpoint, error) {
	cp, err := checkpointFromStatement(statement)
	if err != nil {
		return signedCheckpoint{}, err
	}
	if pinnedKey == "" || cp.KeyID != pinnedKey {
		return signedCheckpoint{}, fmt.Errorf("%w: checkpoint is signed by %s, not the pinned key", ErrInvalid, cp.KeyID)
	}
	record, err := checkpoint.ParseRecord(statement)
	if err != nil {
		return signedCheckpoint{}, fmt.Errorf("%w: checkpoint: %v", ErrInvalid, err)
	}
	return signedCheckpoint{Checkpoint: cp, prevSize: record.PrevSize, prevRoot: record.PrevRoot, commitment: record.NewPeaks, prevCommitment: record.PrevPeaks}, nil
}

// checkCLLInclusion runs the inclusion and consistency steps of the
// anchor_inclusion procedure (profile §6.2.1 steps 12 and 13) for entry
// under cp.
func checkCLLInclusion(cp signedCheckpoint, entry []byte, inclusion, consistency []byte) error {
	if err := evidencerequest.VerifyCLLInclusion(cp.commitment, entry, inclusion); err != nil {
		return err
	}
	if consistency == nil {
		return nil
	}
	if cp.prevSize == 0 {
		return fmt.Errorf("%w: the checkpoint extends no earlier commitment", ErrInvalid)
	}
	return checkCLLConsistency(cp.prevCommitment, cp.commitment, consistency)
}

// checkCLLConsistency checks cll-consistency-proof octets: that the
// commitment oldPeaks extends to newPeaks.
func checkCLLConsistency(oldPeaks, newPeaks [][]byte, consistency []byte) error {
	return evidencerequest.VerifyCLLConsistency(oldPeaks, newPeaks, consistency)
}
