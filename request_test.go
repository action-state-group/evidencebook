// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func requestBytes(t *testing.T, req EvidenceRequest) []byte {
	t.Helper()
	data, err := canonicalJSON(req)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fresh() Coverage { return Coverage{MinFreshness: &Freshness{Size: 1}} }

func respond(t *testing.T, book *Book, req []byte, requester string) Response {
	t.Helper()
	resp, answered, err := book.Respond(context.Background(), req, RespondOptions{RequesterID: requester, Policy: DefaultSharePolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if answered.Header.RecordType != RecordTypeRequestAnswered {
		t.Fatal("every inbound request must be sealed as a record")
	}
	return resp
}

func mustArtifact(t *testing.T, resp Response) ArtifactResponse {
	t.Helper()
	if resp.Artifact == nil || resp.Refusal != nil {
		t.Fatalf("want an artifact, got %+v", resp.Refusal)
	}
	if err := resp.Artifact.Verify(); err != nil {
		t.Fatal(err)
	}
	return *resp.Artifact
}

func mustRefusal(t *testing.T, resp Response, reason string) {
	t.Helper()
	if resp.Refusal == nil || resp.Artifact != nil {
		t.Fatalf("want refusal %s, got an artifact", reason)
	}
	if resp.Refusal.Reason != reason {
		t.Fatalf("refusal reason %s, want %s", resp.Refusal.Reason, reason)
	}
	if err := resp.Refusal.Verify(); err != nil {
		t.Fatalf("refusal does not verify: %v", err)
	}
}

func bundleMatched(t *testing.T, a ArtifactResponse) []string {
	t.Helper()
	var wire bundleWire
	if err := json.Unmarshal(a.Artifact, &wire); err != nil {
		t.Fatal(err)
	}
	var subject struct{ Matched []string }
	if err := json.Unmarshal(wire.Extensions[ExtensionSubject], &subject); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBundle(a.Artifact); err != nil {
		t.Fatal(err)
	}
	return subject.Matched
}

func TestRespondResolvesEverySubjectForm(t *testing.T) {
	book, records := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "peer-1", Links: []Link{{Type: Cites, Target: digestBytes([]byte("peer half"))}}})
	// full_history's count grows as answered requests are recorded, so it is
	// checked as a lower bound; every other subject is exact.
	cases := []struct {
		subject Subject
		want    int
	}{
		{Subject{Kind: SubjectRecord, Digest: records[1].RecordID}, 1},
		{Subject{Kind: SubjectRange, First: 2, Last: 3}, 2},
		{Subject{Kind: SubjectCorrelation, Value: "order-17"}, 3},
		{Subject{Kind: SubjectExchange, Digest: digestBytes([]byte("peer half"))}, 1},
		{Subject{Kind: SubjectFullHistory}, 5},
	}
	for _, c := range cases {
		t.Run(string(c.subject.Kind), func(t *testing.T) {
			matched := bundleMatched(t, mustArtifact(t, respond(t, book, requestBytes(t, EvidenceRequest{Subject: c.subject, Coverage: fresh()}), "peer-1")))
			if exact := c.subject.Kind != SubjectFullHistory; (exact && len(matched) != c.want) || len(matched) < c.want {
				t.Fatalf("matched %d records, want %d", len(matched), c.want)
			}
		})
	}
	t.Run("checkpoints", func(t *testing.T) {
		artifact := mustArtifact(t, respond(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}), ""))
		var cps checkpointsArtifact
		if err := json.Unmarshal(artifact.Artifact, &cps); err != nil || len(cps.Checkpoints) == 0 {
			t.Fatalf("checkpoints artifact is empty: %v", err)
		}
		if len(cps.Consistency) != len(cps.Checkpoints)-1 {
			t.Fatalf("want a consistency proof between each pair of checkpoints: %d for %d", len(cps.Consistency), len(cps.Checkpoints))
		}
	})
}

