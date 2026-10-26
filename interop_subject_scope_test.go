// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"
)

// Adversarial tests of subject-scoped authorization (evidencebook#114):
// under the default policy a record or exchange subject is answered only to
// the party every record it names names as its counterparty, and range,
// correlation and full_history are answered to no one.

// tenantsBook holds alice's and bob's records: one each, one of alice's
// citing bob's, and a half both cite.
func tenantsBook(t *testing.T, cfg func(*Config)) (book *Book, alice, bob, aliceCitesBob Record, shared string) {
	t.Helper()
	f := newFixture(t, "book-t", 7)
	c := f.config(t)
	if cfg != nil {
		cfg(&c)
	}
	book, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	shared = digestBytes([]byte("a half both cite"))
	exchange := func(counterparty string, links ...Link) Record {
		return mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, SubjectRef: "deal-" + counterparty,
			CounterpartyRef: counterparty, Links: links, Payloads: [][]byte{[]byte("PAYLOAD-OF-" + counterparty)}})
	}
	alice = exchange("alice", Link{Type: Cites, Target: shared})
	bob = exchange("bob", Link{Type: Cites, Target: shared})
	aliceCitesBob = exchange("alice", Link{Type: Cites, Target: bob.RecordID})
	return book, alice, bob, aliceCitesBob, shared
}

func askDefault(t *testing.T, book *Book, requester string, subject Subject, coverage Coverage) Reply {
	t.Helper()
	reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: subject, Coverage: coverage}), RespondOptions{AuthenticatedRequester: requester, Policy: DefaultSharePolicy()})
	return reply
}

// Being party to one record grants nothing on another party's record, on
// an exchange any other party's record cites, or on the cited record behind
// one's own.
func TestCrossTenantTraversalIsRefused(t *testing.T) {
	book, alice, bob, aliceCitesBob, shared := tenantsBook(t, nil)
	pin := Coverage{ExpectedPin: anchorPin(t, book)}
	mustEnvelope(t, askDefault(t, book, "alice", Subject{Kind: SubjectRecord, Digest: alice.RecordID}, pin))
	mustEnvelope(t, askDefault(t, book, "bob", Subject{Kind: SubjectRecord, Digest: bob.RecordID}, pin))
	// Every record citing bob's record is alice's.
	mustEnvelope(t, askDefault(t, book, "alice", Subject{Kind: SubjectExchange, Digest: bob.RecordID}, pin))
	for name, c := range map[string]struct {
		requester string
		subject   Subject
	}{
		"another party's record":           {"alice", Subject{Kind: SubjectRecord, Digest: bob.RecordID}},
		"the other way":                    {"bob", Subject{Kind: SubjectRecord, Digest: alice.RecordID}},
		"a record citing one's own":        {"bob", Subject{Kind: SubjectRecord, Digest: aliceCitesBob.RecordID}},
		"an exchange both parties cite":    {"alice", Subject{Kind: SubjectExchange, Digest: shared}},
		"an exchange only the other cites": {"bob", Subject{Kind: SubjectExchange, Digest: bob.RecordID}},
	} {
		t.Run(name, func(t *testing.T) {
			mustRefusal(t, askDefault(t, book, c.requester, c.subject, pin), ReasonNotAuthorized)
		})
	}
	// A subject reaching more than one requester's records is refused to
	// each, under every tier but off's refusal of everything.
	for _, tier := range []string{"counterparties", "prospective", "peers"} {
		for _, subject := range []Subject{{Kind: SubjectFullHistory}, {Kind: SubjectRange, Range: [2]uint64{0, 0}}, {Kind: SubjectCorrelation, Correlation: "deal-alice"}} {
			reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: subject, Coverage: pin}), RespondOptions{AuthenticatedRequester: "alice", Policy: policyWith(tier)})
			mustRefusal(t, reply, ReasonNotAuthorized)
		}
	}
}

// What alice is served of her record that cites bob's carries bob's record
// by digest only: never its body, its payload or bob's identity.
func TestCitedRecordsAreNotServedWithTheCitingOne(t *testing.T) {
	book, _, bob, aliceCitesBob, _ := tenantsBook(t, nil)
	pin := Coverage{ExpectedPin: anchorPin(t, book)}
	stored, err := book.storedLocked(context.Background(), bob.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	bobBody, err := capsulePreimage(stored.Capsule, bob.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	for _, subject := range []Subject{{Kind: SubjectRecord, Digest: aliceCitesBob.RecordID}, {Kind: SubjectExchange, Digest: bob.RecordID}} {
		reply := askDefault(t, book, "alice", subject, pin)
		e := mustEnvelope(t, reply)
		if !bytes.Contains(e.Artifact, []byte(bob.RecordID)) && !bytes.Contains(e.Artifact, []byte(hex.EncodeToString([]byte(bob.RecordID)))) {
			t.Fatalf("%s: the citation of bob's record is missing", subject.Kind)
		}
		for name, leak := range map[string][]byte{
			"bob's body":     bobBody,
			"bob's body hex": []byte(hex.EncodeToString(bobBody)),
			"bob's payload":  []byte("PAYLOAD-OF-bob"),
			"bob's name":     []byte("bob"),
			"alice's own":    []byte("PAYLOAD-OF-alice"),
		} {
			if bytes.Contains(reply.Octets, leak) || bytes.Contains(e.Artifact, leak) {
				t.Fatalf("%s: alice's answer carries %s", subject.Kind, name)
			}
		}
	}
}

// A requester no record names learns nothing of what the book holds: a
// record held, one not held and one beyond the anchor get one refusal.
// Under the uniform refusal policy a counterparty learns nothing more of
// what is not its own: another party's record, one not held and one beyond
// the anchor are all policy_declined.
func TestRefusalsDoNotEnumerateRecords(t *testing.T) {
	never := digestBytes([]byte("never recorded"))
	t.Run("stranger", func(t *testing.T) {
		book, alice, _, _, shared := tenantsBook(t, nil)
		pin := Coverage{ExpectedPin: anchorPin(t, book)}
		later := mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "alice"})
		for _, subject := range []Subject{
			{Kind: SubjectRecord, Digest: alice.RecordID}, {Kind: SubjectRecord, Digest: never}, {Kind: SubjectRecord, Digest: later.RecordID},
			{Kind: SubjectExchange, Digest: shared}, {Kind: SubjectExchange, Digest: never},
		} {
			mustRefusal(t, askDefault(t, book, "mallory", subject, pin), ReasonNotAuthorized)
		}
	})
	t.Run("uniform", func(t *testing.T) {
		book, alice, bob, _, _ := tenantsBook(t, func(c *Config) { c.Responder = ResponderPolicy{RefusalPolicy: RefusalPolicyUniform} })
		pin := Coverage{ExpectedPin: anchorPin(t, book)}
		later := mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "bob"})
		for _, subject := range []Subject{
			{Kind: SubjectRecord, Digest: bob.RecordID}, {Kind: SubjectRecord, Digest: never}, {Kind: SubjectRecord, Digest: later.RecordID},
			{Kind: SubjectExchange, Digest: never}, {Kind: SubjectFullHistory},
		} {
			mustRefusal(t, askDefault(t, book, "alice", subject, pin), ReasonPolicyDeclined)
		}
		mustEnvelope(t, askDefault(t, book, "alice", Subject{Kind: SubjectRecord, Digest: alice.RecordID}, pin))
	})
}
