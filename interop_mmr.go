// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math/bits"
)

// The Merkle Mountain Range functions the profile's CLL material uses
// (profile §6.2.1), written from the algorithms of the pinned MMR revision
// (draft-bryce-cose-receipts-mmr-profile-02): index_height (§9),
// included_root (§5.2), peaks (§7.1.1) and consistent_roots (§7.1). Node
// positions are one-based in the hash (pos = index + 1), and an interior
// node is SHA-256(pos as 8 big-endian octets || left || right) (§8.2.1).
// all_ones is read as the profile reads it (P-31): pos & (pos+1) == 0.

func mmrAllOnes(pos uint64) bool { return pos != 0 && pos&(pos+1) == 0 }

// mmrIndexHeight is the height of the node at zero-based index i; leaves
// have height 0.
func mmrIndexHeight(i uint64) uint64 {
	pos := i + 1
	for !mmrAllOnes(pos) {
		pos = pos - (uint64(1) << (bits.Len64(pos) - 1)) + 1
	}
	return uint64(bits.Len64(pos) - 1)
}

func mmrHashPosPair(pos uint64, left, right []byte) []byte {
	h := sha256.New()
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], pos)
	h.Write(encoded[:])
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// mmrIncludedRoot is the root reached from node at index i by proof.
func mmrIncludedRoot(i uint64, node []byte, proof [][]byte) []byte {
	root, g := node, mmrIndexHeight(i)
	for _, sibling := range proof {
		if mmrIndexHeight(i+1) > g {
			i++
			root = mmrHashPosPair(i+1, sibling, root)
		} else {
			i += uint64(2) << g
			root = mmrHashPosPair(i+1, root, sibling)
		}
		g++
	}
	return root
}

// mmrPeaks is the indices of the peaks of the MMR whose last node is at
// index i.
func mmrPeaks(i uint64) []uint64 {
	var out []uint64
	var peak uint64
	size := i + 1
	for size > 0 {
		highest := (uint64(1) << (bits.Len64(size+1) - 1)) - 1
		peak += highest
		out = append(out, peak-1)
		size -= highest
	}
	return out
}

// mmrConsistentRoots is the roots each old peak reaches by its consistency
// path, with adjacent repeats merged.
func mmrConsistentRoots(ifrom uint64, accumulator [][]byte, paths [][][]byte) [][]byte {
	var roots [][]byte
	for k, index := range mmrPeaks(ifrom) {
		if k >= len(accumulator) || k >= len(paths) {
			break
		}
		root := mmrIncludedRoot(index, accumulator[k], paths[k])
		if len(roots) == 0 || !bytes.Equal(roots[len(roots)-1], root) {
			roots = append(roots, root)
		}
	}
	return roots
}

// mmrLeafIndex is the zero-based MMR node index of zero-based leaf n.
func mmrLeafIndex(n uint64) uint64 {
	return 2*n - uint64(bits.OnesCount64(n))
}

// cllLeafValue is the profile's leaf for a logged entry E: SHA-256(0x00 || E)
// (profile §6.2.1, P-31).
func cllLeafValue(entry []byte) []byte {
	sum := sha256.Sum256(append([]byte{0}, entry...))
	return sum[:]
}
