// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/action-state-group/cll-go/checkpoint"
	"github.com/action-state-group/cll-go/cll"
	"github.com/action-state-group/cll-go/mmr"
	"github.com/action-state-group/cll-go/store/jsonl"
)

// SubstrateKindCLL labels checkpoints and proofs produced by the CLL binding.
const SubstrateKindCLL = "cll"

// CLLSubstrate embeds a Checkpointed Local Log. It is the only place this
// module touches CLL; everything it returns is an evidencebook type.
type CLLSubstrate struct {
	mu     sync.Mutex
	logID  string
	log    *jsonl.Store
	runner *checkpoint.Runner
}

// OpenCLL opens (initializing if absent) a CLL journal at path. checkpointKey
// signs the log's checkpoints in the CLL checkpoint wire format; it is the
// substrate's key, held by the substrate, and may differ from the book signer.
func OpenCLL(path, logID string, checkpointKey ed25519.PrivateKey) (*CLLSubstrate, error) {
	if err := cll.ValidateIdentifier(logID); err != nil {
		return nil, fmt.Errorf("%w: log id: %v", ErrInvalid, err)
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := jsonl.Init(path); err != nil {
			return nil, fmt.Errorf("init CLL journal: %w", err)
		}
	}
	log, err := jsonl.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open CLL journal: %w", err)
	}
	signer, err := checkpoint.NewEd25519Signer(checkpointKey)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%w: checkpoint key: %v", ErrInvalid, err), log.Close())
	}
	config := checkpoint.DefaultRunnerConfig(logID)
	// The book, not the runner, decides when to checkpoint: any pending entry
	// is due the moment the book asks.
	config.Cadence = checkpoint.Config{CadenceEntries: 1, CadenceAge: time.Hour}
	config.ScanLimit = cll.MaxScanLimit
	runner, err := checkpoint.NewRunner(config, log, signer)
	if err != nil {
		return nil, errors.Join(err, log.Close())
	}
	return &CLLSubstrate{logID: logID, log: log, runner: runner}, nil
}

func (s *CLLSubstrate) Info() SubstrateInfo {
	return SubstrateInfo{Kind: SubstrateKindCLL, LogID: s.logID}
}

func (s *CLLSubstrate) Append(ctx context.Context, recordID string, at time.Time) (uint64, error) {
	value, err := decodeRecordID(recordID)
	if err != nil {
		return 0, err
	}
	result, err := s.log.Append(ctx, cll.AppendInput{Value: value, AppendedAt: at.UTC()})
	if err != nil {
		return 0, fmt.Errorf("CLL append: %w", err)
	}
	return result.Entry.Seq, nil
}

func (s *CLLSubstrate) Size(ctx context.Context) (uint64, error) {
	var size uint64
	for {
		entries, err := s.log.ScanEntries(ctx, size, cll.MaxScanLimit)
		if err != nil {
			return 0, err
		}
		if len(entries) == 0 {
			return size, nil
		}
		size = entries[len(entries)-1].Seq
	}
}

func (s *CLLSubstrate) Entries(ctx context.Context, afterSeq uint64, limit int) ([]SubstrateEntry, error) {
	if limit <= 0 || limit > cll.MaxScanLimit {
		limit = cll.MaxScanLimit
	}
	entries, err := s.log.ScanEntries(ctx, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SubstrateEntry, len(entries))
	for i, entry := range entries {
		out[i] = SubstrateEntry{Seq: entry.Seq, RecordID: hex.EncodeToString(entry.Value), AppendedAt: entry.AppendedAt}
	}
	return out, nil
}

func (s *CLLSubstrate) Checkpoint(ctx context.Context, now time.Time) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		changed, err := s.runner.RunOnce(ctx, now)
		if err != nil {
			return Checkpoint{}, fmt.Errorf("CLL checkpoint: %w", err)
		}
		if !changed {
			break
		}
	}
	return s.Latest(ctx)
}

