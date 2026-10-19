// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/action-state-group/agent-action-capsule/go/disclosure"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/capsule-emit/go/artifact"
)

// ownSealer seals a capsule of the caller's own: its payload is the caller's
// input, bound to agent_input_digest, and it carries the record header the
// book hands it in RecordHeaderMember, signed with key.
func ownSealer(t *testing.T, seed byte, payload string, member func(Header, []byte) []byte) CapsuleSealer {
	t.Helper()
	return func(header Header, headerJSON []byte) (emit.Result, error) {
		identity, err := emit.NewEd25519SigningIdentity(seededKey(seed))
		if err != nil {
			return emit.Result{}, err
		}
		value := headerJSON
		if member != nil {
			value = member(header, headerJSON)
		}
		input := emit.Input{
			ActionID: "own-action", ActionType: emit.ActionTypeFYI,
			Operator: "caller", Developer: "caller", Timestamp: time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC),
		}
		if value != nil {
			input.Extensions = []emit.Extension{{Name: RecordHeaderMember, Value: value}}
		}
		return emit.Seal(emit.SealInput{Capsule: input, Payload: json.RawMessage(payload), Identity: identity})
	}
}

func boundPayload(payload string) []artifact.Artifact {
	return []artifact.Artifact{{Name: "payload", Binding: artifact.PayloadDigest, State: artifact.Present, Content: []byte(payload)}}
}

func publishedEntry() Entry {
	return Entry{RecordType: "published_capsule", EpistemicType: ProducerClaim, SubjectRef: "publication-1"}
}

