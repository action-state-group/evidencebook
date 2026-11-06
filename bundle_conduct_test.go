package evidencebook

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// A conduct pack never appears in a scoped bundle sent to a counterparty
// (Terms Pack Studio PRD v5 §13). The book holds a conduct Result and a deal
// Result; a bundle for anyone but the owner is refused when it would disclose
// the conduct one, whether as the root, by closure or by Include, and the
// owner's own copy carries it.
func TestBundleRefusesAConductResultForACounterparty(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct", 9).open(t)
	seed := mustAppend(t, book, observation("everyday-rules@1", "a daily report"))
	link := []Link{{Type: Cites, Target: seed.RecordID}}
	conduct := mustAppend(t, book, Entry{
		RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "asg/everyday-rules@1", Links: link,
		Statement: []byte(`{"claims":[{"claim_id":"CONDUCT-ROW-7f3a","contract_ref":"asg/everyday-rules@1","text":"CONDUCT-CLAIM-never-leaves-the-owner"}]}`),
	})
	deal := mustAppend(t, book, Entry{
		RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "asg/seller@2", Links: link,
		Statement: []byte(`{"claims":[{"claim_id":"DEAL-ROW-1","contract_ref":"asg/seller@2","text":"deal-claim"}]}`),
	})
	above := mustAppend(t, book, Entry{
		RecordType: "note", EpistemicType: ObservedEvent, SubjectRef: "n", Links: []Link{{Type: Cites, Target: conduct.RecordID}},
		Payloads: [][]byte{[]byte("cites the conduct result")},
	})
	private := []string{"asg/everyday-rules"}

	for name, req := range map[string]BundleRequest{
		"root":    {Root: conduct.RecordID},
		"closure": {Root: above.RecordID},
		"include": {Root: deal.RecordID, Include: []string{conduct.RecordID}},
	} {
		req.ConductPacks = private
		if _, err := book.Bundle(ctx, req); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err = %v; want ErrInvalid naming the conduct pack", name, err)
		}
	}

	bundle, err := book.Bundle(ctx, BundleRequest{Root: deal.RecordID, ConductPacks: private})
	if err != nil {
		t.Fatalf("a deal Result alongside a conduct one in the book: %v", err)
	}
	for _, leaked := range []string{"CONDUCT-ROW-7f3a", "CONDUCT-CLAIM-never-leaves-the-owner"} {
		if bytes.Contains(bundle.JSON, []byte(leaked)) {
			t.Fatalf("the counterparty bundle carries %q", leaked)
		}
	}

	own, err := book.Bundle(ctx, BundleRequest{Root: conduct.RecordID, ConductPacks: private, OwnerCopy: true})
	if err != nil {
		t.Fatalf("the owner's own bundle: %v", err)
	}
	if !bytes.Contains(own.JSON, []byte("CONDUCT-ROW-7f3a")) {
		t.Fatal("the owner's bundle should carry the conduct Result")
	}
}