func (s *CLLSubstrate) Latest(ctx context.Context) (Checkpoint, error) {
	state, err := s.log.LoadCLL(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	if state.Checkpoint == nil {
		return Checkpoint{}, fmt.Errorf("%w: no checkpoint yet", ErrNotFound)
	}
	return checkpointFromStatement(state.Checkpoint.Bytes)
}

// checkpointFromStatement parses and signature-checks a CLL checkpoint
// statement. The returned Entries is the leaf count at the checkpoint size.
func checkpointFromStatement(statement []byte) (Checkpoint, error) {
	record, err := checkpoint.ParseRecord(statement)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint statement: %v", ErrCorrupt, err)
	}
	if err := record.VerifySignature(); err != nil {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint signature: %v", ErrCorrupt, err)
	}
	leaves, ok := mmr.LeafCount(record.MMRSize)
	if !ok {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint size %d is not a complete MMR", ErrCorrupt, record.MMRSize)
	}
	return Checkpoint{
		Kind:      SubstrateKindCLL,
		LogID:     record.LogID,
		ID:        fmt.Sprintf("%s:%d", record.Root, record.MMRSize),
		Entries:   leaves,
		Root:      record.Root,
		TreeSize:  record.MMRSize,
		KeyID:     record.KeyID,
		IssuedAt:  record.Timestamp,
		Statement: append([]byte(nil), statement...),
	}, nil
}

// tree restores the MMR over every indexed node and checks it reaches at.
func (s *CLLSubstrate) tree(ctx context.Context, at Checkpoint) (*mmr.Tree, error) {
	if at.Kind != SubstrateKindCLL || at.LogID != s.logID {
		return nil, fmt.Errorf("%w: checkpoint is not from this log", ErrInvalid)
	}
	state, err := s.log.LoadCLL(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := mmr.New(state.Nodes)
	if err != nil {
		return nil, fmt.Errorf("%w: restore MMR: %v", ErrCorrupt, err)
	}
	if at.TreeSize > tree.Size() {
		return nil, fmt.Errorf("%w: checkpoint size %d exceeds indexed tree %d", ErrNotCovered, at.TreeSize, tree.Size())
	}
	peaks, err := tree.PeakHashesAt(at.TreeSize)
	if err != nil {
		return nil, err
	}
	if hex.EncodeToString(mmr.RootFromPeaks(peaks)) != at.Root {
		return nil, fmt.Errorf("%w: checkpoint root does not match this log", ErrCorrupt)
	}
	return tree, nil
}

func (s *CLLSubstrate) ProveInclusion(ctx context.Context, seq uint64, at Checkpoint) (InclusionEvidence, error) {
	if !at.Covers(seq) {
		return InclusionEvidence{}, fmt.Errorf("%w: seq %d under checkpoint %s", ErrNotCovered, seq, at.ID)
	}
	tree, err := s.tree(ctx, at)
	if err != nil {
		return InclusionEvidence{}, err
	}
	proof, err := tree.InclusionProof(seq-1, at.TreeSize)
	if err != nil {
		return InclusionEvidence{}, err
	}
	encoded, err := json.Marshal(portableInclusion(proof))
	if err != nil {
		return InclusionEvidence{}, err
	}
	return InclusionEvidence{Kind: "cll-mmr-inclusion", Seq: seq, Proof: encoded}, nil
}

func (s *CLLSubstrate) ProveInterval(ctx context.Context, firstSeq uint64, at Checkpoint) (IntervalEvidence, error) {
	if !at.Covers(firstSeq) {
		return IntervalEvidence{}, fmt.Errorf("%w: seq %d under checkpoint %s", ErrNotCovered, firstSeq, at.ID)
	}
	tree, err := s.tree(ctx, at)
	if err != nil {
		return IntervalEvidence{}, err
	}
	entries, err := s.entriesThrough(ctx, at.Entries)
	if err != nil {
		return IntervalEvidence{}, err
	}
	certificate := CompletenessCertificate{LogID: s.logID, RangeRoot: at.Root, FirstSeq: firstSeq, LastSeq: at.Entries, Memberships: make(map[string]Membership)}
	for _, entry := range entries[firstSeq-1:] {
		proof, err := tree.InclusionProof(entry.Seq-1, at.TreeSize)
		if err != nil {
			return IntervalEvidence{}, err
		}
		id := hex.EncodeToString(entry.Value)
		certificate.BodyDigests = append(certificate.BodyDigests, id)
		certificate.Memberships[id] = Membership{
			LogCoordinates: LogCoordinates{LogID: s.logID, Seq: entry.Seq, LeafIndex: entry.Seq - 1},
			InclusionProof: portableInclusion(proof),
		}
	}
	rangeProof, err := tree.RangeProof(firstSeq-1, at.Entries-1, at.TreeSize)
	if err != nil {
		return IntervalEvidence{}, err
	}
	certificate.RangeProof = RangeWitness{FromSeq: firstSeq, ToSeq: at.Entries, Size: at.TreeSize, FromIndex: firstSeq - 1, ToIndex: at.Entries - 1, Witness: hexList(rangeProof.Witness)}
	return IntervalEvidence{
		Certificate: certificate,
		Checkpoint:  BundleCheckpoint{LogID: s.logID, Root: at.Root, MMRSize: at.TreeSize, COSE: base64.RawURLEncoding.EncodeToString(at.Statement)},
	}, nil
}

func (s *CLLSubstrate) entriesThrough(ctx context.Context, last uint64) ([]cll.Entry, error) {
	var out []cll.Entry
	for uint64(len(out)) < last {
		batch, err := s.log.ScanEntries(ctx, uint64(len(out)), cll.MaxScanLimit)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return nil, fmt.Errorf("%w: log ends at %d before %d", ErrCorrupt, len(out), last)
		}
		out = append(out, batch...)
	}
	return out[:last], nil
}

