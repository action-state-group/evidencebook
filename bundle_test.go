// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/disclosure"
	aacverify "github.com/action-state-group/agent-action-capsule/go/verify"
)

// linkedBook is three records: an observation, a claim citing it, and an
// adjudication of the claim, plus an unrelated observation after them.
func linkedBook(t *testing.T) (*Book, []Record) {
	t.Helper()
	book := newFixture(t, "book-a", 1).open(t)
	observed := mustAppend(t, book, observation("order-17", `{"status":"approved"}`))
	claim := mustAppend(t, book, Entry{RecordType: "claim", EpistemicType: ProducerClaim, SubjectRef: "order-17", Links: []Link{{Type: Cites, Target: observed.RecordID}}})
	ruling := mustAppend(t, book, Entry{RecordType: "ruling", EpistemicType: Adjudication, SubjectRef: "order-17", Links: []Link{{Type: Adjudicates, Target: claim.RecordID}, {Type: Cites, Target: observed.RecordID}}})
	other := mustAppend(t, book, observation("order-18", `{"status":"held"}`))
	return book, []Record{observed, claim, ruling, other}
}

func aacVerify(t *testing.T, data []byte) aacbundle.VerificationResult {
	t.Helper()
	decoded, err := decodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return aacbundle.VerifyBundle(decoded)
}

// TestBundleRoundTripVerifiesWithNeutralVerifier is the DONE round trip: a
// record sealed here is bundled, and the neutral AAC verifiers (bundle and
// per-capsule Class 1) accept it with the checkpoint authenticated, not
// merely asserted by the producer.
func TestBundleRoundTripVerifiesWithNeutralVerifier(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	bundle, err := book.Bundle(ctx, BundleRequest{Root: records[2].RecordID, Payloads: PayloadsSelected})
	if err != nil {
		t.Fatal(err)
	}
	result := aacVerify(t, bundle.JSON)
	for name, claim := range map[string]aacbundle.ClaimResult{"graph": result.GraphClosure, "interval": result.IntervalCoverage, "membership": result.PerRecordMembership} {
		if claim.Status != "pass" || len(claim.Findings) != 0 {
			t.Fatalf("%s claim = %s %v; want pass with an authenticated checkpoint", name, claim.Status, claim.Findings)
		}
	}
	if result.BundleDigest == nil || *result.BundleDigest != bundle.Digest {
		t.Fatal("bundle digest differs from the verifier's")
	}
	for id, capsuleResult := range result.CapsuleResults {
		if !capsuleResult.OK {
			t.Fatalf("capsule %s fails Class 1: %+v", id, capsuleResult.Findings)
		}
	}
	matched := 0
	for _, d := range result.Disclosures {
		if d.Member == HeaderMember && d.Status == disclosure.Match {
			matched++
		}
	}
	// selected: the ruling's closure is the ruling, the claim and the observation.
	if matched != 3 {
		t.Fatalf("want 3 disclosed headers to match their commitments, got %d: %+v", matched, result.Disclosures)
	}
	for _, id := range []string{records[0].RecordID, records[1].RecordID, records[2].RecordID} {
		if !slices.Contains(bundle.Disclosure.Header.LinksTo(Cites), id) {
			t.Fatalf("disclosure record does not cite disclosed record %s", id)
		}
	}
	if slices.Contains(bundle.Disclosure.Header.LinksTo(Cites), records[3].RecordID) {
		t.Fatal("disclosure record cites a record whose header was not disclosed")
	}
	if bundle.Disclosure.Header.EpistemicType != ProducerClaim {
		t.Fatal("a disclosure record is the store's own claim about what it revealed")
	}

	verified, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.AnchorAuthenticated || verified.Payloads[records[0].Header.PayloadCommitments[0]] == nil {
		t.Fatalf("VerifyBundle did not authenticate the anchor or carry the selected payload: %+v", verified)
	}
}

