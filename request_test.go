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

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

func sizeFloor(n uint64) *Freshness { return &Freshness{Size: &n} }

func fresh() Coverage { return Coverage{MinFreshness: sizeFloor(1)} }

// requestIn encodes req (with the profile member set) in mediaType.
func requestIn(t *testing.T, req EvidenceRequest, mediaType string) Message {
	t.Helper()
	if req.Profile == "" {
		req.Profile = ProfileID
	}
	octets, err := EncodeRequest(req, mediaType)
	if err != nil {
		t.Fatal(err)
	}
	return Message{Octets: octets, MediaType: mediaType}
}

func jsonRequest(t *testing.T, req EvidenceRequest) Message {
	t.Helper()
	return requestIn(t, req, MediaTypeJSON)
}

// respondAs answers req as if the transport had authenticated the caller as
// requester ("" for an unauthenticated caller) under the peers tier, so a
// record, exchange or correlation subject is answered to any authenticated requester and
// a test of anything but the requester gate needs no counterparty records.
// range and full_history stay refused under every tier.
func respondAs(t *testing.T, book *Book, req Message, requester string) Reply {
	t.Helper()
	reply, _, err := book.Respond(context.Background(), req, RespondOptions{AuthenticatedRequester: requester, Policy: policyWith("peers")})
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func mustEnvelope(t *testing.T, reply Reply) Envelope {
	t.Helper()
	if reply.Envelope == nil || reply.Refusal != nil {
		t.Fatalf("want an envelope, got refusal %+v", reply.Refusal)
	}
	if err := reply.Envelope.Verify(); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseReply(reply.Message)
	if err != nil || parsed.Envelope == nil || !bytes.Equal(parsed.Envelope.Artifact, reply.Envelope.Artifact) {
		t.Fatalf("the reply octets do not carry the envelope: %v", err)
	}
	return *reply.Envelope
}

func mustRefusal(t *testing.T, reply Reply, reason string) Refusal {
	t.Helper()
	if reply.Refusal == nil || reply.Envelope != nil {
		t.Fatalf("want refusal %s, got an envelope", reason)
	}
	if reply.Refusal.Reason != reason {
		t.Fatalf("refusal reason %s, want %s", reply.Refusal.Reason, reason)
	}
	if err := reply.Refusal.Verify(); err != nil || !reply.Refusal.Conforming() {
		t.Fatalf("refusal does not verify or does not conform: %v", err)
	}
	parsed, err := ParseReply(reply.Message)
	if err != nil || parsed.Refusal == nil || parsed.Refusal.Sig != reply.Refusal.Sig {
		t.Fatalf("the reply octets do not carry the refusal: %v", err)
	}
	return *reply.Refusal
}

// artifactRecords reads a record-set artifact's entries.
func artifactRecords(t *testing.T, e Envelope) []map[string]any {
	t.Helper()
	var artifact struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(e.Artifact, &artifact); err != nil {
		t.Fatal(err)
	}
	return artifact.Records
}

// anchorPin checkpoints the book now and returns the anchor's digest, the
// value a requester pins.
func anchorPin(t *testing.T, book *Book) string {
	t.Helper()
	cp, err := book.Checkpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return anchorDigest(cp)
}

func TestRespondResolvesEverySubjectForm(t *testing.T) {
	book, records := linkedBook(t)
	half := digestBytes([]byte("peer half"))
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "peer-1", Links: []Link{{Type: Cites, Target: half}}})
	pin := anchorPin(t, book)
	cases := []struct {
		subject Subject
		want    int
	}{
		{Subject{Kind: SubjectExchange, Digest: half}, 1},
		{Subject{Kind: SubjectExchange, Digest: records[0].RecordID}, 2},
		{Subject{Kind: SubjectCorrelation, Correlation: "order-17"}, 3},
	}
	for _, c := range cases {
		t.Run(string(c.subject.Kind), func(t *testing.T) {
			e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: c.subject, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"))
			got := artifactRecords(t, e)
			if len(got) != c.want {
				t.Fatalf("served %d records, want %d", len(got), c.want)
			}
			for _, entry := range got {
				body, err := hex.DecodeString(entry["body"].(string))
				if err != nil || digestBytes(body) != entry["digest"] {
					t.Fatal("a served record's body does not hash to its digest")
				}
			}
		})
	}
	// No requester is party to every record these reach: they are refused
	// under every tier, before they are resolved.
	for _, subject := range []Subject{{Kind: SubjectRange, Range: [2]uint64{1, 2}}, {Kind: SubjectFullHistory}} {
		t.Run(string(subject.Kind), func(t *testing.T) {
			mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: subject, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"), ReasonNotAuthorized)
		})
	}
	t.Run("record", func(t *testing.T) {
		e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[1].RecordID}, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"))
		if e.ArtifactDigest != records[1].RecordID {
			t.Fatal("a record's artifact is the record's own octets: its digest is the record id")
		}
		kinds := []string{}
		for _, entry := range e.Verification {
			kinds = append(kinds, entry.Kind)
		}
		if strings.Join(kinds, ",") != KindCLLCheckpoint+","+KindCLLInclusionProof {
			t.Fatalf("record material kinds %v", kinds)
		}
		if e.Verification[0].Digest != pin || e.Anchor.Digest != pin {
			t.Fatal("the anchor is the checkpoint the material carries")
		}
	})
	for _, derivation := range []string{"", DerivationHistoryCard} {
		t.Run("checkpoints "+derivation, func(t *testing.T) {
			e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: derivation, Coverage: Coverage{ExpectedPin: pin}}), ""))
			if len(e.Verification) != 0 {
				t.Fatal("for checkpoints and history_card/1 the artifact is the material")
			}
			if _, err := verifyCheckpointChain(e, checkpointKeyID(1)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRespondRefusalReasons(t *testing.T) {
	book, records := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "peer-1"})
	pin := anchorPin(t, book)
	record := Subject{Kind: SubjectRecord, Digest: records[0].RecordID}
	raw := func(s string) Message { return Message{Octets: []byte(s), MediaType: MediaTypeJSON} }
	subjectJSON := `"subject":{"record":"` + records[0].RecordID + `"}`
	cases := []struct {
		name, requester, reason string
		req                     Message
	}{
		{"malformed JSON", "peer-1", ReasonRequestMalformed, raw(`{"subject":`)},
		{"trailing bytes", "peer-1", ReasonRequestMalformed, raw(`{` + subjectJSON + `,"coverage":{"expected_pin":"` + pin + `"}} {}`)},
		{"duplicate member", "peer-1", ReasonRequestMalformed, raw(`{` + subjectJSON + `,` + subjectJSON + `,"coverage":{"expected_pin":"` + pin + `"}}`)},
		{"case-variant member is not the member", "peer-1", ReasonRequestMalformed, raw(`{"SUBJECT":{"record":"` + records[0].RecordID + `"},"coverage":{"expected_pin":"` + pin + `"}}`)},
		{"old subject shape", "peer-1", ReasonRequestMalformed, raw(`{"subject":{"kind":"record","digest":"` + records[0].RecordID + `"},"coverage":{"expected_pin":"` + pin + `"}}`)},
		{"deadline not a time", "peer-1", ReasonRequestMalformed, raw(`{` + subjectJSON + `,"coverage":{"expected_pin":"` + pin + `"},"deadline":"whenever"}`)},
		{"both coverage members", "peer-1", ReasonCoverageUnsatisfiable, raw(`{` + subjectJSON + `,"coverage":{"expected_pin":"` + pin + `","min_freshness":1}}`)},
		{"no coverage member", "peer-1", ReasonCoverageUnsatisfiable, raw(`{` + subjectJSON + `,"coverage":{}}`)},
		{"coverage missing", "peer-1", ReasonCoverageUnsatisfiable, raw(`{` + subjectJSON + `}`)},
		{"unknown profile", "peer-1", ReasonRequestMalformed, raw(`{` + subjectJSON + `,"coverage":{"expected_pin":"` + pin + `"},"profile":"evidence-interop/9"}`)},
		{"unknown pin", "peer-1", ReasonCoverageUnsatisfiable, jsonRequest(t, EvidenceRequest{Subject: record, Coverage: Coverage{ExpectedPin: records[0].RecordID}})},
		{"freshness beyond the log", "peer-1", ReasonCoverageUnsatisfiable, jsonRequest(t, EvidenceRequest{Subject: record, Coverage: Coverage{MinFreshness: sizeFloor(1000)}})},
		{"derivation unsupported", "peer-1", ReasonDerivationUnsupported, jsonRequest(t, EvidenceRequest{Subject: record, Coverage: fresh(), Derivation: "count/1"})},
		{"derivation digest unknown", "peer-1", ReasonDerivationUnsupported, jsonRequest(t, EvidenceRequest{Subject: record, Coverage: fresh(), Derivation: digestBytes([]byte("def"))})},
		{"history card not for a record", "peer-1", ReasonNoSuchSubject, jsonRequest(t, EvidenceRequest{Subject: record, Coverage: fresh(), Derivation: DerivationHistoryCard})},
		{"stranger under default policy", "", ReasonNotAuthorized, jsonRequest(t, EvidenceRequest{Subject: record, Coverage: fresh()})},
		{"deadline passed", "peer-1", ReasonDeadlineUnmet, jsonRequest(t, EvidenceRequest{Subject: record, Coverage: fresh(), Deadline: "2020-01-01T00:00:00Z"})},
		{"pin alone is answerable", "peer-1", "", jsonRequest(t, EvidenceRequest{Subject: record, Coverage: Coverage{ExpectedPin: pin}})},
		{"unknown member is ignored", "peer-1", "", raw(`{` + subjectJSON + `,"coverage":{"expected_pin":"` + pin + `"},"page":{"size":10}}`)},
		{"no such record", "peer-1", ReasonNoSuchSubject, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: digestBytes([]byte("never recorded"))}, Coverage: fresh()})},
		{"range is refused before it is resolved", "peer-1", ReasonNotAuthorized, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRange, Range: [2]uint64{1, 500}}, Coverage: fresh()})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply := respondAs(t, book, c.req, c.requester)
			if c.reason == "" {
				mustEnvelope(t, reply)
				return
			}
			refusal := mustRefusal(t, reply, c.reason)
			if refusal.RequestDigest != digestBytes(c.req.Octets) {
				t.Fatal("a refusal must name the digest of the request octets as received")
			}
		})
	}
	t.Run("unknown profile lists the supported one", func(t *testing.T) {
		refusal := mustRefusal(t, respondAs(t, book, raw(`{`+subjectJSON+`,"coverage":{"expected_pin":"`+pin+`"},"profile":"evidence-interop/9"}`), "peer-1"), ReasonRequestMalformed)
		if !strings.Contains(refusal.Detail, ProfileID) {
			t.Fatalf("detail %q does not list %s", refusal.Detail, ProfileID)
		}
	})
}

