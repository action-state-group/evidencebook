// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/internal/detrand"
)

// testdata/shelf/tree.json: the cadence's constructions and the shelf's
// random draws, written by this build (evidencebook#254 §5), for Rust and
// Python to read. The scripted shelf's vectors beside it are
// internal/shelfscript's.

type treeVectorLeaf struct {
	LogID            string `json:"log_id"`
	Size             uint64 `json:"size"`
	CheckpointSHA256 string `json:"checkpoint_sha256"`
	Salt             string `json:"salt"`
	Leaf             string `json:"leaf"`
}

type treeVectorFiller struct {
	Nonce string `json:"nonce"`
	Leaf  string `json:"leaf"`
}

type treeVectorPosition struct {
	Position uint64 `json:"position"`
	Leaf     string `json:"leaf"`
}

type treeVectorDerived struct {
	Seed    string               `json:"seed"`
	Count   uint64               `json:"count"`
	Used    []uint64             `json:"used"`
	Fillers []treeVectorPosition `json:"fillers"`
}

type treeVectorPath struct {
	Index uint64   `json:"index"`
	Path  []string `json:"path"`
}

type treeVectorTree struct {
	// Leaves is a tick's stored leaves JSON, as cadence_ticks.leaves holds it.
	Leaves string           `json:"leaves"`
	Root   string           `json:"root"`
	Paths  []treeVectorPath `json:"paths"`
}

type treeVectorPadding struct {
	Nonce    string `json:"nonce"`
	RecordID string `json:"record_id"`
}

type treeVectorDraw struct {
	// Draw is "bytes" (N bytes, hex), "uniform" (uniform(N)) or "position"
	// (a tree position, drawn again while one of Used is taken).
	Draw  string   `json:"draw"`
	N     uint64   `json:"n,omitempty"`
	Used  []uint64 `json:"used,omitempty"`
	Value any      `json:"value"`
}

type treeVectors struct {
	Description string              `json:"description"`
	Depth       int                 `json:"depth"`
	EmptyRoots  []string            `json:"empty_roots"`
	Leaves      []treeVectorLeaf    `json:"leaves"`
	Fillers     []treeVectorFiller  `json:"fillers"`
	Derived     []treeVectorDerived `json:"derived_fillers"`
	Trees       []treeVectorTree    `json:"trees"`
	Padding     []treeVectorPadding `json:"padding"`
	DrawStream  string              `json:"draw_stream"`
	Draws       []treeVectorDraw    `json:"draws"`
}

const drawStream = "evidencebook shelf vectors/draws"