func TestBundleTamperingIsCaughtByNeutralVerifier(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	bundle, err := book.Bundle(ctx, BundleRequest{Root: records[1].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	var wire bundleWire
	if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
		t.Fatal(err)
	}
	// Drop an interior record: its membership is no longer bound.
	dropped := wire
	dropped.Records = slices.Delete(slices.Clone(wire.Records), 1, 2)
	data, err := json.Marshal(dropped)
	if err != nil {
		t.Fatal(err)
	}
	if aacVerify(t, data).PerRecordMembership.Status != "fail" {
		t.Fatal("a dropped interior record must fail per-record membership")
	}
	if _, err := VerifyBundle(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("VerifyBundle must refuse a bundle the neutral verifier fails, got %v", err)
	}
	// Swap a disclosed header for another record's header.
	swapped := wire
	swapped.Disclosures = map[string]map[string]json.RawMessage{records[1].RecordID: wire.Disclosures[records[0].RecordID]}
	if data, err = json.Marshal(swapped); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range verified.Records {
		if r.RecordID == records[1].RecordID && r.HeaderVerified {
			t.Fatal("a header that does not hash to the record's commitment was accepted")
		}
	}
}

func TestBundleSuppressedHeaderIsWithheldWithDigest(t *testing.T) {
	book, records := linkedBook(t)
	bundle, err := book.Bundle(context.Background(), BundleRequest{Root: records[1].RecordID, Suppress: []string{HeaderMember}})
	if err != nil {
		t.Fatal(err)
	}
	var statement DisclosureStatement
	if err := json.Unmarshal(bundle.Disclosure.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	stored, err := book.storedLocked(context.Background(), records[1].RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(statement.Withheld, WithheldItem{RecordID: records[1].RecordID, Member: HeaderMember, Digest: digestBytes(stored.Header)}) {
		t.Fatalf("suppressed header not listed as withheld with its digest: %+v", statement.Withheld)
	}
	withheld := 0
	for _, d := range aacVerify(t, bundle.JSON).Disclosures {
		if d.Status == "withheld" {
			withheld++
		}
		if d.Status == disclosure.Match {
			t.Fatal("a suppressed header was disclosed")
		}
	}
	if withheld == 0 {
		t.Fatal("the neutral verifier must report suppressed headers as withheld")
	}
}

func TestBundleDeclaresMissingCitationTargets(t *testing.T) {
	book := newFixture(t, "book-a", 1).open(t)
	foreign := digestBytes([]byte("a record held by someone else"))
	root := mustAppend(t, book, Entry{RecordType: "reply", EpistemicType: ProducerClaim, Links: []Link{{Type: Cites, Target: foreign}}})
	bundle, err := book.Bundle(context.Background(), BundleRequest{Root: root.RecordID})
	if err != nil {
		t.Fatal(err)
	}
	if got := aacVerify(t, bundle.JSON).GraphClosure; got.Status != "withheld" || !slices.Contains(got.Findings, "declared_incomplete") {
		t.Fatalf("a citation the book does not hold must be declared missing, got %+v", got)
	}
}

func TestBundleWithholdsUnavailablePayloads(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	digest := records[0].Header.PayloadCommitments[0]
	if _, err := book.SetRetention(ctx, records[0].RecordID, digest, Deleted, "retention period ended"); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Bundle(ctx, BundleRequest{Root: records[0].RecordID, Payloads: PayloadsAll}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("payloads=all over a deleted payload is a false claim and must be refused, got %v", err)
	}
	bundle, err := book.Bundle(ctx, BundleRequest{Root: records[0].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	var statement DisclosureStatement
	if err := json.Unmarshal(bundle.Disclosure.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(statement.Withheld, WithheldItem{RecordID: records[0].RecordID, PayloadCommitment: digest, Digest: digest, RetentionState: Deleted}) {
		t.Fatalf("deleted payload not reported as withheld-with-digest: %+v", statement.Withheld)
	}
	verified, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, carried := verified.Payloads[digest]; carried {
		t.Fatal("deleted payload bytes were carried")
	}
}

func TestBundleRefusesRecordsTheAnchorDoesNotCover(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	later := mustAppend(t, book, observation("order-19", "late"))
	if _, err := book.Bundle(ctx, BundleRequest{Root: later.RecordID, At: &anchor}); !errors.Is(err, ErrNotCovered) {
		t.Fatalf("want ErrNotCovered, got %v", err)
	}
	if _, err := book.Bundle(ctx, BundleRequest{Root: records[0].RecordID, At: &anchor}); err != nil {
		t.Fatalf("an older pinned anchor must still produce a bundle: %v", err)
	}
}

func TestBundleCheckpointCOSEIsTheSignedStatement(t *testing.T) {
	book, records := linkedBook(t)
	bundle, err := book.Bundle(context.Background(), BundleRequest{Root: records[0].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	var wire bundleWire
	if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
		t.Fatal(err)
	}
	statement, err := base64.RawURLEncoding.DecodeString(wire.Checkpoint.COSE)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := checkpointFromStatement(statement)
	if err != nil || parsed.ID != bundle.Anchor.ID {
		t.Fatalf("bundle checkpoint is not the anchor's signed statement: %v", err)
	}
	decoded, err := decodeJSON(wire.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if !aacverify.Verify(decoded, nil, nil).OK {
		t.Fatal("record capsule fails Class 1")
	}
}

// The bundle's checkpoint states the log id its statement signs, as the
// completeness certificate does: a verifier that holds the checkpoint's own
// copy of the log id to the signed one (an older capsulectl, among others)
// finds it there.
func TestBundleCheckpointStatesTheSignedLogID(t *testing.T) {
	book, records := linkedBook(t)
	bundle, err := book.Bundle(context.Background(), BundleRequest{Root: records[0].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Certificate struct {
			LogID string `json:"log_id"`
		} `json:"completeness_certificate"`
		Checkpoint struct {
			LogID string `json:"log_id"`
			COSE  string `json:"cose"`
		} `json:"checkpoint"`
	}
	if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
		t.Fatal(err)
	}
	statement, err := base64.RawURLEncoding.DecodeString(wire.Checkpoint.COSE)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := checkpointFromStatement(statement)
	if err != nil {
		t.Fatal(err)
	}
	if wire.Checkpoint.LogID == "" || wire.Checkpoint.LogID != signed.LogID || wire.Checkpoint.LogID != wire.Certificate.LogID {
		t.Fatalf("checkpoint log_id = %q; want the signed %q (certificate %q)", wire.Checkpoint.LogID, signed.LogID, wire.Certificate.LogID)
	}
	verified, err := VerifyBundle(bundle.JSON)
	if err != nil || verified.Anchor.LogID != signed.LogID {
		t.Fatalf("VerifyBundle: anchor log_id %q, err %v", verified.Anchor.LogID, err)
	}
}

// A checkpoint whose stated log id differs from the one its statement signs
// is refused; one that states none (a bundle from before it was stated) is
// read as before.
func TestVerifyBundleHoldsTheCheckpointLogIDToTheSignedOne(t *testing.T) {
	book, records := linkedBook(t)
	bundle, err := book.Bundle(context.Background(), BundleRequest{Root: records[0].RecordID})
	if err != nil {
		t.Fatal(err)
	}
	edit := func(change func(checkpoint map[string]any)) []byte {
		var tree map[string]any
		if err := json.Unmarshal(bundle.JSON, &tree); err != nil {
			t.Fatal(err)
		}
		change(tree["checkpoint"].(map[string]any))
		out, err := json.Marshal(tree)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, err := VerifyBundle(edit(func(cp map[string]any) { cp["log_id"] = "another-log" })); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a checkpoint log_id other than the signed one: err = %v; want ErrInvalid", err)
	}
	if _, err := VerifyBundle(edit(func(cp map[string]any) { delete(cp, "log_id") })); err != nil {
		t.Fatalf("a checkpoint that states no log_id: %v", err)
	}
}
