// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

// dealsOfMaterial is the test host's SH-2 mapping over handed records: the
// same reading as dealsOf, from the headers the bundles disclose. A record
// whose header is withheld could be a deal, so it is withheld material.
func dealsOfMaterial(records []MaterialRecord) ([]standardhistory.Deal, error) {
	deals := []standardhistory.Deal{}
	for _, r := range records {
		if r.Header == nil || !r.HeaderVerified {
			return nil, ErrMaterialWithheld
		}
		if r.Header.RecordType != "deal_terms" {
			continue
		}
		var d standardhistory.Deal
		if err := json.Unmarshal(r.Header.Statement, &d); err != nil {
			return nil, err
		}
		d.Deal, d.AgreedAt = r.RecordID, r.Header.CommittedAt
		deals = append(deals, d)
	}
	return deals, nil
}

func addDeal(t *testing.T, book *Book, amount string) {
	t.Helper()
	mustAppend(t, book, Entry{RecordType: "deal_terms", EpistemicType: ProducerClaim, Statement: json.RawMessage(
		`{"role":"provided","amount":{"value":"` + amount + `","assetCode":"USD","assetScale":0},"outcome":"held","happened":"no_record","disputes":[]}`)})
}

// cardOf is the card the book's host derives as of cp from all its deal
// records.
func cardOf(t *testing.T, book *Book, cp Checkpoint) ([]byte, standardhistory.AsOf) {
	t.Helper()
	var records []Record
	for _, r := range book.log[:cp.Entries] {
		records = append(records, r.detached())
	}
	policy, deals, err := dealsOf(nil)(context.Background(), cp, records)
	if err != nil {
		t.Fatal(err)
	}
	at := evidencerequest.ProfileTime(cp.IssuedAt)
	asOf := standardhistory.AsOf{Anchor: anchorDigest(cp), AnchorTime: at, FirstCheckpointTime: at}
	card, err := DeriveStandardHistory("person", asOf, policy, deals)
	if err != nil {
		t.Fatal(err)
	}
	return card, asOf
}

type material struct {
	book *Book
	cp   Checkpoint
	m    StandardHistoryMaterial
}

func newMaterial(t *testing.T, disclose func(Record) bool) material {
	t.Helper()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	cp, err := book.Checkpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	card, asOf := cardOf(t, book, cp)
	bundle, err := book.StandardHistoryBundle(context.Background(), cp, StandardHistoryBundleRequest{Disclose: disclose})
	if err != nil {
		t.Fatal(err)
	}
	policy := testPolicy
	return material{book: book, cp: cp, m: StandardHistoryMaterial{
		Card: card, AsOf: asOf, Policy: &policy, Keys: responderKeys(book, 9),
		Committed: map[string]CommittedLog{cp.LogID: {Checkpoint: anchorDigest(cp), Leaves: cp.Entries}},
		Bundles:   [][]byte{bundle.JSON}, Deals: dealsOfMaterial,
	}}
}

func recompute(t *testing.T, m StandardHistoryMaterial) StandardHistoryCheck {
	t.Helper()
	check, err := CheckStandardHistoryMaterial(m)
	if err != nil {
		t.Fatal(err)
	}
	return check
}

func TestHandedMaterialRecomputesTheCounts(t *testing.T) {
	m := newMaterial(t, nil).m
	check := recompute(t, m)
	if check.Properties["counts_recompute"] != standardhistory.Pass || check.Why != "" {
		t.Fatalf("counts_recompute %s (%s)", check.Properties["counts_recompute"], check.Why)
	}
	if !standardhistory.IsStandard(check.Properties) || check.Properties["window_from_policy"] != standardhistory.Pass {
		t.Fatalf("properties %v", check.Properties)
	}
	// Without the policy the counts still recompute over the card's own window.
	m.Policy = nil
	check = recompute(t, m)
	if check.Properties["counts_recompute"] != standardhistory.Pass || check.Properties["window_from_policy"] != standardhistory.NotChecked {
		t.Fatalf("no policy: %v", check.Properties)
	}
}

