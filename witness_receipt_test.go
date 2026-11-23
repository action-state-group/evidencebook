// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Receipts gives the receipts Witnessed kept for a checkpoint, none for a
// checkpoint no witness timed.
func TestCadenceReceipts(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	tick := mustTick(t, c, tickTime)
	if got, err := c.Receipts(ctx, tick.Checkpoint); err != nil || len(got) != 0 {
		t.Fatalf("before any receipt: %v %v", got, err)
	}
	first := receiptFor(t, tick.Checkpoint, testWitnessURL)
	if check, err := c.Witnessed(ctx, tick.N, first); err != nil || check.State != WitnessVerified {
		t.Fatalf("witnessed: %+v %v", check, err)
	}
	got, err := c.Receipts(ctx, tick.Checkpoint)
	if err != nil || len(got) != 1 || got[0].TSURL != testWitnessURL {
		t.Fatalf("receipts: %+v %v", got, err)
	}
	// What a party hands over verifies as the reader checks it.
	if check, keyID, err := VerifyWitnessReceipt(tick.Checkpoint, got[0], testWitnesses(t)); err != nil || check.State != WitnessVerified || keyID == "" {
		t.Fatalf("a held receipt does not verify: %+v %q %v", check, keyID, err)
	}
	if other, err := c.Receipts(ctx, []byte("another statement")); err != nil || len(other) != 0 {
		t.Fatalf("another checkpoint: %v %v", other, err)
	}
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Receipts(ctx, tick.Checkpoint); !errors.Is(err, ErrReleased) {
		t.Fatalf("a released shelf: %v", err)
	}
}

// VerifyWitnessReceipt takes a receipt as Witnessed does: a trailing slash
// on the witness's URL is dropped, and a receipt missing a part is invalid
// with no error.
func TestVerifyWitnessReceipt(t *testing.T) {
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	dir := testWitnesses(t)
	tick := mustTick(t, c, tickTime)
	r := receiptFor(t, tick.Checkpoint, testWitnessURL)
	r.TSURL += "/"
	check, keyID, err := VerifyWitnessReceipt(tick.Checkpoint, r, dir)
	if err != nil || check.State != WitnessVerified || keyID == "" {
		t.Fatalf("a receipt with a trailing slash: %+v %q %v", check, keyID, err)
	}
	check, keyID, err = VerifyWitnessReceipt(tick.Checkpoint, WitnessReceipt{TSURL: testWitnessURL, Receipt: []byte("r")}, dir)
	if err != nil || check.State != WitnessInvalid || keyID != "" {
		t.Fatalf("no entry hash: %+v %q %v", check, keyID, err)
	}
}

// VerifyWitnessReceiptTime gives a verified receipt's registration time, and
// none for a receipt without one or one that does not verify.
func TestVerifyWitnessReceiptTime(t *testing.T) {
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	dir := testWitnesses(t)
	tick := mustTick(t, c, tickTime)
	iat := tickTime.Add(5 * time.Minute).Truncate(time.Second)
	check, keyID, at, ok, err := VerifyWitnessReceiptTime(tick.Checkpoint, receiptAt(t, tick.Checkpoint, testWitnessURL, iat), dir)
	if err != nil || check.State != WitnessVerified || keyID == "" || !ok || !at.Equal(iat) {
		t.Fatalf("a receipt with a time: %+v %q %v %v %v", check, keyID, at, ok, err)
	}
	check, keyID, _, ok, err = VerifyWitnessReceiptTime(tick.Checkpoint, receiptFor(t, tick.Checkpoint, testWitnessURL), dir)
	if err != nil || check.State != WitnessVerified || keyID == "" || ok {
		t.Fatalf("a receipt with no time verifies and gives none: %+v %q %v %v", check, keyID, ok, err)
	}
	other := receiptAt(t, tick.Checkpoint, testWitnessURL, iat)
	later := mustTick(t, c, tickTime.Add(time.Hour))
	other.Receipt = receiptAt(t, later.Checkpoint, testWitnessURL, iat).Receipt
	if check, _, _, ok, err := VerifyWitnessReceiptTime(tick.Checkpoint, other, dir); err != nil || check.State == WitnessVerified || ok {
		t.Fatalf("a receipt for another statement gives no time: %+v %v %v", check, ok, err)
	}
}
