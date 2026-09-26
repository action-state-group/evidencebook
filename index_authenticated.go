// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// AuthenticatedIndex is a deterministic structure whose root the book commits
// as a record.
//
// What a party that does not trust the book can check, and what it cannot:
// a membership proof shows a (key, record id) leaf is under the committed
// root, with no further trust. Non-membership, range and completeness proofs
// additionally rely on the committed tree being sorted and holding every
// record: a book that commits an unsorted or incomplete tree can prove a
// false absence or omit a match. Only a party holding the full leaf set (for
// example the book's full history) can audit that, by rebuilding the root.
// Those three claims are therefore statements by the book, checkable against
// its commitment but not against a dishonest book.
type AuthenticatedIndex interface {
	Name() string
	// KeyOf returns the key a record contributes, or false for none.
	KeyOf(record Record) (string, bool)
	Add(key, recordID string) error
	Reset()
	Root() IndexRoot
	ProveMember(key, recordID string) (LeafProof, error)
	// ProveRange proves exactly which records carry a key in [lo, hi]; with
	// lo == hi it is a completeness proof for one key, and an empty result is
	// a non-membership proof.
	ProveRange(lo, hi string) (KeyRangeProof, error)
}

// IndexRoot is what the book commits: the index name, its Merkle root, and
// its leaf count.
type IndexRoot struct {
	Index string `json:"index"`
	Root  string `json:"root"`
	Size  uint64 `json:"size"`
}

// LeafProof proves one (key, record id) leaf sits at Index under a root.
type LeafProof struct {
	Key      string   `json:"key"`
	RecordID string   `json:"record_id"`
	Index    uint64   `json:"index"`
	Path     []string `json:"path"`
}

// KeyRangeProof proves the complete set of leaves whose key lies in [Lo, Hi]:
// the matches are contiguous, and the neighbours on either side (when the
// range does not touch an edge of the tree) have keys outside the range.
type KeyRangeProof struct {
	Lo      string      `json:"lo"`
	Hi      string      `json:"hi"`
	First   uint64      `json:"first"`
	Matches []LeafProof `json:"matches"`
	Left    *LeafProof  `json:"left,omitempty"`
	Right   *LeafProof  `json:"right,omitempty"`
}

type indexLeaf struct{ key, recordID string }

func (l indexLeaf) less(o indexLeaf) bool {
	if l.key != o.key {
		return l.key < o.key
	}
	return l.recordID < o.recordID
}

func (l indexLeaf) hash() []byte {
	var length [4]byte
	h := sha256.New()
	h.Write([]byte{0})
	binary.BigEndian.PutUint32(length[:], uint32(len(l.key)))
	h.Write(length[:])
	h.Write([]byte(l.key))
	binary.BigEndian.PutUint32(length[:], uint32(len(l.recordID)))
	h.Write(length[:])
	h.Write([]byte(l.recordID))
	return h.Sum(nil)
}

// SortedMerkleIndex is the reference AuthenticatedIndex: a sorted list of
// (key, record id) leaves under an RFC 9162 Merkle tree.
type SortedMerkleIndex struct {
	mu     sync.Mutex
	name   string
	keyOf  func(Record) (string, bool)
	leaves []indexLeaf
}

// NewRecordIDIndex indexes every record under its own record id, so the book
// can prove that a record id is, or is not, committed.
func NewRecordIDIndex() *SortedMerkleIndex {
	return &SortedMerkleIndex{name: "record_id", keyOf: func(r Record) (string, bool) { return r.RecordID, true }}
}

// NewSubjectIndex indexes records under subject_ref, so the book can prove the
// complete set of records it holds for one correlation subject.
func NewSubjectIndex() *SortedMerkleIndex {
	return &SortedMerkleIndex{name: "subject_ref", keyOf: func(r Record) (string, bool) {
		return r.Header.SubjectRef, r.Header.SubjectRef != ""
	}}
}

func (m *SortedMerkleIndex) Name() string                       { return m.name }
func (m *SortedMerkleIndex) KeyOf(record Record) (string, bool) { return m.keyOf(record) }

func (m *SortedMerkleIndex) Add(key, recordID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	leaf := indexLeaf{key: key, recordID: recordID}
	at := sort.Search(len(m.leaves), func(i int) bool { return !m.leaves[i].less(leaf) })
	if at < len(m.leaves) && m.leaves[at] == leaf {
		return nil
	}
	m.leaves = append(m.leaves, indexLeaf{})
	copy(m.leaves[at+1:], m.leaves[at:])
	m.leaves[at] = leaf
	return nil
}

