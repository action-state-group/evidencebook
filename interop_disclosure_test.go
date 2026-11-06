// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"testing"
)

// NFR-2: every artifact is scanned before release. A record whose committed
// octets carry a principal or counterparty reference this book holds is not
// released, and neither is any artifact carrying a member outside its form's
// declared field set.
func TestDisclosureScanRefusesLinkableArtifacts(t *testing.T) {
	book, records := linkedBook(t)
	// The capsule's operator member is the operator name; a counterparty of
	// that name makes every record linkable to it.
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: book.operator})
	pin := anchorPin(t, book)
	// The scan is policy, not a limit: policy_declined under either profile.
	for _, profile := range []string{ProfileID, ProfileIDv1} {
		for _, subject := range []Subject{{Kind: SubjectRecord, Digest: records[0].RecordID}, {Kind: SubjectExchange, Digest: records[0].RecordID}} {
			mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: subject, Coverage: Coverage{ExpectedPin: pin}, Profile: profile}), "peer-1"), ReasonPolicyDeclined)
		}
	}
	for name, artifact := range map[string]string{
		"undeclared top member":   `{"anchor":"a","evidence_stream":"s","records":[],"subject":{"full_history":null},"producer":"x"}`,
		"undeclared entry member": `{"anchor":"a","evidence_stream":"s","records":[{"body":"","digest":"d","leaf_index":0,"verification":{}}],"subject":{"full_history":null}}`,
	} {
		view, err := book.view(t.Context(), book.log, nil)
		if err != nil {
			t.Fatal(err)
		}
		if view.disclosureClean([]byte(artifact), &recordSetFields) {
			t.Fatalf("%s passed the disclosure scan", name)
		}
	}
}

// The scan treats a record the snapshot holds but does not serve, such as one
// that cites a conduct pack, like a record type that is never served: an
// artifact naming its digest fails (evidencebook#418).
func TestDisclosureScanRefusesAPrivateRecordsDigest(t *testing.T) {
	book := newFixture(t, "conduct-scan", 20).open(t)
	private := mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "asg/everyday-rules@1", Payloads: [][]byte{[]byte("x")}})
	open := mustAppend(t, book, observation("asg/seller@2", "y"))
	artifact := func(id string) []byte {
		return []byte(`{"anchor":"a","evidence_stream":"s","records":[{"body":"","digest":"` + id + `","leaf_index":0}],"subject":{"full_history":null}}`)
	}
	view, err := book.view(t.Context(), book.log, []string{"asg/everyday-rules"})
	if err != nil {
		t.Fatal(err)
	}
	if view.disclosureClean(artifact(private.RecordID), &recordSetFields) {
		t.Fatal("an artifact naming a conduct record passed the disclosure scan")
	}
	if !view.disclosureClean(artifact(open.RecordID), &recordSetFields) {
		t.Fatal("control: an artifact naming an ordinary record failed the scan")
	}
}
