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
// recipient, reveals only its declared fields, has its own identity, and is
// never listed in the manifest. The underived subject's artifact is
// unchanged by it.
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
	// The grant is the derivation: under the default tier the underived
	// correlation is refused to its recipient, whom no record names.
	underivedCorrelation, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: subject, Coverage: Coverage{ExpectedPin: pin}}), RespondOptions{AuthenticatedRequester: "alice", Policy: DefaultSharePolicy()})
	mustRefusal(t, underivedCorrelation, ReasonNotAuthorized)
	record := Subject{Kind: SubjectRecord, Digest: records[1].RecordID}
	askRecord := func(derivation, requester string) Reply {
		return respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: record, Derivation: derivation, Coverage: Coverage{ExpectedPin: pin}}), requester)
	}
	projected := mustEnvelope(t, askRecord(forAlice, "alice"))
	underived := mustEnvelope(t, askRecord("", "alice"))
	if bytes.Equal(underived.Artifact, projected.Artifact) || underived.ArtifactDigest == projected.ArtifactDigest {
		t.Fatal("a projection is presented as the canonical Artifact of the underived subject")
	}
	if again := mustEnvelope(t, askRecord("", "bob")); !bytes.Equal(again.Artifact, underived.Artifact) {
		t.Fatal("registering projections changed the underived artifact")
	}
	manifest, err := book.Manifest(ManifestOptions{Bindings: []string{BindingHTTP}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifest, []byte(forAlice)) || bytes.Contains(manifest, []byte("alice")) {
		t.Fatal("the manifest lists a recipient-specific derivation")
	}
	if _, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(`{}`), Forms: []SubjectKind{SubjectCheckpoints}, Fields: []string{"seq"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a record projection over checkpoints was registered")
	}
	if _, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(`{}`), Forms: []SubjectKind{SubjectRecord}, Fields: []string{"payload"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a field outside the record header was declared")
	}
}

// #86: the identifier binds only the document, so registering it again may
// repeat what it declares but never change it. The issue's example: `{}`
// registered for alice, then for bob, still answers alice and not bob.
func TestRegisterDerivationAgain(t *testing.T) {
	book, _ := linkedBook(t)
	pin := anchorPin(t, book)
	def := DerivationDefinition{Document: []byte(`{}`), Recipient: "alice", Forms: []SubjectKind{SubjectCorrelation}, Fields: []string{"seq"}}
	id, err := book.RegisterDerivation(def)
	if err != nil {
		t.Fatal(err)
	}
	again, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(` { } `), Recipient: "alice", Forms: []SubjectKind{SubjectCorrelation}, Fields: []string{"seq", "seq"}})
	if err != nil || again != id {
		t.Fatalf("the same declarations: %q, %v", again, err)
	}
	for _, other := range []DerivationDefinition{
		{Document: []byte(`{}`), Recipient: "bob", Forms: def.Forms, Fields: def.Fields},
		{Document: []byte(`{}`), Forms: def.Forms, Fields: def.Fields},
		{Document: []byte(`{}`), Recipient: "alice", Forms: []SubjectKind{SubjectCorrelation, SubjectRecord}, Fields: def.Fields},
		{Document: []byte(`{}`), Recipient: "alice", Forms: def.Forms, Fields: []string{"v"}},
	} {
		if got, err := book.RegisterDerivation(other); !errors.Is(err, ErrInvalid) || got != "" {
			t.Fatalf("%+v: %q, %v", other, got, err)
		}
	}
	// A registration whose declarations are invalid is refused for that
	// first, as it would be for a new document.
	if _, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(`{}`), Recipient: "alice", Forms: def.Forms, Fields: []string{"payload"}}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "record header member") {
		t.Fatalf("an invalid re-registration: %v", err)
	}
	ask := func(requester string) Reply {
		return respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: "order-17"}, Derivation: id, Coverage: Coverage{ExpectedPin: pin}}), requester)
	}
	mustEnvelope(t, ask("alice"))
	mustRefusal(t, ask("bob"), ReasonNotAuthorized)
}
