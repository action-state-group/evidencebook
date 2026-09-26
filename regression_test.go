// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// flakySubstrate fails the next N appends, then behaves.
type flakySubstrate struct {
	Substrate
	failures int
}

func (f *flakySubstrate) Append(ctx context.Context, recordID string, at time.Time) (uint64, error) {
	if f.failures > 0 {
		f.failures--
		return 0, errors.New("substrate unavailable")
	}
	return f.Substrate.Append(ctx, recordID, at)
}

// A transient substrate failure must not let a second record claim the same
// position, and the book must reopen afterwards.
func TestTransientSubstrateFailureKeepsPositionsDense(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	cfg := f.config(t)
	flaky := &flakySubstrate{Substrate: cfg.Substrate}
	cfg.Substrate = flaky
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, book, observation("s", "one"))
	flaky.failures = 1
	if _, err := book.Append(ctx, observation("s", "two")); err == nil {
		t.Fatal("the failing append reported success")
	}
	third := mustAppend(t, book, observation("s", "three"))
	if third.Seq != 3 || book.Size() != 3 {
		t.Fatalf("the stored-but-uncommitted record must keep seq 2: third at %d, size %d", third.Seq, book.Size())
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	if reopened.Size() != 3 {
		t.Fatalf("reopen after a transient failure: size %d", reopened.Size())
	}
}

func TestCheckpointEveryOneTerminatesWithoutDuplicateRoots(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	cfg := f.config(t)
	cfg.CheckpointEvery = 1
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		mustAppend(t, book, observation("s", "one"))
		mustAppend(t, book, observation("s", "two"))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Append with CheckpointEvery=1 did not return")
	}
	roots, err := book.Query(ctx, Filter{RecordType: RecordTypeIndexRoot})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[IndexRoot]bool)
	for _, r := range roots {
		var root IndexRoot
		if err := json.Unmarshal(r.Header.Statement, &root); err != nil {
			t.Fatal(err)
		}
		if seen[root] {
			t.Fatalf("index root %+v committed twice", root)
		}
		seen[root] = true
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	before := reopened.Size()
	if _, err := reopened.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if reopened.Size() != before {
		t.Fatal("reopening re-committed unchanged index roots")
	}
}

// A peer that withholds headers has not given a complete account, so none of
// our halves can be proved absent on its side.
func TestReconcileWithheldPeerHeadersNeverProveAbsence(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	if _, err := a.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	anchor, err := b.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exchanges, err := b.Query(ctx, Filter{RecordType: "exchange"})
	if err != nil {
		t.Fatal(err)
	}
	// Disclose one exchange header; the other two ride along header-less.
	bundle, err := b.Bundle(ctx, BundleRequest{Root: exchanges[0].RecordID, ClosureDepth: 1, At: &anchor})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatal(err)
	}
	recon, err := a.Reconcile(ctx, ReconcileInput{Peer: peer, RecordType: "exchange", Compare: SamePayloadCommitments})
	if err != nil {
		t.Fatal(err)
	}
	if recon.PeerComplete || recon.Tallies.AOnly != 0 || recon.Tallies.Matched != 1 {
		t.Fatalf("withheld peer headers must leave our unpaired halves INSUFFICIENT: %+v", recon.Tallies)
	}
}

func TestPairingDoesNotDependOnOrder(t *testing.T) {
	ex := func(id, e, r string) Half {
		return Half{RecordID: id, Correlation: Correlation{ExchangeID: e, RequestDigest: r}, Covered: true, Trusted: true}
	}
	agree := func(Half, Half) bool { return true }
	// A1 would take B1 by request digest before A2 could join it on exchange_id.
	a := HalfSet{Complete: true, Halves: []Half{ex("A1", "", "D"), ex("A2", "E", "D")}}
	b := HalfSet{Complete: true, Halves: []Half{ex("B1", "E", "D")}}
	results, _ := ReconcileHalves(a, b, agree)
	if !slices.Contains(results, PairResult{State: Matched, JoinKey: JoinExchangeID, A: "A2", B: "B1"}) {
		t.Fatalf("exchange_id must pair globally before the request_digest fallback: %+v", results)
	}
	// An exact exchange_id + request_digest match beats a conflicting one.
	a = HalfSet{Complete: true, Halves: []Half{ex("A", "E", "D1")}}
	b = HalfSet{Complete: true, Halves: []Half{ex("Bx", "E", "D2"), ex("By", "E", "D1")}}
	_, tallies := ReconcileHalves(a, b, agree)
	if tallies.Conflicting != 0 || tallies.Matched != 1 {
		t.Fatalf("an exact counterpart exists, yet: %+v", tallies)
	}
}

