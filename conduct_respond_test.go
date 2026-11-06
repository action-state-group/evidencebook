package evidencebook

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A statement that holds a contract_ref anywhere is found, whatever its
// shape; a non-Result record is not a Result, so its claims need not be a
// list (evidencebook#418).
func TestConductPackIsFoundInAnyStatementShape(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct-shape", 17).open(t)
	odd := mustAppend(t, book, Entry{
		RecordType: "evidence_request", EpistemicType: ProducerClaim, SubjectRef: "asg/seller@2", CounterpartyRef: "peer",
		Statement: []byte(`{"claims":{"contract_ref":"asg/everyday-rules@1"}}`),
	})
	private := []string{"asg/everyday-rules"}
	if _, err := book.Bundle(ctx, BundleRequest{Root: odd.RecordID, ConductPacks: private}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bundle: err = %v; want ErrInvalid", err)
	}
	reply, st := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: odd.RecordID}, Coverage: fresh()}),
		RespondOptions{AuthenticatedRequester: "peer", Policy: policyWith("peers"), ConductPacks: private})
	if reply.Refusal == nil {
		t.Fatalf("respond: outcome %q; want a refusal", st.Outcome)
	}
}

// An exchange named by a private record's id, and a lapsed retention
// commitment on it, are refused as an unknown id is.
func TestRespondExchangeAndRetentionOnAConductRecord(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "conduct-exchange", 18)
	book := f.open(t)
	conduct := mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "asg/everyday-rules@1", CounterpartyRef: "peer", Payloads: [][]byte{[]byte("x")}})
	mustAppend(t, book, Entry{
		RecordType: "note", EpistemicType: ObservedEvent, SubjectRef: "n", CounterpartyRef: "peer",
		Links: []Link{{Type: Cites, Target: conduct.RecordID}}, Payloads: [][]byte{[]byte("cites it")},
	})
	unknown := digestBytes([]byte("a half no record holds"))
	private := []string{"asg/everyday-rules"}
	ask := func(digest string) Reply {
		reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectExchange, Digest: digest}, Coverage: fresh()}),
			RespondOptions{AuthenticatedRequester: "peer", Policy: policyWith("peers"), ConductPacks: private})
		return reply
	}
	reference := ask(unknown)
	if reference.Refusal == nil {
		t.Fatal("an unknown exchange should be refused")
	}
	same := func(label string, reply Reply) {
		t.Helper()
		if reply.Refusal == nil || reply.Refusal.Reason != reference.Refusal.Reason || len(reply.Message.Octets) != len(reference.Message.Octets) {
			t.Fatalf("%s: %+v; want the refusal an unknown id gets (%q)", label, reply.Refusal, reference.Refusal.Reason)
		}
	}
	same("an exchange on a conduct record", ask(conduct.RecordID))

	until := f.clock.t.Add(time.Hour)
	for _, digest := range []string{conduct.RecordID, unknown} {
		if _, err := book.CommitRetention(ctx, Subject{Kind: SubjectExchange, Digest: digest}, until); err != nil {
			t.Fatal(err)
		}
	}
	f.clock.t = until.Add(time.Minute)
	// The unknown id's lapsed commitment answers retention_expired; the
	// conduct record's must not, or the two are told apart.
	if got := ask(unknown); got.Refusal == nil || got.Refusal.Reason != ReasonRetentionExpired {
		t.Fatalf("control: a lapsed commitment on an unknown id: %+v; want %s", got.Refusal, ReasonRetentionExpired)
	}
	if got := ask(conduct.RecordID); got.Refusal == nil || got.Refusal.Reason != reference.Refusal.Reason {
		t.Fatalf("a lapsed commitment on a conduct record: %+v; want %q", got.Refusal, reference.Refusal.Reason)
	}
}

// The MCP carriage takes the list: a conduct record is refused as an unknown
// id, and a pack id that could never match is refused at construction, for A2A too.
func TestCarriagesTakeTheConductList(t *testing.T) {
	book := newFixture(t, "conduct-carriage", 19).open(t)
	conduct := mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "asg/everyday-rules@1", CounterpartyRef: "peer", Payloads: [][]byte{[]byte("x")}})
	manifest := ManifestOptions{Bindings: []string{BindingStream, BindingMCP, BindingA2A}}
	server, err := book.MCP(MCPOptions{Manifest: manifest, Policy: policyWith("peers"), ConductPacks: []string{"asg/everyday-rules"}})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(digest string) Reply {
		return mcpReply(t, server, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: digest}, Coverage: fresh()}), "peer")
	}
	known, unknown := ask(conduct.RecordID), ask(strings.Repeat("cd", 32))
	if known.Refusal == nil || unknown.Refusal == nil || known.Refusal.Reason != unknown.Refusal.Reason {
		t.Fatalf("MCP: %+v vs an unknown id's %+v", known.Refusal, unknown.Refusal)
	}
	if _, err := book.MCP(MCPOptions{Manifest: manifest, Policy: policyWith("peers"), ConductPacks: []string{"asg/everyday-rules@1"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("MCP with a versioned pack id: err = %v; want ErrInvalid", err)
	}
	if _, err := book.A2A(A2AOptions{Manifest: manifest, Policy: policyWith("peers"), Name: "n", URL: "https://example.test/a2a", ConductPacks: []string{""}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("A2A with an empty pack id: err = %v; want ErrInvalid", err)
	}
}
