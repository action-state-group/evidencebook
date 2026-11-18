// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// DerivationHistoryCard2 is history_card/2 (ER -03 §8.1.2): a sample of the
// stream's checkpoints whose size grows with the logarithm of their number,
// where history_card/1 lists every one.
const DerivationHistoryCard2 = "history_card/2"

// History card windows: the number of newest checkpoints a /2 card lists in
// full, chosen by the responder from 1 to 64 (ER -03 §8.1.2).
const (
	MinHistoryWindow     = 1
	MaxHistoryWindow     = 64
	DefaultHistoryWindow = 16
)

// HistoryCardPositions is P(n, w) of ER -03 §8.1.2: the checkpoint position
// 0, the positions n-2^k for every 2^k <= n, and the latest window, positions
// max(0, n-w+1) through n, ascending and each once. It depends only on n and
// w. w outside 1 to 64 gives nil.
func HistoryCardPositions(n uint64, w int) []uint64 {
	if w < MinHistoryWindow || w > MaxHistoryWindow {
		return nil
	}
	set := map[uint64]bool{0: true}
	for k := 0; k < 64 && uint64(1)<<k <= n && n > 0; k++ {
		set[n-uint64(1)<<k] = true
	}
	low := uint64(0)
	if n >= uint64(w) {
		low = n - uint64(w) + 1
	}
	for p := low; p <= n; p++ {
		set[p] = true
	}
	out := make([]uint64, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

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
	positions := HistoryCardPositions(uint64(len(chain)-1), window)
	return historyEntries2(ctx, b.commitments.(cllMaterialSource), chain, anchor, positions)
}

// checkpointsRange is the answer to a checkpoints subject with content
// (first, last) (ER -03 §8.1.3): one entry per position from first through
// last, each proof from the position before it, none in the first.
func (b *Book) checkpointsRange(ctx context.Context, chain []Checkpoint, anchor Checkpoint, first, last uint64) ([]any, error) {
	positions := make([]uint64, 0, last-first+1)
	for p := first; p <= last; p++ {
		positions = append(positions, p)
	}
	return historyEntries2(ctx, b.commitments.(cllMaterialSource), chain, anchor, positions)
}

// CheckHistoryCard2 checks a history_card/2 artifact by the seven ordered
// checks of ER -03 §8.1.2. anchor is the digest the response resolved. A
// card whose checkpoints are not strictly growing in size is refused (a
// book never issues two checkpoints over the same body). Which receipts
// count is the requester's policy, so only their form (a list) is checked. The checkpoint key is the first checkpoint's, as for /1:
// judging it is the reader's.
func CheckHistoryCard2(artifact []byte, anchor string) error {
	_, err := checkHistoryCard2(artifact, anchor, "")
	return err
}

// checkHistoryCard2 is CheckHistoryCard2 under checkpointKey, the key every
// checkpoint must be signed by; empty names the first checkpoint's. It
// returns the last checkpoint's statement.
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
	// 1. window.
	w, isNumber := evidencerequest.UintValue(card["window"])
	if !isNumber || w < MinHistoryWindow || w > MaxHistoryWindow {
		return nil, bad("window is not an integer from 1 to 64")
	}
	history, _ := card["history"].([]any)
	// 2. positions start at 0 and strictly increase.
	if len(history) == 0 {
		return nil, bad("has no history")
	}
	positions := make([]uint64, len(history))
	statements := make([][]byte, len(history))
	proofs := make([][]byte, len(history))
	for i, item := range history {
		entry, _ := item.(map[string]any)
		if !keysExactly(entry, "position", "checkpoint", "receipts", "consistency_from_previous") {
			return nil, bad(fmt.Sprintf("entry %d has members other than a position, checkpoint, receipts and consistency proof", i))
		}
		pos, isNumber := evidencerequest.UintValue(entry["position"])
		if !isNumber {
			return nil, bad(fmt.Sprintf("entry %d's position is not an unsigned integer", i))
		}
		positions[i] = pos
		if i == 0 && positions[i] != 0 || i > 0 && positions[i] <= positions[i-1] {
			return nil, bad(fmt.Sprintf("entry %d's position does not strictly increase from 0", i))
		}
		if _, isList := entry["receipts"].([]any); !isList {
			return nil, bad(fmt.Sprintf("entry %d's receipts are not a list", i))
		}
		text, _ := entry["checkpoint"].(string)
		if !lowerHex(text) {
			return nil, bad(fmt.Sprintf("entry %d's checkpoint is not hex", i))
		}
		statements[i], _ = hex.DecodeString(text)
		proof, isText := entry["consistency_from_previous"].(string)
		if (i == 0) != (entry["consistency_from_previous"] == nil) || (i > 0 && (!isText || !lowerHex(proof))) {
			return nil, bad(fmt.Sprintf("entry %d is not a checkpoint with its consistency proof", i))
		}
		if i > 0 {
			proofs[i], _ = hex.DecodeString(proof)
		}
	}
	// 3. the positions are exactly P(n, window).
	n := positions[len(positions)-1]
	if !slices.Equal(positions, HistoryCardPositions(n, int(w))) {
		return nil, bad("positions are not those of P(n, window)")
	}
	// 4 and 5. the checkpoints verify under one key and one log, do not
	// shrink, and each proof extends the entry before it.
	key := checkpointKey
	first, err := checkpointFromStatement(statements[0])
	if err != nil {
		return nil, fmt.Errorf("%w: the history_card/2's first checkpoint: %v", ErrInvalid, err)
	}
	if key == "" {
		key = first.KeyID
	}
	var previous signedCheckpoint
	for i, raw := range statements {
		cp, err := readSignedCheckpoint(raw, key)
		if err != nil {
			return nil, fmt.Errorf("the history_card/2's entry %d: %w", i, err)
		}
		if i > 0 {
			if cp.LogID != previous.LogID || cp.TreeSize <= previous.TreeSize {
				return nil, bad(fmt.Sprintf("entry %d does not extend the log past entry %d", i, i-1))
			}
			if err := checkCLLConsistency(previous.commitment, cp.commitment, proofs[i]); err != nil {
				return nil, fmt.Errorf("the history_card/2's entry %d: %w", i, err)
			}
		}
		previous = cp
	}
	// 6. receipts cover their checkpoints: a receipt's form is the
	// deployment's and which count is the requester's policy, as the package
	// leaves a stapled card's witness receipts to the carrier (Staple), so
	// only that each entry's receipts are a list is checked above.
	// 7. the last checkpoint is the anchor.
	if digestBytes(statements[len(statements)-1]) != anchor {
		return nil, bad("does not end at its anchor")
	}
	return statements[len(statements)-1], nil
}
