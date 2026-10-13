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

// Tests for the requester gate: protected subjects are answered only to an
// authenticated requester, a declared or disclosed id never grants scope, and
// answered-request records never carry a requester id.

func policyWith(tier string) SharePolicy {
	p := DefaultSharePolicy()
	p.HistorySegments = tier
	return p
}

func respondWith(t *testing.T, book *Book, req []byte, opts RespondOptions) (Response, AnsweredStatement) {
	t.Helper()
	resp, answered, err := book.Respond(context.Background(), req, opts)
	if err != nil {
		t.Fatal(err)
	}
	var statement AnsweredStatement
	if err := json.Unmarshal(answered.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	return resp, statement
}

// (a) Without an authenticated requester only public subjects are answered;
// everything else gets a signed not_authorized refusal, whatever id the
// caller declares and whatever tier the policy is set to.
func TestUnauthenticatedRequesterGetsOnlyPublicSubjects(t *testing.T) {
	book, _ := linkedBook(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "known-peer"})
	history := requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()})
	cases := map[string]RespondOptions{
		"no id, default policy":        {Policy: DefaultSharePolicy()},
		"no id, peers":                 {Policy: policyWith("peers")},
		"declared id, default policy":  {RequesterID: "someone", Policy: DefaultSharePolicy()},
		"declared id, peers":           {RequesterID: "someone", Policy: policyWith("peers")},
		"declared counterparty, peers": {RequesterID: "known-peer", Policy: policyWith("peers")},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			resp, statement := respondWith(t, book, history, opts)
			mustRefusal(t, resp, ReasonNotAuthorized)
			if resp.Refusal.KeyID != KeyID(book.signer) {
				t.Fatal("the refusal must be signed by the book's own key")
			}
			if statement.Relationship != RelationshipStranger {
				t.Fatalf("relationship %q, want %q", statement.Relationship, RelationshipStranger)
			}
		})
	}
	t.Run("checkpoints stay public", func(t *testing.T) {
		resp, _ := respondWith(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}), RespondOptions{Policy: DefaultSharePolicy()})
		mustArtifact(t, resp)
	})
}

// (b) One requester's id never appears, in any recoverable form, in what
// another requester is later served.
func TestEarlierRequesterIDAbsentFromLaterAnswers(t *testing.T) {
	book, records := linkedBook(t)
	const alice = "alice-private-handle"
	peers := policyWith("peers")
	_, statement := respondWith(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()}),
		RespondOptions{RequesterID: alice, AuthenticatedRequester: alice, Policy: peers})
	if statement.Outcome != OutcomeArtifact {
		t.Fatalf("alice's request was not answered: %+v", statement)
	}
	// The floor makes bob's answer cover the record of alice's request.
	covering := Coverage{MinFreshness: &Freshness{Size: uint64(len(book.order))}}
	resp, _ := respondWith(t, book, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: covering}),
		RespondOptions{RequesterID: "bob", AuthenticatedRequester: "bob", Policy: peers})
	artifact := mustArtifact(t, resp)
	if !bytes.Contains(artifact.Artifact, []byte(RecordTypeRequestAnswered)) {
		t.Fatal("bob's full_history must include the record of alice's request for this test to mean anything")
	}
	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
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
		if bytes.Contains(wire, []byte(form)) {
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
	req := requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()})
	for name, opts := range map[string]RespondOptions{
		"claimed, unauthenticated":         {RequesterID: "known-peer", Policy: policyWith("counterparties")},
		"claimed, authenticated as other":  {RequesterID: "known-peer", AuthenticatedRequester: "someone-else", Policy: policyWith("counterparties")},
		"claimed, unauthenticated, prosp.": {RequesterID: "known-peer", Policy: policyWith("prospective")},
	} {
		t.Run(name, func(t *testing.T) {
			resp, statement := respondWith(t, book, req, opts)
			mustRefusal(t, resp, ReasonNotAuthorized)
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
	req := requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: fresh()})
	resp, statement := respondWith(t, book, req, RespondOptions{RequesterID: "known-peer", AuthenticatedRequester: "known-peer", Policy: policyWith("counterparties")})
	mustArtifact(t, resp)
	if statement.Relationship != RelationshipCounterparty {
		t.Fatalf("relationship %q, want %q", statement.Relationship, RelationshipCounterparty)
	}
}