type portableConsistency struct {
	V        uint64     `json:"v"`
	Kind     string     `json:"kind"`
	OldSize  uint64     `json:"old_size"`
	NewSize  uint64     `json:"new_size"`
	OldPeaks []string   `json:"old_peaks"`
	Witness  [][]string `json:"witness"`
	NewPeaks []string   `json:"new_peaks"`
}

func (s *CLLSubstrate) ProveConsistency(ctx context.Context, older, newer Checkpoint) (ConsistencyEvidence, error) {
	if older.TreeSize > newer.TreeSize {
		return ConsistencyEvidence{}, fmt.Errorf("%w: older checkpoint is larger than newer", ErrInvalid)
	}
	tree, err := s.tree(ctx, newer)
	if err != nil {
		return ConsistencyEvidence{}, err
	}
	proof, err := tree.ConsistencyProof(older.TreeSize, newer.TreeSize)
	if err != nil {
		return ConsistencyEvidence{}, err
	}
	witness := make([][]string, len(proof.Witness))
	for i, path := range proof.Witness {
		witness[i] = hexList(path)
	}
	encoded, err := json.Marshal(portableConsistency{V: proof.V, Kind: proof.Kind, OldSize: proof.OldSize, NewSize: proof.NewSize, OldPeaks: hexList(proof.OldPeaks), Witness: witness, NewPeaks: hexList(proof.NewPeaks)})
	if err != nil {
		return ConsistencyEvidence{}, err
	}
	return ConsistencyEvidence{Kind: "cll-mmr-consistency", Proof: encoded}, nil
}

func (s *CLLSubstrate) Release() error {
	return s.log.Close()
}

func portableInclusion(proof mmr.InclusionProof) MMRInclusion {
	return MMRInclusion{V: proof.V, Kind: proof.Kind, Size: proof.Size, LeafIndex: proof.LeafIndex, Witness: hexList(proof.Witness), PeaksLeft: hexList(proof.PeaksLeft), PeaksRight: hexList(proof.PeaksRight)}
}

func hexList(values [][]byte) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = hex.EncodeToString(value)
	}
	return out
}

func decodeRecordID(recordID string) ([]byte, error) {
	if !hex64.MatchString(recordID) {
		return nil, fmt.Errorf("%w: record id %q is not 64 lowercase hex characters", ErrInvalid, recordID)
	}
	return hex.DecodeString(recordID)
}