func TestRespondNeverDisclosesPayloadBytes(t *testing.T) {
	book := newFixture(t, "book-a", 1).open(t)
	secret := mustAppend(t, book, observation("private", "SECRET-PAYLOAD"))
	asked := mustAppend(t, book, Entry{RecordType: "claim", EpistemicType: ProducerClaim, SubjectRef: "asked", CounterpartyRef: "peer-1", Links: []Link{{Type: Cites, Target: secret.RecordID}}, Payloads: [][]byte{[]byte("OWN-PAYLOAD")}})
	resp := respond(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: asked.RecordID}, Coverage: fresh()}), "peer-1")
	artifact := mustArtifact(t, resp)
	if strings.Contains(string(artifact.Artifact), "SECRET") {
		t.Fatal("an answer carried payload bytes")
	}
	verified, err := VerifyBundle(artifact.Artifact)
	if err != nil || len(verified.Payloads) != 0 {
		t.Fatalf("answer carried %d payloads (%v)", len(verified.Payloads), err)
	}
}

func TestPayloadsAllDisclosesOnlyTheSelectedSet(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	root := mustAppend(t, book, observation("a", "ROOT"))
	unrelated := mustAppend(t, book, observation("b", "UNRELATED"))
	bundle, err := book.Bundle(ctx, BundleRequest{Root: root.RecordID, Payloads: PayloadsAll})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := verified.Payloads[unrelated.Header.PayloadCommitments[0]]; leaked {
		t.Fatal("payloads=all disclosed a record outside the selected set")
	}
	if _, carried := verified.Payloads[root.Header.PayloadCommitments[0]]; !carried {
		t.Fatal("payloads=all did not carry the root's payload")
	}
	var statement DisclosureStatement
	if err := json.Unmarshal(bundle.Disclosure.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	stored, err := book.storedLocked(ctx, unrelated.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(statement.Withheld, WithheldItem{RecordID: unrelated.RecordID, Member: HeaderMember, Digest: digestBytes(stored.Header)}) {
		t.Fatal("a carried but undisclosed header is missing from the disclosure record")
	}
}

func TestRetentionBelongsToTheRecordNotTheBytes(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	first := mustAppend(t, book, observation("a", "SAME"))
	second := mustAppend(t, book, observation("b", "SAME"))
	digest := first.Header.PayloadCommitments[0]
	if _, err := book.SetRetention(ctx, second.RecordID, digest, Deleted, "expiry"); err != nil {
		t.Fatal(err)
	}
	if state, err := book.Retention(ctx, first.RecordID, digest); err != nil || state != Available {
		t.Fatalf("deleting one record's payload changed another's: %s %v", state, err)
	}
	if data, err := book.ResolvePayload(ctx, first.RecordID, digest); err != nil || string(data) != "SAME" {
		t.Fatalf("bytes still held by a live record were destroyed: %v", err)
	}
	if _, err := book.ResolvePayload(ctx, second.RecordID, digest); !errors.Is(err, ErrUnavailable) {
		t.Fatal("the deleted record's payload still resolves")
	}
}

func TestVerifyBundleRefusesAmbiguousHeaders(t *testing.T) {
	book, records := linkedBook(t)
	stored, err := book.storedLocked(context.Background(), records[0].RecordID)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"case-folded duplicate": strings.Replace(string(stored.Header), `"seq":1`, `"seq":1,"SEQ":7`, 1),
		"unknown field":         strings.Replace(string(stored.Header), `"seq":1`, `"seq":1,"extra":true`, 1),
	} {
		if _, err := strictHeader(json.RawMessage(raw)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: accepted %s", name, raw)
		}
	}
	if _, err := strictHeader(stored.Header); err != nil {
		t.Fatalf("the canonical header was refused: %v", err)
	}
}

func TestExistingSubjectBeyondTheAnchorIsCoverageNotAbsence(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "peer-1"})
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := ParsePin(anchor.ID)
	if err != nil {
		t.Fatal(err)
	}
	later := mustAppend(t, book, observation("late", "x"))
	for name, subject := range map[string]Subject{
		"record":      {Kind: SubjectRecord, Digest: later.RecordID},
		"correlation": {Kind: SubjectCorrelation, Value: "late"},
		"range":       {Kind: SubjectRange, First: 1, Last: later.Seq},
	} {
		resp := respond(t, book, requestBytes(t, EvidenceRequest{Subject: subject, Coverage: Coverage{ExpectedPin: &pin}}), "peer-1")
		if resp.Refusal == nil || resp.Refusal.Reason != ReasonCoverageUnsatisfiable {
			t.Fatalf("%s held beyond the pin must be coverage_unsatisfiable, got %+v", name, resp.Refusal)
		}
	}
}

