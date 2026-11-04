// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	return newMaterialWith(t, disclose, nil)
}

func newMaterialWith(t *testing.T, disclose func(Record) bool, prepare func(*Book)) material {
	t.Helper()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	if prepare != nil {
		prepare(book)
	}
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

// The interval starts at seq 1 even when the share gate refuses the first
// records: they ride along digest-only, which is what coverage needs.
func TestTheIntervalStartsAtSeq1WhateverTheGateRefuses(t *testing.T) {
	h := newMaterial(t, func(r Record) bool { return r.Seq > 2 })
	verified, err := VerifyBundleAnswer(h.m.Bundles[0], h.m.Keys)
	if err != nil {
		t.Fatal(err)
	}
	if verified.IntervalFirst != 1 || verified.IntervalLast != h.cp.Entries || uint64(len(verified.Records)) != h.cp.Entries {
		t.Fatalf("seq %d to %d with %d records", verified.IntervalFirst, verified.IntervalLast, len(verified.Records))
	}
	for _, r := range verified.Records {
		if (r.Seq <= 2) != (r.Header == nil) {
			t.Fatalf("record at seq %d: header %v", r.Seq, r.Header)
		}
	}
	// Coverage is shown, and the withheld headers keep the check NOT_CHECKED.
	if check := recompute(t, h.m); check.Properties["counts_recompute"] != standardhistory.NotChecked {
		t.Fatalf("%v", check.Properties)
	}
}

// A record that cites a record outside the log leaves the bundle's citation
// closure incomplete, which SH-3 does not read: coverage still shows.
func TestACitationOutsideTheLogDoesNotHoldBackTheCounts(t *testing.T) {
	h := newMaterialWith(t, nil, func(book *Book) {
		mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent,
			Links: []Link{{Type: Cites, Target: digestBytes([]byte("a record of another log"))}}})
	})
	if check := recompute(t, h.m); check.Properties["counts_recompute"] != standardhistory.Pass {
		t.Fatalf("%v (%s)", check.Properties, check.Why)
	}
}

// A card whose counts differ is FAIL only over shown coverage: a mapping
// that skips a record whose header is withheld (as it might for any record
// it cannot read) must not turn a partial handover into one.
func TestAWithheldHeaderIsNotLeftToTheMapping(t *testing.T) {
	h := newMaterial(t, func(r Record) bool { return r.Header.RecordType != "deal_terms" || r.Seq != 1 })
	h.m.Deals = func(records []MaterialRecord) ([]standardhistory.Deal, error) {
		var kept []MaterialRecord
		for _, r := range records {
			if r.Header != nil {
				kept = append(kept, r)
			}
		}
		return dealsOfMaterial(kept)
	}
	check := recompute(t, h.m)
	if got := check.Properties["counts_recompute"]; got != standardhistory.NotChecked {
		t.Fatalf("counts_recompute %s (%s), want NOT_CHECKED", got, check.Why)
	}
}

// The anchor's time is not bound by the card's as_of, but the window is
// read against it: material handed with another time is not recomputed.
func TestTheAnchorTimeIsTheCheckpointsOwn(t *testing.T) {
	h := newMaterial(t, nil)
	h.m.AsOf.AnchorTime = "2025-01-01T00:00:00Z"
	check := recompute(t, h.m)
	if got := check.Properties["counts_recompute"]; got != standardhistory.NotChecked || check.Why == "" {
		t.Fatalf("counts_recompute %s (%q)", got, check.Why)
	}
}

func TestTwoBundlesForOneLogAreNotCoverage(t *testing.T) {
	h := newMaterial(t, nil)
	h.m.Bundles = append(h.m.Bundles, h.m.Bundles[0])
	if got := recompute(t, h.m).Properties["counts_recompute"]; got != standardhistory.NotChecked {
		t.Fatalf("counts_recompute %s", got)
	}
}

