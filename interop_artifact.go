// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"fmt"
)

// The canonical Artifact (ER §4.1, §5; profile §6.2) is a deterministic
// function of (subject, resolved anchor, derivation) only. Its forms:
//
//   - record, no derivation: the record's own octets, the capsule preimage
//     whose SHA-256 is the record id, so subject.record equals
//     artifact_digest and the CLL material proves it (profile §6.2.1);
//   - full_history, range, correlation, exchange, no derivation: JCS of
//     {anchor, evidence_stream, records, subject}, each record as
//     {body, digest, leaf_index} with body the record octets in hex;
//   - checkpoints, no derivation: JCS of {anchor, checkpoints,
//     evidence_stream, subject}, each checkpoint its signed statement in hex;
//   - history_card/1: JCS of {anchor, derivation, evidence_stream, history,
//     subject}, each entry {checkpoint, consistency_from_previous} (ER §8.1);
//   - a registered derivation: JCS of {anchor, derivation, evidence_stream,
//     records, subject}, each record carrying only its digest, its position
//     and the definition's declared fields (profile §6.5).
//
// The member names are the stream binding's string identifiers (ruling 4).
// No member names a product, and nothing the responder asserts about its own
// checks is carried: a verifier checks the bytes.

// builtArtifact is a canonical Artifact and the material that proves it.
type builtArtifact struct {
	artifact     []byte
	verification []VerificationEntry
	material     [][]byte
}

func (b *Book) buildArtifactLocked(ctx context.Context, view *bookView, req EvidenceRequest, anchor Checkpoint, leaves []uint64, def *derivationEntry) (builtArtifact, error) {
	head := map[string]any{"anchor": anchorDigest(anchor), "evidence_stream": anchor.LogID, "subject": req.Subject.value()}
	var built builtArtifact
	switch {
	case req.Derivation == DerivationHistoryCard || req.Subject.Kind == SubjectCheckpoints:
		chain := checkpointsThrough(view.checkpoints, anchor)
		if req.Derivation == DerivationHistoryCard {
			history, err := b.historyCardLocked(ctx, chain, anchor)
			if err != nil {
				return builtArtifact{}, err
			}
			head["derivation"], head["history"] = DerivationHistoryCard, history
		} else {
			list := make([]any, len(chain))
			for i, cp := range chain {
				list[i] = hex.EncodeToString(cp.Statement)
			}
			head["checkpoints"] = list
		}
	case def != nil:
		records, err := b.projectLocked(ctx, leaves, def)
		if err != nil {
			return builtArtifact{}, err
		}
		head["derivation"], head["records"] = def.id, records
	case req.Subject.Kind == SubjectRecord:
		id := b.order[leaves[0]]
		stored, err := b.storedLocked(ctx, id)
		if err != nil {
			return builtArtifact{}, err
		}
		body, err := capsulePreimage(stored.Capsule, id)
		if err != nil {
			return builtArtifact{}, err
		}
		verification, material, err := cllMaterial(ctx, b.commitments.(cllMaterialSource), leaves[0]+1, anchor)
		if err != nil {
			return builtArtifact{}, err
		}
		return builtArtifact{artifact: body, verification: verification, material: material}, nil
	default:
		records := make([]any, len(leaves))
		for i, leaf := range leaves {
			id := b.order[leaf]
			stored, err := b.storedLocked(ctx, id)
			if err != nil {
				return builtArtifact{}, err
			}
			body, err := capsulePreimage(stored.Capsule, id)
			if err != nil {
				return builtArtifact{}, err
			}
			records[i] = map[string]any{"leaf_index": leaf, "digest": id, "body": hex.EncodeToString(body)}
		}
		head["records"] = records
	}
	artifact, err := toJCS(head)
	if err != nil {
		return builtArtifact{}, err
	}
	built.artifact = artifact
	return built, nil
}

// checkpointsThrough is the stored checkpoints from the first through the
// anchor, oldest first.
func checkpointsThrough(history []Checkpoint, anchor Checkpoint) []Checkpoint {
	var out []Checkpoint
	for _, cp := range history {
		if cp.Entries <= anchor.Entries && cp.TreeSize <= anchor.TreeSize {
			out = append(out, cp)
		}
		if cp.ID == anchor.ID {
			break
		}
	}
	return out
}

// historyCardLocked is history_card/1 (ER §8.1): for each checkpoint from
// the stream's first to the anchor, the checkpoint and the consistency proof
// from the previous one (cll-consistency-proof octets, hex). The card holds
// no record content. This responder holds no witness receipts for its
// checkpoints, so none are listed.
func (b *Book) historyCardLocked(ctx context.Context, chain []Checkpoint, anchor Checkpoint) ([]any, error) {
	source := b.commitments.(cllMaterialSource)
	history := make([]any, len(chain))
	for i, cp := range chain {
		var consistency any
		if i > 0 && chain[i-1].TreeSize < cp.TreeSize {
			proof, err := source.cllConsistencyProof(ctx, chain[i-1].TreeSize, cp.TreeSize, anchor)
			if err != nil {
				return nil, err
			}
			consistency = hex.EncodeToString(proof)
		}
		history[i] = map[string]any{"checkpoint": hex.EncodeToString(cp.Statement), "consistency_from_previous": consistency}
	}
	return history, nil
}

// projectLocked builds a registered derivation's records: each record's
// digest, its position, and the declared header fields it carries.
func (b *Book) projectLocked(ctx context.Context, leaves []uint64, def *derivationEntry) ([]any, error) {
	records := make([]any, len(leaves))
	for i, leaf := range leaves {
		id := b.order[leaf]
		stored, err := b.storedLocked(ctx, id)
		if err != nil {
			return nil, err
		}
		header, err := decodeStrictJSON(stored.Header)
		fields, ok := header.(map[string]any)
		if err != nil || !ok {
			return nil, fmt.Errorf("%w: header of %s", ErrCorrupt, id)
		}
		entry := map[string]any{"digest": id, "leaf_index": leaf}
		for _, name := range def.def.Fields {
			if value, present := fields[name]; present {
				entry[name] = value
			}
		}
		records[i] = entry
	}
	return records, nil
}
