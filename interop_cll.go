// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
)

// cllMaterialSource is implemented by a substrate whose evidence stream is a
// Checkpointed Local Log. Only such a substrate's anchors and proofs are
// served under the profile; every other substrate answers no request whose
// answer would need them (FR-20: a receipt held but never verified is not
// presented as a checkpoint or a proof).
type cllMaterialSource interface {
	// cllInclusionProof is the cll-inclusion-proof octets for the record at
	// seq under at: deterministic CBOR of [index, inclusion-path].
	cllInclusionProof(ctx context.Context, seq uint64, at Checkpoint) ([]byte, error)
	// cllConsistencyProof is the cll-consistency-proof octets proving the
	// tree at newSize extends the tree at oldSize: deterministic CBOR of
	// [tree-size-1, tree-size-2, consistency-paths, right-peaks].
	cllConsistencyProof(ctx context.Context, oldSize, newSize uint64, at Checkpoint) ([]byte, error)
}

var _ cllMaterialSource = (*CLLSubstrate)(nil)

func (s *CLLSubstrate) cllInclusionProof(ctx context.Context, seq uint64, at Checkpoint) ([]byte, error) {
	if !at.Covers(seq) {
		return nil, fmt.Errorf("%w: seq %d under checkpoint %s", ErrNotCovered, seq, at.ID)
	}
	tree, err := s.tree(ctx, at)
	if err != nil {
		return nil, err
	}
	proof, err := tree.InclusionProofAt(seq-1, at.TreeSize)
	if err != nil {
		return nil, err
	}
	path := make([]any, len(proof.Witness))
	for i, node := range proof.Witness {
		path[i] = node
	}
	return toDeterministicCBOR([]any{mmrLeafIndex(seq - 1), path})
}

func (s *CLLSubstrate) cllConsistencyProof(ctx context.Context, oldSize, newSize uint64, at Checkpoint) ([]byte, error) {
	tree, err := s.tree(ctx, at)
	if err != nil {
		return nil, err
	}
	if oldSize == 0 || oldSize > newSize || newSize > at.TreeSize {
		return nil, fmt.Errorf("%w: consistency from %d to %d under %s", ErrInvalid, oldSize, newSize, at.ID)
	}
	proof, err := tree.ConsistencyProofAt(oldSize, newSize)
	if err != nil {
		return nil, err
	}
	roots := mmrConsistentRoots(oldSize-1, proof.OldPeaks, proof.Witness)
	if len(roots) > len(proof.NewPeaks) {
		return nil, fmt.Errorf("%w: consistency roots exceed the new peaks", ErrCorrupt)
	}
	right := proof.NewPeaks[len(roots):]
	if !slices.EqualFunc(append(slices.Clone(roots), right...), proof.NewPeaks, bytes.Equal) {
		return nil, fmt.Errorf("%w: consistency proof from %d to %d does not reach the new peaks", ErrCorrupt, oldSize, newSize)
	}
	paths := make([]any, len(proof.Witness))
	for i, path := range proof.Witness {
		nodes := make([]any, len(path))
		for j, node := range path {
			nodes[j] = node
		}
		paths[i] = nodes
	}
	peaks := make([]any, len(right))
	for i, node := range right {
		peaks[i] = node
	}
	return toDeterministicCBOR([]any{oldSize, newSize, paths, peaks})
}

// anchorDigest identifies a checkpoint as a coverage anchor: SHA-256 over
// its signed statement octets (profile §6.2.1, P-31). It is the value a
// requester pins in expected_pin.
func anchorDigest(cp Checkpoint) string {
	return digestBytes(cp.Statement)
}

// cllMaterial builds the §6.2.1 material for the record at seq under the
// anchor: the anchor checkpoint, the record's inclusion proof, and, when the
// checkpoint extends an earlier one, the proof that it does.
func cllMaterial(ctx context.Context, source cllMaterialSource, seq uint64, at Checkpoint) ([]VerificationEntry, [][]byte, error) {
	signed, err := checkpoint.ParseRecord(at.Statement)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: anchor checkpoint: %v", ErrCorrupt, err)
	}
	inclusion, err := source.cllInclusionProof(ctx, seq, at)
	if err != nil {
		return nil, nil, err
	}
	items := [][]byte{at.Statement, inclusion}
	kinds := []string{KindCLLCheckpoint, KindCLLInclusionProof}
	if signed.PrevSize > 0 {
		consistency, err := source.cllConsistencyProof(ctx, signed.PrevSize, signed.MMRSize, at)
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
	return signedCheckpoint{Checkpoint: cp, prevSize: record.PrevSize, commitment: record.NewPeaks, prevCommitment: record.PrevPeaks}, nil
}

