// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// The cadence tree (evidencebook#36 §2): each tick commits every live log's
// checkpoint as a salted leaf at a random position of a sparse, fixed-depth
// tree, and only the root goes to the profile's cadence log and its
// witness. Every construction here is capsule-cli's (internal/cli
// deal_cadence.go at 7a6b101: cadenceLeaf, cadenceFillerLeaf, cadenceNode,
// cadenceEmpty, cadenceTree, cadenceFold), byte for byte, so receipts its
// released builds issued keep verifying; testdata/cadence holds vectors
// written by that code.

// CadenceDepth is the tree's fixed depth: every path is 16 hashes, and a
// tick holds at most 2^16 - 1 logs beside its filler.
const CadenceDepth = 16

// cadenceLeaf is SHA-256(0x00 ‖ JCS({checkpoint_sha256, log_id, salt,
// size})), size being the log's MMR size at the checkpoint.
func cadenceLeaf(logID string, size uint64, checkpointSHA256, salt string) ([]byte, error) {
	jcs, err := canonicalJSON(map[string]any{"checkpoint_sha256": checkpointSHA256, "log_id": logID, "salt": salt, "size": size})
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(append([]byte{0x00}, jcs...))
	return h[:], nil
}

// cadenceFillerLeaf is SHA-256(0x03 ‖ nonce).
func cadenceFillerLeaf(nonce []byte) []byte {
	h := sha256.Sum256(append([]byte{0x03}, nonce...))
	return h[:]
}

