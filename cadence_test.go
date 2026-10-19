// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"
)

// ended is a test's Ended predicate: the logs it names have ended.
type ended struct {
	mu   sync.Mutex
	logs map[string]bool
}

func (e *ended) set(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logs[id] = true
}

func (e *ended) check(_ context.Context, id string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logs[id], nil
}

func newCadence(t *testing.T, shelf *Shelf) (*Cadence, *ended) {
	t.Helper()
	e := &ended{logs: map[string]bool{}}
	c, err := shelf.Cadence(context.Background(), CadenceConfig{LogID: "deal-cadence/0123456789abcdef", Ended: e.check})
	if err != nil {
		t.Fatal(err)
	}
	return c, e
}

var tickTime = time.Date(2026, 10, 19, 12, 0, 30, 0, time.UTC)

func mustTick(t *testing.T, c *Cadence, now time.Time) CadenceTick {
	t.Helper()
	result, err := c.Tick(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ticked {
		t.Fatalf("no tick at %s (due %s)", now, result.DueNext)
	}
	return result.Tick
}

func TestATickIsDueOnTimeAlone(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	first := mustTick(t, c, tickTime)
	if first.N != 1 || !first.At.Equal(tickTime.Truncate(time.Minute)) {
		t.Fatalf("first tick %d at %s", first.N, first.At)
	}
	if d := first.DueNext.Sub(tickTime); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("due in %s, want 5m ± 1m", d)
	}
	early, err := c.Tick(ctx, first.DueNext.Add(-time.Second))
	if err != nil || early.Ticked || !early.DueNext.Equal(first.DueNext) {
		t.Fatalf("before due: %+v %v", early, err)
	}
	if second := mustTick(t, c, first.DueNext); second.N != 2 {
		t.Fatalf("second tick %d", second.N)
	}
	// No log at all: each tick still holds its filler and adds exactly one
	// entry to the cadence log (the pad bucket is 1).
	if first.Leaves.FillerCount != 0 || len(first.Leaves.Deals) != 0 {
		t.Fatalf("an empty profile's tick: %+v", first.Leaves)
	}
}

// Each live log's leaf commits its checkpoint at the tick, and the tick's
// tree is the one its stored leaves rebuild.
func TestATickCommitsEachLiveLog(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustAppend(t, mustOpenBook(t, shelf, "deal/b"), observation("b", "1"))
	tick := mustTick(t, c, tickTime)
	if len(tick.Leaves.Deals) != 2 {
		t.Fatalf("leaves %+v", tick.Leaves.Deals)
	}
	leaf := tick.Leaves.Deals["deal/a"]
	statement, err := base64.RawURLEncoding.DecodeString(leaf.Statement)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(statement)
	latest, err := a.commitments.Latest(ctx)
	if err != nil || hex.EncodeToString(sum[:]) != leaf.CheckpointSHA256 || !bytes.Equal(latest.Statement, statement) || leaf.Size != latest.TreeSize {
		t.Fatalf("leaf %+v does not commit the log's checkpoint (%v)", leaf, err)
	}
	if !latest.IssuedAt.Equal(tickTime.Truncate(time.Minute)) {
		t.Fatalf("the log's checkpoint is cut at the tick's minute: %s", latest.IssuedAt)
	}
	stored, err := c.Get(ctx, tick.N)
	if err != nil {
		t.Fatal(err)
	}
	r1, _, _ := tick.Leaves.Tree(leaf.Index)
	r2, path, _ := stored.Leaves.Tree(leaf.Index)
	leafHash, _ := cadenceLeaf(leaf.LogID, leaf.Size, leaf.CheckpointSHA256, leaf.Salt)
	if !bytes.Equal(r1, r2) || !bytes.Equal(cadenceFold(leafHash, leaf.Index, path), r1) {
		t.Fatal("the stored tick does not rebuild its tree")
	}
}