func TestRespondRefusalReasons(t *testing.T) {
	book, records := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "peer-1"})
	anchor, err := book.Checkpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pin, err := ParsePin(anchor.ID)
	if err != nil {
		t.Fatal(err)
	}
	record := Subject{Kind: SubjectRecord, Digest: records[0].RecordID}
	// A real pin: each member alone would be answered, so only the
	// exactly-one rule can refuse this.
	both := Coverage{ExpectedPin: &pin, MinFreshness: &Freshness{Size: 1}}
	cases := []struct {
		name, requester, reason string
		body                    []byte
	}{
		{"malformed JSON", "peer-1", ReasonRequestMalformed, []byte(`{"subject":`)},
		{"unknown field", "peer-1", ReasonRequestMalformed, []byte(`{"subject":{"kind":"full_history"},"coverage":{"min_freshness":{"size":1}},"extra":1}`)},
		{"bad subject", "peer-1", ReasonRequestMalformed, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: "nope"}, Coverage: fresh()})},
		{"both coverage members", "peer-1", ReasonCoverageUnsatisfiable, requestBytes(t, EvidenceRequest{Subject: record, Coverage: both})},
		{"no coverage member", "peer-1", ReasonCoverageUnsatisfiable, requestBytes(t, EvidenceRequest{Subject: record})},
		{"unknown pin", "peer-1", ReasonCoverageUnsatisfiable, requestBytes(t, EvidenceRequest{Subject: record, Coverage: Coverage{ExpectedPin: &Pin{Root: records[0].RecordID, MMRSize: 99}}})},
		{"freshness beyond the log", "peer-1", ReasonCoverageUnsatisfiable, requestBytes(t, EvidenceRequest{Subject: record, Coverage: Coverage{MinFreshness: &Freshness{Size: 1000}}})},
		{"derivation", "peer-1", ReasonDerivationUnsupported, requestBytes(t, EvidenceRequest{Subject: record, Coverage: fresh(), Derivation: "count/1"})},
		{"stranger under default policy", "", ReasonNotAuthorized, requestBytes(t, EvidenceRequest{Subject: record, Coverage: fresh()})},
		{"pin alone is answerable", "peer-1", "", requestBytes(t, EvidenceRequest{Subject: record, Coverage: Coverage{ExpectedPin: &pin}})},
		{"no such record", "peer-1", ReasonNoSuchSubject, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: digestBytes([]byte("never recorded"))}, Coverage: fresh()})},
		{"range past the anchor", "peer-1", ReasonNoSuchSubject, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRange, First: 2, Last: 500}, Coverage: fresh()})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := respond(t, book, c.body, c.requester)
			if c.reason == "" {
				mustArtifact(t, resp)
				return
			}
			mustRefusal(t, resp, c.reason)
			if resp.Refusal.RequestDigest != digestBytes(c.body) {
				t.Fatal("a refusal must name the digest of the request bytes as received")
			}
		})
	}
}

func TestRefusalSignatureCoversReasonAndTime(t *testing.T) {
	book := newFixture(t, "book-a", 1).open(t)
	resp := respond(t, book, []byte("{"), "")
	mustRefusal(t, resp, ReasonRequestMalformed)
	for name, mutate := range map[string]func(r *Refusal){
		"reason":  func(r *Refusal) { r.Reason = ReasonPolicyDeclined },
		"time":    func(r *Refusal) { r.IssuedAt = "2020-01-01T00:00:00Z" },
		"request": func(r *Refusal) { r.RequestDigest = digestBytes([]byte("other")) },
		"key":     func(r *Refusal) { r.KeyID = hex.EncodeToString(seededKey(9)[32:]) },
	} {
		forged := *resp.Refusal
		mutate(&forged)
		if forged.Verify() == nil {
			t.Fatalf("a refusal with a changed %s still verifies", name)
		}
	}
	body, err := resp.Refusal.SigningBody()
	if err != nil || !bytes.Equal(body, []byte(`{"issued_at":"`+resp.Refusal.IssuedAt+`","reason":"request_malformed","request_digest":"`+resp.Refusal.RequestDigest+`"}`)) {
		t.Fatalf("refusal signing body is not the sorted compact JSON the other implementations sign: %s", body)
	}
}

