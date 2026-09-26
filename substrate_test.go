// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

type fakeTransparencyService struct{ registered []string }

func (f *fakeTransparencyService) Register(_ context.Context, recordID string) (Receipt, error) {
	f.registered = append(f.registered, recordID)
	return Receipt{ServiceID: "ts.example", TreeSize: uint64(1000 + len(f.registered)), LeafIndex: uint64(999 + len(f.registered)), Bytes: []byte("receipt:" + recordID)}, nil
}

// TestBookRunsWithoutCLL: the same book, over a substrate that only holds
// transparency-service receipts, records, answers and bundles. What it cannot
// do (prove an interval over a log it does not keep) it says it cannot do.
func TestBookRunsWithoutCLL(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-s", 7)
	cfg := f.config(t)
	if err := cfg.Substrate.Release(); err != nil {
		t.Fatal(err)
	}
	service := &fakeTransparencyService{}
	substrate, err := NewSCITTReceiptSubstrate("ts.example", service)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Substrate = substrate
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = book.Release() }()
	if _, info := book.Info(); info.Kind != SubstrateKindSCITTReceipt {
		t.Fatalf("book states substrate %q", info.Kind)
	}
	observed := mustAppend(t, book, observation("order-17", "x"))
	claim := mustAppend(t, book, Entry{RecordType: "claim", EpistemicType: ProducerClaim, SubjectRef: "order-17", Links: []Link{{Type: Cites, Target: observed.RecordID}}})
	if !slices.Contains(service.registered, claim.RecordID) {
		t.Fatal("records must be registered with the transparency service")
	}
	found, err := book.Query(ctx, Filter{SubjectRef: "order-17"})
	if err != nil || len(found) != 2 {
		t.Fatalf("query over the receipt substrate: %d (%v)", len(found), err)
	}
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Kind != SubstrateKindSCITTReceipt || cp.ID != "ts.example:1004" {
		t.Fatalf("checkpoint identity must come from the service's coordinates, got %+v", cp)
	}

	bundle, err := book.Bundle(ctx, BundleRequest{Root: claim.RecordID})
	if err != nil {
		t.Fatal(err)
	}
	result := aacVerify(t, bundle.JSON)
	if result.GraphClosure.Status != "pass" {
		t.Fatalf("graph closure over a receipt-only bundle: %+v", result.GraphClosure)
	}
	if result.IntervalCoverage.Status != "withheld" || !slices.Contains(result.IntervalCoverage.Findings, "completeness_evidence_absent") {
		t.Fatalf("a receipt-only bundle must not claim interval completeness: %+v", result.IntervalCoverage)
	}
	var wire bundleWire
	if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
		t.Fatal(err)
	}
	var inclusion map[string]InclusionEvidence
	if err := json.Unmarshal(wire.Extensions[ExtensionInclusion], &inclusion); err != nil {
		t.Fatal(err)
	}
	if got := inclusion[claim.RecordID]; got.Kind != "scitt-receipt" {
		t.Fatalf("claim carries no receipt: %+v", inclusion)
	}
	if _, err := book.commitments.ProveConsistency(ctx, cp, cp); !errors.Is(err, ErrUnsupported) {
		t.Fatal("a receipt-only substrate must report consistency as unsupported, not fabricate it")
	}
	peers := DefaultSharePolicy()
	peers.HistorySegments = "peers"
	resp, _, err := book.Respond(ctx, requestBytes(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: observed.RecordID}, Coverage: fresh()}), RespondOptions{Policy: peers})
	if err != nil {
		t.Fatal(err)
	}
	mustArtifact(t, resp)
}

func TestCLLConsistencyBetweenCheckpoints(t *testing.T) {
	ctx := context.Background()
	book, _ := linkedBook(t)
	older, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, book, observation("order-20", "later"))
	newer, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if newer.Entries <= older.Entries || newer.ID == older.ID {
		t.Fatalf("checkpoints did not advance: %s -> %s", older.ID, newer.ID)
	}
	proof, err := book.commitments.ProveConsistency(ctx, older, newer)
	if err != nil || proof.Kind != "cll-mmr-consistency" {
		t.Fatalf("consistency proof: %v", err)
	}
	history, err := book.Checkpoints(ctx)
	if err != nil || len(history) < 2 {
		t.Fatalf("checkpoint history not kept: %d (%v)", len(history), err)
	}
	if _, err := OpenCLL(filepath.Join(t.TempDir(), "x.jsonl"), "bad id with spaces", seededKey(1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid log id accepted: %v", err)
	}
}

func TestRetentionLifecycle(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	record := mustAppend(t, book, observation("s", "sensitive"))
	digest := record.Header.PayloadCommitments[0]

	if data, err := book.ResolvePayload(ctx, record.RecordID, digest); err != nil || string(data) != "sensitive" {
		t.Fatalf("available payload did not resolve: %v", err)
	}
	if _, err := book.SetRetention(ctx, record.RecordID, digest, Withheld, "counterparty request"); err != nil {
		t.Fatal(err)
	}
	var unavailable *UnavailableError
	if _, err := book.ResolvePayload(ctx, record.RecordID, digest); !errors.As(err, &unavailable) || unavailable.State != Withheld {
		t.Fatalf("withheld payload: %v", err)
	}
	if _, err := book.SetRetention(ctx, record.RecordID, digest, LegalHold, "hold"); err != nil {
		t.Fatal(err)
	}
	if _, err := book.SetRetention(ctx, record.RecordID, digest, Deleted, "expiry"); !errors.Is(err, ErrLegalHold) {
		t.Fatalf("deletion under legal hold: %v", err)
	}
	if _, err := book.ResolvePayload(ctx, record.RecordID, digest); err != nil {
		t.Fatalf("a legal hold does not itself change resolvability: %v", err)
	}
	if _, err := book.SetRetention(ctx, record.RecordID, digest, Available, "hold lifted"); err != nil {
		t.Fatal(err)
	}
	tombstone, err := book.SetRetention(ctx, record.RecordID, digest, Deleted, "expiry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.ResolvePayload(ctx, record.RecordID, digest); !errors.As(err, &unavailable) || unavailable.State != Deleted {
		t.Fatalf("deleted payload: %v", err)
	}
	if _, err := book.payloads.Resolve(ctx, digest); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted payload bytes remain in the resolver")
	}
	original, err := book.Get(ctx, record.RecordID)
	if err != nil || original.Header.RetentionState != Available || !slices.Equal(original.Header.PayloadCommitments, []string{digest}) {
		t.Fatal("a deletion must never mutate the original commitment")
	}
	if !slices.Contains(tombstone.Header.LinksTo(Cites), record.RecordID) {
		t.Fatal("the lifecycle record must cite the record whose payload it retires")
	}
	if _, err := book.SetRetention(ctx, record.RecordID, digest, Available, "undo"); !errors.Is(err, ErrInvalid) {
		t.Fatal("DELETED must be permanent")
	}
}
