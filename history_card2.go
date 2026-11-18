// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// DerivationHistoryCard2 is history_card/2 (ER -03 §8.1.2): a sample of the
// stream's checkpoints whose size grows with the logarithm of their number,
// where history_card/1 lists every one.
const DerivationHistoryCard2 = evidencerequest.DerivationHistoryCard2

// History card windows: the number of newest checkpoints a /2 card lists in
// full, chosen by the responder from 1 to 64 (ER -03 §8.1.2).
const (
	MinHistoryWindow     = 1
	MaxHistoryWindow     = evidencerequest.MaxHistoryWindow
	DefaultHistoryWindow = 16
)

// historyEntries2 is the history of a /2 card (or a ranged answer): one
// entry {position, checkpoint, receipts, consistency_from_previous} per
// position, each proof from the entry listed before it. chain[i] is the
// checkpoint at position i; positions ascend. This responder holds no
// witness receipts for its checkpoints, so every list is empty.
func historyEntries2(ctx context.Context, src cllMaterialSource, chain []Checkpoint, anchor Checkpoint, positions []uint64) ([]any, error) {
	var prover *cllProver
	history := make([]any, len(positions))
	for i, p := range positions {
		if p >= uint64(len(chain)) {
			return nil, fmt.Errorf("%w: checkpoint position %d is past the stream's %d checkpoints", ErrInvalid, p, len(chain))
		}
		cp := chain[p]
		var consistency any
		if i > 0 {
			before := chain[positions[i-1]]
			if before.TreeSize >= cp.TreeSize {
				return nil, fmt.Errorf("%w: checkpoint %d does not grow the log past checkpoint %d", ErrCorrupt, p, positions[i-1])
			}
			if prover == nil {
				var err error
				if prover, err = src.cllProver(ctx, anchor); err != nil {
					return nil, err
				}
			}
			proof, err := prover.consistencyProof(before.TreeSize, cp.TreeSize)
			if err != nil {
				return nil, err
			}
			consistency = hex.EncodeToString(proof)
		}
		history[i] = map[string]any{
			"position": p, "checkpoint": hex.EncodeToString(cp.Statement),
			"receipts": []any{}, "consistency_from_previous": consistency,
		}
	}
	return history, nil
}

// historyCard2 is the history of a history_card/2 as of the chain's last
// checkpoint, which is the anchor: the entries at P(n, window).
func (b *Book) historyCard2(ctx context.Context, chain []Checkpoint, anchor Checkpoint, window int) ([]any, error) {
	positions := evidencerequest.HistoryPositions(uint64(len(chain)-1), uint64(window))
	return historyEntries2(ctx, b.commitments.(cllMaterialSource), chain, anchor, positions)
}

// checkpointsRange is the answer to a checkpoints subject with content
// (first, last) (ER -03 §8.1.3): one entry per position from first through
// last, each proof from the position before it, none in the first.
func (b *Book) checkpointsRange(ctx context.Context, chain []Checkpoint, anchor Checkpoint, first, last uint64) (history []any, toAnchor any, err error) {
	positions := make([]uint64, 0, last-first+1)
	for p := first; p <= last; p++ {
		positions = append(positions, p)
	}
	src := b.commitments.(cllMaterialSource)
	if history, err = historyEntries2(ctx, src, chain, anchor, positions); err != nil {
		return nil, nil, err
	}
	// consistency_to_anchor: from the checkpoint at b to the anchor's, null
	// when b is the anchor's position.
	if last != uint64(len(chain)-1) {
		prover, err := src.cllProver(ctx, anchor)
		if err != nil {
			return nil, nil, err
		}
		proof, err := prover.consistencyProof(chain[last].TreeSize, anchor.TreeSize)
		if err != nil {
			return nil, nil, err
		}
		toAnchor = hex.EncodeToString(proof)
	}
	return history, toAnchor, nil
}