func buildTreeVectors(t *testing.T) treeVectors {
	t.Helper()
	inputs := newRandomSource(detrand.New("evidencebook shelf vectors/tree inputs"))
	hex32 := func() string {
		s, err := inputs.hex(32)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	v := treeVectors{
		Description: "The cadence tree's constructions and the shelf's random draws (evidencebook#254 §5), written by shelf_vectors_test.go (EB_WRITE_SHELF_VECTORS=1). Inputs are from detrand stream \"evidencebook shelf vectors/tree inputs\"; draws read draw_stream in order. testdata/shelf/cadence holds capsule-cli's own vectors of the same tree.",
		Depth:       CadenceDepth,
		DrawStream:  drawStream,
	}
	for _, h := range cadenceEmpty {
		v.EmptyRoots = append(v.EmptyRoots, hex.EncodeToString(h))
	}
	for i, id := range []string{"deal/a", "deal/b", "legacy/old", "x"} {
		l := treeVectorLeaf{LogID: id, Size: uint64(i*i*7 + 1), CheckpointSHA256: hex32(), Salt: hex32()}
		leaf, err := cadenceLeaf(l.LogID, l.Size, l.CheckpointSHA256, l.Salt)
		if err != nil {
			t.Fatal(err)
		}
		l.Leaf = hex.EncodeToString(leaf)
		v.Leaves = append(v.Leaves, l)
	}
	for range 3 {
		nonce := hex32()
		raw, _ := hex.DecodeString(nonce)
		v.Fillers = append(v.Fillers, treeVectorFiller{Nonce: nonce, Leaf: hex.EncodeToString(cadenceFillerLeaf(raw))})
	}
	for _, c := range []struct {
		count uint64
		used  []uint64
	}{{1, []uint64{0}}, {5, []uint64{7, 9, 1000}}, {40, []uint64{}}} {
		seed := hex32()
		raw, _ := hex.DecodeString(seed)
		used := map[uint64]bool{}
		for _, p := range c.used {
			used[p] = true
		}
		fillers, err := DerivedFillers(raw, c.count, used)
		if err != nil {
			t.Fatal(err)
		}
		d := treeVectorDerived{Seed: seed, Count: c.count, Used: c.used}
		for p, leaf := range fillers {
			d.Fillers = append(d.Fillers, treeVectorPosition{Position: p, Leaf: hex.EncodeToString(leaf)})
		}
		sort.Slice(d.Fillers, func(i, j int) bool { return d.Fillers[i].Position < d.Fillers[j].Position })
		v.Derived = append(v.Derived, d)
	}
	trees := []TickLeaves{
		{Deals: map[string]TickLeaf{}, FillerIndex: 0, FillerNonce: hex32()},
		{Deals: map[string]TickLeaf{}, FillerIndex: 65535, FillerNonce: hex32()},
		{Deals: map[string]TickLeaf{}, FillerIndex: 12345, FillerNonce: hex32(), FillerSeed: hex32(), FillerCount: 3},
	}
	for i, l := range v.Leaves[:3] {
		trees[1].Deals[l.LogID] = TickLeaf{LogID: l.LogID, Size: l.Size, CheckpointSHA256: l.CheckpointSHA256, Salt: l.Salt, Index: uint64(2*i + 1)}
		trees[2].Deals[l.LogID] = TickLeaf{LogID: l.LogID, Size: l.Size, CheckpointSHA256: l.CheckpointSHA256, Salt: l.Salt, Index: uint64(40000 + i*511)}
	}
	for _, leaves := range trees {
		encoded, err := json.Marshal(leaves)
		if err != nil {
			t.Fatal(err)
		}
		root, _, err := leaves.Tree(leaves.FillerIndex)
		if err != nil {
			t.Fatal(err)
		}
		tree := treeVectorTree{Leaves: string(encoded), Root: hex.EncodeToString(root)}
		indexes := []uint64{leaves.FillerIndex}
		for _, l := range leaves.Deals {
			indexes = append(indexes, l.Index)
		}
		sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
		for _, index := range indexes {
			_, path, err := leaves.Tree(index)
			if err != nil {
				t.Fatal(err)
			}
			p := treeVectorPath{Index: index}
			for _, node := range path {
				p.Path = append(p.Path, hex.EncodeToString(node))
			}
			tree.Paths = append(tree.Paths, p)
		}
		v.Trees = append(v.Trees, tree)
	}
	for range 3 {
		nonce := hex32()
		id, err := paddingRecordIDFor(nonce)
		if err != nil {
			t.Fatal(err)
		}
		v.Padding = append(v.Padding, treeVectorPadding{Nonce: nonce, RecordID: id})
	}

	draws := newRandomSource(detrand.New(drawStream))
	jitter := uint64(2*time.Minute) + 1
	for _, d := range []treeVectorDraw{
		{Draw: "position"}, {Draw: "bytes", N: 32}, {Draw: "bytes", N: 32}, {Draw: "position"},
		{Draw: "uniform", N: jitter}, {Draw: "uniform", N: 3}, {Draw: "uniform", N: 1<<63 + 1},
		{Draw: "bytes", N: 32}, {Draw: "position"},
	} {
		switch d.Draw {
		case "bytes":
			value, err := draws.hex(int(d.N))
			if err != nil {
				t.Fatal(err)
			}
			d.Value = value
		case "uniform":
			value, err := draws.uniform(d.N)
			if err != nil {
				t.Fatal(err)
			}
			d.Value = value
		case "position":
			used := positions{}
			for _, earlier := range v.Draws {
				if earlier.Draw == "position" {
					used[earlier.Value.(uint64)] = true
					d.Used = append(d.Used, earlier.Value.(uint64))
				}
			}
			value, err := used.random(draws)
			if err != nil {
				t.Fatal(err)
			}
			d.Value = value
		}
		v.Draws = append(v.Draws, d)
	}
	return v
}

func TestShelfTreeVectors(t *testing.T) {
	path := filepath.Join("testdata", "shelf", "tree.json")
	got, err := json.MarshalIndent(buildTreeVectors(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if os.Getenv("EB_WRITE_SHELF_VECTORS") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from this build's constructions; regenerate only for an intended change (EB_WRITE_SHELF_VECTORS=1)", path)
	}
	// The file is read back, not only compared: every path folds to its
	// tree's root.
	var v treeVectors
	if err := json.Unmarshal(want, &v); err != nil {
		t.Fatal(err)
	}
	for i, tree := range v.Trees {
		for _, p := range tree.Paths {
			var leaves TickLeaves
			if err := json.Unmarshal([]byte(tree.Leaves), &leaves); err != nil {
				t.Fatal(err)
			}
			var leaf []byte
			if p.Index == leaves.FillerIndex {
				nonce, _ := hex.DecodeString(leaves.FillerNonce)
				leaf = cadenceFillerLeaf(nonce)
			} else {
				for _, l := range leaves.Deals {
					if l.Index == p.Index {
						leaf, _ = cadenceLeaf(l.LogID, l.Size, l.CheckpointSHA256, l.Salt)
					}
				}
			}
			path := make([][]byte, len(p.Path))
			for j := range p.Path {
				path[j], _ = hex.DecodeString(p.Path[j])
			}
			if got := hex.EncodeToString(cadenceFold(leaf, p.Index, path)); got != tree.Root {
				t.Fatalf("tree %d: the path of %d folds to %s, not %s", i, p.Index, got, tree.Root)
			}
		}
	}
}
