// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// SubstrateKindSCITTReceipt labels the receipt-holding binding.
const SubstrateKindSCITTReceipt = "scitt-receipt"

// Registrar registers a record identity with a SCITT transparency service and
// returns the service's receipt. The service, not the book, assigns the
// position the receipt proves.
type Registrar interface {
	Register(ctx context.Context, recordID string) (Receipt, error)
}

// Receipt is an opaque transparency-service receipt (for example a COSE
// Receipt) with the tree coordinates it attests.
type Receipt struct {
	ServiceID string `json:"service_id"`
	TreeSize  uint64 `json:"tree_size"`
	LeafIndex uint64 `json:"leaf_index"`
	Bytes     []byte `json:"bytes"`
}

// SCITTReceiptSubstrate is a stub binding: it holds one receipt per record in
// memory and answers inclusion with that receipt. It keeps no log of its own,
// so interval and consistency proofs are ErrUnsupported and the book's
// bundles over it carry receipts instead of a completeness certificate.
// It exists so the "no particular substrate required" property is exercised
// in code, not asserted in prose; it is not durable.
type SCITTReceiptSubstrate struct {
	mu        sync.Mutex
	serviceID string
	registrar Registrar
	entries   []SubstrateEntry
	receipts  map[string]Receipt
}

// NewSCITTReceiptSubstrate returns the receipt-holding substrate stub.
func NewSCITTReceiptSubstrate(serviceID string, registrar Registrar) (*SCITTReceiptSubstrate, error) {
	if serviceID == "" || registrar == nil {
		return nil, fmt.Errorf("%w: service id and registrar are required", ErrInvalid)
	}
	return &SCITTReceiptSubstrate{serviceID: serviceID, registrar: registrar, receipts: make(map[string]Receipt)}, nil
}

func (s *SCITTReceiptSubstrate) Info() SubstrateInfo {
	return SubstrateInfo{Kind: SubstrateKindSCITTReceipt, LogID: s.serviceID}
}

func (s *SCITTReceiptSubstrate) Append(ctx context.Context, recordID string, at time.Time) (uint64, error) {
	if _, err := decodeRecordID(recordID); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.entries {
		if entry.RecordID == recordID {
			return entry.Seq, nil
		}
	}
	receipt, err := s.registrar.Register(ctx, recordID)
	if err != nil {
		return 0, fmt.Errorf("SCITT registration: %w", err)
	}
	seq := uint64(len(s.entries)) + 1
	s.entries = append(s.entries, SubstrateEntry{Seq: seq, RecordID: recordID, AppendedAt: at.UTC()})
	s.receipts[recordID] = receipt
	return seq, nil
}

func (s *SCITTReceiptSubstrate) Size(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return uint64(len(s.entries)), nil
}

func (s *SCITTReceiptSubstrate) Entries(_ context.Context, afterSeq uint64, limit int) ([]SubstrateEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if afterSeq >= uint64(len(s.entries)) {
		return nil, nil
	}
	tail := s.entries[afterSeq:]
	if limit > 0 && len(tail) > limit {
		tail = tail[:limit]
	}
	return append([]SubstrateEntry(nil), tail...), nil
}

// Checkpoint names the latest receipt's tree state. The identity comes from
// the transparency service's coordinates, not from anything the book computes.
func (s *SCITTReceiptSubstrate) Checkpoint(ctx context.Context, _ time.Time) (Checkpoint, error) {
	return s.Latest(ctx)
}

func (s *SCITTReceiptSubstrate) Latest(context.Context) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return Checkpoint{}, fmt.Errorf("%w: no receipt yet", ErrNotFound)
	}
	last := s.entries[len(s.entries)-1]
	receipt := s.receipts[last.RecordID]
	return Checkpoint{
		Kind:      SubstrateKindSCITTReceipt,
		LogID:     s.serviceID,
		ID:        fmt.Sprintf("%s:%d", s.serviceID, receipt.TreeSize),
		Entries:   last.Seq,
		TreeSize:  receipt.TreeSize,
		IssuedAt:  last.AppendedAt,
		Statement: append([]byte(nil), receipt.Bytes...),
	}, nil
}

func (s *SCITTReceiptSubstrate) ProveInclusion(_ context.Context, seq uint64, at Checkpoint) (InclusionEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !at.Covers(seq) || seq > uint64(len(s.entries)) {
		return InclusionEvidence{}, fmt.Errorf("%w: seq %d", ErrNotCovered, seq)
	}
	encoded, err := json.Marshal(s.receipts[s.entries[seq-1].RecordID])
	if err != nil {
		return InclusionEvidence{}, err
	}
	return InclusionEvidence{Kind: "scitt-receipt", Seq: seq, Proof: encoded}, nil
}

func (s *SCITTReceiptSubstrate) ProveInterval(context.Context, uint64, Checkpoint) (IntervalEvidence, error) {
	return IntervalEvidence{}, fmt.Errorf("%w: a receipt-only substrate keeps no ordered log to prove an interval over", ErrUnsupported)
}

func (s *SCITTReceiptSubstrate) ProveConsistency(context.Context, Checkpoint, Checkpoint) (ConsistencyEvidence, error) {
	return ConsistencyEvidence{}, fmt.Errorf("%w: consistency is the transparency service's to prove, not this store's", ErrUnsupported)
}

func (s *SCITTReceiptSubstrate) Release() error { return nil }