// The live set: an ended log leaves once a witnessed tick carries its final
// checkpoint; a log still open, or one that grew, stays; an append brings a
// log back; and the next tick keeps H+1 occupied positions.
func TestEndedLogsLeaveTheTickOnceWitnessed(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, e := newCadence(t, shelf)
	a, b, d := mustOpenBook(t, shelf, "deal/a"), mustOpenBook(t, shelf, "deal/b"), mustOpenBook(t, shelf, "deal/d")
	for _, book := range []*Book{a, b, d} {
		mustAppend(t, book, observation("s", "1"))
	}
	e.set("deal/a")
	e.set("deal/d")
	first := mustTick(t, c, tickTime)
	// deal/d grows after the tick: its final checkpoint is not the one
	// witnessed.
	mustAppend(t, d, observation("s", "2"))
	if err := c.Witnessed(ctx, first.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	live, err := c.Live(ctx)
	if err != nil || len(live) != 2 || live[0] != "deal/b" || live[1] != "deal/d" {
		t.Fatalf("live %v %v", live, err)
	}
	second := mustTick(t, c, first.DueNext)
	if _, in := second.Leaves.Deals["deal/a"]; in {
		t.Fatal("an ended, witnessed log is not checkpointed again")
	}
	occupied := len(second.Leaves.Deals) + 1 + int(second.Leaves.FillerCount)
	if occupied != 4 || second.Leaves.FillerCount != 1 {
		t.Fatalf("occupancy %d (fillers %d), want H+1 = 4", occupied, second.Leaves.FillerCount)
	}
	// A late receipt for the first tick changes nothing.
	if err := c.Witnessed(ctx, first.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	if live, _ := c.Live(ctx); len(live) != 2 {
		t.Fatalf("a repeated receipt moved the live set: %v", live)
	}
	// Later evidence on the closed deal brings it back.
	mustAppend(t, a, observation("s", "late"))
	if live, _ := c.Live(ctx); len(live) != 3 {
		t.Fatalf("an append must re-enter the tick: %v", live)
	}
}

// A receipt for an older tick reported after a newer one's never moves a
// log's standing back: the newer witnessed checkpoint keeps it out.
func TestALateReceiptNeverMovesStandingBack(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, e := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("s", "1"))
	first := mustTick(t, c, tickTime)
	mustAppend(t, a, observation("s", "2"))
	e.set("deal/a")
	second := mustTick(t, c, first.DueNext)
	if err := c.Witnessed(ctx, second.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	if live, _ := c.Live(ctx); len(live) != 0 {
		t.Fatalf("live after the final checkpoint's receipt: %v", live)
	}
	if err := c.Witnessed(ctx, first.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	if live, _ := c.Live(ctx); len(live) != 0 {
		t.Fatalf("an older receipt brought the log back: %v", live)
	}
}

// A bundle's tick is found by the index: the very checkpoint ("all"), else
// the newest earlier one ("part").
func TestTheWitnessedTickIsFoundByIndex(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("s", "1"))
	first := mustTick(t, c, tickTime)
	if err := c.Witnessed(ctx, first.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	leaf := first.Leaves.Deals["deal/a"]
	extent, tick, got, err := c.WitnessedTick(ctx, "deal/a", leaf.CheckpointSHA256, leaf.Size)
	if err != nil || extent != "all" || tick.N != first.N || got != leaf {
		t.Fatalf("exact: %s %d %v", extent, tick.N, err)
	}
	mustAppend(t, a, observation("s", "2"))
	later, err := a.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(later.Statement)
	extent, tick, _, err = c.WitnessedTick(ctx, "deal/a", hex.EncodeToString(sum[:]), later.TreeSize)
	if err != nil || extent != "part" || tick.N != first.N {
		t.Fatalf("a later checkpoint: %s %d %v", extent, tick.N, err)
	}
	if _, _, _, err := c.WitnessedTick(ctx, "deal/none", leaf.CheckpointSHA256, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a log no witnessed tick carries: %v", err)
	}
}

// A deal's log from before the upgrade joins the cadence as it is, counts
// into H, and is written only through the shelf.
func TestALegacyLogJoinsTheCadence(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	// A pre-upgrade deal: a CLL log in the profile's file, not a book.
	old, err := OpenCLLSQLite(f.path, "deal/legacy", f.cfg.CheckpointKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Append(ctx, hex.EncodeToString(bytes.Repeat([]byte{5}, 32)), tickTime); err != nil {
		t.Fatal(err)
	}
	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	shelf := f.open(t)
	c, e := newCadence(t, shelf)
	legacy, err := shelf.Legacy(ctx, "deal/legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shelf.Legacy(ctx, "deal/never"); err == nil {
		t.Fatal("Legacy never provisions a log")
	}
	tick := mustTick(t, c, tickTime)
	if _, in := tick.Leaves.Deals["deal/legacy"]; !in {
		t.Fatalf("the legacy log is not in the tick: %+v", tick.Leaves.Deals)
	}
	e.set("deal/legacy")
	if err := c.Witnessed(ctx, tick.N, testReceipt); err != nil {
		t.Fatal(err)
	}
	if live, _ := c.Live(ctx); len(live) != 0 {
		t.Fatalf("live %v", live)
	}
	// Later evidence on it, written by the profile's own step under the
	// shelf's lock, brings it back.
	wrote := false
	if err := legacy.Append(ctx, func() error { wrote = true; return nil }); err != nil || !wrote {
		t.Fatalf("append: %v", err)
	}
	if live, _ := c.Live(ctx); len(live) != 1 {
		t.Fatalf("live after a legacy append %v", live)
	}
	next := mustTick(t, c, tick.DueNext)
	if next.Leaves.FillerCount != 0 || len(next.Leaves.Deals) != 1 {
		t.Fatalf("H is 1: %+v", next.Leaves)
	}
}

// What the witness sees does not depend on activity: the cadence log grows
// by the same number of entries every tick, busy or idle.
func TestATicksSizeDoesNotDependOnActivity(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	now := tickTime
	var sizes []uint64
	for i := 0; i < 4; i++ {
		if i%2 == 0 {
			mustAppend(t, a, observation("s", "busy"))
		}
		tick := mustTick(t, c, now)
		sizes = append(sizes, tick.EntrySeq)
		now = tick.DueNext
	}
	for i := 1; i < len(sizes); i++ {
		if sizes[i]-sizes[i-1] != 1 {
			t.Fatalf("the cadence log grew by %d at tick %d", sizes[i]-sizes[i-1], i+1)
		}
	}
	_ = ctx
}

var testReceipt = WitnessReceipt{TSURL: "https://witness.example", EntryHash: "e1", Receipt: []byte("receipt"), LeafIndex: 3, TreeSize: 4}