func TestSharePolicyGateByRelationship(t *testing.T) {
	book, records := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "known-peer"})
	req := requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()})
	matrix := map[string]map[string]bool{
		"off":            {"": false, "someone": false, "known-peer": false},
		"counterparties": {"": false, "someone": false, "known-peer": true},
		"prospective":    {"": false, "someone": true, "known-peer": true},
		"peers":          {"": true, "someone": true, "known-peer": true},
	}
	for tier, requesters := range matrix {
		for requester, answered := range requesters {
			policy := DefaultSharePolicy()
			policy.HistorySegments = tier
			resp, _, err := book.Respond(context.Background(), req, RespondOptions{RequesterID: requester, Policy: policy})
			if err != nil {
				t.Fatal(err)
			}
			if (resp.Artifact != nil) != answered {
				t.Fatalf("tier %s requester %q: answered=%v, want %v", tier, requester, resp.Artifact != nil, answered)
			}
		}
	}
	// Asking never makes a requester a counterparty.
	for range 2 {
		policy := DefaultSharePolicy()
		policy.HistorySegments = "counterparties"
		resp, _, err := book.Respond(context.Background(), req, RespondOptions{RequesterID: "someone", Policy: policy})
		if err != nil {
			t.Fatal(err)
		}
		mustRefusal(t, resp, ReasonNotAuthorized)
	}
	// checkpoints is always answerable.
	mustArtifact(t, respond(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}), ""))
	if _, _, err := book.Respond(context.Background(), req, RespondOptions{Policy: SharePolicy{HistorySegments: "everyone"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("an out-of-vocabulary policy must be rejected, not coerced")
	}
}

// TestCallerInvariance: nonce, route and the requester's identity never change
// the artifact served for one (subject, coverage) pair.
func TestCallerInvariance(t *testing.T) {
	ctx := context.Background()
	book, _ := linkedBook(t)
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := ParsePin(anchor.ID)
	if err != nil {
		t.Fatal(err)
	}
	subject := Subject{Kind: SubjectCorrelation, Value: "order-17"}
	peers := DefaultSharePolicy()
	peers.HistorySegments = "peers"
	var artifacts [][]byte
	for _, variant := range []struct{ nonce, route, requester string }{{"n1", "https://a.example", "alice"}, {"n2", "peer:xyz", "bob"}, {"", "", ""}} {
		req := requestBytes(t, EvidenceRequest{Subject: subject, Coverage: Coverage{ExpectedPin: &pin}, Nonce: variant.nonce, Route: variant.route})
		resp, _, err := book.Respond(ctx, req, RespondOptions{RequesterID: variant.requester, Policy: peers})
		if err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, mustArtifact(t, resp).Artifact)
		mustAppend(t, book, observation("order-17", "arrives after the pin"))
	}
	for i := 1; i < len(artifacts); i++ {
		if !bytes.Equal(artifacts[0], artifacts[i]) {
			t.Fatalf("artifact %d differs from artifact 0 for the same (subject, coverage)", i)
		}
	}
}

