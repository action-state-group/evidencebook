// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"time"
)

// Substrate is the commitment-substrate interface (Evidence Layer §8). A book
// needs exactly four properties from it: append order, inclusion,
// consistency, and checkpoint identity. A Checkpointed Local Log is the
// reference binding (OpenCLL); a SCITT receipt-holding binding
// (NewSCITTReceiptSubstrate) shows the book does not require one.
//
// Record identities cross this interface as lowercase hex strings and every
// proof leaves it as an evidencebook type, so no substrate implementation
// type is ever visible to a book caller.
type Substrate interface {
	Info() SubstrateInfo
	// Append assigns the next position to recordID. Appending an identity
	// that is already present returns its original position.
	Append(ctx context.Context, recordID string, at time.Time) (uint64, error)
	Size(ctx context.Context) (uint64, error)
	Entries(ctx context.Context, afterSeq uint64, limit int) ([]SubstrateEntry, error)
	// Checkpoint commits a checkpoint over everything appended so far.
	Checkpoint(ctx context.Context, now time.Time) (Checkpoint, error)
	// Latest returns the newest checkpoint, or ErrNotFound when none exists.
	Latest(ctx context.Context) (Checkpoint, error)
	ProveInclusion(ctx context.Context, seq uint64, at Checkpoint) (InclusionEvidence, error)
	// ProveInterval returns the per-record range and membership evidence for
	// positions [firstSeq, at.Entries]. Substrates that do not maintain an
	// ordered log return ErrUnsupported.
	ProveInterval(ctx context.Context, firstSeq uint64, at Checkpoint) (IntervalEvidence, error)
	ProveConsistency(ctx context.Context, older, newer Checkpoint) (ConsistencyEvidence, error)
	Release() error
}

// SubstrateInfo names the substrate a book states it uses.
type SubstrateInfo struct {
	Kind  string `json:"kind"`
	LogID string `json:"log_id"`
}

// SubstrateEntry is one committed position.
type SubstrateEntry struct {
	Seq        uint64
	RecordID   string
	AppendedAt time.Time
}

// Checkpoint names one committed state. ID is independent of which party
// serves the checkpoint: for the CLL binding it is "<root-hex>:<mmr_size>".
type Checkpoint struct {
	Kind      string    `json:"kind"`
	LogID     string    `json:"log_id"`
	ID        string    `json:"id"`
	Entries   uint64    `json:"entries"`
	Root      string    `json:"root,omitempty"`
	TreeSize  uint64    `json:"tree_size,omitempty"`
	KeyID     string    `json:"key_id,omitempty"`
	IssuedAt  time.Time `json:"issued_at"`
	Statement []byte    `json:"statement,omitempty"`
}

// Covers reports whether seq is committed under this checkpoint.
func (c Checkpoint) Covers(seq uint64) bool {
	return seq >= 1 && seq <= c.Entries
}

// InclusionEvidence is a substrate-specific inclusion proof for one record,
// labelled by Kind so a verifier knows which algorithm checks it.
type InclusionEvidence struct {
	Kind  string          `json:"kind"`
	Seq   uint64          `json:"seq"`
	Proof json.RawMessage `json:"proof"`
}

// ConsistencyEvidence proves newer append-only extends older.
type ConsistencyEvidence struct {
	Kind  string          `json:"kind"`
	Proof json.RawMessage `json:"proof"`
}

// IntervalEvidence carries the completeness certificate and authenticated
// checkpoint in the Evidence Bundle v2 wire shape.
type IntervalEvidence struct {
	Certificate CompletenessCertificate `json:"completeness_certificate"`
	Checkpoint  BundleCheckpoint        `json:"checkpoint"`
}

// CompletenessCertificate is the Evidence Bundle v2 per-record range claim.
type CompletenessCertificate struct {
	LogID       string                `json:"log_id"`
	RangeRoot   string                `json:"range_root"`
	FirstSeq    uint64                `json:"first_seq"`
	LastSeq     uint64                `json:"last_seq"`
	BodyDigests []string              `json:"body_digests"`
	RangeProof  RangeWitness          `json:"range_proof"`
	Memberships map[string]Membership `json:"memberships"`
}

// RangeWitness is the index-shaped range proof a completeness certificate carries.
type RangeWitness struct {
	FromSeq   uint64   `json:"from_seq"`
	ToSeq     uint64   `json:"to_seq"`
	Size      uint64   `json:"size"`
	FromIndex uint64   `json:"from_index"`
	ToIndex   uint64   `json:"to_index"`
	Witness   []string `json:"witness"`
}

// Membership binds one record to its log coordinates with an inclusion proof.
type Membership struct {
	LogCoordinates LogCoordinates `json:"log_coordinates"`
	InclusionProof MMRInclusion   `json:"inclusion_proof"`
}

// LogCoordinates locates a record in the log.
type LogCoordinates struct {
	LogID     string `json:"log_id"`
	Seq       uint64 `json:"seq"`
	LeafIndex uint64 `json:"leaf_index"`
}

// MMRInclusion is the portable MMR inclusion proof.
type MMRInclusion struct {
	V          uint64   `json:"v"`
	Kind       string   `json:"kind"`
	Size       uint64   `json:"size"`
	LeafIndex  uint64   `json:"leaf_index"`
	Witness    []string `json:"witness"`
	PeaksLeft  []string `json:"peaks_left"`
	PeaksRight []string `json:"peaks_right"`
}

// BundleCheckpoint is the checkpoint member of an Evidence Bundle. COSE is the
// base64url signed checkpoint statement, so a verifier authenticates the root
// rather than trusting the producer's copy of it.
type BundleCheckpoint struct {
	Root    string `json:"root"`
	MMRSize uint64 `json:"mmr_size"`
	COSE    string `json:"cose,omitempty"`
}
