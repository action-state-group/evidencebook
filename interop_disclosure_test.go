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
	for _, subject := range []Subject{{Kind: SubjectRecord, Digest: records[0].RecordID}, {Kind: SubjectExchange, Digest: records[0].RecordID}} {
		mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: subject, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"), ReasonPolicyDeclined)
	}
	for name, artifact := range map[string]string{
		"undeclared top member":   `{"anchor":"a","evidence_stream":"s","records":[],"subject":{"full_history":null},"producer":"x"}`,
		"undeclared entry member": `{"anchor":"a","evidence_stream":"s","records":[{"body":"","digest":"d","leaf_index":0,"verification":{}}],"subject":{"full_history":null}}`,
	} {
		if book.disclosureClean([]byte(artifact), &recordSetFields) {
			t.Fatalf("%s passed the disclosure scan", name)
		}
	}
}
