package evidencebook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

// holds reports whether data carries marker as it travels: plain, in base64
// (a payload in a bundle) or in hex (a record body in a reply).
func holds(data []byte, marker string) bool {
	for _, form := range [][]byte{
		[]byte(marker),
		[]byte(base64.StdEncoding.EncodeToString([]byte(marker))),
		[]byte(base64.RawURLEncoding.EncodeToString([]byte(marker))),
		[]byte(hex.EncodeToString([]byte(marker))),
	} {
		if bytes.Contains(data, form) {
			return true
		}
	}
	return false
}

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

// The rule is about the pack, not the record type (PRD v5 §13): an
// observation or an evidence_request whose subject_ref names a conduct pack is
// refused as a Result is, as root, by closure or by Include, and no bundle
// holds the row text.
func TestBundleRefusesAnyRecordTypeNamingAConductPack(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct-any-type", 13).open(t)
	deal := mustAppend(t, book, observation("asg/seller@2", "deal fact"))
	private := []string{"asg/everyday-rules"}
	for name, entry := range map[string]Entry{
		"observation": {
			RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "asg/everyday-rules@1",
			Payloads: [][]byte{[]byte("CONDUCT-ROW-TEXT-observation")},
		},
		"evidence_request": {
			RecordType: "evidence_request", EpistemicType: ProducerClaim, SubjectRef: "asg/everyday-rules@1",
			Statement: []byte(`{"text":"CONDUCT-ROW-TEXT-request"}`),
		},
	} {
		rec := mustAppend(t, book, entry)
		above := mustAppend(t, book, Entry{
			RecordType: "note", EpistemicType: ObservedEvent, SubjectRef: "n-" + name, Links: []Link{{Type: Cites, Target: rec.RecordID}},
			Payloads: [][]byte{[]byte("cites it")},
		})
		for how, req := range map[string]BundleRequest{
			"root":    {Root: rec.RecordID},
			"closure": {Root: above.RecordID},
			"include": {Root: deal.RecordID, Include: []string{rec.RecordID}},
		} {
			req.ConductPacks = private
			if _, err := book.Bundle(ctx, req); !errors.Is(err, ErrInvalid) {
				t.Fatalf("%s %s: err = %v; want ErrInvalid", name, how, err)
			}
		}
		own, err := book.Bundle(ctx, BundleRequest{Root: rec.RecordID, ConductPacks: private, OwnerCopy: true})
		if err != nil {
			t.Fatalf("%s: the owner's own bundle: %v", name, err)
		}
		if !bytes.Contains(own.JSON, []byte("everyday-rules")) {
			t.Fatalf("%s: the owner's bundle should carry the record", name)
		}
		// The detector must see what the owner's copy carries (the payload
		// travels base64), or a clean counterparty bundle proves nothing.
		if marker := "CONDUCT-ROW-TEXT-" + name; name == "observation" && !holds(own.JSON, marker) {
			t.Fatalf("%s: the leak check does not find %q in the owner's bundle", name, marker)
		}
	}
	bundle, err := book.Bundle(ctx, BundleRequest{Root: deal.RecordID, ConductPacks: private})
	if err != nil {
		t.Fatalf("a deal record alongside conduct ones: %v", err)
	}
	for _, leaked := range []string{"CONDUCT-ROW-TEXT-observation", "CONDUCT-ROW-TEXT-request", "everyday-rules"} {
		if holds(bundle.JSON, leaked) {
			t.Fatalf("the counterparty bundle carries %q", leaked)
		}
	}
}