func TestRefusalSignatureCoversReasonAndTime(t *testing.T) {
	book := newFixture(t, "book-a", 1).open(t)
	original := mustRefusal(t, respondAs(t, book, Message{Octets: []byte("{"), MediaType: MediaTypeJSON}, ""), ReasonRequestMalformed)
	for name, mutate := range map[string]func(r *Refusal){
		"reason":  func(r *Refusal) { r.Reason = ReasonPolicyDeclined },
		"time":    func(r *Refusal) { r.IssuedAt = "2020-01-01T00:00:00Z" },
		"request": func(r *Refusal) { r.RequestDigest = digestBytes([]byte("other")) },
		"key":     func(r *Refusal) { r.KeyID = hex.EncodeToString(seededKey(9)[32:]) },
	} {
		forged := original
		mutate(&forged)
		if forged.Verify() == nil {
			t.Fatalf("a refusal with a changed %s still verifies", name)
		}
	}
	body, err := original.SigningBody()
	if err != nil || !bytes.Equal(body, []byte(`{"issued_at":"`+original.IssuedAt+`","reason":"request_malformed","request_digest":"`+original.RequestDigest+`"}`)) {
		t.Fatalf("refusal signing body is not exactly {issued_at, reason, request_digest} in JCS: %s", body)
	}
	if _, ok := evidencerequest.ParseProfileTime(original.IssuedAt); !ok {
		t.Fatalf("issued_at %q is not the §5.2 time form", original.IssuedAt)
	}
}

