// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

// A shelf card is as of a cadence tick: the anchor is the tick's cadence
// checkpoint, and each log is committed at the largest checkpoint of it any
// tick so far carried, so a log a later tick drops keeps its extent.
func TestTheStandardHistoryAnchorIsACadenceTick(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	if _, err := c.StandardHistoryAnchor(ctx, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no tick yet: %v", err)
	}
	a := mustOpenBook(t, shelf, "deal/a")
	b := mustOpenBook(t, shelf, "deal/b")
	// entries is how many log entries a book's newest checkpoint commits:
	// a record's seq is its position among them.
	entries := func(book *Book) uint64 {
		t.Helper()
		latest, err := book.commitments.Latest(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return latest.Entries
	}
	mustAppend(t, a, observation("a", "1"))
	mustAppend(t, b, observation("b", "1"))
	first := mustTick(t, c, tickTime)
	a1, b1 := entries(a), entries(b)
	mustAppend(t, a, observation("a", "2"))
	mustAppend(t, a, observation("a", "3"))
	second := mustTick(t, c, first.DueNext)
	a2 := entries(a)

	// A tick that drops deal/b (as the live set does once it ends) still
	// leaves it committed at its tick-1 checkpoint.
	if _, err := shelf.db.Exec(`DELETE FROM cadence_live WHERE log_id = 'deal/b'`); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, a, observation("a", "4"))
	third := mustTick(t, c, second.DueNext)
	a3 := entries(a)
	if a1 >= a2 || a2 >= a3 {
		t.Fatalf("deal/a did not grow: %d, %d, %d", a1, a2, a3)
	}
	if _, ok := third.Leaves.Deals["deal/b"]; ok {
		t.Fatal("the third tick still carries deal/b")
	}

	for _, tc := range []struct {
		n       uint64
		tick    CadenceTick
		extents map[string]uint64
	}{
		{0, third, map[string]uint64{"deal/a": a3, "deal/b": b1}},
		{1, first, map[string]uint64{"deal/a": a1, "deal/b": b1}},
		{2, second, map[string]uint64{"deal/a": a2, "deal/b": b1}},
	} {
		anchor, err := c.StandardHistoryAnchor(ctx, tc.n)
		if err != nil {
			t.Fatal(err)
		}
		if anchor.Tick.N != tc.tick.N || len(anchor.Extents) != len(tc.extents) {
			t.Fatalf("tick %d: anchor %d, extents %v", tc.n, anchor.Tick.N, anchor.Extents)
		}
		for id, want := range tc.extents {
			if anchor.Extents[id] != want {
				t.Fatalf("tick %d: %s extent %d, want %d", tc.n, id, anchor.Extents[id], want)
			}
		}
		sum := sha256.Sum256(tc.tick.Checkpoint)
		at := tc.tick.At.Format("2006-01-02T15:04:05Z")
		if anchor.AsOf.Anchor != hex.EncodeToString(sum[:]) || anchor.AsOf.AnchorTime != at {
			t.Fatalf("tick %d: as_of %+v", tc.n, anchor.AsOf)
		}
		if anchor.AsOf.FirstCheckpointTime != first.At.Format("2006-01-02T15:04:05Z") {
			t.Fatalf("tick %d: first checkpoint time %s", tc.n, anchor.AsOf.FirstCheckpointTime)
		}
	}
	if _, err := c.StandardHistoryAnchor(ctx, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a tick not yet cut: %v", err)
	}
}

// An unfinished tick (stored, its root not yet in the cadence log) is no
// anchor: the newest finished tick is.
func TestAnUnfinishedTickIsNoAnchor(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	first := mustTick(t, c, tickTime)
	if _, err := shelf.db.Exec(`INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (2, ?, ?, 0, 0, x'', '{"deals":{},"filler_index":0,"filler_nonce":""}')`,
		first.DueNext.Format(time.RFC3339), first.DueNext.Add(5*time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	anchor, err := c.StandardHistoryAnchor(ctx, 0)
	if err != nil || anchor.Tick.N != 1 {
		t.Fatalf("newest finished tick: %d, %v", anchor.Tick.N, err)
	}
	if _, err := c.StandardHistoryAnchor(ctx, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unfinished tick 2: %v", err)
	}
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StandardHistoryAnchor(ctx, 0); !errors.Is(err, ErrReleased) {
		t.Fatalf("released shelf: %v", err)
	}
}

// DeriveStandardHistory releases only a card that checks against its own
// inputs, and refuses a holder kind the card cannot state.
func TestDeriveStandardHistory(t *testing.T) {
	asOf := standardhistory.AsOf{Anchor: digestBytes([]byte("anchor")), AnchorTime: "2026-10-31T12:00:00Z", FirstCheckpointTime: "2026-01-02T00:00:00Z"}
	deals := []standardhistory.Deal{{
		Deal: "deal-1", AgreedAt: "2026-10-01T09:00:00Z", Role: "bought",
		Amount:  standardhistory.Amount{Value: "4200", AssetCode: "USD", AssetScale: 2},
		Outcome: "held", Happened: "receipt_only", Disputes: []standardhistory.Dispute{},
	}}
	card, err := DeriveStandardHistory("node", asOf, testPolicy, deals)
	if err != nil {
		t.Fatal(err)
	}
	properties, err := standardhistory.Check(card, &testPolicy, &asOf, deals)
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range standardhistory.Properties {
		if properties[property] != standardhistory.Pass {
			t.Fatalf("%s: %s", property, properties[property])
		}
	}
	if _, err := DeriveStandardHistory("node", asOf, testPolicy, nil); err != nil {
		t.Fatalf("no deals is a card of zeros: %v", err)
	}
	if _, err := DeriveStandardHistory("", asOf, testPolicy, deals); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no holder kind: %v", err)
	}
	bad := append([]standardhistory.Deal(nil), deals...)
	bad[0].Amount.AssetCode = "dollars"
	if _, err := DeriveStandardHistory("person", asOf, testPolicy, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a deal that makes no standard card: %v", err)
	}
}
