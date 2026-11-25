// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"errors"
	"fmt"
	"time"
)

// Agreed no later than witnessed (agent-action-capsule
// spec/standard-history-v0.md §11.2, #696; Ethan, 2026-11-21: keep agreed_at
// and check that, where a witness exists, it is not later than the first
// witnessed fact). A deal's agreed_at is the holder's own agreement record's
// at (BuildStatus), its own word; its first witnessed time is the earliest
// registration time a witness its agreement names gives any of the holder's
// own records of the deal, which is the record time of the book's first
// record, since a stamp that covers a record covers those before it
// (Shelf.RecordTimeByKey or HandoverTimes.RecordTimeByKey, position 1). The
// other side's records are not read: it may have agreed first.

// AgreedAtSkew is how far agreed_at may run past the first witnessed time:
// agreed_at is the holder's clock at sealing and the registration time the
// witness's, so a holder clock a little ahead of the witness's must not
// refuse an honest deal (Sherlock2's what-check on #696).
const AgreedAtSkew = 300 * time.Second

// ErrAgreedAfterWitnessed is a deal whose agreed_at is later than its first
// witnessed time by more than AgreedAtSkew: no witness can have timed a
// record of an agreement not yet made, so the deal entry is not well formed.
// A host cannot answer /3 or /4 with it; a verifier that forms the entries
// from a handover reports counts_recompute FAIL.
var ErrAgreedAfterWitnessed = errors.New("xdealv1: agreed_at is later than the deal's first witnessed time")

// CheckAgreedAt checks a deal's agreed_at, RFC 3339 as the status value
// carries it, against its first witnessed time. Call it only for a deal that
// has one: a deal with no witnessed time is not checked.
func CheckAgreedAt(agreedAt string, firstWitnessed time.Time) error {
	agreed, err := time.Parse(time.RFC3339, agreedAt)
	if err != nil {
		return fmt.Errorf("xdealv1: agreed_at %q: %v", agreedAt, err)
	}
	if agreed.Sub(firstWitnessed) > AgreedAtSkew {
		return fmt.Errorf("%w: agreed_at %s, first witnessed %s (allowance %s)", ErrAgreedAfterWitnessed,
			agreed.UTC().Format(time.RFC3339), firstWitnessed.UTC().Format(time.RFC3339), AgreedAtSkew)
	}
	return nil
}