func TestFutureFreshnessRefusesWithoutWork(t *testing.T) {
	book, _ := linkedBook(t)
	before := book.Size()
	future := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	resp := respond(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: Coverage{MinFreshness: &Freshness{Time: future}}}), "")
	mustRefusal(t, resp, ReasonCoverageUnsatisfiable)
	if book.Size() != before+1 {
		t.Fatalf("a future freshness floor made the book do work: %d records added", book.Size()-before)
	}
	resp = respond(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: Coverage{MinFreshness: &Freshness{Time: "yesterday"}}}), "")
	mustRefusal(t, resp, ReasonRequestMalformed)
}

func TestRequesterRecordingRefusesUnknownRequests(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	if _, err := book.RecordAbsence(ctx, records[0].RecordID, time.Time{}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an absence was recorded for a record that is not a request: %v", err)
	}
	if _, err := book.RecordAbsence(ctx, digestBytes([]byte("made up")), time.Time{}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an absence was recorded for a request that was never made: %v", err)
	}
}

func TestRecordResponseEnforcesThePinnedResponderKey(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	requester := newFixture(t, "requester", 30).open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	resp := respond(t, responder, sent.Bytes, "requester")
	if _, err := requester.RecordResponse(ctx, sent.RecordID, resp, ResponderKeys{Signer: KeyID(requester.signer), Checkpoint: checkpointKeyID(1)}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a response under a key other than the pinned one was recorded")
	}
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, resp, responderKeys(responder, 1))
	if err != nil {
		t.Fatal(err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil || statement.KeyID != KeyID(responder.signer) || statement.Outcome != OutcomeArtifact {
		t.Fatalf("pinned response: %+v %v", statement, err)
	}
}

func TestCheckpointsArtifactProofsAreVerified(t *testing.T) {
	ctx := context.Background()
	book, _ := linkedBook(t)
	for i := range 3 {
		mustAppend(t, book, observation("s", fmt.Sprint(i)))
		if _, err := book.Checkpoint(ctx); err != nil {
			t.Fatal(err)
		}
	}
	artifact := mustArtifact(t, respond(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}), ""))
	var cps checkpointsArtifact
	if err := json.Unmarshal(artifact.Artifact, &cps); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCheckpoints(cps.Checkpoints, cps.Consistency, checkpointKeyID(1)); err != nil {
		t.Fatalf("an honest checkpoint history does not verify: %v", err)
	}
	cps.Consistency[0], cps.Consistency[1] = cps.Consistency[1], cps.Consistency[0]
	if _, err := VerifyCheckpoints(cps.Checkpoints, cps.Consistency, checkpointKeyID(1)); !errors.Is(err, ErrInvalid) {
		t.Fatal("swapped consistency proofs verified")
	}
}

func TestVerifyKeyRangeRejectsInvertedBounds(t *testing.T) {
	index := NewSubjectIndex()
	proof, err := index.ProveRange("a", "a")
	if err != nil {
		t.Fatal(err)
	}
	proof.Lo, proof.Hi = "z", "a"
	if _, err := VerifyKeyRange(index.Root(), "z", "a", proof); !errors.Is(err, ErrInvalid) {
		t.Fatal("an inverted range verified")
	}
}

func TestProveKeyRangeIsBoundToACommittedRoot(t *testing.T) {
	ctx := context.Background()
	book := populated(t, newFixture(t, "book-a", 1))
	answer, err := book.ProveKeyRange(ctx, "subject_ref", "subject-1", "subject-1")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := VerifyKeyRange(answer.Root, "subject-1", "subject-1", answer.Proof)
	if err != nil || len(ids) != 3 {
		t.Fatalf("proved %d records (%v)", len(ids), err)
	}
	rootRecord, err := book.Get(ctx, answer.RootRecordID)
	if err != nil {
		t.Fatal(err)
	}
	var committed IndexRoot
	if err := json.Unmarshal(rootRecord.Header.Statement, &committed); err != nil || committed != answer.Root || !answer.Anchor.Covers(rootRecord.Seq) {
		t.Fatalf("proof root is not the committed, checkpoint-covered root: %+v", committed)
	}
	// Records appended later are proved against a fresh committed root.
	mustAppend(t, book, observation("subject-1", "late"))
	later, err := book.ProveKeyRange(ctx, "subject_ref", "subject-1", "subject-1")
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := VerifyKeyRange(later.Root, "subject-1", "subject-1", later.Proof); err != nil || len(ids) != 4 {
		t.Fatalf("after a new record: %d (%v)", len(ids), err)
	}
	absent, err := book.ProveKeyRange(ctx, "subject_ref", "nobody", "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := VerifyKeyRange(absent.Root, "nobody", "nobody", absent.Proof); err != nil || len(ids) != 0 {
		t.Fatalf("non-membership: %v %v", ids, err)
	}
}
