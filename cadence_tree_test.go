// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type cadenceVectors struct {
	Depth       int    `json:"depth"`
	FillerNonce string `json:"filler_nonce"`
	FillerIndex uint64 `json:"filler_index"`
	FillerLeaf  string `json:"filler_leaf"`
	Leaves      []struct {
		LogID            string   `json:"log_id"`
		Size             uint64   `json:"size"`
		CheckpointSHA256 string   `json:"checkpoint_sha256"`
		Salt             string   `json:"salt"`
		Index            uint64   `json:"index"`
		Leaf             string   `json:"leaf"`
		Path             []string `json:"path"`
	} `json:"leaves"`
	Root      string `json:"root"`
	EmptyRoot string `json:"empty_root"`
}

// The construction is capsule-cli's, byte for byte: the vectors were written
// by its own cadenceLeaf, cadenceFillerLeaf and cadenceTree (testdata).
func TestCadenceTreeMatchesCapsuleCLI(t *testing.T) {
	raw, err := os.ReadFile("testdata/cadence/capsule-cli-7a6b101.json")
	if err != nil {
		t.Fatal(err)
	}
	var v cadenceVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Depth != CadenceDepth {
		t.Fatalf("depth %d", v.Depth)
	}
	if hex.EncodeToString(cadenceEmpty[CadenceDepth]) != v.EmptyRoot {
		t.Fatal("empty root")
	}
	stored := TickLeaves{Deals: map[string]TickLeaf{}, FillerIndex: v.FillerIndex, FillerNonce: v.FillerNonce}
	nonce, _ := hex.DecodeString(v.FillerNonce)
	if hex.EncodeToString(cadenceFillerLeaf(nonce)) != v.FillerLeaf {
		t.Fatal("filler leaf")
	}
	for _, l := range v.Leaves {
		leaf, err := cadenceLeaf(l.LogID, l.Size, l.CheckpointSHA256, l.Salt)
		if err != nil || hex.EncodeToString(leaf) != l.Leaf {
			t.Fatalf("leaf of %s: %x, want %s (%v)", l.LogID, leaf, l.Leaf, err)
		}
		stored.Deals[l.LogID] = TickLeaf{LogID: l.LogID, Size: l.Size, CheckpointSHA256: l.CheckpointSHA256, Salt: l.Salt, Index: l.Index}
	}
	for _, l := range v.Leaves {
		root, path, err := stored.Tree(l.Index)
		if err != nil || hex.EncodeToString(root) != v.Root {
			t.Fatalf("root %x, want %s (%v)", root, v.Root, err)
		}
		for i, p := range path {
			if hex.EncodeToString(p) != l.Path[i] {
				t.Fatalf("path of %s at height %d", l.LogID, i)
			}
		}
		leaf, _ := hex.DecodeString(l.Leaf)
		if !bytes.Equal(cadenceFold(leaf, l.Index, path), root) {
			t.Fatal("fold")
		}
	}
	// A tick a released build stored (no filler_seed) reads unchanged.
	var legacy TickLeaves
	if err := json.Unmarshal([]byte(`{"deals":{},"filler_index":3,"filler_nonce":"`+v.FillerNonce+`"}`), &legacy); err != nil || legacy.FillerCount != 0 {
		t.Fatalf("legacy leaves: %v %+v", err, legacy)
	}
}

func TestDerivedFillersAreDeterministicAndFillTheGap(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, 32)
	used := map[uint64]bool{0: true, 5: true}
	a, err := DerivedFillers(seed, 50, map[uint64]bool{0: true, 5: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := DerivedFillers(seed, 50, used)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 50 || len(used) != 52 {
		t.Fatalf("%d fillers, %d used", len(a), len(used))
	}
	for position, value := range a {
		if position == 0 || position == 5 || !bytes.Equal(b[position], value) {
			t.Fatalf("filler at %d differs or collides", position)
		}
	}
	other, _ := DerivedFillers(bytes.Repeat([]byte{8}, 32), 50, map[uint64]bool{0: true, 5: true})
	same := 0
	for position := range other {
		if _, ok := a[position]; ok {
			same++
		}
	}
	if same == 50 {
		t.Fatal("another seed gives other positions")
	}
	if _, err := DerivedFillers(seed, 1<<CadenceDepth, map[uint64]bool{0: true}); err == nil {
		t.Fatal("more fillers than the tree holds are refused")
	}
}

// The occupancy floor: a tick with H logs ever and fewer live ones has H+1
// occupied positions, exactly as a tick that held every log, and its tree is
// rebuilt from the stored seed and count alone.
func TestATicksOccupancyIsTheLifetimeCountPlusOne(t *testing.T) {
	seedHex, _ := newRandomSource(nil).hex(32)
	nonceHex, _ := newRandomSource(nil).hex(32)
	l := TickLeaves{Deals: map[string]TickLeaf{
		"deal/a": {LogID: "deal/a", Size: 3, CheckpointSHA256: hex.EncodeToString(bytes.Repeat([]byte{1}, 32)), Salt: hex.EncodeToString(bytes.Repeat([]byte{2}, 32)), Index: 9},
	}, FillerIndex: 1, FillerNonce: nonceHex, FillerSeed: seedHex, FillerCount: 4}
	root1, path, err := l.Tree(9)
	if err != nil {
		t.Fatal(err)
	}
	root2, _, _ := l.Tree(9)
	if !bytes.Equal(root1, root2) {
		t.Fatal("a tick's tree is rebuilt from what it stored")
	}
	// Count the occupied positions: 1 live + filler 0 + 4 derived = H(5)+1.
	seed, _ := hex.DecodeString(seedHex)
	used := map[uint64]bool{1: true, 9: true}
	fillers, _ := DerivedFillers(seed, 4, used)
	if len(used) != 6 || len(fillers) != 4 {
		t.Fatalf("occupancy %d", len(used))
	}
	if len(path) != CadenceDepth {
		t.Fatalf("path length %d", len(path))
	}
	l.FillerCount = 3
	if root3, _, _ := l.Tree(9); bytes.Equal(root3, root1) {
		t.Fatal("the filler count is part of the tree")
	}
}

// Stored ticks are data: a filler count that would overflow, a nil used set
// and a position past the tree are refused, never a panic or a silently
// smaller tree.
func TestTickLeavesRefuseOutOfRangeInput(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, 32)
	if _, err := DerivedFillers(seed, ^uint64(0), map[uint64]bool{0: true}); err == nil {
		t.Fatal("a filler count that wraps must be refused")
	}
	if _, err := DerivedFillers(seed, 1, nil); err == nil {
		t.Fatal("a nil used set must be refused")
	}
	nonce := hex.EncodeToString(bytes.Repeat([]byte{1}, 32))
	leaf := TickLeaf{LogID: "deal/a", Size: 1, CheckpointSHA256: nonce, Salt: nonce, Index: 1 << CadenceDepth}
	for name, l := range map[string]TickLeaves{
		"a leaf past the tree":   {Deals: map[string]TickLeaf{"deal/a": leaf}, FillerNonce: nonce},
		"the filler past it":     {Deals: map[string]TickLeaf{}, FillerIndex: 1 << CadenceDepth, FillerNonce: nonce},
		"a filler count wrapped": {Deals: map[string]TickLeaf{}, FillerNonce: nonce, FillerSeed: nonce, FillerCount: ^uint64(0)},
	} {
		if _, _, err := l.Tree(0); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
	if _, _, err := (TickLeaves{Deals: map[string]TickLeaf{}, FillerNonce: nonce}).Tree(1 << CadenceDepth); err == nil {
		t.Fatal("a path for a position past the tree must be refused")
	}
}
