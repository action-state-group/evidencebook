// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func populated(t *testing.T, f *bookFixture) *Book {
	t.Helper()
	book := f.open(t)
	var previous string
	for i := range 12 {
		entry := observation(fmt.Sprintf("subject-%d", i%4), fmt.Sprintf("payload-%d", i))
		entry.CounterpartyRef = fmt.Sprintf("party-%d", i%3)
		entry.Correlation = Correlation{ExchangeID: fmt.Sprintf("x-%d", i), RequestDigest: digestBytes([]byte{byte(i)})}
		if previous != "" {
			entry.Links = []Link{{Type: Cites, Target: previous}}
		}
		previous = mustAppend(t, book, entry).RecordID
	}
	return book
}

// TestOperationalRebuildIsIdempotentAndLogDerived: Rebuild reproduces the
// incrementally built index exactly, twice in a row, and a fresh process that
// has only the committed history reaches the same state.
func TestOperationalRebuildIsIdempotentAndLogDerived(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := populated(t, f)
	incremental, err := book.IndexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := book.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := book.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first != incremental || second != first {
		t.Fatalf("rebuild is not idempotent: incremental %s, first %s, second %s", incremental, first, second)
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	fresh, err := reopened.IndexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh != incremental {
		t.Fatal("an index rebuilt from the committed history alone differs from the live one")
	}
	ids, err := reopened.operational.Find(ctx, Filter{LinkType: Cites, SubjectRef: "subject-1"})
	if err != nil || len(ids) != 3 {
		t.Fatalf("rebuilt index lost link rows: %d ids (%v)", len(ids), err)
	}
}

func TestOperationalStateDigestSeesEveryRow(t *testing.T) {
	ctx := context.Background()
	book := populated(t, newFixture(t, "book-a", 1))
	before, err := book.IndexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A rebuild from a source that drops one record must not reproduce the state.
	short := func(ctx context.Context, yield func(Record) error) error {
		return book.source()(ctx, func(r Record) error {
			if r.Seq == 5 {
				return nil
			}
			return yield(r)
		})
	}
	if err := book.operational.Rebuild(ctx, short); err != nil {
		t.Fatal(err)
	}
	after, err := book.IndexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("state digest did not change when a record was missing")
	}
}

func TestAuthenticatedIndexProofs(t *testing.T) {
	ctx := context.Background()
	book := populated(t, newFixture(t, "book-a", 1))
	if _, err := book.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	subjects := book.index[1]
	root := subjects.Root()

	proof, err := subjects.ProveRange("subject-2", "subject-2")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := VerifyKeyRange(root, "subject-2", "subject-2", proof)
	if err != nil {
		t.Fatal(err)
	}
	want, err := book.Query(ctx, Filter{SubjectRef: "subject-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != len(want) || len(ids) != 3 {
		t.Fatalf("completeness proof proved %d records, the book holds %d", len(ids), len(want))
	}

	absent, err := subjects.ProveRange("subject-20", "subject-20")
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := VerifyKeyRange(root, "subject-20", "subject-20", absent); err != nil || len(ids) != 0 {
		t.Fatalf("non-membership proof failed: %v %v", ids, err)
	}

	tamper := map[string]func(p *KeyRangeProof){
		"drop a match": func(p *KeyRangeProof) { p.Matches = p.Matches[1:]; p.First++ },
		"hide a match behind right": func(p *KeyRangeProof) {
			p.Right = &p.Matches[len(p.Matches)-1]
			p.Matches = p.Matches[:len(p.Matches)-1]
		},
		"omit left neighbour": func(p *KeyRangeProof) { p.Left = nil },
		"forge a record id":   func(p *KeyRangeProof) { p.Matches[0].RecordID = digestBytes([]byte("forged")) },
	}
	for name, mutate := range tamper {
		t.Run(name, func(t *testing.T) {
			var p KeyRangeProof
			data, err := json.Marshal(proof)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &p); err != nil {
				t.Fatal(err)
			}
			mutate(&p)
			if _, err := VerifyKeyRange(root, "subject-2", "subject-2", p); !errors.Is(err, ErrInvalid) {
				t.Fatalf("tampered proof accepted: %v", err)
			}
		})
	}

	// The root is committed as a record, so a verifier can pin it.
	roots, err := book.Query(ctx, Filter{RecordType: RecordTypeIndexRoot})
	if err != nil {
		t.Fatal(err)
	}
	var committed []IndexRoot
	for _, r := range roots {
		var got IndexRoot
		if err := json.Unmarshal(r.Header.Statement, &got); err != nil {
			t.Fatal(err)
		}
		committed = append(committed, got)
	}
	if !slices.Contains(committed, root) {
		t.Fatalf("live root %+v was never committed as a record: %+v", root, committed)
	}
}

func TestAuthenticatedRootIsDeterministicFromTheLog(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := populated(t, f)
	before := []IndexRoot{book.index[0].Root(), book.index[1].Root()}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	after := []IndexRoot{reopened.index[0].Root(), reopened.index[1].Root()}
	if !slices.Equal(before, after) {
		t.Fatalf("authenticated roots differ after rebuild from the log: %+v vs %+v", before, after)
	}
	record, err := reopened.Get(ctx, reopened.order[3])
	if err != nil {
		t.Fatal(err)
	}
	member, err := reopened.index[0].ProveMember(record.RecordID, record.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyLeaf(after[0], member); err != nil {
		t.Fatal(err)
	}
}

func TestRFC9162AuditPathsAtEverySize(t *testing.T) {
	for n := 1; n <= 17; n++ {
		leaves := make([][]byte, n)
		for i := range leaves {
			leaves[i] = indexLeaf{key: fmt.Sprint(i), recordID: "r"}.hash()
		}
		root := merkleRoot(leaves)
		for m := range n {
			path := auditPath(m, leaves)
			if !verifyAuditPath(uint64(m), uint64(n), leaves[m], path, root) {
				t.Fatalf("leaf %d of %d does not verify", m, n)
			}
			if n > 1 && verifyAuditPath(uint64((m+1)%n), uint64(n), leaves[m], path, root) {
				t.Fatalf("leaf %d of %d verifies at the wrong index", m, n)
			}
		}
	}
}
