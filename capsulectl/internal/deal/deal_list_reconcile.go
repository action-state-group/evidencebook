package deal

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
)

// dealReconcileFacts are what a deal's sealed terms Results state about the
// two halves, as `deal list --json` reports them (evidencebook-deal#171), so
// a plugin can count held halves and conflicts per counterparty from sealed
// facts, never from zeros. It is null on a listing when the deal has no
// verified Result on its results book (not reconciled yet), a sale, or a
// deal kept in an older format: "no conflict" is then unknown, not false.
type dealReconcileFacts struct {
	// CounterpartyHalfHeld is whether any verified Result cites two distinct
	// halves, that is, the counterparty's half was received and admitted
	// (citesTwoHalves, as the History card reads it).
	CounterpartyHalfHeld bool `json:"counterparty_half_held"`
	// HalvesConflict is read from the newest verified Result that cites both
	// halves: true when a claim tallies a conflicting pairing, false when
	// every claim matched, null when no such Result exists or a claim could
	// not be compared (insufficient or unresolved). A Result over one half
	// compared nothing, so it never turns a conflict into "no conflict".
	HalvesConflict *bool `json:"halves_conflict"`
	// At is when the newest verified Result was sealed (RFC 3339, UTC).
	At string `json:"at"`
}

// fillReconcile sets Reconcile on each deal of list from the verified
// Results on its results book. With until non-zero, as in a ranged listing,
// only Results sealed before it are read, so the facts are as of then.
func (s *dealSession) fillReconcile(ctx context.Context, list []dealListing, until time.Time) error {
	x := s.books()
	read, err := x.shelf.Records(ctx)
	if err != nil {
		return err
	}
	byBook := map[string][]evidencebook.ShelfRecord{}
	for _, r := range read.Records {
		if r.Header.RecordType == evidencebook.RecordTypeEvidenceResult {
			byBook[r.BookID] = append(byBook[r.BookID], r)
		}
	}
	for i := range list {
		if list[i].Kind != "deal" || len(byBook[dealResultsLogID(list[i].DealID)]) == 0 {
			continue
		}
		if list[i].Reconcile, err = x.reconcileFacts(ctx, s.keys, byBook[dealResultsLogID(list[i].DealID)], until); err != nil {
			return fmt.Errorf("deal %s: %w", list[i].DealID, err)
		}
	}
	return nil
}

// reconcileFacts reads a deal's results-book records, in seq order, and
// returns nil when none is a verified Result sealed before until.
func (x shelfDealIndex) reconcileFacts(ctx context.Context, keys []ed25519.PublicKey, records []evidencebook.ShelfRecord, until time.Time) (*dealReconcileFacts, error) {
	var facts *dealReconcileFacts
	for _, r := range records {
		at, err := time.Parse(time.RFC3339Nano, r.Header.CommittedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: record %s has no commit time", cli.ErrConflict, r.RecordID)
		}
		if !until.IsZero() && !at.Before(until) {
			continue
		}
		payload, verified, err := x.sealedPayload(ctx, keys, r)
		if err != nil {
			return nil, err
		}
		if !verified {
			continue
		}
		doc, err := termsPayloadResult(payload)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		if facts == nil {
			facts = &dealReconcileFacts{}
		}
		if citesTwoHalves(raw) {
			facts.CounterpartyHalfHeld = true
			facts.HalvesConflict = resultConflicts(raw)
		}
		facts.At = at.UTC().Format(time.RFC3339)
	}
	return facts, nil
}

// resultConflicts is whether a two-half Result's claims tally a conflicting
// pairing (true), all matched (false), or could not all be compared (nil).
func resultConflicts(result []byte) *bool {
	var doc struct {
		Claims []struct {
			Reconcile struct {
				Tallies evidencebook.ResultTallies `json:"tallies"`
			} `json:"reconcile"`
		} `json:"claims"`
	}
	if json.Unmarshal(result, &doc) != nil || len(doc.Claims) == 0 {
		return nil
	}
	matched := true
	for _, c := range doc.Claims {
		t := c.Reconcile.Tallies
		if t.Conflicting > 0 {
			conflict := true
			return &conflict
		}
		matched = matched && t.Matched > 0
	}
	if !matched {
		return nil
	}
	none := false
	return &none
}