// A record that is not a Result is also found where a Result is: a claim
// whose contract_ref names the pack, and a caller-sealed record with its
// header inside its capsule anywhere in the carried interval.
func TestBundleRefusesANonResultByClaimAndInline(t *testing.T) {
	ctx := context.Background()
	private := []string{"asg/everyday-rules"}

	book := newFixture(t, "conduct-claim-any", 16).open(t)
	byClaim := mustAppend(t, book, Entry{
		RecordType: "evidence_request", EpistemicType: ProducerClaim, SubjectRef: "asg/seller@2",
		Statement: []byte(`{"claims":[{"contract_ref":"asg/everyday-rules@1"}]}`),
	})
	if _, err := book.Bundle(ctx, BundleRequest{Root: byClaim.RecordID, ConductPacks: private}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("claim contract_ref: err = %v; want ErrInvalid", err)
	}

	inline := newFixture(t, "conduct-inline-any", 1)
	book = inline.open(t)
	earlier := mustAppend(t, book, observation("s", "x"))
	payload := `{"order":17}`
	if _, err := book.AppendCapsule(ctx, Entry{
		RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "asg/everyday-rules@1",
		Statement: []byte(`{"text":"INLINE-ROW"}`),
	}, boundPayload(payload), ownSealer(t, 1, payload, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Bundle(ctx, BundleRequest{Root: earlier.RecordID, ConductPacks: private}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("inline observation in the interval: err = %v; want ErrInvalid", err)
	}
	if _, err := book.Bundle(ctx, BundleRequest{Root: earlier.RecordID, ConductPacks: private, OwnerCopy: true}); err != nil {
		t.Fatalf("owner copy: %v", err)
	}
}

// A handover takes the same rule for a record that is not a Result.
func TestStandardHistoryBundleRefusesAnyRecordTypeNamingAConductPack(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct-handover-any", 14).open(t)
	mustAppend(t, book, Entry{
		RecordType: "evidence_request", EpistemicType: ProducerClaim, SubjectRef: "asg/everyday-rules@1",
		Statement: []byte(`{"text":"CONDUCT-ROW-TEXT-request"}`),
	})
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.StandardHistoryBundle(ctx, cp, StandardHistoryBundleRequest{ConductPacks: []string{"asg/everyday-rules"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v; want ErrInvalid", err)
	}
}

// book respond (evidence-interop) serves a record the book sealed only as a
// digest-only capsule, so a requester naming a conduct observation or
// evidence_request, or the correlation key of its subject_ref, gets neither
// the row text nor a pack id beyond the key it sent. A record its caller
// sealed is served whole, header included, so RespondOptions.ConductPacks
// declines it (evidencebook#418, #417).
func TestRespondServesNoConductRowText(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "conduct-respond", 15).open(t)
	obs := mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "asg/everyday-rules@1", CounterpartyRef: "peer", Payloads: [][]byte{[]byte("CONDUCT-ROW-TEXT-observation")}})
	req := mustAppend(t, book, Entry{RecordType: "evidence_request", EpistemicType: ProducerClaim, SubjectRef: "asg/everyday-rules@1", CounterpartyRef: "peer", Statement: []byte(`{"text":"CONDUCT-ROW-TEXT-request"}`)})
	payload := `{"order":17}`
	inline, err := book.AppendCapsule(ctx, Entry{
		RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "asg/everyday-rules@1",
		Statement: []byte(`{"text":"CONDUCT-ROW-TEXT-inline"}`),
	}, boundPayload(payload), ownSealer(t, 15, payload, nil))
	if err != nil {
		t.Fatal(err)
	}
	// served is everything a reply carries: the message, the artifact and the
	// body of each record in a record set.
	served := func(subject Subject, packs []string) ([]byte, string) {
		reply, st := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: subject, Coverage: fresh()}),
			RespondOptions{AuthenticatedRequester: "peer", Policy: policyWith("peers"), ConductPacks: packs})
		if reply.Envelope == nil {
			return nil, st.Reason
		}
		all := append([]byte(nil), reply.Message.Octets...)
		artifact := mustEnvelope(t, reply).Artifact
		all = append(all, artifact...)
		var set struct {
			Records []struct {
				Body string `json:"body"`
			} `json:"records"`
		}
		if json.Unmarshal(artifact, &set) == nil {
			for _, r := range set.Records {
				body, _ := hex.DecodeString(r.Body)
				all = append(all, body...)
			}
		}
		return all, ""
	}
	private := []string{"asg/everyday-rules"}

	// The control: the check finds a caller-sealed record's text when it is served.
	if out, reason := served(Subject{Kind: SubjectRecord, Digest: inline.RecordID}, nil); out == nil || !holds(out, "CONDUCT-ROW-TEXT-inline") {
		t.Fatalf("control: a caller-sealed record without ConductPacks should be served whole (declined %q)", reason)
	}
	if out, reason := served(Subject{Kind: SubjectRecord, Digest: inline.RecordID}, private); out != nil || reason != ReasonPolicyDeclined {
		t.Fatalf("a caller-sealed conduct record: served=%v reason=%q; want declined", out != nil, reason)
	}
	if out, reason := served(Subject{Kind: SubjectCorrelation, Correlation: "asg/everyday-rules@1"}, private); out != nil || reason != ReasonPolicyDeclined {
		t.Fatalf("a correlation holding a caller-sealed conduct record: served=%v reason=%q; want declined", out != nil, reason)
	}
	for _, subject := range []Subject{
		{Kind: SubjectRecord, Digest: obs.RecordID},
		{Kind: SubjectRecord, Digest: req.RecordID},
	} {
		out, reason := served(subject, private)
		if out == nil {
			t.Fatalf("%s: declined %q; a digest-only capsule is served", subject.Kind, reason)
		}
		for _, leaked := range []string{"CONDUCT-ROW-TEXT-observation", "CONDUCT-ROW-TEXT-request", "everyday-rules"} {
			if holds(out, leaked) {
				t.Fatalf("%s: the reply carries %q", subject.Kind, leaked)
			}
		}
	}
}