func TestACardWhoseCountsDifferFailsOverShownCoverage(t *testing.T) {
	h := newMaterial(t, nil)
	var records []Record
	for _, r := range h.book.log {
		records = append(records, r.detached())
	}
	policy, deals, err := dealsOf(nil)(context.Background(), h.cp, records)
	if err != nil {
		t.Fatal(err)
	}
	// A card that leaves out the first deal, handed with the whole log.
	if h.m.Card, err = DeriveStandardHistory("person", h.m.AsOf, policy, deals[1:]); err != nil {
		t.Fatal(err)
	}
	check := recompute(t, h.m)
	if check.Properties["counts_recompute"] != standardhistory.Fail || standardhistory.IsStandard(check.Properties) {
		t.Fatalf("%v", check.Properties)
	}
}

func TestPartialMaterialIsNeverPassAndNeverFail(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *material){
		"a record's header withheld": func(t *testing.T, h *material) {
			*h = newMaterial(t, func(r Record) bool { return r.Header.RecordType != "observation" })
		},
		"no committed form (SH-10)": func(_ *testing.T, h *material) { h.m.Committed = nil },
		"the extent is shorter than the log": func(_ *testing.T, h *material) {
			h.m.Committed = map[string]CommittedLog{h.cp.LogID: {Checkpoint: anchorDigest(h.cp), Leaves: h.cp.Entries - 1}}
		},
		"the anchor is no committed log's checkpoint": func(_ *testing.T, h *material) {
			h.m.Committed = map[string]CommittedLog{h.cp.LogID: {Checkpoint: "00", Leaves: h.cp.Entries}}
		},
		"another committed log is not handed": func(_ *testing.T, h *material) {
			h.m.Committed["other"] = CommittedLog{Checkpoint: "22", Leaves: 3}
		},
		"no bundle at all":         func(_ *testing.T, h *material) { h.m.Bundles = nil },
		"no mapping":               func(_ *testing.T, h *material) { h.m.Deals = nil },
		"a bundle that is no JSON": func(_ *testing.T, h *material) { h.m.Bundles = [][]byte{[]byte("{")} },
		"another holder's key": func(_ *testing.T, h *material) {
			h.m.Keys.Checkpoint = responderKeys(h.book, 10).Checkpoint
		},
		"a mapping that reports withheld": func(_ *testing.T, h *material) {
			h.m.Deals = func([]MaterialRecord) ([]standardhistory.Deal, error) { return nil, ErrMaterialWithheld }
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newMaterial(t, nil)
			mutate(t, &h)
			check := recompute(t, h.m)
			if got := check.Properties["counts_recompute"]; got != standardhistory.NotChecked || check.Why == "" {
				t.Fatalf("counts_recompute %s (%q), want NOT_CHECKED with a reason", got, check.Why)
			}
			if !standardhistory.IsStandard(check.Properties) {
				t.Fatalf("a partial handover made a non-standard card: %v", check.Properties)
			}
		})
	}
}

func TestACardThatIsNotWellFormedIsNotRecomputed(t *testing.T) {
	h := newMaterial(t, nil)
	h.m.Card = []byte(`{"not":"a card"}`)
	check := recompute(t, h.m)
	if check.Properties["counts_recompute"] != standardhistory.NotChecked || check.Properties["card_form"] != standardhistory.Fail {
		t.Fatalf("%v", check.Properties)
	}
}

func TestAMappingFailureIsAnError(t *testing.T) {
	h := newMaterial(t, nil)
	h.m.Deals = func([]MaterialRecord) ([]standardhistory.Deal, error) { return nil, errors.New("not a deal") }
	if _, err := CheckStandardHistoryMaterial(h.m); err == nil {
		t.Fatal("a mapping failure was read as no deals")
	}
}

