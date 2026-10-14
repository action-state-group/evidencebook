// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// appendEveryLinkType appends one record per link type and returns them in
// order. test/interop/capsules builds the same book for the AAC registry
// conformance check in CI.
func appendEveryLinkType(t *testing.T, book *Book) []Record {
	t.Helper()
	add := func(recordType string, epistemic EpistemicType, links ...Link) Record {
		if links == nil {
			links = []Link{}
		}
		return mustAppend(t, book, Entry{RecordType: recordType, EpistemicType: epistemic, SubjectRef: "order-17", Links: links})
	}
	observed := add("observation", ObservedEvent)
	claim := add("claim", ProducerClaim, Link{Cites, observed.RecordID})
	corrected := add("claim", ProducerClaim, Link{Supersedes, claim.RecordID})
	closed := add("close", ProducerClaim, Link{Closes, corrected.RecordID})
	acknowledged := add("acknowledgement", HumanReport, Link{Acknowledges, closed.RecordID})
	rebutted := add("rebuttal", HumanReport, Link{Rebuts, closed.RecordID})
	adjudicated := add("adjudication", Adjudication,
		Link{Adjudicates, acknowledged.RecordID}, Link{Adjudicates, rebutted.RecordID}, Link{Cites, closed.RecordID})
	return []Record{observed, claim, corrected, closed, acknowledged, rebutted, adjudicated}
}

type sealedReference struct {
	Type      string `json:"type"`
	DigestAlg string `json:"digest_alg"`
	Digest    string `json:"digest"`
}

// capsuleReferences decodes a capsule's references[] and reports whether any
// entry carries citation_purpose, with its value.
func capsuleReferences(t *testing.T, capsule []byte) ([]sealedReference, []string) {
	t.Helper()
	var doc struct {
		References []map[string]json.RawMessage `json:"references"`
	}
	if err := json.Unmarshal(capsule, &doc); err != nil {
		t.Fatal(err)
	}
	var refs []sealedReference
	var purposes []string
	for _, raw := range doc.References {
		if purpose, ok := raw["citation_purpose"]; ok {
			var p string
			_ = json.Unmarshal(purpose, &p)
			purposes = append(purposes, p)
		}
		entry, _ := json.Marshal(raw)
		var ref sealedReference
		if err := json.Unmarshal(entry, &ref); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	return refs, purposes
}

// A link type is an Evidence Layer link type, not an AAC citation purpose:
// no record capsule carries one in references[].citation_purpose. The link
// type stays in the committed header's links[], and each link is still a
// references[] entry by digest.
func TestRecordCapsulesCarryNoLinkTypeAsCitationPurpose(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "links", 2)
	book := f.open(t)
	records := appendEveryLinkType(t, book)
	seen := map[LinkType]bool{}
	for _, record := range records {
		stored, err := book.store.GetRecord(ctx, record.RecordID)
		if err != nil {
			t.Fatal(err)
		}
		refs, purposes := capsuleReferences(t, stored.Capsule)
		if len(purposes) != 0 {
			t.Fatalf("record %s (%s) carries citation_purpose %q; a link type is not a citation purpose", record.RecordID, record.Header.RecordType, purposes)
		}
		var header Header
		if err := json.Unmarshal(stored.Header, &header); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(header.Links, record.Header.Links) || len(refs) != len(header.Links) {
			t.Fatalf("record %s: %d references for header links %v", record.RecordID, len(refs), header.Links)
		}
		for i, link := range header.Links {
			seen[link.Type] = true
			if refs[i] != (sealedReference{Type: "agent-action-capsule", DigestAlg: "SHA-256", Digest: link.Target}) {
				t.Fatalf("record %s reference %d = %+v, want the %s link's target %s", record.RecordID, i, refs[i], link.Type, link.Target)
			}
		}
	}
	for _, linkType := range LinkTypes() {
		if !seen[linkType] {
			t.Fatalf("link type %s was not exercised", linkType)
		}
	}
	got, err := book.Get(ctx, records[len(records)-1].RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Header.LinksTo(Adjudicates)) != 2 || len(got.Header.LinksTo(Cites)) != 1 {
		t.Fatalf("link types are not read back from the header: %+v", got.Header.Links)
	}
}

// Records sealed before this change carry the link type in
// references[].citation_purpose. They keep verifying: the capsule signature
// covers what was sealed, an unregistered citation_purpose is informational
// to a verifier (never a rejection), and evidencebook reads link types only
// from the header. testdata/legacy-citation-purpose holds a book and a
// bundle written by test/interop/capsules against the previous writer
// (book.go at db4e312).
func TestLegacyCitationPurposeRecordsStillVerify(t *testing.T) {
	ctx := context.Background()
	legacy := filepath.Join("testdata", "legacy-citation-purpose")
	data, err := os.ReadFile(filepath.Join(legacy, "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBundle(data)
	if err != nil {
		t.Fatalf("legacy bundle: %v", err)
	}
	if err := verified.FullyVerified(); err != nil {
		t.Fatalf("legacy bundle: %v", err)
	}

	f := newFixture(t, "links", 2)
	for _, name := range []string{"cll.jsonl", filepath.Join("records", "records.jsonl"), filepath.Join("records", "checkpoints.jsonl")} {
		raw, err := os.ReadFile(filepath.Join(legacy, "book", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(f.dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	book := f.open(t) // decodes and verifies every stored capsule
	stored, err := book.store.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var legacyPurposes []string
	for _, record := range stored {
		_, purposes := capsuleReferences(t, record.Capsule)
		legacyPurposes = append(legacyPurposes, purposes...)
	}
	for _, linkType := range LinkTypes() {
		if !slices.Contains(legacyPurposes, string(linkType)) {
			t.Fatalf("the legacy fixture does not carry link type %s as a citation_purpose: %q", linkType, legacyPurposes)
		}
	}
	adjudications, err := book.Query(ctx, Filter{RecordType: "adjudication"})
	if err != nil {
		t.Fatal(err)
	}
	if len(adjudications) != 1 || len(adjudications[0].Header.LinksTo(Adjudicates)) != 2 || len(adjudications[0].Header.LinksTo(Cites)) != 1 {
		t.Fatalf("legacy link types are not read from the header: %+v", adjudications)
	}
	last := adjudications[0].RecordID
	next := mustAppend(t, book, Entry{RecordType: "claim", EpistemicType: ProducerClaim, SubjectRef: "order-17", Links: []Link{{Cites, last}}})
	if _, purposes := capsuleReferences(t, storedCapsule(t, book, next.RecordID)); len(purposes) != 0 {
		t.Fatalf("a record appended to a legacy book carries citation_purpose %q", purposes)
	}
	bundle, err := book.Bundle(ctx, BundleRequest{Root: next.RecordID})
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatalf("bundle mixing legacy and new records: %v", err)
	}
	if err := mixed.FullyVerified(); err != nil {
		t.Fatalf("bundle mixing legacy and new records: %v", err)
	}
}

func storedCapsule(t *testing.T, book *Book, recordID string) []byte {
	t.Helper()
	stored, err := book.store.GetRecord(context.Background(), recordID)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Capsule
}