// cadenceNode is SHA-256(0x01 ‖ left ‖ right).
func cadenceNode(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// cadenceEmpty[h] is the root of an empty subtree of height h, from the
// empty leaf SHA-256(0x02).
var cadenceEmpty = func() [][]byte {
	levels := make([][]byte, CadenceDepth+1)
	leaf := sha256.Sum256([]byte{0x02})
	levels[0] = leaf[:]
	for h := 1; h <= CadenceDepth; h++ {
		levels[h] = cadenceNode(levels[h-1], levels[h-1])
	}
	return levels
}()

// cadenceTree returns the root of the sparse tree over leaves and, for
// index, its path: the sibling at each height, leaf level first. A missing
// sibling is the empty subtree's root, which is what a path shows of the
// tree's occupancy.
func cadenceTree(leaves map[uint64][]byte, index uint64) (root []byte, path [][]byte) {
	level := leaves
	for h := 0; h < CadenceDepth; h++ {
		sibling, ok := level[index^1]
		if !ok {
			sibling = cadenceEmpty[h]
		}
		path = append(path, sibling)
		next := make(map[uint64][]byte, len(level)/2+1)
		for i := range level {
			parent := i >> 1
			if _, done := next[parent]; done {
				continue
			}
			left, ok := level[parent<<1]
			if !ok {
				left = cadenceEmpty[h]
			}
			right, ok := level[parent<<1|1]
			if !ok {
				right = cadenceEmpty[h]
			}
			next[parent] = cadenceNode(left, right)
		}
		level, index = next, index>>1
	}
	if r, ok := level[0]; ok {
		return r, path
	}
	return cadenceEmpty[CadenceDepth], path
}

// cadenceFold recomputes a root from a leaf, its position and its path.
func cadenceFold(leaf []byte, index uint64, path [][]byte) []byte {
	h := leaf
	for level, sibling := range path {
		if (index>>level)&1 == 0 {
			h = cadenceNode(h, sibling)
		} else {
			h = cadenceNode(sibling, h)
		}
	}
	return h
}

// TickLeaf is one log's leaf in one tick, as stored on the device. It is
// capsule-cli's dealTickLeaf, member for member.
type TickLeaf struct {
	LogID            string `json:"log_id"`
	Size             uint64 `json:"size"`
	CheckpointSHA256 string `json:"checkpoint_sha256"`
	Salt             string `json:"salt"`
	Index            uint64 `json:"index"`
	// Statement is the log's checkpoint itself (base64url COSE), kept on
	// the device so a later bundle can show an earlier witnessed checkpoint
	// is a prefix of its current one. The leaf commits to its SHA-256.
	Statement string `json:"statement,omitempty"`
}

// TickLeaves is every leaf of one tick: capsule-cli's dealTickLeaves (the
// logs under "deals", one filler) plus the derived fillers that keep a
// tick's occupancy at the profile's lifetime log count (§2, occupancy
// floor). A tick stored by a released capsule-cli build has no
// filler_seed and no derived fillers, and reads unchanged.
type TickLeaves struct {
	Deals       map[string]TickLeaf `json:"deals"`
	FillerIndex uint64              `json:"filler_index"`
	FillerNonce string              `json:"filler_nonce"`
	// FillerSeed and FillerCount derive fillers 1..FillerCount
	// (DerivedFillers). The seed stays on the device, like the salts.
	FillerSeed  string `json:"filler_seed,omitempty"`
	FillerCount uint64 `json:"filler_count,omitempty"`
}

// positions hands out fresh, uniform, unused positions in the tree.
type positions map[uint64]bool

func (used positions) random(source randomSource) (uint64, error) {
	if len(used) >= 1<<CadenceDepth {
		return 0, fmt.Errorf("%w: the cadence tree is full", ErrInvalid)
	}
	for {
		i, err := source.uniform(1 << CadenceDepth)
		if err != nil {
			return 0, err
		}
		if !used[i] {
			used[i] = true
			return i, nil
		}
	}
}

// DerivedFillers returns fillers 1..count of a tick, by position. Filler i
// is SHA-256(0x03 ‖ n_i) with n_i = SHA-256(seed ‖ uint64be(i)); its
// position is the first unused one among, for attempt a = 0, 1, …,
// uint64be(SHA-256(seed ‖ "pos" ‖ uint64be(i) ‖ uint32be(a))[0:8]) mod
// 2^16, after the logs' leaves and filler 0 (used) and the fillers before
// it. used is updated. The same seed, count and used positions give the
// same fillers, so a tick's tree is rebuilt from what it stored.
func DerivedFillers(seed []byte, count uint64, used map[uint64]bool) (map[uint64][]byte, error) {
	const capacity = uint64(1) << CadenceDepth
	if used == nil {
		return nil, fmt.Errorf("%w: the used positions are required", ErrInvalid)
	}
	// Compared without adding, so no count can wrap past the check.
	if taken := uint64(len(used)); taken > capacity || count > capacity-taken {
		return nil, fmt.Errorf("%w: %d fillers do not fit beside %d leaves", ErrInvalid, count, len(used))
	}
	out := make(map[uint64][]byte, count)
	for i := uint64(1); i <= count; i++ {
		var ib [8]byte
		binary.BigEndian.PutUint64(ib[:], i)
		nonce := sha256.Sum256(append(append([]byte{}, seed...), ib[:]...))
		for a := uint32(0); ; a++ {
			var ab [4]byte
			binary.BigEndian.PutUint32(ab[:], a)
			h := sha256.New()
			h.Write(seed)
			h.Write([]byte("pos"))
			h.Write(ib[:])
			h.Write(ab[:])
			position := binary.BigEndian.Uint64(h.Sum(nil)[:8]) % (1 << CadenceDepth)
			if !used[position] {
				used[position] = true
				out[position] = cadenceFillerLeaf(nonce[:])
				break
			}
		}
	}
	return out, nil
}

// Tree rebuilds the tick's tree from its stored leaves and returns the root
// and the path of the leaf at index.
func (l TickLeaves) Tree(index uint64) (root []byte, path [][]byte, err error) {
	const capacity = uint64(1) << CadenceDepth
	// A position past the tree would fold into another one, and its leaf
	// would silently leave the root.
	if index >= capacity || l.FillerIndex >= capacity {
		return nil, nil, fmt.Errorf("%w: a cadence position is under %d", ErrCorrupt, capacity)
	}
	leaves := map[uint64][]byte{}
	used := map[uint64]bool{}
	nonce, err := hex.DecodeString(l.FillerNonce)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: filler nonce: %v", ErrCorrupt, err)
	}
	leaves[l.FillerIndex], used[l.FillerIndex] = cadenceFillerLeaf(nonce), true
	for _, leaf := range l.Deals {
		if leaf.Index >= capacity {
			return nil, nil, fmt.Errorf("%w: position %d of %s is past the tree", ErrCorrupt, leaf.Index, leaf.LogID)
		}
		if used[leaf.Index] {
			return nil, nil, fmt.Errorf("%w: two leaves at position %d", ErrCorrupt, leaf.Index)
		}
		value, err := cadenceLeaf(leaf.LogID, leaf.Size, leaf.CheckpointSHA256, leaf.Salt)
		if err != nil {
			return nil, nil, err
		}
		leaves[leaf.Index], used[leaf.Index] = value, true
	}
	if l.FillerCount > 0 {
		seed, err := hex.DecodeString(l.FillerSeed)
		if err != nil || len(seed) != 32 {
			return nil, nil, fmt.Errorf("%w: filler seed", ErrCorrupt)
		}
		fillers, err := DerivedFillers(seed, l.FillerCount, used)
		if err != nil {
			return nil, nil, err
		}
		for position, value := range fillers {
			leaves[position] = value
		}
	}
	root, path = cadenceTree(leaves, index)
	return root, path, nil
}
