// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
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
//   - standard_history/1: JCS of the card (SH §4), counts only;
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
	// declined: the answer failed the disclosure scan; it is refused
	// policy_declined.
	declined bool
	// overLimit: the answer would carry more items than the cost policy
	// allows; it is refused for a limit (limitReason).
	overLimit bool
}

// declaredFields is the field set an artifact form may carry (NFR-2). The
// disclosure scan refuses an artifact with any member outside it.
type declaredFields struct {
	top   []string
	entry []string
	// list is the member holding the entries; bodies are hex record octets
	// in body (records) or none.
	list string
}

var (
	recordSetFields   = declaredFields{top: []string{"anchor", "evidence_stream", "records", "subject"}, entry: []string{"body", "digest", "leaf_index"}, list: "records"}
	checkpointsFields = declaredFields{top: []string{"anchor", "checkpoints", "evidence_stream", "subject"}, list: "checkpoints"}
	historyFields     = declaredFields{top: []string{"anchor", "derivation", "evidence_stream", "history", "subject"}, entry: []string{"checkpoint", "consistency_from_previous"}, list: "history"}
)

// buildArtifact builds the canonical Artifact from view's snapshot. It
// reads the store and substrate, which lock themselves, and nothing else the
// book changes, so it runs without the book's lock.
func (b *Book) buildArtifact(ctx context.Context, view *bookView, req EvidenceRequest, anchor Checkpoint, leaves []uint64, def *derivationEntry) (builtArtifact, error) {
	if req.Derivation == DerivationStandardHistory {
		// A card of counts whatever the history's length: the item cap
		// bounds record lists, and the card lists none.
		return b.standardHistoryCard(ctx, view, anchor, leaves)
	}
	maxItems := b.responder.MaxAnswerItems
	if maxItems == 0 {
		maxItems = DefaultMaxAnswerItems
	}
	if len(leaves) > maxItems {
		return builtArtifact{overLimit: true}, nil
	}
	head := map[string]any{"anchor": anchorDigest(anchor), "evidence_stream": anchor.LogID, "subject": req.Subject.Value()}
	var built builtArtifact
	var declared declaredFields
	switch {
	case req.Derivation == DerivationHistoryCard || req.Subject.Kind == SubjectCheckpoints:
		chain := checkpointsThrough(view.checkpoints, anchor)
		if len(chain) > maxItems {
			return builtArtifact{overLimit: true}, nil
		}
		if req.Derivation == DerivationHistoryCard {
			history, err := b.historyCard(ctx, chain, anchor)
			if err != nil {
				return builtArtifact{}, err
			}
			head["derivation"], head["history"], declared = DerivationHistoryCard, history, historyFields
		} else {
			list := make([]any, len(chain))
			for i, cp := range chain {
				list[i] = hex.EncodeToString(cp.Statement)
			}
			head["checkpoints"], declared = list, checkpointsFields
		}
	case def != nil:
		records, err := b.project(ctx, view, leaves, def)
		if err != nil {
			return builtArtifact{}, err
		}
		head["derivation"], head["records"] = def.id, records
		declared = declaredFields{top: []string{"anchor", "derivation", "evidence_stream", "records", "subject"}, entry: append([]string{"digest", "leaf_index"}, def.def.Fields...), list: "records"}
	case req.Subject.Kind == SubjectRecord:
		id := view.log[leaves[0]].RecordID
		stored, err := b.store.GetRecord(ctx, id)
		if err != nil {
			return builtArtifact{}, err
		}
		body, err := capsulePreimage(stored.Capsule, id)
		if err != nil {
			return builtArtifact{}, err
		}
		if !view.disclosureClean(body, nil) {
			return builtArtifact{declined: true}, nil
		}
		prover, err := b.commitments.(cllMaterialSource).cllProver(ctx, anchor)
		if err != nil {
			return builtArtifact{}, err
		}
		verification, material, err := cllMaterial(prover, leaves[0]+1)
		if err != nil {
			return builtArtifact{}, err
		}
		return builtArtifact{artifact: body, verification: verification, material: material}, nil
	default:
		records := make([]any, len(leaves))
		for i, leaf := range leaves {
			id := view.log[leaf].RecordID
			stored, err := b.store.GetRecord(ctx, id)
			if err != nil {
				return builtArtifact{}, err
			}
			body, err := capsulePreimage(stored.Capsule, id)
			if err != nil {
				return builtArtifact{}, err
			}
			records[i] = map[string]any{"leaf_index": leaf, "digest": id, "body": hex.EncodeToString(body)}
		}
		head["records"], declared = records, recordSetFields
	}
	artifact, err := evidencerequest.EncodeJCS(head)
	if err != nil {
		return builtArtifact{}, err
	}
	if !view.disclosureClean(artifact, &declared) {
		return builtArtifact{declined: true}, nil
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

// historyCard is history_card/1 (ER §8.1): for each checkpoint from
// the stream's first to the anchor, the checkpoint and the consistency proof
// from the previous one (cll-consistency-proof octets, hex). The card holds
// no record content. This responder holds no witness receipts for its
// checkpoints, so none are listed.
func (b *Book) historyCard(ctx context.Context, chain []Checkpoint, anchor Checkpoint) ([]any, error) {
	var prover *cllProver
	history := make([]any, len(chain))
	for i, cp := range chain {
		var consistency any
		if i > 0 && chain[i-1].TreeSize < cp.TreeSize {
			if prover == nil {
				var err error
				if prover, err = b.commitments.(cllMaterialSource).cllProver(ctx, anchor); err != nil {
					return nil, err
				}
			}
			proof, err := prover.consistencyProof(chain[i-1].TreeSize, cp.TreeSize)
			if err != nil {
				return nil, err
			}
			consistency = hex.EncodeToString(proof)
		}
		history[i] = map[string]any{"checkpoint": hex.EncodeToString(cp.Statement), "consistency_from_previous": consistency}
	}
	return history, nil
}

// disclosureClean is the byte scan and linkage check run on every artifact
// before release (NFR-2). With declared set, the artifact is a JSON object
// and every member, and every member of each entry of its list, must be in
// the declared field set. Every record body it carries, and the artifact
// itself when it is a bare record, must contain no principal or
// counterparty reference this book holds, and no entry may be a record the
// responder keeps for its own bookkeeping.
func (v *bookView) disclosureClean(artifact []byte, declared *declaredFields) bool {
	identities := map[string]bool{}
	for _, record := range v.log {
		h := record.Header
		for _, ref := range []string{h.PrincipalRef, h.CounterpartyRef} {
			if ref != "" {
				identities[ref] = true
			}
		}
	}
	if declared == nil {
		return !mentionsAny(artifact, identities)
	}
	root, err := evidencerequest.DecodeJSON(artifact)
	object, ok := root.(map[string]any)
	if err != nil || !ok {
		return false
	}
	for name := range object {
		if !slices.Contains(declared.top, name) {
			return false
		}
	}
	entries, _ := object[declared.list].([]any)
	for _, item := range entries {
		entry, isMap := item.(map[string]any)
		if !isMap {
			// checkpoints: a hex statement, nothing else.
			if _, isText := item.(string); !isText || declared.entry != nil {
				return false
			}
			continue
		}
		for name := range entry {
			if !slices.Contains(declared.entry, name) {
				return false
			}
		}
		if digest, ok := entry["digest"].(string); ok {
			if record, held := v.record(digest); held && (record.Header.RecordType == RecordTypeRequestAnswered || record.Header.RecordType == RecordTypeDisclosure) {
				return false
			}
		}
		if body, ok := entry["body"].(string); ok {
			raw, err := hex.DecodeString(body)
			if err != nil || mentionsAny(raw, identities) {
				return false
			}
		}
	}
	return true
}

// mentionsAny reports whether a JSON document carries any of the strings as
// a string value or member name.
func mentionsAny(document []byte, strs map[string]bool) bool {
	if len(strs) == 0 {
		return false
	}
	root, err := evidencerequest.DecodeJSON(document)
	if err != nil {
		// Not JSON: scan the raw bytes.
		for s := range strs {
			if strings.Contains(string(document), s) {
				return true
			}
		}
		return false
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case string:
			return strs[x]
		case []any:
			return slices.ContainsFunc(x, walk)
		case map[string]any:
			for k, item := range x {
				if strs[k] || walk(item) {
					return true
				}
			}
		}
		return false
	}
	return walk(root)
}

// project builds a registered derivation's records: each record's
// digest, its position, and the declared header fields it carries.
func (b *Book) project(ctx context.Context, view *bookView, leaves []uint64, def *derivationEntry) ([]any, error) {
	records := make([]any, len(leaves))
	for i, leaf := range leaves {
		id := view.log[leaf].RecordID
		stored, err := b.store.GetRecord(ctx, id)
		if err != nil {
			return nil, err
		}
		header, err := evidencerequest.DecodeJSON(stored.Header)
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
