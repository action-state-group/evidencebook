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
	for _, leaked := range []string{"CONDUCT-ROW-7f3a", "CONDUCT-CLAIM-never-leaves-the-owner", "asg/everyday-rules"} {
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

// The pack is found in any claim's contract_ref, not only in the subject.
func TestBundleRefusesAResultWhoseLaterClaimCitesAConductPack(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct-claim", 10).open(t)
	conduct := mustAppend(t, book, Entry{
		RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "asg/seller@2",
		Statement: []byte(`{"claims":[{"contract_ref":"asg/seller@2"},{"claim_id":"LATE-CLAIM","contract_ref":"asg/everyday-rules@1"}]}`),
	})
	req := BundleRequest{Root: conduct.RecordID, ConductPacks: []string{"asg/everyday-rules"}}
	if _, err := book.Bundle(ctx, req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v; want ErrInvalid", err)
	}
	req.OwnerCopy = true
	if _, err := book.Bundle(ctx, req); err != nil {
		t.Fatalf("owner copy: %v", err)
	}
}

// A Result the caller sealed carries its header, and so its claims, inside
// its capsule; every capsule of the interval travels, so it is refused even
// when the bundle's root is an earlier record and does not select it.
func TestBundleRefusesAnInlineConductResultInTheInterval(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "conduct-inline", 1)
	book := f.open(t)
	earlier := mustAppend(t, book, observation("s", "x"))
	payload := `{"order":17}`
	_, err := book.AppendCapsule(ctx, Entry{
		RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "asg/everyday-rules@1",
		Statement: []byte(`{"claims":[{"claim_id":"INLINE-ROW","contract_ref":"asg/everyday-rules@1"}]}`),
	}, boundPayload(payload), ownSealer(t, 1, payload, nil))
	if err != nil {
		t.Fatal(err)
	}
	req := BundleRequest{Root: earlier.RecordID, ConductPacks: []string{"asg/everyday-rules"}}
	if _, err := book.Bundle(ctx, req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v; want ErrInvalid", err)
	}
	req.OwnerCopy = true
	if _, err := book.Bundle(ctx, req); err != nil {
		t.Fatalf("owner copy: %v", err)
	}
}

// A handover goes to a recipient, so it takes the same policy.
func TestStandardHistoryBundleRefusesAConductResult(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct-handover", 12).open(t)
	mustAppend(t, book, Entry{
		RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "asg/everyday-rules@1",
		Statement: []byte(`{"claims":[{"claim_id":"HANDOVER-ROW","contract_ref":"asg/everyday-rules@1"}]}`),
	})
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.StandardHistoryBundle(ctx, cp, StandardHistoryBundleRequest{ConductPacks: []string{"asg/everyday-rules"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v; want ErrInvalid", err)
	}
	if _, err := book.StandardHistoryBundle(ctx, cp, StandardHistoryBundleRequest{}); err != nil {
		t.Fatalf("without a conduct list: %v", err)
	}
}

// A pack id with its version could never match a Result's reference, so it
// is refused instead of protecting nothing.
func TestConductPacksTakeBareIds(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct-ids", 13).open(t)
	r := mustAppend(t, book, observation("s", "x"))
	for _, bad := range []string{"asg/everyday-rules@1", ""} {
		if _, err := book.Bundle(ctx, BundleRequest{Root: r.RecordID, ConductPacks: []string{bad}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q: err = %v; want ErrInvalid", bad, err)
		}
	}
}

// A handover that Disclose keeps the conduct Result out of travels digest-only
// and carries none of its claim text.
func TestStandardHistoryBundleWithheldConductResultCarriesNoClaim(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct-withheld", 14).open(t)
	mustAppend(t, book, observation("s", "x"))
	mustAppend(t, book, Entry{
		RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "asg/everyday-rules@1",
		Statement: []byte(`{"claims":[{"claim_id":"WITHHELD-ROW","contract_ref":"asg/everyday-rules@1"}]}`),
	})
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := book.StandardHistoryBundle(ctx, cp, StandardHistoryBundleRequest{
		ConductPacks: []string{"asg/everyday-rules"},
		Disclose:     func(r Record) bool { return r.Header.RecordType != RecordTypeEvidenceResult },
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bundle.JSON, []byte("WITHHELD-ROW")) {
		t.Fatal("the handover carries a withheld conduct Result's claim")
	}
}

// A tick whose set holds a conduct Result is refused before any book of the
// set is written, whichever book is built first.
func TestAConductSetAppendsNothing(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	plain := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, plain, observation("a", "1"))
	private := mustOpenBook(t, shelf, "deal/b")
	mustAppend(t, private, Entry{
		RecordType: RecordTypeEvidenceResult, EpistemicType: DerivedMetric, SubjectRef: "asg/everyday-rules@1",
		Statement: []byte(`{"claims":[{"contract_ref":"asg/everyday-rules@1"}]}`),
	})
	mustTick(t, c, tickTime)
	anchor, err := c.StandardHistoryAnchor(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := len(plain.log) + len(private.log)
	if _, err := c.StandardHistoryBundles(ctx, anchor, StandardHistoryBundleRequest{ConductPacks: []string{"asg/everyday-rules"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v; want ErrInvalid", err)
	}
	if got := len(plain.log) + len(private.log); got != before {
		t.Fatalf("the set grew from %d to %d records though it was refused", before, got)
	}
}
