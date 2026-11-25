// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// ForeignRecord is one record of a book log another key signed and
// checkpointed, as a holder received it: the Capsule (its JCS bytes, without
// signature), the Producer Envelope that signs it, and the record header the
// Capsule commits as agent_input. Payloads are the record's payloads by
// payload commitment; only the root's are read.
type ForeignRecord struct {
	Capsule  []byte
	Envelope []byte
	Header   []byte
	Payloads map[string][]byte
}

// ForeignBundle builds an Evidence Bundle of a book log the caller holds but
// does not own (evidencebook#850): records are every record of that log from
// seq 1 to the checkpoint's last entry, in order, and checkpoint is the log
// owner's own signed checkpoint statement over them. The bundle is built by
// agent-action-capsule's producer over the same proof snapshot a book's own
// bundle uses, so it is a bundle of the owner's log and its anchor is the
// owner's signature; the caller adds no signature of its own. Only root is
// disclosed (its header and payloads); every other record rides along with
// its header and payloads withheld, so membership and interval coverage are
// proven. It refuses records that do not recompute the checkpoint's root,
// and returns the bundle only once VerifyBundle passes every claim.
func ForeignBundle(ctx context.Context, checkpoint []byte, records []ForeignRecord, root string) ([]byte, error) {
	at, err := checkpointFromStatement(checkpoint)
	if err != nil {
		return nil, err
	}
	if uint64(len(records)) != at.Entries {
		return nil, fmt.Errorf("%w: %d records for a checkpoint of %d entries", ErrInvalid, len(records), at.Entries)
	}
	leaves := make([][]byte, len(records))
	stored := make(map[string]aacbundle.StoredRecord, len(records))
	var rootRecord *ForeignRecord
	for i := range records {
		var c struct {
			CapsuleID string `json:"capsule_id"`
		}
		if err := json.Unmarshal(records[i].Capsule, &c); err != nil {
			return nil, fmt.Errorf("%w: record %d: the Capsule: %v", ErrInvalid, i+1, err)
		}
		if leaves[i], err = hex.DecodeString(c.CapsuleID); err != nil || len(leaves[i]) != 32 {
			return nil, fmt.Errorf("%w: record %d: capsule_id is not 32 bytes of hex", ErrInvalid, i+1)
		}
		if _, dup := stored[c.CapsuleID]; dup {
			return nil, fmt.Errorf("%w: record %s appears twice", ErrInvalid, c.CapsuleID)
		}
		stored[c.CapsuleID] = aacbundle.StoredRecord{Capsule: records[i].Capsule, ProducerEnvelope: records[i].Envelope}
		if c.CapsuleID == root {
			rootRecord = &records[i]
		}
	}
	if rootRecord == nil {
		return nil, fmt.Errorf("%w: root %s is not one of the records", ErrInvalid, root)
	}
	snapshot, err := snapshotOfLeaves(at, leaves)
	if err != nil {
		return nil, err
	}
	header, err := decodeJSON(rootRecord.Header)
	if err != nil {
		return nil, fmt.Errorf("%w: the root's header: %v", ErrInvalid, err)
	}
	h, err := strictHeader(rootRecord.Header)
	if err != nil {
		return nil, fmt.Errorf("%w: the root's header: %v", ErrInvalid, err)
	}
	payloads := make(map[string]any, len(h.PayloadCommitments))
	for _, digest := range h.PayloadCommitments {
		data, ok := rootRecord.Payloads[digest]
		if !ok || digestBytes(data) != digest {
			return nil, fmt.Errorf("%w: the root's payload %s is not given", ErrInvalid, digest)
		}
		payloads[digest] = base64.RawURLEncoding.EncodeToString(data)
	}
	depth := 0
	opts := aacbundle.BuildOptions{
		Root: root, IntervalFrom: 1, ClosureDepth: &depth,
		Disclosures:  map[string]map[string]any{root: {HeaderMember: header}},
		PayloadsMode: string(PayloadsSelected),
		Verification: map[string]any{"producer": DefaultDeveloper, "checks": []any{"graph_closure", "interval_coverage", "per_record_membership"}},
	}
	if len(payloads) > 0 {
		opts.Extensions = map[string]any{ExtensionPayloads: payloads}
	}
	built, err := aacbundle.BuildEvidenceBundle(ctx, foreignProofSource{snapshot: snapshot, records: stored}, opts)
	var refused *aacbundle.BuildError
	if errors.As(err, &refused) {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, refused)
	}
	if err != nil {
		return nil, err
	}
	data, err := canonical.JCS(built)
	if err != nil {
		return nil, err
	}
	verified, err := VerifyBundle(data)
	if err != nil {
		return nil, err
	}
	if err := verified.FullyVerified(); err != nil {
		return nil, err
	}
	return data, nil
}

// snapshotOfLeaves is the proof snapshot of a log given its leaves through
// at.Entries: the MMR built from them, checked to recompute at's root. It is
// what CLLSubstrate.proofSnapshot gives for a log the caller does not hold
// as a substrate (a held log, ForeignBundle).
func snapshotOfLeaves(at Checkpoint, leaves [][]byte) (*proofSnapshot, error) {
	tree, err := mmr.New(nil)
	if err != nil {
		return nil, err
	}
	for _, leaf := range leaves {
		if _, err := tree.Append(leaf); err != nil {
			return nil, err
		}
	}
	if tree.Size() != at.TreeSize {
		return nil, fmt.Errorf("%w: the leaves make a tree of size %d, the checkpoint's is %d", ErrInvalid, tree.Size(), at.TreeSize)
	}
	peaks, err := tree.PeakHashesAt(at.TreeSize)
	if err != nil {
		return nil, err
	}
	if hex.EncodeToString(mmr.RootFromPeaks(peaks)) != at.Root {
		return nil, fmt.Errorf("%w: the records do not recompute the checkpoint's root", ErrInvalid)
	}
	return &proofSnapshot{at: at, tree: tree, leaves: leaves}, nil
}

// foreignProofSource is a held log at one checkpoint as
// agent-action-capsule's producer reads it, as bookProofSource is a book's.
type foreignProofSource struct {
	snapshot *proofSnapshot
	records  map[string]aacbundle.StoredRecord
}

func (s foreignProofSource) Checkpoint(ctx context.Context) ([]byte, error) {
	return bookProofSource{snapshot: s.snapshot}.Checkpoint(ctx)
}

func (s foreignProofSource) Leaves(ctx context.Context) ([][]byte, error) {
	return bookProofSource{snapshot: s.snapshot}.Leaves(ctx)
}

func (s foreignProofSource) Record(_ context.Context, capsuleID string) (aacbundle.StoredRecord, bool, error) {
	r, ok := s.records[capsuleID]
	return r, ok, nil
}

func (s foreignProofSource) InclusionProof(_ context.Context, seq uint64) (aacbundle.InclusionProof, error) {
	return s.snapshot.inclusionProof(seq)
}

func (s foreignProofSource) RangeProof(_ context.Context, firstSeq uint64) (aacbundle.RangeProof, error) {
	return s.snapshot.rangeProof(firstSeq)
}
