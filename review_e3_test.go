// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests turn the line-review attacks on commit cb41932 into assertions.
// Each attack succeeded against that commit.

// appendMember adds one more top-level member to a bundle's JSON.
func appendMember(t *testing.T, bundle []byte, member string) []byte {
	t.Helper()
	trimmed := bytes.TrimSuffix(bytes.TrimSpace(bundle), []byte("}"))
	return append(trimmed, []byte(","+member+"}")...)
}

// B1, attack 1: a second, case-variant "Disclosures" member carrying a forged
// header. The neutral verifier checks "disclosures"; a case-insensitive
// reader took the forged copy and reported it verified, so a Close read as
// AGREED under the genuine pinned key.
func TestCaseVariantDisclosuresCannotForgeAHeader(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	bundle, err := book.Bundle(ctx, BundleRequest{Root: records[2].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	closeID := strings.Repeat("ab", 32)
	forgedHeader := records[2].Header
	forgedHeader.Links = append(append([]Link{}, forgedHeader.Links...), Link{Type: Acknowledges, Target: closeID})
	forged, err := canonicalJSON(forgedHeader)
	if err != nil {
		t.Fatal(err)
	}
	data := appendMember(t, bundle.JSON, `"Disclosures":{"`+records[2].RecordID+`":{"agent_input":`+string(forged)+`}}`)
	verified, err := VerifyBundle(data)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bundle with a case-variant duplicate member was accepted (err=%v)", err)
	}
	if status, err := StatusOfClose(closeID, "book-a", checkpointKeyID(1), verified); err == nil && status == Agreed {
		t.Fatal("the forged acknowledgement made the Close AGREED")
	}
}

// B1, attack 2: an attacker's own book with the victim's book id, plus a
// case-variant "CHECKPOINT" member copied from a genuine victim bundle. The
// reader reported the victim's checkpoint key, so the attacker's forged
// acknowledgement read as AGREED without any of the victim's keys.
func TestCaseVariantCheckpointCannotBorrowAnotherBooksKey(t *testing.T) {
	ctx := context.Background()
	victim, victimRecords := linkedBook(t)
	genuine, err := victim.Bundle(ctx, BundleRequest{Root: victimRecords[2].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(genuine.JSON, &members); err != nil {
		t.Fatal(err)
	}
	attacker := newFixture(t, "book-a", 77).open(t)
	closeID := strings.Repeat("cd", 32)
	ack := mustAppend(t, attacker, Entry{RecordType: RecordTypeAcknowledgement, EpistemicType: ProducerClaim, Links: []Link{{Type: Acknowledges, Target: closeID}}})
	fake, err := attacker.Bundle(ctx, BundleRequest{Root: ack.RecordID})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CHECKPOINT", "Checkpoint"} {
		data := appendMember(t, fake.JSON, `"`+key+`":`+string(members["checkpoint"]))
		verified, err := VerifyBundle(data)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: a bundle with a case-variant duplicate checkpoint was accepted (err=%v)", key, err)
		}
		if verified.AnchorKeyID == checkpointKeyID(1) {
			t.Fatalf("%s: the attacker's bundle reported the victim's checkpoint key", key)
		}
		if status, err := StatusOfClose(closeID, "book-a", checkpointKeyID(1), verified); err == nil && status == Agreed {
			t.Fatalf("%s: the attacker's acknowledgement read as AGREED under the victim's key", key)
		}
	}
}

// B1: a member present only under a case-variant key is absent, exactly as
// the neutral verifier sees it; it is never read into the result.
func TestCaseVariantOnlyMemberIsAbsent(t *testing.T) {
	book, records := linkedBook(t)
	bundle, err := book.Bundle(context.Background(), BundleRequest{Root: records[0].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Replace(bundle.JSON, []byte(`"checkpoint":`), []byte(`"Checkpoint":`), 1)
	verified, err := VerifyBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	if verified.AnchorAuthenticated || verified.AnchorKeyID != "" || verified.Anchor.Root != "" {
		t.Fatalf("a checkpoint present only as \"Checkpoint\" was read: %+v", verified.Anchor)
	}
}

// B2: VerifyCheckpoints bound only ID/LogID/KeyID. A forked log signed by the
// same checkpoint key, listed with the honest Root/TreeSize and an inflated
// Entries, was accepted as an append-only extension.
func TestCheckpointFieldsAreTakenFromTheSignedStatement(t *testing.T) {
	ctx := context.Background()
	book, _ := linkedBook(t)
	cp1, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, book, observation("s", "x"))
	cp2, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := book.commitments.ProveConsistency(ctx, cp1, cp2)
	if err != nil {
		t.Fatal(err)
	}
	key := checkpointKeyID(1)
	if _, err := VerifyCheckpoints([]Checkpoint{cp1, cp2}, []ConsistencyEvidence{proof}, key); err != nil {
		t.Fatalf("the honest history must verify: %v", err)
	}
	fork, err := OpenCLL(filepath.Join(t.TempDir(), "cll.jsonl"), "book-a-log", seededKey(101))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fork.Release() }()
	for i := range 9 {
		if _, err := fork.Append(ctx, strings.Repeat("0", 63)+string("123456789"[i]), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	forkCP, err := fork.Checkpoint(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lying := forkCP
	lying.Root, lying.TreeSize, lying.Entries = cp2.Root, cp2.TreeSize, 9999
	if _, err := VerifyCheckpoints([]Checkpoint{cp1, lying}, []ConsistencyEvidence{proof}, key); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a forked statement listed with the honest root was accepted (err=%v)", err)
	}
	for name, mutate := range map[string]func(c *Checkpoint){
		"root":     func(c *Checkpoint) { c.Root = forkCP.Root },
		"size":     func(c *Checkpoint) { c.TreeSize++ },
		"entries":  func(c *Checkpoint) { c.Entries = 9999 },
		"id":       func(c *Checkpoint) { c.ID = forkCP.ID },
		"log":      func(c *Checkpoint) { c.LogID = "other-log" },
		"issuedAt": func(c *Checkpoint) { c.IssuedAt = c.IssuedAt.Add(time.Hour) },
	} {
		listed := cp2
		mutate(&listed)
		if _, err := VerifyCheckpoints([]Checkpoint{cp1, listed}, []ConsistencyEvidence{proof}, key); !errors.Is(err, ErrInvalid) {
			t.Fatalf("a listed %s differing from the signed statement was accepted", name)
		}
	}
	if _, err := VerifyCheckpoints([]Checkpoint{cp1, cp2}, []ConsistencyEvidence{proof}, checkpointKeyID(2)); !errors.Is(err, ErrInvalid) {
		t.Fatal("checkpoints under a key other than the pinned one were accepted")
	}
	if _, err := VerifyCheckpoints([]Checkpoint{cp1, forkCP}, []ConsistencyEvidence{proof}, key); !errors.Is(err, ErrInvalid) {
		t.Fatal("a same-key fork presented as the next checkpoint was accepted")
	}
	last, err := VerifyCheckpoints([]Checkpoint{cp1, cp2}, []ConsistencyEvidence{proof}, key)
	if err != nil || last.ID != cp2.ID || last.Entries != cp2.Entries {
		t.Fatalf("the verified newest checkpoint must be the signed one: %+v %v", last, err)
	}
}

// B3: a response with the pinned key id but no valid signature was recorded
// as artifact_failed_verification with key_pinned: true, and the real signed
// response was then refused because an outcome already existed.
func TestUnsignedResponseCannotForecloseARequest(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	f := newFixture(t, "requester", 30)
	requester := f.open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	keys := responderKeys(responder, 1)
	junk := []Response{
		{Artifact: &ArtifactResponse{RequestDigest: sent.Digest, ArtifactKind: "x", Artifact: json.RawMessage(`{}`), KeyID: keys.Signer, Sig: "00"}},
		{Artifact: &ArtifactResponse{RequestDigest: sent.Digest, ArtifactKind: ArtifactEvidenceBundle, Artifact: json.RawMessage(`{}`), ArtifactDigest: digestBytes([]byte(`{}`)), KeyID: keys.Signer, Sig: strings.Repeat("00", 64)}},
		{Refusal: &Refusal{RequestDigest: sent.Digest, Reason: ReasonNotAuthorized, IssuedAt: "2026-09-26T12:00:00Z", KeyID: keys.Signer, Sig: "00"}},
	}
	before := requester.Size()
	for i, response := range junk {
		if _, err := requester.RecordResponse(ctx, sent.RecordID, response, keys); !errors.Is(err, ErrInvalid) {
			t.Fatalf("junk response %d was recorded (err=%v)", i, err)
		}
	}
	if requester.Size() != before {
		t.Fatal("an unauthenticated response left a record behind")
	}
	real := respond(t, responder, sent.Bytes, "requester")
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, real, keys)
	if err != nil {
		t.Fatalf("the genuine response was refused after junk: %v", err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil || statement.Outcome != OutcomeArtifact || statement.KeyID != keys.Signer {
		t.Fatalf("genuine response recorded as %+v (%v)", statement, err)
	}

	// Junk must not block a later recorded absence either.
	quiet, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := requester.RecordResponse(ctx, quiet.RecordID, junk[0], keys); !errors.Is(err, ErrInvalid) {
		t.Fatal("junk recorded for the second request")
	}
	f.clock.t = f.clock.t.Add(time.Hour)
	if _, err := requester.RecordAbsence(ctx, quiet.RecordID, f.clock.t.Add(-time.Minute), ""); err != nil {
		t.Fatalf("junk blocked a recorded absence: %v", err)
	}
}

// S2: the responder keys are required, and a correctly self-signed refusal
// or artifact under any other key is refused, not recorded.
func TestRecordResponseRequiresAndEnforcesPinnedKeys(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	requester := newFixture(t, "requester", 30).open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	impostor := newFixture(t, "book-a", 77).open(t)
	mustAppend(t, impostor, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	selfSigned, _, err := impostor.Respond(ctx, sent.Bytes, RespondOptions{RequesterID: "requester", Policy: DefaultSharePolicy()})
	if err != nil || selfSigned.Refusal == nil || selfSigned.Refusal.Verify() != nil {
		t.Fatalf("the impostor must produce a refusal that verifies under its own key: %+v %v", selfSigned, err)
	}
	keys := responderKeys(responder, 1)
	for name, pinned := range map[string]ResponderKeys{"none": {}, "signer only": {Signer: keys.Signer}, "checkpoint only": {Checkpoint: keys.Checkpoint}} {
		if _, err := requester.RecordResponse(ctx, sent.RecordID, selfSigned, pinned); !errors.Is(err, ErrInvalid) {
			t.Fatalf("pinned keys %s: recorded without both keys", name)
		}
	}
	if _, err := requester.RecordResponse(ctx, sent.RecordID, selfSigned, keys); !errors.Is(err, ErrInvalid) {
		t.Fatal("a refusal self-signed by another key was recorded")
	}
	// A genuine artifact anchored to a checkpoint key other than the pinned
	// one is recorded as failed, never as a grant.
	wrongCheckpoint := ResponderKeys{Signer: keys.Signer, Checkpoint: checkpointKeyID(77)}
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, respond(t, responder, sent.Bytes, "requester"), wrongCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil || statement.Outcome != OutcomeArtifactFailed {
		t.Fatalf("an artifact under an unpinned checkpoint key was recorded as %+v", statement)
	}
}

// S1: an absence whose window ends before, or too soon after, the request
// was recorded is refused.
func TestAbsenceWindowMustFollowTheRequest(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "requester", 30)
	requester := f.open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}, "x")
	if err != nil {
		t.Fatal(err)
	}
	record, err := requester.Get(ctx, sent.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	asked, err := time.Parse(time.RFC3339Nano, record.Header.CommittedAt)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.t = asked.Add(time.Hour)
	for name, end := range map[string]time.Time{
		"epoch":              time.Unix(0, 0),
		"before the ask":     asked.Add(-time.Second),
		"at the ask":         asked,
		"inside the minimum": asked.Add(MinimumAbsenceWindow - time.Second),
	} {
		if _, err := requester.RecordAbsence(ctx, sent.RecordID, end, ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: absence recorded (err=%v)", name, err)
		}
	}
	if _, err := requester.RecordAbsence(ctx, sent.RecordID, asked.Add(MinimumAbsenceWindow), ""); err != nil {
		t.Fatalf("a window of exactly the minimum was refused: %v", err)
	}
}

// Nit: identical requests shared a digest, so an old answer could be replayed
// onto a later identical request. Every request now carries a unique nonce.
func TestIdenticalRequestsGetDistinctDigests(t *testing.T) {
	ctx := context.Background()
	requester := newFixture(t, "requester", 30).open(t)
	req := EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}
	first, err := requester.Request(ctx, req, "x")
	if err != nil {
		t.Fatal(err)
	}
	second, err := requester.Request(ctx, req, "x")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest {
		t.Fatal("two requests share a digest; an answer to one would be accepted for the other")
	}
	var sentReq EvidenceRequest
	if err := json.Unmarshal(first.Bytes, &sentReq); err != nil || sentReq.Nonce == "" {
		t.Fatal("the transmitted request carries no nonce")
	}
	req.Nonce = sentReq.Nonce
	if _, err := requester.Request(ctx, req, "x"); !errors.Is(err, ErrInvalid) {
		t.Fatal("a reused nonce was accepted")
	}
}

// Nit: the artifact signing body carries its own context member.
func TestArtifactSigningBodyNamesItsType(t *testing.T) {
	body, err := ArtifactResponse{RequestDigest: "r", Anchor: "a", ArtifactKind: "k", ArtifactDigest: "d", IssuedAt: "t"}.SigningBody()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"type":"`+ArtifactResponseType+`"`)) {
		t.Fatalf("artifact signing body has no type member: %s", body)
	}
}

// encoding/json matches keys with Unicode simple folding, under which the
// Kelvin sign folds to "k"; the duplicate check must fold the same way.
func TestFoldedKeyCheckMatchesJSONFolding(t *testing.T) {
	for _, pair := range [][2]string{{"checkpoint", "CHECKPOINT"}, {"kind", "Kind"}, {"disclosures", "Disclosures"}} {
		decoded, err := decodeJSON([]byte(`{"` + pair[0] + `":1,"` + pair[1] + `":2}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := rejectFoldedKeys(jsonNode{value: decoded}, "$"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q and %q were not treated as the same key", pair[0], pair[1])
		}
	}
	for _, nested := range []string{`{"a":{"b":1,"B":2}}`, `{"c":[{"d":1,"D":2}]}`, `{"records":[{"model_attestation":{"compute_attestation":{"x":1,"X":2}}}]}`} {
		decoded, err := decodeJSON([]byte(nested))
		if err != nil {
			t.Fatal(err)
		}
		if err := rejectFoldedKeys(jsonNode{value: decoded}, "$"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("a nested case-variant duplicate was not refused: %s", nested)
		}
	}
	decoded, err := decodeJSON([]byte(`{"a":{"b":1},"c":[{"d":1,"e":2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectFoldedKeys(jsonNode{value: decoded}, "$"); err != nil {
		t.Fatalf("distinct keys refused: %v", err)
	}
}

// S2/B3: an artifact validly self-signed by a key other than the pinned one
// is refused and not recorded, so it cannot become an outcome or foreclose
// the genuine answer; a genuine answer with no pinned keys is refused too.
func TestForeignSignedArtifactIsRefusedNotRecorded(t *testing.T) {
	ctx := context.Background()
	responder, _ := linkedBook(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	requester := newFixture(t, "requester", 30).open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	impostor, _ := linkedBook(t)
	impostor.signer, err = NewEd25519Signer(seededKey(77))
	if err != nil {
		t.Fatal(err)
	}
	foreign := respond(t, impostor, sent.Bytes, "")
	if foreign.Artifact == nil || foreign.Artifact.Verify() != nil {
		t.Fatal("the impostor must produce an artifact that verifies under its own key")
	}
	keys := responderKeys(responder, 1)
	before := requester.Size()
	if _, err := requester.RecordResponse(ctx, sent.RecordID, foreign, keys); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an artifact signed by another key was recorded (err=%v)", err)
	}
	genuine := respond(t, responder, sent.Bytes, "")
	if _, err := requester.RecordResponse(ctx, sent.RecordID, genuine, ResponderKeys{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a response was recorded with no pinned keys")
	}
	if requester.Size() != before {
		t.Fatal("a refused response left a record behind")
	}
	if _, err := requester.RecordResponse(ctx, sent.RecordID, genuine, keys); err != nil {
		t.Fatalf("the genuine answer was foreclosed: %v", err)
	}
}