// Another committed log with no record has nothing to show (SH-3 (b)).
func TestAnEmptyCommittedLogNeedsNoBundle(t *testing.T) {
	h := newMaterial(t, nil)
	h.m.Committed["empty"] = CommittedLog{Checkpoint: "33"}
	if check := recompute(t, h.m); check.Properties["counts_recompute"] != standardhistory.Pass {
		t.Fatalf("%v (%s)", check.Properties, check.Why)
	}
}

// A caller-sealed record holds its header in its capsule, which travels
// with the interval, so a share gate cannot refuse it; and a bundle another
// producer declares incomplete for a citation outside the log (graph closure
// "withheld") still shows coverage.
func TestACapsuleSealedLog(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-sealed", 1).open(t)
	absent := strings.Repeat("ab", 32)
	payload := `{"order":17}`
	mustAppend(t, book, observation("first", `{"n":1}`))
	sealed, err := book.AppendCapsule(ctx, Entry{RecordType: "published_capsule", EpistemicType: ProducerClaim, SubjectRef: "p", Links: []Link{{Type: Cites, Target: absent}}}, boundPayload(payload), citingSealer(t, payload, absent))
	if err != nil {
		t.Fatal(err)
	}
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.StandardHistoryBundle(ctx, cp, StandardHistoryBundleRequest{Disclose: func(r Record) bool { return r.RecordID != sealed.RecordID }}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("refusing a capsule-sealed record: %v", err)
	}
	// The same log, handed as a bundle rooted at the record citing outside.
	var all []string
	for _, r := range book.log[:cp.Entries] {
		all = append(all, r.RecordID)
	}
	foreign, err := book.Bundle(ctx, BundleRequest{Root: sealed.RecordID, Include: all, At: &cp})
	if err != nil {
		t.Fatal(err)
	}
	if verified, err := VerifyBundle(foreign.JSON); err != nil || verified.Claims.GraphClosure == "pass" {
		t.Fatalf("the foreign bundle's graph closure is %q (%v), want a closure that is not pass", verified.Claims.GraphClosure, err)
	}
	card, asOf := cardOf(t, book, cp)
	policy := testPolicy
	check := recompute(t, StandardHistoryMaterial{
		Card: card, AsOf: asOf, Policy: &policy, Keys: responderKeys(book, 1),
		Committed: map[string]CommittedLog{cp.LogID: {Checkpoint: anchorDigest(cp), Leaves: cp.Entries}},
		Bundles:   [][]byte{foreign.JSON}, Deals: dealsOfMaterial,
	})
	if check.Properties["counts_recompute"] != standardhistory.Pass {
		t.Fatalf("%v (%s)", check.Properties, check.Why)
	}
}

// The root of a handover brings no link targets with it: a refused record
// a disclosed one cites stays digest-only.
func TestTheRootOfAHandoverDisclosesNoLinkTarget(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-root", 1).open(t)
	hidden := mustAppend(t, book, observation("hidden", `{"n":1}`))
	mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: hidden.RecordID}}})
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := book.StandardHistoryBundle(ctx, cp, StandardHistoryBundleRequest{Disclose: func(r Record) bool { return r.RecordID != hidden.RecordID }})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range verified.Records {
		if (r.RecordID == hidden.RecordID) != (r.Header == nil) {
			t.Fatalf("record %s: header %v", r.RecordID, r.Header)
		}
	}
}

// A set that cannot be handed in full builds no bundle: none of its books
// gets a disclosure record for it.
func TestAFailedSetAppendsNothing(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustTick(t, c, tickTime)
	anchor, err := c.StandardHistoryAnchor(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := len(a.log)
	anchor.Extents["deal/none"] = 1
	if _, err := c.StandardHistoryBundles(ctx, anchor, StandardHistoryBundleRequest{}); err == nil {
		t.Fatal("a log the shelf does not hold was handed")
	}
	if len(a.log) != before {
		t.Fatalf("deal/a grew from %d to %d records for a set that failed", before, len(a.log))
	}
}
