// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

func rawReceipt(t *testing.T, receipt map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A host pins the anchor, has a witness receipt made for it and gives the
// receipt to Bundle: the statement registers end to end, with no edit of the
// bundle JSON.
func TestBundleCarriesWitnessReceipts(t *testing.T) {
	ctx := context.Background()
	rf := newFixture(t, "responder", 31)
	responder := rf.open(t)
	witnessKey, directory := testWitness()
	statementRecord, err := responder.SealUnavailableWindow(ctx, rf.clock.t.Add(-time.Hour), rf.clock.t.Add(30*time.Minute), "flood")
	if err != nil {
		t.Fatal(err)
	}
	responderKey := KeyID(responder.signer)
	anchor, err := responder.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	receipt := rawReceipt(t, mintReceiptAt(t, anchor.Statement, witnessKey, rf.clock.t))
	bundle, err := responder.Bundle(ctx, BundleRequest{Root: statementRecord.RecordID, At: &anchor, Witnesses: []json.RawMessage{receipt}, WitnessDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Checkpoint bundleWireCheckpoint `json:"checkpoint"`
	}
	if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Checkpoint.Witnesses) != 1 {
		t.Fatalf("checkpoint.witnesses: %s", wire.Checkpoint.Witnesses)
	}
	verified, err := VerifyBundleAnswer(bundle.JSON, withWitnesses(responderKeys(responder, 31), directory))
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Witnessed {
		t.Fatalf("the bundle verified unwitnessed: %v", verified.Findings)
	}
	statements, err := verified.UnavailableWindowStatements(responderKey, directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 1 {
		t.Fatalf("statements: %+v", statements)
	}
	reading := evidencerequest.RecognizeUnavailableWindow(statements[0].Statement, statements[0].Receipts, responderKey)
	if !reading.Recognized || reading.RegistrationTime == "" {
		t.Fatalf("the statement is not registered: %+v", reading)
	}

	t.Run("without receipts the bundle carries no member", func(t *testing.T) {
		plain, err := responder.Bundle(ctx, BundleRequest{Root: statementRecord.RecordID, At: &anchor})
		if err != nil {
			t.Fatal(err)
		}
		var generic struct {
			Checkpoint map[string]json.RawMessage `json:"checkpoint"`
		}
		if err := json.Unmarshal(plain.JSON, &generic); err != nil {
			t.Fatal(err)
		}
		if _, present := generic.Checkpoint["witnesses"]; present {
			t.Fatal("checkpoint.witnesses present without receipts")
		}
		if plain.Digest == bundle.Digest {
			t.Fatal("the digest ignores the receipts")
		}
	})
}

// A receipt is checked against the pinned anchor before it is emitted; a
// refusal leaves the book unchanged (no disclosure record).
func TestBundleRefusesBadWitnessReceipts(t *testing.T) {
	ctx := context.Background()
	rf := newFixture(t, "responder", 31)
	responder := rf.open(t)
	witnessKey, directory := testWitness()
	record, err := responder.SealUnavailableWindow(ctx, rf.clock.t.Add(-time.Hour), rf.clock.t.Add(time.Hour), "outage")
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := responder.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	good := rawReceipt(t, mintReceipt(t, anchor.Statement, witnessKey))
	// A receipt for another checkpoint of the same book.
	if _, err := responder.SealUnavailableWindow(ctx, rf.clock.t.Add(-2*time.Hour), rf.clock.t.Add(time.Hour), "other_declared"); err != nil {
		t.Fatal(err)
	}
	later, err := responder.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wrongAnchor := rawReceipt(t, mintReceipt(t, later.Statement, witnessKey))
	unlisted := []WitnessRow{{Name: "another", Endpoint: "https://other.test", KeyIDs: directory[0].KeyIDs}}
	tooMany := make([]json.RawMessage, MaxReplyReceipts+1)
	for i := range tooMany {
		tooMany[i] = good
	}
	before := len(responder.log)
	for name, req := range map[string]BundleRequest{
		"a receipt for another checkpoint": {Witnesses: []json.RawMessage{wrongAnchor}, WitnessDirectory: directory},
		"no directory":                     {Witnesses: []json.RawMessage{good}},
		"a witness the directory omits":    {Witnesses: []json.RawMessage{good}, WitnessDirectory: unlisted},
		"not a receipt":                    {Witnesses: []json.RawMessage{json.RawMessage(`{"ts_url":"x"}`)}, WitnessDirectory: directory},
		"over the limit":                   {Witnesses: tooMany, WitnessDirectory: directory},
		"a member besides the five":        {Witnesses: []json.RawMessage{receiptWith(t, good, "extra", "x")}, WitnessDirectory: directory},
		"a differently-cased member name":  {Witnesses: []json.RawMessage{receiptWith(t, good, "TS_URL", "https://wrong.test")}, WitnessDirectory: directory},
		"non-canonical base64":             {Witnesses: []json.RawMessage{receiptWith(t, good, "receipt_b64", "!")}, WitnessDirectory: directory},
	} {
		t.Run(name, func(t *testing.T) {
			req.Root, req.At = record.RecordID, &anchor
			if _, err := responder.Bundle(ctx, req); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	t.Run("no pinned anchor", func(t *testing.T) {
		checkpoints, err := responder.Checkpoints(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := responder.Bundle(ctx, BundleRequest{Root: record.RecordID, Witnesses: []json.RawMessage{good}, WitnessDirectory: directory}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v, want ErrInvalid", err)
		}
		if again, _ := responder.Checkpoints(ctx); len(again) != len(checkpoints) {
			t.Fatal("a refused request issued a checkpoint")
		}
	})
	if after := len(responder.log); after != before {
		t.Fatalf("a refused bundle appended %d records", after-before)
	}
}

// receiptWith is receipt with one member set.
func receiptWith(t *testing.T, receipt json.RawMessage, name, value string) json.RawMessage {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(receipt, &fields); err != nil {
		t.Fatal(err)
	}
	fields[name] = value
	return rawReceipt(t, fields)
}

// The interval path (a substrate whose bundle the producer does not build)
// carries the receipts in the bundle's checkpoint too.
func TestBundleCarriesWitnessReceiptsOnTheIntervalPath(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "responder", 31)
	cfg := f.config(t)
	cfg.Substrate = &flakySubstrate{Substrate: cfg.Substrate}
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	witnessKey, directory := testWitness()
	record, err := book.SealUnavailableWindow(ctx, f.clock.t.Add(-time.Hour), f.clock.t.Add(time.Hour), "outage")
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	receipt := rawReceipt(t, mintReceiptAt(t, anchor.Statement, witnessKey, f.clock.t))
	bundle, err := book.Bundle(ctx, BundleRequest{Root: record.RecordID, At: &anchor, Witnesses: []json.RawMessage{receipt}, WitnessDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(verified.Witnesses) != 1 {
		t.Fatalf("witnesses: %d", len(verified.Witnesses))
	}
}

// A SCITT book's statement is a transparency receipt, not a signed checkpoint,
// so no receipt verifies against it: the request is refused, never dropped.
func TestBundleRefusesWitnessReceiptsOnASCITTBook(t *testing.T) {
	ctx := context.Background()
	book := scittBook(t)
	record, err := book.Append(ctx, Entry{RecordType: "note", EpistemicType: ProducerClaim, Statement: []byte(`{"n":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = book.Bundle(ctx, BundleRequest{Root: record.RecordID, At: &anchor, Witnesses: []json.RawMessage{json.RawMessage(`{"ts_url":"https://w.test","entry_hash":"x","receipt_b64":"AAAA","leaf_index":0,"tree_size":1}`)}, WitnessDirectory: []WitnessRow{{Name: "w", Endpoint: testWitnessURL}}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}
