// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// Chain gives capsule-cli's cadenceChain members: the leaf folds to the
// tick's root along its path, and that root is the cadence log entry the
// inclusion proof names. A later checkpoint of the log gets "part", with
// its earlier witnessed checkpoint and a consistency proof.
func TestAChainIsCapsuleCLIsCadenceChain(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("s", "1"))
	mustAppend(t, mustOpenBook(t, shelf, "deal/b"), observation("s", "1"))
	tick := mustTick(t, c, tickTime)
	if _, err := c.Chain(ctx, tick.Checkpoint); !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unwitnessed tick: %v", err)
	}
	statement, _ := base64.RawURLEncoding.DecodeString(tick.Leaves.Deals["deal/a"].Statement)
	if _, err := c.Chain(ctx, statement); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before the receipt: %v", err)
	}
	if err := c.Witnessed(ctx, tick.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	all, err := c.Chain(ctx, statement)
	if err != nil {
		t.Fatal(err)
	}
	checkChain(t, all, statement, tick)
	if all["extent"] != "all" {
		t.Fatalf("extent %v", all["extent"])
	}

	mustAppend(t, a, observation("s", "2"))
	later, err := a.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	part, err := c.Chain(ctx, later.Statement)
	if err != nil {
		t.Fatal(err)
	}
	checkChain(t, part, statement, tick)
	if part["extent"] != "part" || part["earlier"] == nil {
		t.Fatalf("part: %v", part["extent"])
	}
	if out := os.Getenv("EVIDENCEBOOK_CADENCE_CHAIN_OUT"); out != "" {
		vector := map[string]any{
			"all":  map[string]any{"statement": base64.RawURLEncoding.EncodeToString(statement), "bundle": map[string]any{"extensions": map[string]any{CadenceExtension: all}}},
			"part": map[string]any{"statement": base64.RawURLEncoding.EncodeToString(later.Statement), "bundle": map[string]any{"extensions": map[string]any{CadenceExtension: part}}},
		}
		encoded, _ := json.MarshalIndent(vector, "", "  ")
		if err := os.WriteFile(out, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// checkChain re-derives what a verifier checks before the receipt: the
// leaf from the (earlier) statement, its fold to the tick's root, and that
// root as the cadence entry.
func checkChain(t *testing.T, chain map[string]any, statement []byte, tick CadenceTick) {
	t.Helper()
	sum := sha256.Sum256(statement)
	leaf, err := cadenceLeaf(chain["log_id"].(string), chain["size"].(uint64), hex.EncodeToString(sum[:]), chain["salt"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var path [][]byte
	for _, p := range chain["path"].([]any) {
		h, _ := hex.DecodeString(p.(string))
		path = append(path, h)
	}
	root := cadenceFold(leaf, chain["index"].(uint64), path)
	want, _, _ := tick.Leaves.Tree(tick.Leaves.FillerIndex)
	if !bytes.Equal(root, want) {
		t.Fatal("the chain's leaf does not fold to the tick's root")
	}
	inner := chain["cadence"].(map[string]any)
	if inner["entry_index"].(uint64) != tick.EntrySeq-1 || len(chain["path"].([]any)) != CadenceDepth {
		t.Fatalf("cadence %+v", inner)
	}
	var proof MMRInclusion
	if err := json.Unmarshal(inner["inclusion_proof"].(json.RawMessage), &proof); err != nil || proof.LeafIndex != tick.EntrySeq-1 || proof.Size != tick.CheckpointSize {
		t.Fatalf("inclusion proof %+v %v", proof, err)
	}
}
