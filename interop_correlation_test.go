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
	// The requester is answered as the book's counterparty.
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "peer-1"})
	return book, []Record{first, second, report, legacy}
}

func correlationDigests(t *testing.T, book *Book, id string) []string {
	t.Helper()
	pin := anchorPin(t, book)
	e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: id}, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"))
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
	mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: "deal-a/1"}, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}), "peer-1"), ReasonNoSuchSubject)
}

// The subject_ref index proves the set of records under one subject_ref,
// never a correlation set. For the deal, which no record carries as its
// subject_ref, its proof is a proof of absence, while the correlation
// subject is answered with both steps. So that proof cannot stand for the
// completeness of the set a correlation subject is answered with.
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