// The share policy gates record and exchange subjects by the records they
// name: under counterparties (the default) and prospective, only the party
// each record names; under peers, any authenticated peer.
func TestSharePolicyGateByRelationship(t *testing.T) {
	book, records := linkedBook(t)
	own := mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "known-peer"})
	if DefaultSharePolicy().HistorySegments != "counterparties" {
		t.Fatalf("the default tier is %q, want counterparties", DefaultSharePolicy().HistorySegments)
	}
	theirs := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: own.RecordID}, Coverage: fresh()})
	other := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()})
	matrix := map[string]map[string][2]bool{ // requester: {own record answered, another record answered}
		"off":            {"": {false, false}, "someone": {false, false}, "known-peer": {false, false}},
		"counterparties": {"": {false, false}, "someone": {false, false}, "known-peer": {true, false}},
		"prospective":    {"": {false, false}, "someone": {false, false}, "known-peer": {true, false}},
		"peers":          {"": {false, false}, "someone": {true, true}, "known-peer": {true, true}},
	}
	for tier, requesters := range matrix {
		for requester, answered := range requesters {
			for i, req := range []Message{theirs, other} {
				reply, _, err := book.Respond(context.Background(), req, RespondOptions{AuthenticatedRequester: requester, Policy: policyWith(tier)})
				if err != nil {
					t.Fatal(err)
				}
				if (reply.Envelope != nil) != answered[i] {
					t.Fatalf("tier %s requester %q request %d: answered=%v, want %v", tier, requester, i, reply.Envelope != nil, answered[i])
				}
			}
		}
	}
	req := other
	// Asking never makes a requester a counterparty.
	for range 2 {
		reply, _, err := book.Respond(context.Background(), req, RespondOptions{AuthenticatedRequester: "someone", Policy: policyWith("counterparties")})
		if err != nil {
			t.Fatal(err)
		}
		mustRefusal(t, reply, ReasonNotAuthorized)
	}
	// checkpoints is always answerable.
	mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}), ""))
	if _, _, err := book.Respond(context.Background(), req, RespondOptions{Policy: SharePolicy{HistorySegments: "everyone"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("an out-of-vocabulary policy must be rejected, not coerced")
	}
}

// TestCallerInvariance (FR-8, ER §5): nonce, route, the requester's
// identity, the profile member and the encoding never change the canonical
// Artifact served for one (subject, anchor, derivation). Only the envelope
// differs.
func TestCallerInvariance(t *testing.T) {
	book, records := linkedBook(t)
	half := digestBytes([]byte("peer half"))
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: half}}})
	pin := anchorPin(t, book)
	peers := policyWith("peers")
	subjects := []EvidenceRequest{
		{Subject: Subject{Kind: SubjectRecord, Digest: records[1].RecordID}},
		{Subject: Subject{Kind: SubjectExchange, Digest: records[0].RecordID}},
		{Subject: Subject{Kind: SubjectCheckpoints}},
		{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard},
		{Subject: Subject{Kind: SubjectExchange, Digest: half}},
	}
	variants := []struct{ nonce, route, requester, profile, mediaType string }{
		{"n1", "https://a.example", "alice", ProfileID, MediaTypeJSON},
		{"n2", "peer:xyz", "bob", ProfileID, MediaTypeCBOR},
		{"", "", "carol", "", MediaTypeJSON},
		{"n4", "", "alice", "", MediaTypeCBOR},
	}
	for _, base := range subjects {
		t.Run(string(base.Subject.Kind)+base.Derivation, func(t *testing.T) {
			var artifacts [][]byte
			var digests []string
			for _, v := range variants {
				req := base
				req.Coverage, req.Nonce, req.Route, req.Profile = Coverage{ExpectedPin: pin}, v.nonce, v.route, v.profile
				octets, err := EncodeRequest(req, v.mediaType)
				if err != nil {
					t.Fatal(err)
				}
				reply, _, err := book.Respond(context.Background(), Message{Octets: octets, MediaType: v.mediaType}, RespondOptions{AuthenticatedRequester: v.requester, Policy: peers})
				if err != nil {
					t.Fatal(err)
				}
				if reply.MediaType != v.mediaType {
					t.Fatalf("a reply is in the request's encoding: got %s for %s", reply.MediaType, v.mediaType)
				}
				e := mustEnvelope(t, reply)
				artifacts, digests = append(artifacts, e.Artifact), append(digests, e.RequestDigest)
				// Growth beyond the pin changes nothing pinned.
				mustAppend(t, book, observation("order-17", "arrives after the pin"))
			}
			for i := 1; i < len(artifacts); i++ {
				if !bytes.Equal(artifacts[0], artifacts[i]) {
					t.Fatalf("artifact %d differs from artifact 0 for the same (subject, anchor, derivation)", i)
				}
				if digests[i] == digests[0] {
					t.Fatal("distinct requests must have distinct request digests")
				}
			}
		})
	}
}

