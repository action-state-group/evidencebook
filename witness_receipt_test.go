// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"testing"
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
	if other, err := c.Receipts(ctx, []byte("another statement")); err != nil || len(other) != 0 {
		t.Fatalf("another checkpoint: %v %v", other, err)
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