// verifyCheckpointRange checks the artifact of a checkpoints subject with a
// range (ER -03 §8.1.3): one entry per position from a to b in order, every
// checkpoint signed under the pinned key in one log, each proof extending the
// entry before it, the first entry without one. It returns the last
// checkpoint's statement; whether the range reaches the envelope's anchor is
// the request's coverage, not the artifact's.
func verifyCheckpointRange(req EvidenceRequest, e Envelope, checkpointKey string) ([]byte, error) {
	bad := func(why string) error {
		return fmt.Errorf("%w: anchor_inclusion: the checkpoint range %s", ErrInvalid, why)
	}
	decoded, err := evidencerequest.DecodeJSON(e.Artifact)
	card, ok := decoded.(map[string]any)
	if err != nil || !ok || !keysExactly(card, "history", "consistency_to_anchor") {
		return nil, bad("is not a map with the members history and consistency_to_anchor")
	}
	history, _ := card["history"].([]any)
	first, last := req.Subject.Range[0], req.Subject.Range[1]
	if uint64(len(history)) != last-first+1 {
		return nil, bad("does not hold one entry per position")
	}
	var previous signedCheckpoint
	var raw []byte
	for i, item := range history {
		entry, _ := item.(map[string]any)
		if !keysExactly(entry, "position", "checkpoint", "receipts", "consistency_from_previous") {
			return nil, bad(fmt.Sprintf("entry %d has members other than a position, checkpoint, receipts and consistency proof", i))
		}
		if position, ok := evidencerequest.UintValue(entry["position"]); !ok || position != first+uint64(i) {
			return nil, bad(fmt.Sprintf("entry %d is not at position %d", i, first+uint64(i)))
		}
		if _, isList := entry["receipts"].([]any); !isList {
			return nil, bad(fmt.Sprintf("entry %d's receipts are not a list", i))
		}
		text, _ := entry["checkpoint"].(string)
		if !lowerHex(text) {
			return nil, bad(fmt.Sprintf("entry %d's checkpoint is not hex", i))
		}
		raw, _ = hex.DecodeString(text)
		cp, err := readSignedCheckpoint(raw, checkpointKey)
		if err != nil {
			return nil, fmt.Errorf("anchor_inclusion: entry %d: %w", i, err)
		}
		proof, isText := entry["consistency_from_previous"].(string)
		if (i == 0) != (entry["consistency_from_previous"] == nil) || (i > 0 && (!isText || !lowerHex(proof))) {
			return nil, bad(fmt.Sprintf("entry %d is not a checkpoint with its consistency proof", i))
		}
		if i > 0 {
			if cp.LogID != previous.LogID || cp.TreeSize <= previous.TreeSize {
				return nil, bad(fmt.Sprintf("entry %d does not extend the log", i))
			}
			octets, _ := hex.DecodeString(proof)
			if err := checkCLLConsistency(previous.commitment, cp.commitment, octets); err != nil {
				return nil, fmt.Errorf("anchor_inclusion: entry %d: %w", i, err)
			}
		}
		previous = cp
	}
	// consistency_to_anchor binds the answer to the coverage anchor: null
	// when the last entry is the anchor, else a proof to the anchor's
	// checkpoint, which the envelope carries as material.
	proof := card["consistency_to_anchor"]
	if proof == nil {
		if digestBytes(raw) != e.Anchor.Digest {
			return nil, bad("ends before its anchor and carries no consistency_to_anchor")
		}
		return raw, nil
	}
	text, isText := proof.(string)
	if !isText || !lowerHex(text) {
		return nil, bad("has a consistency_to_anchor that is not hex")
	}
	var anchorStatement []byte
	for i, entry := range e.Verification {
		if entry.Kind == KindCLLCheckpoint {
			anchorStatement = e.VerificationContent[i]
		}
	}
	if anchorStatement == nil || digestBytes(anchorStatement) != e.Anchor.Digest {
		return nil, bad("carries no anchor checkpoint to check consistency_to_anchor against")
	}
	anchorCp, err := readSignedCheckpoint(anchorStatement, checkpointKey)
	if err != nil {
		return nil, fmt.Errorf("anchor_inclusion: the anchor: %w", err)
	}
	if anchorCp.LogID != previous.LogID || anchorCp.TreeSize <= previous.TreeSize {
		return nil, bad("does not reach an anchor that extends it")
	}
	octets, _ := hex.DecodeString(text)
	if err := checkCLLConsistency(previous.commitment, anchorCp.commitment, octets); err != nil {
		return nil, fmt.Errorf("anchor_inclusion: consistency_to_anchor: %w", err)
	}
	return anchorStatement, nil
}

// CheckHistoryCard2 checks a history_card/2 artifact by the seven ordered
// checks of ER -03 §8.1.2 (evidencerequest.CheckHistoryCard2), after the
// members this profile's artifact has and nothing else. anchor is the digest
// the response resolved.
func CheckHistoryCard2(artifact []byte, anchor string) error {
	_, err := checkHistoryCard2(artifact, anchor, "")
	return err
}

// checkHistoryCard2 is CheckHistoryCard2 under checkpointKey, the key every
// checkpoint must be signed by; empty names the first checkpoint's, which the
// reader judges. It returns the last checkpoint's statement.
func checkHistoryCard2(artifact []byte, anchor, checkpointKey string) ([]byte, error) {
	bad := func(why string) error { return fmt.Errorf("%w: the history_card/2 %s", ErrInvalid, why) }
	decoded, err := evidencerequest.DecodeJSON(artifact)
	card, ok := decoded.(map[string]any)
	if err != nil || !ok {
		return nil, bad("is not a JSON object")
	}
	if !keysExactly(card, "anchor", "derivation", "evidence_stream", "history", "subject", "window") {
		return nil, bad("has members other than the card's")
	}
	stream, _ := card["evidence_stream"].(string)
	subject, _ := card["subject"].(map[string]any)
	if card["derivation"] != DerivationHistoryCard2 || card["anchor"] != anchor || stream == "" ||
		len(subject) != 1 || !(keysExactly(subject, "checkpoints") && subject["checkpoints"] == nil || keysExactly(subject, "full_history") && subject["full_history"] == nil) {
		return nil, bad("does not name its derivation, anchor, stream and subject")
	}
	// The profile's artifact carries these members of an entry and no other,
	// the disclosure scan's declared field set.
	entries, _ := card["history"].([]any)
	for i, item := range entries {
		entry, _ := item.(map[string]any)
		if !keysExactly(entry, "position", "checkpoint", "receipts", "consistency_from_previous") {
			return nil, bad(fmt.Sprintf("entry %d has members other than a position, checkpoint, receipts and consistency proof", i))
		}
	}
	key := checkpointKey
	if key == "" {
		if history, _ := card["history"].([]any); len(history) > 0 {
			entry, _ := history[0].(map[string]any)
			text, _ := entry["checkpoint"].(string)
			if raw, err := hex.DecodeString(text); err == nil {
				if cp, err := checkpointFromStatement(raw); err == nil {
					key = cp.KeyID
				}
			}
		}
	}
	if finding := evidencerequest.CheckHistoryCard2(card, anchor, []string{key}); finding != "" {
		return nil, bad("fails " + finding)
	}
	history := card["history"].([]any)
	last, _ := hex.DecodeString(history[len(history)-1].(map[string]any)["checkpoint"].(string))
	return last, nil
}