// TestAppendCapsuleIsTheRecord: the caller's own capsule is the record. Its
// id is the log entry, it carries its header, the artifact store holds it
// once with its originals, and it survives a reopen.
func TestAppendCapsuleIsTheRecord(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	before := mustAppend(t, book, observation("s", "x"))
	payload := `{"order":17}`
	record, err := book.AppendCapsule(ctx, publishedEntry(), boundPayload(payload), ownSealer(t, 1, payload, nil))
	if err != nil {
		t.Fatal(err)
	}
	if record.Seq != before.Seq+1 || record.Header.Seq != record.Seq {
		t.Fatalf("the capsule did not take the next position: %d after %d", record.Seq, before.Seq)
	}
	entries, err := book.commitments.Entries(ctx, before.Seq, 0)
	if err != nil || len(entries) != 1 || entries[0].RecordID != record.RecordID {
		t.Fatalf("the log entry is not the capsule's id: %+v %v", entries, err)
	}
	if record.Header.RetentionState != Available || len(record.Header.PayloadCommitments) != 1 || record.Header.PayloadCommitments[0] != digestBytes([]byte(payload)) {
		t.Fatalf("the header does not commit the original: %+v", record.Header)
	}
	held, err := book.originals.Get(ctx, record.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := book.storedLocked(ctx, record.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if string(held.Capsule) != string(stored.Capsule) || len(held.Artifacts) != 1 || held.Artifacts[0].Binding != artifact.PayloadDigest {
		t.Fatal("the artifact store does not hold the capsule with its bound original")
	}
	carried, inline, err := inlineHeader(stored.Capsule)
	if err != nil || !inline || string(carried) != string(stored.Header) {
		t.Fatal("the capsule does not carry its record header")
	}
	if data, err := book.ResolvePayload(ctx, record.RecordID, record.Header.PayloadCommitments[0]); err != nil || string(data) != payload {
		t.Fatalf("the original does not resolve: %v", err)
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	if got, err := reopened.Get(ctx, record.RecordID); err != nil || got.Seq != record.Seq {
		t.Fatalf("the caller-sealed record did not survive a reopen: %v", err)
	}
	if next := mustAppend(t, reopened, observation("s", "y")); next.Seq != record.Seq+1 {
		t.Fatalf("the book did not continue after the caller-sealed record: %d", next.Seq)
	}
}

// TestAppendCapsuleRefusesOtherCapsules: a capsule that does not carry this
// exact header, or is not signed by the book's signer, is not a record of
// this book, and nothing is written for it.
func TestAppendCapsuleRefusesOtherCapsules(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	payload := `{"order":17}`
	otherHeader := func(h Header, _ []byte) []byte {
		h.Seq++
		raw, err := canonicalJSON(h)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for name, seal := range map[string]CapsuleSealer{
		"no header":        ownSealer(t, 1, payload, func(Header, []byte) []byte { return nil }),
		"another header":   ownSealer(t, 1, payload, otherHeader),
		"another signer":   ownSealer(t, 9, payload, nil),
		"the sealer fails": func(Header, []byte) (emit.Result, error) { return emit.Result{}, errors.New("no key") },
	} {
		if _, err := book.AppendCapsule(ctx, publishedEntry(), boundPayload(payload), seal); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if _, err := book.AppendCapsule(ctx, Entry{RecordType: "published_capsule", EpistemicType: ProducerClaim, Payloads: [][]byte{[]byte("x")}}, nil, ownSealer(t, 1, payload, nil)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bare payloads on a caller-sealed record: %v", err)
	}
	if _, err := book.AppendCapsule(ctx, publishedEntry(), []artifact.Artifact{{Name: "payload", State: artifact.Present}}, ownSealer(t, 1, payload, nil)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a present original without content: %v", err)
	}
	if _, err := book.AppendCapsule(ctx, publishedEntry(), []artifact.Artifact{{Name: "payload", State: artifact.Purged, ContentSHA256: digestBytes([]byte(payload))}}, ownSealer(t, 1, payload, nil)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a purged original: %v", err)
	}
	if book.Size() != 0 {
		t.Fatalf("a refused capsule left %d records", book.Size())
	}
	if next := mustAppend(t, book, observation("s", "x")); next.Seq != 1 {
		t.Fatalf("a refused capsule consumed a position: %d", next.Seq)
	}
}

// TestAppendCapsuleKeepsNeverRetainedDeclarations: an original the caller
// declares never retained is stored as declared and not committed.
func TestAppendCapsuleKeepsNeverRetainedDeclarations(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	payload := `{"order":17}`
	originals := append(boundPayload(payload), artifact.Artifact{Name: "receipt", State: artifact.NeverRetained})
	record, err := book.AppendCapsule(ctx, publishedEntry(), originals, ownSealer(t, 1, payload, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Header.PayloadCommitments) != 1 {
		t.Fatalf("a never-retained original was committed: %v", record.Header.PayloadCommitments)
	}
	held, err := book.originals.Get(ctx, record.RecordID)
	if err != nil || len(held.Artifacts) != 2 || held.Artifacts[1].State != artifact.NeverRetained {
		t.Fatalf("the declaration was not stored: %+v %v", held.Artifacts, err)
	}
}

// failingStore fails PutRecord once: a crash between sealing and storing.
type failingStore struct {
	Store
	fail bool
}

func (s *failingStore) PutRecord(ctx context.Context, record StoredRecord) error {
	if s.fail {
		s.fail = false
		return errors.New("disk full")
	}
	return s.Store.PutRecord(ctx, record)
}

// TestAppendCapsulePositionIsReusedBeforeStore: the position is only in
// memory until the record is stored, so a failure before that leaves no
// record, and the next one takes the same position.
func TestAppendCapsulePositionIsReusedBeforeStore(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	cfg := f.config(t)
	store := &failingStore{Store: cfg.Store, fail: true}
	cfg.Store = store
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = book.Release() }()
	payload := `{"order":17}`
	if _, err := book.AppendCapsule(ctx, publishedEntry(), boundPayload(payload), ownSealer(t, 1, payload, nil)); err == nil {
		t.Fatal("a failed store reported success")
	}
	record, err := book.AppendCapsule(ctx, publishedEntry(), boundPayload(payload), ownSealer(t, 1, payload, nil))
	if err != nil {
		t.Fatal(err)
	}
	if record.Seq != 1 || book.Size() != 1 {
		t.Fatalf("the position was not reused: seq %d, size %d", record.Seq, book.Size())
	}
}

// TestCallerSealedRecordInABundle: a bundle carrying a caller-sealed record
// verifies with the neutral verifier, its header travels with its capsule,
// and a reader of the bundle reads that header as verified.
func TestCallerSealedRecordInABundle(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	payload := `{"order":17}`
	// other comes first, so a bundle rooted on it carries the caller-sealed
	// record without selecting it.
	other := mustAppend(t, book, observation("s", "x"))
	own, err := book.AppendCapsule(ctx, publishedEntry(), boundPayload(payload), ownSealer(t, 1, payload, nil))
	if err != nil {
		t.Fatal(err)
	}
	citing := mustAppend(t, book, Entry{RecordType: "claim", EpistemicType: ProducerClaim, Links: []Link{{Type: Cites, Target: own.RecordID}}})
	for _, req := range []BundleRequest{{Root: citing.RecordID, Payloads: PayloadsAll}, {Root: other.RecordID}, {Root: own.RecordID, Suppress: []string{HeaderMember}}} {
		bundle, err := book.Bundle(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if result := aacVerify(t, bundle.JSON); result.GraphClosure.Status != "pass" || result.PerRecordMembership.Status != "pass" {
			t.Fatalf("neutral verifier: %+v", result)
		}
		verified, err := VerifyBundle(bundle.JSON)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, r := range verified.Records {
			if r.RecordID == own.RecordID {
				found = r.HeaderVerified && r.Header != nil && r.Header.Seq == own.Seq
			}
		}
		if !found {
			t.Fatal("the caller-sealed record's header is not read as verified")
		}
		var statement DisclosureStatement
		if err := json.Unmarshal(bundle.Disclosure.Header.Statement, &statement); err != nil {
			t.Fatal(err)
		}
		for _, w := range statement.Withheld {
			if w.RecordID == own.RecordID && w.Member == HeaderMember {
				t.Fatal("a header the capsule carries is listed as withheld")
			}
		}
		var wire bundleWire
		if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
			t.Fatal(err)
		}
		// The header travels in the capsule. agent_input is the capsule's
		// own member: the bound original, disclosed when the record is
		// selected with its payloads and agent_input is not suppressed.
		if req.Root == own.RecordID {
			// agent_input suppressed: the original bound to it is withheld,
			// not shipped as a payload.
			if bytes.Contains(bundle.JSON, []byte(base64.RawURLEncoding.EncodeToString([]byte(payload)))) {
				t.Fatal("a suppressed member's original shipped as a payload")
			}
			withheld := false
			for _, w := range statement.Withheld {
				withheld = withheld || (w.RecordID == own.RecordID && w.PayloadCommitment != "")
			}
			if !withheld {
				t.Fatal("the suppressed original is not listed as withheld")
			}
		}
		members, disclosed := wire.Disclosures[own.RecordID]
		want := req.Root == citing.RecordID
		if disclosed != want {
			t.Fatalf("root %s: own capsule's members disclosed = %v, want %v", req.Root, disclosed, want)
		}
		if !disclosed {
			continue
		}
		if string(members["agent_input"]) != payload {
			t.Fatalf("agent_input disclosed as %s, want the bound original", members["agent_input"])
		}
		var capsule any
		for _, raw := range wire.Records {
			c, err := decodeJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			if c.(map[string]any)["capsule_id"] == own.RecordID {
				capsule = c
			}
		}
		input, err := decodeJSON(members["agent_input"])
		if err != nil {
			t.Fatal(err)
		}
		checked := disclosure.Verify(map[string]any{"capsule": capsule, "disclosures": map[string]any{"agent_input": input}}, nil)
		if checked.DisclosuresMatched() != 1 {
			t.Fatalf("the neutral disclosure check: %+v", checked.DisclosureFindings)
		}
	}
}

// TestRetentionCommitmentHoldsTheRecord: a retention commitment holds every
// record it covers at LEGAL_HOLD until its until, so deleting one is
// refused; afterwards the record's own state applies again.
func TestRetentionCommitmentHoldsTheRecord(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	named := mustAppend(t, book, observation("a", "one"))
	ranged := mustAppend(t, book, observation("b", "two"))
	half := digestBytes([]byte("exchange half"))
	exchanged := mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: named.RecordID}, {Type: Cites, Target: half}}, Payloads: [][]byte{[]byte("three")}})
	free := mustAppend(t, book, observation("c", "four"))
	until := f.clock.Now().Add(time.Hour)
	for _, subject := range []Subject{
		{Kind: SubjectRecord, Digest: named.RecordID},
		{Kind: SubjectRange, Range: [2]uint64{ranged.Seq - 1, ranged.Seq - 1}},
		{Kind: SubjectExchange, Digest: half},
	} {
		if _, err := book.CommitRetention(ctx, subject, until); err != nil {
			t.Fatal(err)
		}
	}
	for _, held := range []Record{named, ranged, exchanged} {
		if state, err := book.Retention(ctx, held.RecordID); err != nil || state != LegalHold {
			t.Fatalf("record %d under a live commitment: %s %v", held.Seq, state, err)
		}
		if _, err := book.SetRetention(ctx, held.RecordID, Deleted, "expiry"); !errors.Is(err, ErrLegalHold) {
			t.Fatalf("record %d was purged under a live commitment: %v", held.Seq, err)
		}
		if data, err := book.ResolvePayload(ctx, held.RecordID, held.Header.PayloadCommitments[0]); err != nil || len(data) == 0 {
			t.Fatalf("a hold must not change resolvability: %v", err)
		}
	}
	if state, err := book.Retention(ctx, free.RecordID); err != nil || state != Available {
		t.Fatalf("a record no commitment covers: %s %v", state, err)
	}
	// A withheld record stays withheld under a hold, and still cannot go.
	if _, err := book.SetRetention(ctx, free.RecordID, Withheld, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := book.CommitRetention(ctx, Subject{Kind: SubjectRecord, Digest: free.RecordID}, until); err != nil {
		t.Fatal(err)
	}
	if state, _ := book.Retention(ctx, free.RecordID); state != Withheld {
		t.Fatalf("a held withheld record reads %s", state)
	}
	if _, err := book.SetRetention(ctx, free.RecordID, Deleted, "expiry"); !errors.Is(err, ErrLegalHold) {
		t.Fatalf("a held withheld record was purged: %v", err)
	}
	f.clock.mu.Lock()
	f.clock.t = until.Add(time.Minute)
	f.clock.mu.Unlock()
	if state, err := book.Retention(ctx, named.RecordID); err != nil || state != Available {
		t.Fatalf("after until the record's own state applies: %s %v", state, err)
	}
	if _, err := book.SetRetention(ctx, named.RecordID, Deleted, "expiry"); err != nil {
		t.Fatalf("deletion after the commitment expired: %v", err)
	}
	if _, err := book.ResolvePayload(ctx, named.RecordID, named.Header.PayloadCommitments[0]); !errors.Is(err, ErrUnavailable) {
		t.Fatal("a deleted record still resolves")
	}
}

// TestRetentionNeedsCommittedPayloads: a record that commits no payload has
// no retention state to change.
func TestRetentionNeedsCommittedPayloads(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	bare := mustAppend(t, book, Entry{RecordType: "claim", EpistemicType: ProducerClaim})
	if _, err := book.Retention(ctx, bare.RecordID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retention of a record without payloads: %v", err)
	}
	if _, err := book.SetRetention(ctx, bare.RecordID, Deleted, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting a record without payloads: %v", err)
	}
}

// TestRecordResponseKeepsTheReplyOctets: the requester's response record
// keeps the reply exactly as received, and ReplyOctets reads it back from
// the artifact store.
func TestRecordResponseKeepsTheReplyOctets(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	requester := newFixture(t, "requester", 30).open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	reply := respondAs(t, responder, sent.Message(), "requester")
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(responder, 1))
	if err != nil {
		t.Fatal(err)
	}
	octets, err := requester.ReplyOctets(ctx, recorded.RecordID)
	if err != nil || string(octets) != string(reply.Message.Octets) {
		t.Fatalf("the reply octets were not kept: %v", err)
	}
	if _, err := requester.ReplyOctets(ctx, sent.RecordID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a request record is not a response: %v", err)
	}
}

// TestMigratePayloadDirKeepsEveryProof: a book written with the earlier
// payload directory moves its bytes into the artifact store. No record,
// log entry or checkpoint changes, an interrupted run is resumed, and a
// payload already deleted stays absent.
func TestMigratePayloadDirKeepsEveryProof(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	kept := mustAppend(t, book, observation("a", "kept"))
	gone := mustAppend(t, book, observation("b", "gone"))
	mustAppend(t, book, Entry{RecordType: "claim", EpistemicType: ProducerClaim})
	before, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	// Recreate the earlier layout: the originals as files named by digest
	// (one deleted), and an artifact store that does not hold them yet.
	dir := filepath.Join(f.dir, "payloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, kept.Header.PayloadCommitments[0]), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.dir, "originals.jsonl")); err != nil {
		t.Fatal(err)
	}
	cfg := f.config(t)
	moved, err := MigratePayloadDir(ctx, cfg.Store, cfg.Originals, dir)
	if err != nil || moved != 2 {
		t.Fatalf("migration moved %d records: %v", moved, err)
	}
	if again, err := MigratePayloadDir(ctx, cfg.Store, cfg.Originals, dir); err != nil || again != 0 {
		t.Fatalf("a repeated migration moved %d records: %v", again, err)
	}
	if err := errors.Join(cfg.Store.Release(), cfg.Substrate.Release()); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	checkpoints, err := reopened.Checkpoints(ctx)
	if err != nil || len(checkpoints) != 1 || checkpoints[0].ID != before.ID {
		t.Fatalf("the checkpoint history changed: %v", err)
	}
	if got, err := reopened.Get(ctx, kept.RecordID); err != nil || !slices.Equal(got.Header.PayloadCommitments, kept.Header.PayloadCommitments) {
		t.Fatalf("a migrated record changed: %v", err)
	}
	if data, err := reopened.ResolvePayload(ctx, kept.RecordID, kept.Header.PayloadCommitments[0]); err != nil || string(data) != "kept" {
		t.Fatalf("a migrated original does not resolve: %v", err)
	}
	if _, err := reopened.ResolvePayload(ctx, gone.RecordID, gone.Header.PayloadCommitments[0]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("a payload missing before migration must not resolve: %v", err)
	}
	if after, err := reopened.Checkpoint(ctx); err != nil || after.Entries < before.Entries {
		t.Fatalf("the log did not continue from its checkpoint: %v", err)
	}
}

// A record already in the artifact store without a payload the directory
// still holds is not skipped: the directory is that payload's only copy.
func TestMigratePayloadDirRefusesAHeldRecordMissingAPayload(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	kept := mustAppend(t, book, observation("a", "kept"))
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.dir, "payloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, kept.Header.PayloadCommitments[0]), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The store holds the record with no payload (written by hand: the
	// book never stores one so).
	if err := os.Remove(filepath.Join(f.dir, "originals.jsonl")); err != nil {
		t.Fatal(err)
	}
	cfg := f.config(t)
	stored, err := cfg.Store.GetRecord(ctx, kept.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Originals.Put(ctx, artifact.Record{CapsuleID: kept.RecordID, Capsule: stored.Capsule, ProducerEnvelope: stored.Envelope}); err != nil {
		t.Fatal(err)
	}
	if _, err := MigratePayloadDir(ctx, cfg.Store, cfg.Originals, dir); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("a held record missing a payload the directory holds was skipped: %v", err)
	}
	if err := errors.Join(cfg.Store.Release(), cfg.Substrate.Release()); err != nil {
		t.Fatal(err)
	}
}

// Under the earlier dedupe rule a deleted record's file survived when
// another record shared its digest. Migration must not bring those bytes
// back as the deleted record's originals.
func TestMigratePayloadDirLeavesADeletedRecordDeleted(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	kept := mustAppend(t, book, observation("a", "shared"))
	gone := mustAppend(t, book, observation("b", "shared"))
	if _, err := book.SetRetention(ctx, gone.RecordID, Deleted, "test"); err != nil {
		t.Fatal(err)
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.dir, "payloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, kept.Header.PayloadCommitments[0]), []byte("shared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.dir, "originals.jsonl")); err != nil {
		t.Fatal(err)
	}
	cfg := f.config(t)
	if moved, err := MigratePayloadDir(ctx, cfg.Store, cfg.Originals, dir); err != nil || moved != 1 {
		t.Fatalf("migration moved %d records: %v", moved, err)
	}
	if _, err := cfg.Originals.Get(ctx, gone.RecordID); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("the deleted record's originals were stored: %v", err)
	}
	if err := errors.Join(cfg.Store.Release(), cfg.Substrate.Release()); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	if data, err := reopened.ResolvePayload(ctx, kept.RecordID, kept.Header.PayloadCommitments[0]); err != nil || string(data) != "shared" {
		t.Fatalf("the kept record's original does not resolve: %v", err)
	}
	var unavailable *UnavailableError
	if _, err := reopened.ResolvePayload(ctx, gone.RecordID, gone.Header.PayloadCommitments[0]); !errors.As(err, &unavailable) || unavailable.State != Deleted {
		t.Fatalf("the deleted record resolved: %v", err)
	}
}

