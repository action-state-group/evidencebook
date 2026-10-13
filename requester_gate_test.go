// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// Tests for the requester gate (NFR-4, NFR-5): protected subjects are
// answered only to an authenticated requester, a declared or disclosed id
// never grants scope, and answered-request records never carry a requester
// id and are never served.

func policyWith(tier string) SharePolicy {
	p := DefaultSharePolicy()
	p.HistorySegments = tier
	return p
}

// respondWith answers req and returns the answered-request statement, which
// is zero when nothing was sealed.
func respondWith(t *testing.T, book *Book, req Message, opts RespondOptions) (Reply, AnsweredStatement) {
	t.Helper()
	reply, answered, err := book.Respond(context.Background(), req, opts)
	if err != nil {
		t.Fatal(err)
	}
	var statement AnsweredStatement
	if answered.RecordID != "" {
		if err := json.Unmarshal(answered.Header.Statement, &statement); err != nil {
			t.Fatal(err)
		}
	}
	return reply, statement
}

// (a) Without an authenticated requester only public questions are
// answered; everything else gets a signed not_authorized refusal, whatever
// id the caller declares and whatever tier the policy is set to. Nothing an
// unauthenticated caller sends grows the log.
func TestUnauthenticatedRequesterGetsOnlyPublicSubjects(t *testing.T) {
	book, _ := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "known-peer"})
	pin := anchorPin(t, book)
	history := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: Coverage{ExpectedPin: pin}})
	cases := map[string]RespondOptions{
		"no id, default policy":        {Policy: DefaultSharePolicy()},
		"no id, peers":                 {Policy: policyWith("peers")},
		"declared id, default policy":  {RequesterID: "someone", Policy: DefaultSharePolicy()},
		"declared id, peers":           {RequesterID: "someone", Policy: policyWith("peers")},
		"declared counterparty, peers": {RequesterID: "known-peer", Policy: policyWith("peers")},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			before := book.Size()
			reply, statement := respondWith(t, book, history, opts)
			refusal := mustRefusal(t, reply, ReasonNotAuthorized)
			if refusal.KeyID != KeyID(book.signer) {
				t.Fatal("the refusal must be signed by the book's own key")
			}
			if statement != (AnsweredStatement{}) || book.Size() != before {
				t.Fatal("an unauthenticated request grew the log")
			}
		})
	}
	for _, derivation := range []string{"", DerivationHistoryCard} {
		t.Run("checkpoints stay public "+derivation, func(t *testing.T) {
			reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: derivation, Coverage: Coverage{ExpectedPin: pin}}), RespondOptions{Policy: DefaultSharePolicy()})
			mustEnvelope(t, reply)
		})
	}
}

// (b) One requester's id never appears, in any recoverable form, in what
// another requester is later served.
func TestEarlierRequesterIDAbsentFromLaterAnswers(t *testing.T) {
	book, records := linkedBook(t)
	const alice = "alice-private-handle"
	peers := policyWith("peers")
	_, statement := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}),
		RespondOptions{RequesterID: alice, AuthenticatedRequester: alice, Policy: peers})
	if statement.Outcome != OutcomeArtifact {
		t.Fatalf("alice's request was not answered: %+v", statement)
	}
	var answered string
	for _, id := range book.order {
		if book.records[id].Header.RecordType == RecordTypeRequestAnswered {
			answered = id
		}
	}
	// The floor makes bob's answer cover the record of alice's request.
	covering := Coverage{MinFreshness: sizeFloor(uint64(len(book.order)))}
	reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: covering}),
		RespondOptions{RequesterID: "bob", AuthenticatedRequester: "bob", Policy: peers})
	e := mustEnvelope(t, reply)
	served := false
	for _, entry := range artifactRecords(t, e) {
		served = served || entry["digest"] == answered
	}
	if !served {
		t.Fatal("bob's full_history must include the record of alice's request for this test to mean anything")
	}
	sum := sha256.Sum256([]byte(alice))
	forms := map[string]string{
		"raw":         alice,
		"hex":         hex.EncodeToString([]byte(alice)),
		"base64":      base64.StdEncoding.EncodeToString([]byte(alice)),
		"sha256":      hex.EncodeToString(sum[:]),
		"sha256 b64":  base64.StdEncoding.EncodeToString(sum[:]),
		"field name":  "requester_ref",
		"json-quoted": `"` + alice + `"`,
	}
	for name, form := range forms {
		if bytes.Contains(reply.Octets, []byte(form)) || bytes.Contains(e.Artifact, []byte(form)) {
			t.Fatalf("bob's answer contains alice's id (%s form)", name)
		}
	}
	for _, id := range book.order {
		if h := book.records[id].Header; h.RecordType == RecordTypeRequestAnswered && bytes.Contains(h.Statement, []byte(alice)) {
			t.Fatalf("answered-request record %s carries a requester id", id)
		}
	}
}

// (c) A claimed counterparty id, without authentication, never grants
// counterparty scope, and neither does a declared id that differs from the
// authenticated one.
func TestClaimedCounterpartyIDGrantsNoScope(t *testing.T) {
	book, records := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "known-peer"})
	req := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()})
	for name, opts := range map[string]RespondOptions{
		"claimed, unauthenticated":         {RequesterID: "known-peer", Policy: policyWith("counterparties")},
		"claimed, authenticated as other":  {RequesterID: "known-peer", AuthenticatedRequester: "someone-else", Policy: policyWith("counterparties")},
		"claimed, unauthenticated, prosp.": {RequesterID: "known-peer", Policy: policyWith("prospective")},
	} {
		t.Run(name, func(t *testing.T) {
			reply, statement := respondWith(t, book, req, opts)
			mustRefusal(t, reply, ReasonNotAuthorized)
			if statement.Relationship == RelationshipCounterparty {
				t.Fatal("a claimed id was classified as a counterparty")
			}
		})
	}
}

// (d) No regression: an authenticated counterparty still gets counterparty
// scope.
func TestAuthenticatedCounterpartyKeepsScope(t *testing.T) {
	book, records := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "known-peer"})
	req := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()})
	reply, statement := respondWith(t, book, req, RespondOptions{RequesterID: "known-peer", AuthenticatedRequester: "known-peer", Policy: policyWith("counterparties")})
	mustEnvelope(t, reply)
	if statement.Relationship != RelationshipCounterparty {
		t.Fatalf("relationship %q, want %q", statement.Relationship, RelationshipCounterparty)
	}
}