// Every other log the anchor commits must be handed in full too (SH-3 (b)).
func TestEveryCommittedLogMustBeHanded(t *testing.T) {
	h := newMaterial(t, nil)
	f := newFixture(t, "book-b", 9)
	other, err := Open(context.Background(), f.config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Release() })
	addDeal(t, other, "7")
	cpB, err := other.Checkpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := other.StandardHistoryBundle(context.Background(), cpB, StandardHistoryBundleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// The card counts both logs' deals (three here, one there).
	var records []Record
	for _, r := range h.book.log[:h.cp.Entries] {
		records = append(records, r.detached())
	}
	for _, r := range other.log[:cpB.Entries] {
		records = append(records, r.detached())
	}
	policy, deals, err := dealsOf(nil)(context.Background(), h.cp, records)
	if err != nil {
		t.Fatal(err)
	}
	if h.m.Card, err = DeriveStandardHistory("person", h.m.AsOf, policy, deals); err != nil {
		t.Fatal(err)
	}
	h.m.Committed[cpB.LogID] = CommittedLog{Checkpoint: anchorDigest(cpB), Leaves: cpB.Entries}

	if check := recompute(t, h.m); check.Properties["counts_recompute"] != standardhistory.NotChecked {
		t.Fatalf("one of two committed logs handed: %v", check.Properties)
	}
	h.m.Bundles = append(h.m.Bundles, bundleB.JSON)
	h.m.Committed[cpB.LogID] = CommittedLog{Checkpoint: "00", Leaves: cpB.Entries}
	if check := recompute(t, h.m); check.Properties["counts_recompute"] != standardhistory.NotChecked {
		t.Fatalf("a bundle for another checkpoint of the second log: %v", check.Properties)
	}
	h.m.Committed[cpB.LogID] = CommittedLog{Checkpoint: anchorDigest(cpB), Leaves: cpB.Entries}
	if check := recompute(t, h.m); check.Properties["counts_recompute"] != standardhistory.Pass {
		t.Fatalf("both handed: %v (%s)", check.Properties, check.Why)
	}
}

func TestTheHolderHandsNothingItMayNotShowOrNeverIssued(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, nil, nil)
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.StandardHistoryBundle(ctx, cp, StandardHistoryBundleRequest{Disclose: func(Record) bool { return false }}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a share gate that shows nothing: %v", err)
	}
	if _, err := book.StandardHistoryBundle(ctx, Checkpoint{LogID: cp.LogID, ID: "unknown"}, StandardHistoryBundleRequest{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a checkpoint the book never issued: %v", err)
	}
}

// A record the share gate withholds still travels as a digest-only capsule:
// the recipient sees coverage, and none of the record.
func TestAWithheldRecordTravelsAsADigestOnly(t *testing.T) {
	h := newMaterial(t, func(r Record) bool { return r.Header.RecordType != "observation" })
	verified, err := VerifyBundleAnswer(h.m.Bundles[0], h.m.Keys)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(verified.Records)) != h.cp.Entries || verified.IntervalFirst != 1 || verified.IntervalLast != h.cp.Entries {
		t.Fatalf("%d records over %d..%d", len(verified.Records), verified.IntervalFirst, verified.IntervalLast)
	}
	withheld := 0
	for _, r := range verified.Records {
		if r.Header == nil {
			withheld++
		}
	}
	if withheld != 1 {
		t.Fatalf("%d records without a header, want the one observation", withheld)
	}
}

// A tick's anchor commits its logs through the cadence tree, so the bundles
// of the logs it committed cover each log's extent but show no coverage of
// the anchor (SH-10): the card stays the holder's statement.
func TestACadenceHoldsOneBundlePerCommittedLog(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	b := mustOpenBook(t, shelf, "deal/b")
	mustAppend(t, a, observation("a", "1"))
	mustAppend(t, b, observation("b", "1"))
	first := mustTick(t, c, tickTime)
	mustAppend(t, a, observation("a", "2"))
	mustTick(t, c, first.DueNext)
	mustAppend(t, a, observation("a", "3"))
	anchor, err := c.StandardHistoryAnchor(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := c.StandardHistoryBundles(ctx, anchor, StandardHistoryBundleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != len(anchor.Extents) || len(bundles) != 2 {
		t.Fatalf("%d bundles for extents %v", len(bundles), anchor.Extents)
	}
	for id, extent := range anchor.Extents {
		verified, err := VerifyBundle(bundles[id].JSON)
		if err != nil {
			t.Fatal(err)
		}
		if verified.IntervalFirst != 1 || verified.IntervalLast != extent || verified.Claims.IntervalCoverage != "pass" || verified.Claims.PerRecordMembership != "pass" {
			t.Fatalf("%s: seq %d to %d (extent %d), claims %+v", id, verified.IntervalFirst, verified.IntervalLast, extent, verified.Claims)
		}
	}
	// A log the tick committed that the shelf cannot hand is no bundle.
	anchor.Extents["deal/none"] = 1
	delete(anchor.Extents, "deal/a")
	delete(anchor.Extents, "deal/b")
	if _, err := c.StandardHistoryBundles(ctx, anchor, StandardHistoryBundleRequest{}); err == nil {
		t.Fatal("a log with no record was handed")
	}
}