// A legacy per-payload deletion followed by a later state leaves the record
// readable, as Retention reads it: migration stores its other payload and
// leaves the deleted one out, even when a shared digest kept its file.
func TestMigratePayloadDirFollowsTheLatestLegacyState(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	book := f.open(t)
	sharer := mustAppend(t, book, observation("s", "gone"))
	both := mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "b", Payloads: [][]byte{[]byte("gone"), []byte("kept")}})
	gone, kept := both.Header.PayloadCommitments[0], both.Header.PayloadCommitments[1]
	for _, statement := range []string{
		`{"retention_state":"DELETED","payload_commitment":"` + gone + `"}`,
		`{"retention_state":"WITHHELD"}`,
	} {
		if _, err := book.Append(ctx, Entry{RecordType: RecordTypeLifecycle, EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: both.RecordID}}, Statement: json.RawMessage(statement)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.dir, "payloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for digest, data := range map[string]string{gone: "gone", kept: "kept"} {
		if err := os.WriteFile(filepath.Join(dir, digest), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(f.dir, "originals.jsonl")); err != nil {
		t.Fatal(err)
	}
	cfg := f.config(t)
	if moved, err := MigratePayloadDir(ctx, cfg.Store, cfg.Originals, dir); err != nil || moved != 2 {
		t.Fatalf("migration moved %d records: %v", moved, err)
	}
	if again, err := MigratePayloadDir(ctx, cfg.Store, cfg.Originals, dir); err != nil || again != 0 {
		t.Fatalf("a repeated migration moved %d records: %v", again, err)
	}
	held, err := cfg.Originals.Get(ctx, both.RecordID)
	if err != nil {
		t.Fatalf("the record whose latest state is WITHHELD was not stored: %v", err)
	}
	if _, _, present := originalBytes(held, kept); !present {
		t.Fatal("the payload no lifecycle deleted was not stored")
	}
	if _, found, _ := originalBytes(held, gone); found {
		t.Fatal("a payload deleted under per-payload retention came back")
	}
	if _, _, present := originalBytes(mustGet(t, cfg.Originals, sharer.RecordID), gone); !present {
		t.Fatal("the record sharing the digest lost its original")
	}
	if err := errors.Join(cfg.Store.Release(), cfg.Substrate.Release()); err != nil {
		t.Fatal(err)
	}
}