func (m *SortedMerkleIndex) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leaves = nil
}

func (m *SortedMerkleIndex) hashes() [][]byte {
	out := make([][]byte, len(m.leaves))
	for i, leaf := range m.leaves {
		out[i] = leaf.hash()
	}
	return out
}

func (m *SortedMerkleIndex) Root() IndexRoot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return IndexRoot{Index: m.name, Root: hex.EncodeToString(merkleRoot(m.hashes())), Size: uint64(len(m.leaves))}
}

func (m *SortedMerkleIndex) leafProof(hashes [][]byte, index int) LeafProof {
	leaf := m.leaves[index]
	return LeafProof{Key: leaf.key, RecordID: leaf.recordID, Index: uint64(index), Path: hexList(auditPath(index, hashes))}
}

func (m *SortedMerkleIndex) ProveMember(key, recordID string) (LeafProof, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, leaf := range m.leaves {
		if leaf.key == key && leaf.recordID == recordID {
			return m.leafProof(m.hashes(), i), nil
		}
	}
	return LeafProof{}, fmt.Errorf("%w: %s/%s not in index %s", ErrNotFound, key, recordID, m.name)
}

func (m *SortedMerkleIndex) ProveRange(lo, hi string) (KeyRangeProof, error) {
	if lo > hi {
		return KeyRangeProof{}, fmt.Errorf("%w: range lower bound exceeds upper bound", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	hashes := m.hashes()
	first := sort.Search(len(m.leaves), func(i int) bool { return m.leaves[i].key >= lo })
	end := sort.Search(len(m.leaves), func(i int) bool { return m.leaves[i].key > hi })
	proof := KeyRangeProof{Lo: lo, Hi: hi, First: uint64(first), Matches: []LeafProof{}}
	for i := first; i < end; i++ {
		proof.Matches = append(proof.Matches, m.leafProof(hashes, i))
	}
	if first > 0 {
		left := m.leafProof(hashes, first-1)
		proof.Left = &left
	}
	if end < len(m.leaves) {
		right := m.leafProof(hashes, end)
		proof.Right = &right
	}
	return proof, nil
}

// VerifyLeaf checks one leaf proof against a committed root.
func VerifyLeaf(root IndexRoot, proof LeafProof) error {
	expected, err := hex.DecodeString(root.Root)
	if err != nil {
		return fmt.Errorf("%w: index root is not hex", ErrInvalid)
	}
	path := make([][]byte, len(proof.Path))
	for i, node := range proof.Path {
		if path[i], err = hex.DecodeString(node); err != nil || len(path[i]) != sha256.Size {
			return fmt.Errorf("%w: audit path node %d", ErrInvalid, i)
		}
	}
	leaf := indexLeaf{key: proof.Key, recordID: proof.RecordID}
	if !verifyAuditPath(proof.Index, root.Size, leaf.hash(), path, expected) {
		return fmt.Errorf("%w: leaf %d does not verify under index root", ErrInvalid, proof.Index)
	}
	return nil
}

// VerifyKeyRange checks a range proof against a committed root and returns
// the record ids it proves are exactly the leaves with keys in [lo, hi].
func VerifyKeyRange(root IndexRoot, lo, hi string, proof KeyRangeProof) ([]string, error) {
	if lo > hi || proof.Lo != lo || proof.Hi != hi {
		return nil, fmt.Errorf("%w: proof is for a different range", ErrInvalid)
	}
	ids := make([]string, 0, len(proof.Matches))
	for i, match := range proof.Matches {
		if match.Index != proof.First+uint64(i) || match.Key < lo || match.Key > hi {
			return nil, fmt.Errorf("%w: match %d is out of place or out of range", ErrInvalid, i)
		}
		if i > 0 && !(indexLeaf{proof.Matches[i-1].Key, proof.Matches[i-1].RecordID}).less(indexLeaf{match.Key, match.RecordID}) {
			return nil, fmt.Errorf("%w: matches are not strictly ordered", ErrInvalid)
		}
		if err := VerifyLeaf(root, match); err != nil {
			return nil, err
		}
		ids = append(ids, match.RecordID)
	}
	if proof.First == 0 {
		if proof.Left != nil {
			return nil, fmt.Errorf("%w: left neighbour at tree edge", ErrInvalid)
		}
	} else {
		if proof.Left == nil || proof.Left.Index != proof.First-1 || proof.Left.Key >= lo {
			return nil, fmt.Errorf("%w: left neighbour missing or inside range", ErrInvalid)
		}
		if err := VerifyLeaf(root, *proof.Left); err != nil {
			return nil, err
		}
	}
	end := proof.First + uint64(len(proof.Matches))
	switch {
	case end > root.Size:
		return nil, fmt.Errorf("%w: range runs past the tree", ErrInvalid)
	case end == root.Size:
		if proof.Right != nil {
			return nil, fmt.Errorf("%w: right neighbour at tree edge", ErrInvalid)
		}
	default:
		if proof.Right == nil || proof.Right.Index != end || proof.Right.Key <= hi {
			return nil, fmt.Errorf("%w: right neighbour missing or inside range", ErrInvalid)
		}
		if err := VerifyLeaf(root, *proof.Right); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// merkleRoot is the RFC 9162 Merkle Tree Hash over already-hashed leaves.
func merkleRoot(leaves [][]byte) []byte {
	switch len(leaves) {
	case 0:
		sum := sha256.Sum256(nil)
		return sum[:]
	case 1:
		return leaves[0]
	}
	k := splitPoint(len(leaves))
	return interior(merkleRoot(leaves[:k]), merkleRoot(leaves[k:]))
}

// auditPath is the RFC 9162 inclusion path PATH(m, D[n]).
func auditPath(m int, leaves [][]byte) [][]byte {
	if len(leaves) <= 1 {
		return nil
	}
	k := splitPoint(len(leaves))
	if m < k {
		return append(auditPath(m, leaves[:k]), merkleRoot(leaves[k:]))
	}
	return append(auditPath(m-k, leaves[k:]), merkleRoot(leaves[:k]))
}

// verifyAuditPath is the RFC 9162 §2.1.3.2 inclusion verification.
func verifyAuditPath(index, size uint64, leaf []byte, path [][]byte, root []byte) bool {
	if index >= size {
		return false
	}
	fn, sn := index, size-1
	r := leaf
	for _, p := range path {
		if sn == 0 {
			return false
		}
		if fn&1 == 1 || fn == sn {
			r = interior(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			r = interior(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	return sn == 0 && bytes.Equal(r, root)
}

func splitPoint(n int) int {
	k := 1
	for k<<1 < n {
		k <<= 1
	}
	return k
}

func interior(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{1})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// KeyRangeAnswer is a key-range proof bound to what the book committed: the
// index root it verifies under, the index_root record that committed that
// root, and the checkpoint that covers that record. This package ships no
// standalone verifier for the binding (that the root equals the index_root
// record's statement and that the record is included under Anchor); a
// relying party checks the proof with VerifyKeyRange and the binding from a
// bundle carrying the index_root record. See AuthenticatedIndex for what the
// proof does not establish against a dishonest book.
type KeyRangeAnswer struct {
	Root         IndexRoot     `json:"root"`
	RootRecordID string        `json:"root_record_id"`
	Anchor       Checkpoint    `json:"anchor"`
	Proof        KeyRangeProof `json:"proof"`
}

// ProveKeyRange proves exactly which records carry a key in [lo, hi] of the
// named authenticated index. It first commits the index's current root as a
// record and checkpoints, so the proof is always against a root the book has
// committed and a checkpoint covers — never a live root that has drifted
// from the last committed one. An empty result is a non-membership proof.
func (b *Book) ProveKeyRange(ctx context.Context, index, lo, hi string) (KeyRangeAnswer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var target AuthenticatedIndex
	for _, candidate := range b.index {
		if candidate.Name() == index {
			target = candidate
		}
	}
	if target == nil {
		return KeyRangeAnswer{}, fmt.Errorf("%w: no authenticated index %q", ErrNotFound, index)
	}
	anchor, err := b.checkpointLocked(ctx)
	if err != nil {
		return KeyRangeAnswer{}, err
	}
	root := target.Root()
	answer := KeyRangeAnswer{Root: root, Anchor: anchor}
	for i := len(b.order) - 1; i >= 0; i-- {
		h := b.records[b.order[i]].Header
		if h.RecordType != RecordTypeIndexRoot {
			continue
		}
		var committed IndexRoot
		if err := json.Unmarshal(h.Statement, &committed); err == nil && committed == root {
			answer.RootRecordID = b.order[i]
			break
		}
	}
	if answer.RootRecordID == "" || !anchor.Covers(b.records[answer.RootRecordID].Seq) {
		return KeyRangeAnswer{}, fmt.Errorf("%w: index %s root is not committed under the anchor", ErrCorrupt, index)
	}
	if answer.Proof, err = target.ProveRange(lo, hi); err != nil {
		return KeyRangeAnswer{}, err
	}
	return answer, nil
}
