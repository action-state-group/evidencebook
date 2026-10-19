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
	"fmt"
	"os"
	"reflect"
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
	if _, err := c.Chain(ctx, tick.Checkpoint); !errors.Is(err, ErrNotFound) {
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

// Witnessed takes the receipt as a verifier will read it: an entry hash is
// required, the witness's URL loses a trailing slash, and the first receipt
// reported for a tick is the one kept.
func TestWitnessedKeepsTheReceiptAVerifierReads(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	tick := mustTick(t, c, tickTime)
	for name, receipt := range map[string]WitnessReceipt{
		"no entry hash": {TSURL: "https://w.example", Receipt: []byte("r")},
		"no receipt":    {TSURL: "https://w.example", EntryHash: "e"},
		"no URL":        {EntryHash: "e", Receipt: []byte("r")},
	} {
		if err := c.Witnessed(ctx, tick.N, receipt); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := c.Witnessed(ctx, tick.N, WitnessReceipt{TSURL: "https://w.example//", EntryHash: "e1", Receipt: []byte("first")}); err != nil {
		t.Fatal(err)
	}
	if err := c.Witnessed(ctx, tick.N, WitnessReceipt{TSURL: "https://other.example", EntryHash: "e2", Receipt: []byte("second")}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, tick.N)
	if err != nil || got.Receipt == nil || got.Receipt.TSURL != "https://w.example" || string(got.Receipt.Receipt) != "first" {
		t.Fatalf("receipt %+v %v", got.Receipt, err)
	}
}

// A legacy log's later checkpoint gets a part chain from its own log.
func TestALegacyLogsChainIsPartFromItsOwnLog(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	old, err := OpenCLLSQLite(f.path, "deal/legacy", f.cfg.CheckpointKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Append(ctx, hex.EncodeToString(bytes.Repeat([]byte{5}, 32)), tickTime); err != nil {
		t.Fatal(err)
	}
	_ = old.Release()
	shelf := f.open(t)
	c, _ := newCadence(t, shelf)
	legacy, err := shelf.Legacy(ctx, "deal/legacy")
	if err != nil {
		t.Fatal(err)
	}
	tick := mustTick(t, c, tickTime)
	if err := c.Witnessed(ctx, tick.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.log.Append(ctx, hex.EncodeToString(bytes.Repeat([]byte{6}, 32)), tickTime); err != nil {
		t.Fatal(err)
	}
	later, err := legacy.log.Checkpoint(ctx, tickTime)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := c.Chain(ctx, later.Statement)
	if err != nil || chain["extent"] != "part" || chain["earlier"] == nil {
		t.Fatalf("legacy part chain: %v %v", chain["extent"], err)
	}
}

// A tick whose stored statement is not its leaf's checkpoint is corrupt,
// never a chain.
func TestAChainFromACorruptStatementIsRefused(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("s", "1"))
	tick := mustTick(t, c, tickTime)
	if err := c.Witnessed(ctx, tick.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	leaves := tick.Leaves
	leaf := leaves.Deals["deal/a"]
	leaf.Statement = base64.RawURLEncoding.EncodeToString([]byte("not the checkpoint"))
	leaves.Deals["deal/a"] = leaf
	encoded, _ := json.Marshal(leaves)
	if _, err := shelf.db.Exec(`UPDATE cadence_ticks SET leaves = ? WHERE tick = ?`, string(encoded), tick.N); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, a, observation("s", "2"))
	later, err := a.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Chain(ctx, later.Statement); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("a corrupt earlier statement: %v", err)
	}
}

// shape is a JSON value with its values erased: the member names, nesting,
// array lengths and JSON types a verifier reads, without the salts, hashes
// and proofs that change from run to run.
func shape(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			out[k] = shape(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = shape(child)
		}
		// Proof and path lengths depend on sizes, not on the format; one
		// element's shape stands for all.
		if len(out) > 1 {
			out = out[:1]
		}
		return out
	case string:
		return "string"
	case float64, json.Number:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

// The chains Chain builds today keep the exact shape of the vector that
// capsule-cli 7a6b101's own verifier passed (testdata), so a change to the
// chain's members, nesting or types fails here rather than leaving that
// cross-check stale.
func TestTheChainKeepsTheShapeCapsuleCLIVerified(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("testdata/cadence/chain-verified-by-capsule-cli-7a6b101.json")
	if err != nil {
		t.Fatal(err)
	}
	var verified map[string]struct {
		Bundle struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"bundle"`
	}
	if err := json.Unmarshal(raw, &verified); err != nil {
		t.Fatal(err)
	}
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("s", "1"))
	mustAppend(t, mustOpenBook(t, shelf, "deal/b"), observation("s", "1"))
	tick := mustTick(t, c, tickTime)
	if err := c.Witnessed(ctx, tick.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	statement, _ := base64.RawURLEncoding.DecodeString(tick.Leaves.Deals["deal/a"].Statement)
	all, err := c.Chain(ctx, statement)
	if err != nil {
		t.Fatal(err)
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
	for name, chain := range map[string]map[string]any{"all": all, "part": part} {
		encoded, err := json.Marshal(chain)
		if err != nil {
			t.Fatal(err)
		}
		var now any
		if err := json.Unmarshal(encoded, &now); err != nil {
			t.Fatal(err)
		}
		want := shape(verified[name].Bundle.Extensions[CadenceExtension])
		if got := shape(now); !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			t.Fatalf("%s chain shape drifted from the capsule-cli-verified vector:\n got %s\nwant %s", name, gotJSON, wantJSON)
		}
	}
}
