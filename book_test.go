// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	aacverify "github.com/action-state-group/agent-action-capsule/go/verify"
)

func TestAppendSealsHeaderIntoAVerifyingCapsule(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	record := mustAppend(t, book, observation("order-17", `{"status":"approved"}`))

	stored, err := book.storedLocked(ctx, record.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := decodeJSON(stored.Capsule)
	if err != nil {
		t.Fatal(err)
	}
	result := aacverify.Verify(capsule, nil, nil)
	if !result.OK || result.CapsuleID == nil || *result.CapsuleID != record.RecordID {
		t.Fatalf("record capsule does not verify as its record id: %+v", result.Findings)
	}
	var binding capsuleBinding
	if err := json.Unmarshal(stored.Capsule, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.ModelAttestation.ComputeAttestation.AgentInputDigest != digestBytes(stored.Header) {
		t.Fatal("capsule agent_input_digest does not commit the stored header bytes")
	}
	if record.Header.PayloadCommitments[0] != digestBytes([]byte(`{"status":"approved"}`)) || record.Header.RetentionState != Available {
		t.Fatalf("payload commitment or retention state wrong: %+v", record.Header)
	}
}

func TestRecordModelRules(t *testing.T) {
	book := newFixture(t, "book-a", 1).open(t)
	target := mustAppend(t, book, observation("s", "x"))
	cases := map[string]Entry{
		"unregistered epistemic type":      {RecordType: "claim", EpistemicType: "verified_fact"},
		"unregistered link type":           {RecordType: "claim", EpistemicType: ProducerClaim, Links: []Link{{Type: "endorses", Target: target.RecordID}}},
		"adjudicates without adjudication": {RecordType: "ruling", EpistemicType: SemanticJudgment, Links: []Link{{Type: Adjudicates, Target: target.RecordID}}},
		"link target is not a record id":   {RecordType: "claim", EpistemicType: ProducerClaim, Links: []Link{{Type: Cites, Target: "order-17"}}},
		"record type missing":              {EpistemicType: ProducerClaim},
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := book.Append(context.Background(), entry); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
	if _, err := book.Append(context.Background(), Entry{RecordType: "ruling", EpistemicType: Adjudication, Links: []Link{{Type: Adjudicates, Target: target.RecordID}}}); err != nil {
		t.Fatalf("a well-formed adjudication was refused: %v", err)
	}
	if book.Size() != 2 {
		t.Fatalf("rejected entries must not consume positions: size %d", book.Size())
	}
}

func TestReopenRecoversStoredButUncommittedRecord(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	first := mustAppend(t, book, observation("s", "one"))
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash between the store write and the substrate append: a
	// second record lands in the store only.
	cfg := f.config(t)
	crashed, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	crashed.mu.Lock()
	header := Header{Version: HeaderVersion, BookID: "book-a", Seq: 2, RecordType: "observation", EpistemicType: ObservedEvent, CommittedAt: "2026-09-26T13:00:00Z", Links: []Link{}}
	headerBytes, err := canonicalJSON(header)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := crashed.seal(ctx, header, headerBytes, f.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := crashed.store.PutRecord(ctx, StoredRecord{Seq: 2, RecordID: sealed.CapsuleID, Header: headerBytes, Capsule: sealed.Payload, Envelope: sealed.Envelope}); err != nil {
		t.Fatal(err)
	}
	crashed.mu.Unlock()
	if err := crashed.Release(); err != nil {
		t.Fatal(err)
	}

	reopened := f.open(t)
	if reopened.Size() != 2 {
		t.Fatalf("size after recovery = %d, want 2", reopened.Size())
	}
	size, err := reopened.commitments.Size(ctx)
	if err != nil || size != 2 {
		t.Fatalf("substrate size after recovery = %d (%v), want 2", size, err)
	}
	if got, err := reopened.Get(ctx, first.RecordID); err != nil || got.Seq != 1 {
		t.Fatalf("first record lost: %v", err)
	}
}

func TestOpenRefusesSubstrateIdentityWithNoStoredRecord(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	mustAppend(t, book, observation("s", "one"))
	if _, err := book.commitments.Append(ctx, digestBytes([]byte("not a record")), f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, f.config(t)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt for an identity the book cannot produce, got %v", err)
	}
}

func TestOpenRefusesTamperedStoredHeader(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	record := mustAppend(t, book, Entry{RecordType: "claim", EpistemicType: ProducerClaim})
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	// Upgrade the committed epistemic type in the durable journal, keeping the
	// header canonical so only the capsule commitment can catch it.
	stored := mustStored(t, f, record.RecordID)
	tampered := record.Header
	tampered.EpistemicType = ObservedEvent
	headerBytes, err := canonicalJSON(tampered)
	if err != nil {
		t.Fatal(err)
	}
	stored.Header = headerBytes
	line, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.dir, "records", "records.jsonl")
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, f.config(t)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("an upgraded epistemic_type must fail the header commitment on open, got %v", err)
	}
}

func mustStored(t *testing.T, f *bookFixture, id string) StoredRecord {
	t.Helper()
	store, err := OpenFileStore(filepath.Join(f.dir, "records"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Release() }()
	stored, err := store.GetRecord(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestTornStoreTailIsTruncated(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	mustAppend(t, book, observation("s", "one"))
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	appendRaw(t, filepath.Join(f.dir, "records", "records.jsonl"), `{"seq":2,"record_id":"`)
	reopened := f.open(t)
	if reopened.Size() != 1 {
		t.Fatalf("torn tail was not truncated: size %d", reopened.Size())
	}
	mustAppend(t, reopened, observation("s", "two"))
	if _, err := reopened.Query(ctx, Filter{SubjectRef: "s"}); err != nil {
		t.Fatal(err)
	}
}