func TestRequesterRecordsExactlyOneOutcome(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	f := newFixture(t, "requester", 30)
	requester := f.open(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})

	deadline := f.clock.t.Add(time.Hour).Truncate(time.Second)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[2].RecordID}, Coverage: fresh(), Deadline: evidencerequest.ProfileTime(deadline), Route: "https://book-a.example"}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	// Pending is never absence: the window has not closed.
	if _, err := requester.RecordAbsence(ctx, sent.RecordID, deadline, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("absence recorded while pending: %v", err)
	}
	reply := respondAs(t, responder, sent.Message(), "requester")
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(responder, 1))
	if err != nil {
		t.Fatal(err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil || statement.Outcome != OutcomeArtifact || len(statement.NotChecked) != 0 {
		t.Fatalf("a verified record artifact must be recorded as a grant with every property checked: %+v %v", statement, err)
	}
	f.clock.t = deadline.Add(time.Minute)
	if _, err := requester.RecordAbsence(ctx, sent.RecordID, deadline, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("an absence was recorded after a response: outcomes must never convert")
	}

	// A second request that gets no answer is recorded as an absence in the
	// profile's shape, with the route the request named.
	quiet, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh(), Route: "peer:book-a"}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.t = f.clock.t.Add(time.Hour)
	if _, err := requester.RecordAbsence(ctx, quiet.RecordID, f.clock.t.Add(-time.Minute), "https://elsewhere.example"); !errors.Is(err, ErrInvalid) {
		t.Fatal("an absence naming a route other than the request's was recorded")
	}
	absence, err := requester.RecordAbsence(ctx, quiet.RecordID, f.clock.t.Add(-time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	if absence.Header.RecordType != RecordTypeAbsence || absence.Header.EpistemicType != ObservedEvent {
		t.Fatalf("absence record shape: %+v", absence.Header)
	}
	var shape AbsenceRecord
	if err := json.Unmarshal(absence.Header.Statement, &shape); err != nil {
		t.Fatal(err)
	}
	if shape.Outcome != "recorded_absence" || shape.RequestDigest != quiet.Digest || shape.Route != "peer:book-a" || shape.RecordedAt < shape.Window.Closed || shape.Window.Opened > shape.Window.Closed {
		t.Fatalf("absence record is not the profile's shape: %+v", shape)
	}
	if _, err := evidencerequest.DecodeJSON(absence.Header.Statement); err != nil || strings.Contains(string(absence.Header.Statement), `"reason"`) {
		t.Fatalf("an absence carries no responder act: %s", absence.Header.Statement)
	}
	stored, err := requester.storedLocked(ctx, absence.RecordID)
	if err != nil || len(stored.Envelope) == 0 {
		t.Fatal("a recorded absence must be a signed book statement")
	}
}

// A responder-signed envelope whose artifact is not the record it names is
// recorded as received and failed, never as a grant.
func TestRecordResponseMarksUnverifiableArtifactAsFailed(t *testing.T) {
	ctx := context.Background()
	responder, records := linkedBook(t)
	requester := newFixture(t, "requester", 30).open(t)
	mustAppend(t, responder, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "requester"})
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	e := mustEnvelope(t, respondAs(t, responder, sent.Message(), "requester"))
	e.Artifact = append(e.Artifact, ' ')
	e.ArtifactDigest = digestBytes(e.Artifact)
	resigned := resign(t, responder, e)
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, resigned.Message, responderKeys(responder, 1))
	if err != nil {
		t.Fatal(err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil || statement.Outcome != OutcomeArtifactFailed || !strings.Contains(statement.Failure, "anchor_inclusion") {
		t.Fatalf("a tampered artifact must be recorded as failed, got %+v", statement)
	}
}

// resign signs e again with the responder's own key and encodes it as JSON.
func resign(t *testing.T, responder *Book, e Envelope) Reply {
	t.Helper()
	body, err := e.SigningBody()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := responder.signer.Sign(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	e.Sig = hex.EncodeToString(sig)
	reply, err := evidencerequest.EncodeReply(MediaTypeJSON, &e, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reply
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

// A holder-disclosed bundle whose claims are not all "pass" (here graph
// closure is "withheld": the bundle declares a record missing) does not
// verify as an answer, though VerifyBundle accepts it.
func TestABundleWithAWithheldClaimIsNotAVerifiedAnswer(t *testing.T) {
	ctx := context.Background()
	holder, records := linkedBook(t)
	keys := responderKeys(holder, 1)
	bundle, err := holder.Bundle(ctx, BundleRequest{Root: records[1].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBundleAnswer(bundle.JSON, keys); err != nil {
		t.Fatalf("the genuine bundle must verify: %v", err)
	}
	var doctored map[string]any
	if err := json.Unmarshal(bundle.JSON, &doctored); err != nil {
		t.Fatal(err)
	}
	completeness := doctored["completeness"].(map[string]any)
	completeness["missing"], completeness["records_mode"] = []any{strings.Repeat("ab", 32)}, "declared_incomplete"
	data, err := json.Marshal(doctored)
	if err != nil {
		t.Fatal(err)
	}
	withheld, err := VerifyBundle(data)
	if err != nil || withheld.Claims.GraphClosure != "withheld" || !withheld.AnchorAuthenticated {
		t.Fatalf("the doctored bundle must verify with a withheld graph closure: %+v %v", withheld.Claims, err)
	}
	if _, err := VerifyBundleAnswer(data, keys); err == nil || !strings.Contains(err.Error(), "graph closure") {
		t.Fatalf("a withheld-claim bundle verified as an answer: %v", err)
	}
	if _, err := VerifyBundleAnswer(bundle.JSON, ResponderKeys{Checkpoint: checkpointKeyID(77)}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a bundle anchored under another checkpoint key verified")
	}
}