// checkCLLInclusion runs the inclusion and consistency steps of the
// anchor_inclusion procedure (profile §6.2.1 steps 12 and 13) for entry
// under cp.
func checkCLLInclusion(cp signedCheckpoint, entry []byte, inclusion, consistency []byte) error {
	proof, err := decodeStrictCBOR(inclusion)
	if err != nil {
		return fmt.Errorf("%w: inclusion proof: %v", ErrInvalid, err)
	}
	pair, ok := proof.([]any)
	if !ok || len(pair) != 2 {
		return fmt.Errorf("%w: inclusion proof is not [index, path]", ErrInvalid)
	}
	index, ok := uintValue(pair[0])
	path, pathOK := byteStrings32(pair[1])
	if !ok || !pathOK {
		return fmt.Errorf("%w: inclusion proof is not [index, [32-octet byte strings]]", ErrInvalid)
	}
	if mmrIndexHeight(index) != 0 {
		return fmt.Errorf("%w: inclusion index %d is not a leaf", ErrInvalid, index)
	}
	root := mmrIncludedRoot(index, cllLeafValue(entry), path)
	if !slices.ContainsFunc(cp.commitment, func(peak []byte) bool { return bytes.Equal(peak, root) }) {
		return fmt.Errorf("%w: the inclusion path reaches no peak of the checkpoint", ErrInvalid)
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
// commitment newPeaks extends the commitment oldPeaks (profile §6.2.1 step
// 13; MMR §7.1, §7.1.1).
func checkCLLConsistency(oldPeaks, newPeaks [][]byte, consistency []byte) error {
	decoded, err := decodeStrictCBOR(consistency)
	if err != nil {
		return fmt.Errorf("%w: consistency proof: %v", ErrInvalid, err)
	}
	parts, ok := decoded.([]any)
	if !ok || len(parts) != 4 {
		return fmt.Errorf("%w: consistency proof is not four members", ErrInvalid)
	}
	size1, ok1 := uintValue(parts[0])
	size2, ok2 := uintValue(parts[1])
	rawPaths, ok3 := parts[2].([]any)
	right, ok4 := byteStrings32(parts[3])
	if !ok1 || !ok2 || !ok3 || !ok4 || size1 == 0 || size1 > size2 || len(rawPaths) > 64 {
		return fmt.Errorf("%w: consistency proof is malformed", ErrInvalid)
	}
	paths := make([][][]byte, len(rawPaths))
	for i, raw := range rawPaths {
		if paths[i], ok = byteStrings32(raw); !ok {
			return fmt.Errorf("%w: consistency path %d is malformed", ErrInvalid, i)
		}
	}
	if n := len(mmrPeaks(size1 - 1)); n != len(paths) || n != len(oldPeaks) {
		return fmt.Errorf("%w: consistency proof does not match the earlier commitment", ErrInvalid)
	}
	roots := mmrConsistentRoots(size1-1, oldPeaks, paths)
	if len(right) != len(mmrPeaks(size2-1))-len(roots) {
		return fmt.Errorf("%w: consistency proof has the wrong number of right peaks", ErrInvalid)
	}
	if !slices.EqualFunc(append(roots, right...), newPeaks, bytes.Equal) {
		return fmt.Errorf("%w: consistency proof does not reach the later commitment", ErrInvalid)
	}
	return nil
}

func byteStrings32(v any) ([][]byte, bool) {
	items, ok := v.([]any)
	if !ok || len(items) > 64 {
		return nil, false
	}
	out := make([][]byte, len(items))
	for i, item := range items {
		b, ok := item.([]byte)
		if !ok || len(b) != 32 {
			return nil, false
		}
		out[i] = b
	}
	return out, true
}
