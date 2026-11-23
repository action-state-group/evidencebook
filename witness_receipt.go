// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
)

// VerifyWitnessReceipt verifies a witness receipt for a signed cadence
// checkpoint statement under directory, with the check Cadence.Witnessed
// applies to the holder's own checkpoints, so a reader can check a receipt
// another party sent for its checkpoint (x-deal-v1's entry check,
// evidencebook#773). It returns the check and, when the receipt verified,
// the key id of the directory row it verified under (never one the receipt
// carries). An error means the statement itself does not verify.
func VerifyWitnessReceipt(statement []byte, receipt WitnessReceipt, directory aacwitness.Directory) (WitnessCheck, string, error) {
	check, keyID, _, _, err := VerifyWitnessReceiptTime(statement, receipt, directory)
	return check, keyID, err
}

// VerifyWitnessReceiptTime is VerifyWitnessReceipt with the receipt's
// registration time: the iat of its protected CWT claims, when the receipt
// verified and carries one. ok is false otherwise; a receipt with no iat
// gives no time, never a guessed one. A reader takes the earliest such time,
// among receipts from the witnesses an agreement names, as the time of the
// stamp the statement belongs to (x-deal-v1's ruling, evidencebook#819).
func VerifyWitnessReceiptTime(statement []byte, receipt WitnessReceipt, directory aacwitness.Directory) (check WitnessCheck, keyID string, at time.Time, ok bool, err error) {
	// As Cadence.Witnessed: the witness's URL without a trailing slash, and
	// a receipt with all three parts.
	for len(receipt.TSURL) > 0 && receipt.TSURL[len(receipt.TSURL)-1] == '/' {
		receipt.TSURL = receipt.TSURL[:len(receipt.TSURL)-1]
	}
	if receipt.TSURL == "" || receipt.EntryHash == "" || len(receipt.Receipt) == 0 {
		return WitnessCheck{State: WitnessInvalid, Reason: "a receipt needs its receipt, its entry hash and its witness's URL"}, "", time.Time{}, false, nil
	}
	check, v, err := verifyWitnessReceipt(statement, receipt, directory)
	if err != nil || check.State != WitnessVerified || v.registeredAt == nil {
		return check, v.keyID, time.Time{}, false, err
	}
	return check, v.keyID, time.Unix(*v.registeredAt, 0).UTC(), true, nil
}

// Receipts are the witness receipts this shelf holds for a signed cadence
// checkpoint statement (as Cadence.Witnessed kept them), in witness and key
// order, none when it holds none. A party sends them beside its stamp proof;
// x-deal-v1's entry check reads them to see that a witness registered the
// party's checkpoint (evidencebook#788). A receipt carries no record content:
// only the witness's URL, its signed receipt, and the entry hash with its
// position in the witness's log (leaf index and tree size).
func (c *Cadence) Receipts(ctx context.Context, statement []byte) ([]WitnessReceipt, error) {
	c.shelf.mu.Lock()
	defer c.shelf.mu.Unlock()
	if c.shelf.released {
		return nil, ErrReleased
	}
	return c.receiptsLocked(ctx, statement)
}
