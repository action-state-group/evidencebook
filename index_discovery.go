// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Candidates is the only result type a DiscoveryIndex returns. It is a list
// of places to look, never a statement about what exists or what is true: no
// function in this package that produces a proof, a bundle, or an answer to
// a request accepts it (enforced by TestDiscoveryCandidatesNeverReachProofs).
// To act on a candidate, fetch it by id through the book.
type Candidates struct {
	query string
	ids   []string
}

// Query returns the text that produced these candidates.
func (c Candidates) Query() string { return c.query }

// IDs returns candidate record ids, best match first.
func (c Candidates) IDs() []string { return append([]string(nil), c.ids...) }

// DiscoveryIndex finds candidate records by approximate match. It is always
// rebuildable and is never proof of completeness or of truth.
type DiscoveryIndex interface {
	Add(ctx context.Context, record Record) error
	Search(ctx context.Context, query string, limit int) (Candidates, error)
	Rebuild(ctx context.Context, source RecordSource) error
}

// TokenIndex is the reference DiscoveryIndex.
type TokenIndex struct {
	mu       sync.Mutex
	postings map[string]map[string]struct{}
	order    map[string]uint64
}

// NewTokenIndex is a small reference DiscoveryIndex that matches word tokens
// in record_type, subject_ref and statement text.
func NewTokenIndex() *TokenIndex {
	return &TokenIndex{postings: make(map[string]map[string]struct{}), order: make(map[string]uint64)}
}

func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

func (x *TokenIndex) Add(_ context.Context, record Record) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	h := record.Header
	for _, token := range tokens(h.RecordType + " " + h.SubjectRef + " " + string(h.Statement)) {
		if x.postings[token] == nil {
			x.postings[token] = make(map[string]struct{})
		}
		x.postings[token][record.RecordID] = struct{}{}
	}
	x.order[record.RecordID] = record.Seq
	return nil
}

func (x *TokenIndex) Search(_ context.Context, query string, limit int) (Candidates, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	score := make(map[string]int)
	for _, token := range tokens(query) {
		for id := range x.postings[token] {
			score[id]++
		}
	}
	ids := make([]string, 0, len(score))
	for id := range score {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if score[ids[i]] != score[ids[j]] {
			return score[ids[i]] > score[ids[j]]
		}
		return x.order[ids[i]] < x.order[ids[j]]
	})
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	return Candidates{query: query, ids: ids}, nil
}

func (x *TokenIndex) Rebuild(ctx context.Context, source RecordSource) error {
	x.mu.Lock()
	x.postings = make(map[string]map[string]struct{})
	x.order = make(map[string]uint64)
	x.mu.Unlock()
	return source(ctx, func(record Record) error { return x.Add(ctx, record) })
}