// VerifyCheckpoints checks a checkpoints/v1 history and returns its newest
// checkpoint as the signed statement states it. Every field is taken from
// the signed statement, never from the listed copy: each statement must
// verify, sign under pinnedKey, name the same log as the first, and match
// its listed fields exactly; sizes must strictly grow; and each consistency
// proof must show the next signed root append-only extends the previous one.
func VerifyCheckpoints(checkpoints []Checkpoint, consistency []ConsistencyEvidence, pinnedKey string) (Checkpoint, error) {
	if pinnedKey == "" {
		return Checkpoint{}, fmt.Errorf("%w: a pinned checkpoint key is required", ErrInvalid)
	}
	if len(checkpoints) == 0 {
		return Checkpoint{}, fmt.Errorf("%w: no checkpoints", ErrInvalid)
	}
	if len(consistency) != len(checkpoints)-1 {
		return Checkpoint{}, fmt.Errorf("%w: %d consistency proofs for %d checkpoints", ErrInvalid, len(consistency), len(checkpoints))
	}
	signed := make([]Checkpoint, len(checkpoints))
	for i, listed := range checkpoints {
		parsed, err := checkpointFromStatement(listed.Statement)
		if err != nil {
			return Checkpoint{}, err
		}
		if parsed.KeyID != pinnedKey {
			return Checkpoint{}, fmt.Errorf("%w: checkpoint %d is signed by %s, not the pinned key", ErrInvalid, i, parsed.KeyID)
		}
		if !sameCheckpoint(parsed, listed) {
			return Checkpoint{}, fmt.Errorf("%w: checkpoint %d's listed fields differ from its signed statement", ErrInvalid, i)
		}
		if i > 0 {
			if parsed.LogID != signed[0].LogID {
				return Checkpoint{}, fmt.Errorf("%w: checkpoint %d names log %s, not %s", ErrInvalid, i, parsed.LogID, signed[0].LogID)
			}
			if parsed.TreeSize <= signed[i-1].TreeSize {
				return Checkpoint{}, fmt.Errorf("%w: checkpoint %d does not grow the log", ErrInvalid, i)
			}
			if err := verifyCLLConsistency(signed[i-1], parsed, consistency[i-1]); err != nil {
				return Checkpoint{}, fmt.Errorf("checkpoint %d: %w", i, err)
			}
		}
		signed[i] = parsed
	}
	return signed[len(signed)-1], nil
}

func sameCheckpoint(signed, listed Checkpoint) bool {
	return signed.Kind == listed.Kind && signed.LogID == listed.LogID && signed.ID == listed.ID &&
		signed.Entries == listed.Entries && signed.Root == listed.Root && signed.TreeSize == listed.TreeSize &&
		signed.KeyID == listed.KeyID && signed.IssuedAt.Equal(listed.IssuedAt)
}

// verifyCLLConsistency must only be called with checkpoints parsed from
// their signed statements.
func verifyCLLConsistency(older, newer Checkpoint, evidence ConsistencyEvidence) error {
	var wire portableConsistency
	if evidence.Kind != "cll-mmr-consistency" || json.Unmarshal(evidence.Proof, &wire) != nil {
		return fmt.Errorf("%w: consistency evidence is not a CLL proof", ErrInvalid)
	}
	decode := func(values []string) ([][]byte, error) {
		out := make([][]byte, len(values))
		for i, v := range values {
			b, err := decodeRecordID(v)
			if err != nil {
				return nil, err
			}
			out[i] = b
		}
		return out, nil
	}
	oldPeaks, err1 := decode(wire.OldPeaks)
	newPeaks, err2 := decode(wire.NewPeaks)
	witness := make([][][]byte, len(wire.Witness))
	var err3 error
	for i, path := range wire.Witness {
		if witness[i], err3 = decode(path); err3 != nil {
			break
		}
	}
	oldRoot, err4 := decodeRecordID(older.Root)
	newRoot, err5 := decodeRecordID(newer.Root)
	if err := errors.Join(err1, err2, err3, err4, err5); err != nil {
		return err
	}
	if wire.OldSize != older.TreeSize || wire.NewSize != newer.TreeSize {
		return fmt.Errorf("%w: consistency proof sizes do not match the checkpoints", ErrInvalid)
	}
	proof := mmr.ConsistencyProof{V: wire.V, Kind: wire.Kind, OldSize: wire.OldSize, NewSize: wire.NewSize, OldPeaks: oldPeaks, Witness: witness, NewPeaks: newPeaks}
	if !mmr.VerifyConsistency(oldRoot, newRoot, proof) {
		return fmt.Errorf("%w: consistency proof does not verify", ErrInvalid)
	}
	return nil
}
