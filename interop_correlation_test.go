// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"slices"
	"testing"
)

// correlatedBook is a book whose deal steps carry the deal as their
// correlation identifier and a subject_ref of their own (one per step, as a
// host keeps for retries), beside a report about the deal that carries
// neither, and a record held only under its subject_ref, as records were
// before.
func correlatedBook(t *testing.T) (*Book, []Record) {
	t.Helper()
	book := newFixture(t, "book-c", 3).open(t)
	step := func(n string) Record {
		return mustAppend(t, book, Entry{RecordType: "step_completed", EpistemicType: ProducerClaim, SubjectRef: "deal-a/" + n,
			Correlation: Correlation{ExchangeID: "deal-a"}})
	}
	first, second := step("1"), step("2")
	report := mustAppend(t, book, Entry{RecordType: "report", EpistemicType: ProducerClaim})
	legacy := mustAppend(t, book, observation("order-17", `{"status":"approved"}`))
	return book, []Record{first, second, report, legacy}
}

// forPeer registers the recipient-specific derivation over correlation
// that grants peer-1 a correlation subject, and returns its identifier.
// Nothing else answers a correlation subject.
func forPeer(t *testing.T, book *Book) string {
	t.Helper()
	id, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(`{"d":1}`), Recipient: "peer-1",
		Forms: []SubjectKind{SubjectCorrelation}, Fields: []string{"record_type"}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// correlationDigests is the records peer-1 is answered for correlation id.
func correlationDigests(t *testing.T, book *Book, id string) []string {
	t.Helper()
	pin := anchorPin(t, book)
	e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: id}, Derivation: forPeer(t, book), Coverage: Coverage{ExpectedPin: pin}}), "peer-1"))
	var out []string
	for _, entry := range artifactRecords(t, e) {
		out = append(out, entry["digest"].(string))
	}
	return out
}

// A record that carries a correlation identifier is held under it, and
// under no subject_ref; a record that carries none is held under its
// subject_ref, as before.
func TestCorrelationSubjectReadsTheHeadersCorrelation(t *testing.T) {
	book, records := correlatedBook(t)
	first, second, legacy := records[0], records[1], records[3]
	if got := correlationDigests(t, book, "deal-a"); !slices.Equal(got, []string{first.RecordID, second.RecordID}) {
		t.Fatalf("deal-a holds its steps only, not the report beside them: %v", got)
	}
	if got := correlationDigests(t, book, "order-17"); !slices.Equal(got, []string{legacy.RecordID}) {
		t.Fatalf("a record with no correlation identifier is still held under its subject_ref: %v", got)
	}
	// A step's own subject_ref is no correlation: the step is held under
	// its correlation identifier alone.
	mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: "deal-a/1"}, Derivation: forPeer(t, book), Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}), "peer-1"), ReasonNoSuchSubject)
}

// The subject_ref index proves the set of records under one subject_ref,
// never a correlation set. For the deal, which no record carries as its
// subject_ref, its proof is a proof of absence, while the correlation
// subject is answered with both steps. So that proof cannot stand for the
// completeness of the set a correlation subject is answered with.
// The fallback reads a record's subject_ref only when it carries no
// exchange_id: a record whose correlation has only a request digest or a
// twin bracket is still held under its subject_ref, and never under those.
// Both key spaces answer the same requested identifier, so a record filed
// before under subject_ref deal-a is answered with steps whose exchange_id
// is deal-a.
func TestCorrelationFallbackAndSharedKeySpace(t *testing.T) {
	book, records := correlatedBook(t)
	first, second := records[0], records[1]
	byDigest := mustAppend(t, book, Entry{RecordType: "half", EpistemicType: ObservedEvent, SubjectRef: "order-18", Correlation: Correlation{RequestDigest: digestBytes([]byte("r"))}})
	byTwin := mustAppend(t, book, Entry{RecordType: "half", EpistemicType: ObservedEvent, SubjectRef: "order-19", Correlation: Correlation{TwinBracketID: "twin-1"}})
	before := mustAppend(t, book, observation("deal-a", `{"filed":"before"}`))
	if got := correlationDigests(t, book, "order-18"); !slices.Equal(got, []string{byDigest.RecordID}) {
		t.Fatalf("a record with only a request digest is held under its subject_ref: %v", got)
	}
	if got := correlationDigests(t, book, "order-19"); !slices.Equal(got, []string{byTwin.RecordID}) {
		t.Fatalf("a record with only a twin bracket is held under its subject_ref: %v", got)
	}
	pin := anchorPin(t, book)
	for _, id := range []string{digestBytes([]byte("r")), "twin-1"} {
		mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: id}, Derivation: forPeer(t, book), Coverage: Coverage{ExpectedPin: pin}}), "peer-1"), ReasonNoSuchSubject)
	}
	if got := correlationDigests(t, book, "deal-a"); !slices.Equal(got, []string{first.RecordID, second.RecordID, before.RecordID}) {
		t.Fatalf("an exchange_id and a subject_ref of one value answer together: %v", got)
	}
}

// A correlation answer carries no proof that its set is complete: no
// verification material at all (profile v0 has no material for record
// sets, P-13), so no index proof can stand in for one. Underived, it is not
// answered at all.
func TestCorrelationAnswerCarriesNoCompletenessMaterial(t *testing.T) {
	book, _ := correlatedBook(t)
	id := forPeer(t, book)
	pin := anchorPin(t, book)
	e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: "deal-a"}, Derivation: id, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"))
	if len(e.Verification) != 0 || len(e.VerificationContent) != 0 {
		t.Fatal("a correlation answer carries verification material")
	}
	mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: "deal-a"}, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"), ReasonNotAuthorized)
}

func TestSubjectIndexProofIsNotACorrelationSetsCompleteness(t *testing.T) {
	book, records := correlatedBook(t)
	first, second := records[0], records[1]
	answer, err := book.ProveKeyRange(context.Background(), "subject_ref", "deal-a", "deal-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.Proof.Matches) != 0 {
		t.Fatalf("the subject_ref proof for deal-a names %d records; want a proof of absence", len(answer.Proof.Matches))
	}
	if got := correlationDigests(t, book, "deal-a"); !slices.Equal(got, []string{first.RecordID, second.RecordID}) {
		t.Fatalf("the correlation subject is answered with the steps: %v", got)
	}
	step, err := book.ProveKeyRange(context.Background(), "subject_ref", "deal-a/1", "deal-a/1")
	if err != nil {
		t.Fatal(err)
	}
	if len(step.Proof.Matches) != 1 || step.Proof.Matches[0].RecordID != first.RecordID {
		t.Fatal("the subject_ref index still proves the record under its own subject_ref")
	}
}