func mustGet(t *testing.T, store ArtifactStore, id string) artifact.Record {
	t.Helper()
	held, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return held
}

// An own capsule whose input and output are the same bytes discloses both
// members; suppressing one leaves the other.
func TestIdenticalInputAndOutputAreBothDisclosed(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	value := `{"same":true}`
	seal := func(header Header, headerJSON []byte) (emit.Result, error) {
		identity, err := emit.NewEd25519SigningIdentity(seededKey(1))
		if err != nil {
			return emit.Result{}, err
		}
		input := emit.Input{
			ActionID: "own-action", ActionType: emit.ActionTypeFYI,
			Operator: "caller", Developer: "caller", Timestamp: time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC),
			Extensions: []emit.Extension{{Name: RecordHeaderMember, Value: headerJSON}},
		}
		return emit.Seal(emit.SealInput{Capsule: input, Payload: json.RawMessage(value), AgentOutput: json.RawMessage(value), Identity: identity})
	}
	originals := append(boundPayload(value), artifact.Artifact{Name: "agent_output", Binding: artifact.AgentOutputDigest, State: artifact.Present, Content: []byte(value)})
	own, err := book.AppendCapsule(ctx, publishedEntry(), originals, seal)
	if err != nil {
		t.Fatal(err)
	}
	for suppress, want := range map[string][]string{"": {"agent_input", "agent_output"}, "agent_output": {"agent_input"}, "agent_input": {"agent_output"}} {
		req := BundleRequest{Root: own.RecordID, Payloads: PayloadsSelected}
		if suppress != "" {
			req.Suppress = []string{suppress}
		}
		bundle, err := book.Bundle(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		var wire bundleWire
		if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
			t.Fatal(err)
		}
		var got []string
		for member := range wire.Disclosures[own.RecordID] {
			got = append(got, member)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("suppress %q: disclosed %v, want %v", suppress, got, want)
		}
		// The shared bytes still ship: one member bound to them is disclosed.
		if !bytes.Contains(bundle.JSON, []byte(base64.RawURLEncoding.EncodeToString([]byte(value)))) {
			t.Fatalf("suppress %q: the bytes another member discloses were withheld", suppress)
		}
	}
}

// The reference artifact store is owner-only and durable.
func TestOpenOriginalsFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "originals.jsonl")
	if _, err := OpenOriginalsFile(context.Background(), path, "book", seededKey(1).Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("originals file mode %v", info.Mode().Perm())
	}
}
