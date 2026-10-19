// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// CadenceExtension is the bundle extension a deal's cadence chain travels
// in, capsule-cli's name for it. Bundles from before carried it as
// "cadence-witness/v0" or "x-deal-cadence-v0"; verifiers still read those.
const CadenceExtension = "x-cadence-witness/v0"

// Chain is the witnessed cadence chain for a log's checkpoint (its COSE
// statement): capsule-cli's cadenceChain, member for member, so its released
// verifier (deal_cadence_verify.go) checks it unchanged.
//
//   - "all": a witnessed tick carries this very checkpoint;
//   - "part": the newest witnessed tick carrying an earlier checkpoint of
//     the log, plus that checkpoint and a consistency proof from it to this
//     one ("earlier").
//
// The leaf's salt, position and the tick's path; the cadence log's
// checkpoint, the tick's entry index and its inclusion proof; and the
// witness receipt the host reported. A profile adds its own members (a
// deal's "steps_witnessed" and "steps"). ErrNotFound when no witnessed tick
// carries the log up to this checkpoint: the host then says "pending" or
// "scheduled" from its own witness state.
func (c *Cadence) Chain(ctx context.Context, statement []byte) (map[string]any, error) {
	current, err := checkpointFromStatement(statement)
	if err != nil {
		return nil, err
	}
	// The whole chain is built under the shelf's lock, as every other
	// cadence call is, so a concurrent Release is ErrReleased.
	c.shelf.mu.Lock()
	defer c.shelf.mu.Unlock()
	if c.shelf.released {
		return nil, ErrReleased
	}
	sum := sha256.Sum256(statement)
	extent, tick, leaf, err := c.witnessedTickLocked(ctx, current.LogID, hex.EncodeToString(sum[:]), current.TreeSize)
	if err != nil {
		return nil, err
	}
	if leaf.LogID != current.LogID {
		return nil, fmt.Errorf("%w: witnessed tick %d holds no leaf of %s", ErrCorrupt, tick.N, current.LogID)
	}
	if tick.Receipt == nil {
		return nil, fmt.Errorf("%w: witnessed tick %d holds no receipt", ErrCorrupt, tick.N)
	}
	_, path, err := tick.Leaves.Tree(leaf.Index)
	if err != nil {
		return nil, err
	}
	cadenceCheckpoint, err := checkpointFromStatement(tick.Checkpoint)
	if err != nil {
		return nil, err
	}
	inclusion, err := c.log.ProveInclusion(ctx, tick.EntrySeq, cadenceCheckpoint)
	if err != nil {
		return nil, err
	}
	hexPath := make([]any, len(path))
	for i := range path {
		hexPath[i] = hex.EncodeToString(path[i])
	}
	out := map[string]any{
		"state": "witnessed", "extent": extent, "checkpoint_at": tick.At.UTC().Format(time.RFC3339),
		"log_id": leaf.LogID, "size": leaf.Size, "salt": leaf.Salt, "index": leaf.Index, "path": hexPath,
		"cadence": map[string]any{
			"log_id": c.cfg.LogID, "entry_index": tick.EntrySeq - 1,
			"checkpoint":      map[string]any{"cose": base64.RawURLEncoding.EncodeToString(tick.Checkpoint)},
			"inclusion_proof": json.RawMessage(inclusion.Proof),
			"witnesses": []any{map[string]any{
				"ts_url": tick.Receipt.TSURL, "entry_hash": tick.Receipt.EntryHash,
				"receipt_b64": base64.StdEncoding.EncodeToString(tick.Receipt.Receipt),
				"leaf_index":  tick.Receipt.LeafIndex, "tree_size": tick.Receipt.TreeSize,
			}},
		},
	}
	if extent == "all" {
		return out, nil
	}
	earlierStatement, err := base64.RawURLEncoding.DecodeString(leaf.Statement)
	if err != nil {
		return nil, fmt.Errorf("%w: tick %d's statement of %s: %v", ErrCorrupt, tick.N, leaf.LogID, err)
	}
	earlierSum := sha256.Sum256(earlierStatement)
	earlier, err := checkpointFromStatement(earlierStatement)
	if err != nil || hex.EncodeToString(earlierSum[:]) != leaf.CheckpointSHA256 || earlier.LogID != current.LogID || earlier.TreeSize != leaf.Size {
		return nil, fmt.Errorf("%w: tick %d's statement of %s is not its leaf's checkpoint", ErrCorrupt, tick.N, leaf.LogID)
	}
	log, err := c.shelf.cadenceLogLocked(ctx, current.LogID)
	if err != nil {
		return nil, err
	}
	consistency, err := log.substrate().ProveConsistency(ctx, earlier, current)
	if err != nil {
		return nil, err
	}
	out["earlier"] = map[string]any{
		"checkpoint":        map[string]any{"cose": leaf.Statement},
		"consistency_proof": json.RawMessage(consistency.Proof),
	}
	return out, nil
}
