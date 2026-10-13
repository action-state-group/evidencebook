// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

// Ruling 5, profile §6.5: a recipient-specific disclosure is a derivation
// named by the digest of its definition. It is answered only to its
// recipient, reveals only its declared fields and has its own identity. The
// underived subject's artifact is unchanged by it.
func TestRecipientSpecificDerivation(t *testing.T) {
	book, records := linkedBook(t)
	pin := anchorPin(t, book)
	define := func(recipient string) string {
		id, err := book.RegisterDerivation(DerivationDefinition{
			Document:  []byte(`{"computation":"project","fields":["record_type","committed_at"],"recipient":"` + recipient + `"}`),
			Recipient: recipient, Forms: []SubjectKind{SubjectCorrelation, SubjectRecord}, Fields: []string{"record_type", "committed_at"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	forAlice, forBob := define("alice"), define("bob")
	if forAlice == forBob {
		t.Fatal("two recipients' projections share an identity")
	}
	subject := Subject{Kind: SubjectCorrelation, Correlation: "order-17"}
	ask := func(derivation, requester string) Reply {
		return respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: subject, Derivation: derivation, Coverage: Coverage{ExpectedPin: pin}}), requester)
	}
	e := mustEnvelope(t, ask(forAlice, "alice"))
	if e.Derivation != forAlice {
		t.Fatal("the envelope signs the derivation it answers")
	}
	for _, entry := range artifactRecords(t, e) {
		keys := make([]string, 0, len(entry))
		for k := range entry {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if strings.Join(keys, ",") != "committed_at,digest,leaf_index,record_type" {
			t.Fatalf("the projection carries more than its declared fields: %v", keys)
		}
	}
	mustRefusal(t, ask(forAlice, "bob"), ReasonNotAuthorized)
	mustRefusal(t, ask(forAlice, ""), ReasonNotAuthorized)
	mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRange, Range: [2]uint64{0, 1}}, Derivation: forAlice, Coverage: Coverage{ExpectedPin: pin}}), "alice"), ReasonNoSuchSubject)
	underived := mustEnvelope(t, ask("", "alice"))
	if bytes.Equal(underived.Artifact, e.Artifact) || underived.ArtifactDigest == e.ArtifactDigest {
		t.Fatal("a projection is presented as the canonical Artifact of the underived subject")
	}
	if again := mustEnvelope(t, ask("", "bob")); !bytes.Equal(again.Artifact, underived.Artifact) {
		t.Fatal("registering projections changed the underived artifact")
	}
	if _, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(`{}`), Forms: []SubjectKind{SubjectCheckpoints}, Fields: []string{"seq"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a record projection over checkpoints was registered")
	}
	if _, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(`{}`), Forms: []SubjectKind{SubjectRecord}, Fields: []string{"payload"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a field outside the record header was declared")
	}
	_ = records
}