func TestRequesterRecordsExactlyOneOutcome(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	f := newFixture(t, "requester", 30)
	requester := f.open(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})

	deadline := f.clock.t.Add(time.Hour)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[2].RecordID}, Coverage: fresh(), Deadline: deadline.Format(time.RFC3339Nano)}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	// Pending is never absence: the window has not closed.
	if _, err := requester.RecordAbsence(ctx, sent.RecordID, deadline, "https://book-a.example"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("absence recorded while pending: %v", err)
	}
	resp := respond(t, responder, sent.Bytes, "requester")
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, resp, responderKeys(responder, 1))
	if err != nil {
		t.Fatal(err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil || statement.Outcome != OutcomeArtifact {
		t.Fatalf("a verified artifact must be recorded as a grant: %+v %v", statement, err)
	}
	f.clock.t = deadline.Add(time.Minute)
	if _, err := requester.RecordAbsence(ctx, sent.RecordID, deadline, "https://book-a.example"); !errors.Is(err, ErrInvalid) {
		t.Fatal("an absence was recorded after a response: outcomes must never convert")
	}

	// A second request that gets no answer is recorded as a signed absence.
	quiet, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.t = f.clock.t.Add(time.Hour)
	absence, err := requester.RecordAbsence(ctx, quiet.RecordID, f.clock.t.Add(-time.Minute), "https://book-a.example")
	if err != nil {
		t.Fatal(err)
	}
	if absence.Header.RecordType != RecordTypeAbsence || absence.Header.EpistemicType != ObservedEvent {
		t.Fatalf("absence record shape: %+v", absence.Header)
	}
	stored, err := requester.storedLocked(ctx, absence.RecordID)
	if err != nil || len(stored.Envelope) == 0 {
		t.Fatal("a recorded absence must be a signed book statement")
	}
}

func TestRecordResponseMarksUnverifiableArtifactAsFailed(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	requester := newFixture(t, "requester", 30).open(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	resp := respond(t, responder, sent.Bytes, "requester")
	// The responder signs a bundle that the neutral verifier rejects, so the
	// failure is found in the bundle itself, not in the envelope.
	a := resp.Artifact
	a.Artifact = bytes.Replace(a.Artifact, []byte(`"bundle_version":"2"`), []byte(`"bundle_version":"3"`), 1)
	a.ArtifactDigest = digestBytes(a.Artifact)
	body, err := a.SigningBody()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := responder.signer.Sign(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	a.Sig = hex.EncodeToString(sig)
	if err := a.Verify(); err != nil {
		t.Fatalf("the re-signed envelope must verify so only the bundle can fail: %v", err)
	}
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, resp, responderKeys(responder, 1))
	if err != nil {
		t.Fatal(err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil || statement.Outcome != OutcomeArtifactFailed {
		t.Fatalf("a tampered artifact must be recorded as failed, got %+v", statement)
	}
}

// TestRefusalSignedByCapsuleEmitVerifies: a refusal signed by the Python
// capsule_emit responder verifies with the Go Refusal.Verify, so both
// implementations sign the same body.
func TestRefusalSignedByCapsuleEmitVerifies(t *testing.T) {
	refusal := readJSON[Refusal](t, "testdata/refusal-interop/refusal.json")
	if err := refusal.Verify(); err != nil {
		t.Fatalf("capsule_emit refusal does not verify in Go: %v", err)
	}
	refusal.Reason = ReasonPolicyDeclined
	if refusal.Verify() == nil {
		t.Fatal("a capsule_emit refusal with a changed reason still verifies")
	}
}

// A responder-signed bundle whose claims are not all "pass" (here graph
// closure is "withheld": the bundle declares a record missing) is recorded as
// artifact_failed_verification, never as an artifact.
func TestABundleWithAWithheldClaimIsAFailedArtifact(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	requester := newFixture(t, "requester", 30).open(t)
	keys := responderKeys(responder, 1)
	ask := func() SentRequest {
		sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}, "book-a")
		if err != nil {
			t.Fatal(err)
		}
		return sent
	}
	outcome := func(r Record) ResponseStatement {
		var statement ResponseStatement
		if err := json.Unmarshal(r.Header.Statement, &statement); err != nil {
			t.Fatal(err)
		}
		return statement
	}

	// The genuine response is a fully verified artifact.
	sent := ask()
	genuine := respond(t, responder, sent.Bytes, "requester")
	verified, err := VerifyBundle(genuine.Artifact.Artifact)
	if err != nil || verified.FullyVerified() != nil {
		t.Fatalf("the genuine bundle must fully verify: %v %v", err, verified.FullyVerified())
	}
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, genuine, keys)
	if err != nil || outcome(recorded).Outcome != OutcomeArtifact {
		t.Fatalf("genuine response: %+v %v", outcome(recorded), err)
	}

	// The same answer with a record declared missing, re-signed by the
	// responder: VerifyBundle accepts it (nothing is false), but graph
	// closure is only "withheld".
	sent = ask()
	doctored := *respond(t, responder, sent.Bytes, "requester").Artifact
	var bundle map[string]any
	if err := json.Unmarshal(doctored.Artifact, &bundle); err != nil {
		t.Fatal(err)
	}
	completeness := bundle["completeness"].(map[string]any)
	completeness["missing"], completeness["records_mode"] = []any{strings.Repeat("ab", 32)}, "declared_incomplete"
	if doctored.Artifact, err = json.Marshal(bundle); err != nil {
		t.Fatal(err)
	}
	withheld, err := VerifyBundle(doctored.Artifact)
	if err != nil || withheld.Claims.GraphClosure != "withheld" || !withheld.AnchorAuthenticated {
		t.Fatalf("the doctored bundle must verify with a withheld graph closure: %+v %v", withheld.Claims, err)
	}
	doctored.ArtifactDigest = digestBytes(doctored.Artifact)
	body, err := doctored.SigningBody()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := responder.signer.Sign(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	doctored.Sig = hex.EncodeToString(sig)
	if err := doctored.Verify(); err != nil {
		t.Fatalf("the re-signed response must carry a valid signature: %v", err)
	}
	recorded, err = requester.RecordResponse(ctx, sent.RecordID, Response{Artifact: &doctored}, keys)
	if err != nil {
		t.Fatal(err)
	}
	if got := outcome(recorded); got.Outcome != OutcomeArtifactFailed || !strings.Contains(got.Failure, "graph closure") {
		t.Fatalf("a withheld-claim bundle was recorded as %+v", got)
	}
}
