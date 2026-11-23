// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A grace after a window ends (evidencebook#807): the ended window's book
// takes only a close/v1 Close for a period inside it until the grace ends.

func newGraceFixture(t *testing.T) *shelfFixture {
	t.Helper()
	f := newShelfFixture(t)
	f.cfg.Window = &WindowPolicy{Period: WindowMonth, Base: "acme", Since: utc(2026, 10, 1), Grace: 7 * 24 * time.Hour}
	return f
}

func octoberClose(t *testing.T, book *Book, period ClosePeriod) (Record, error) {
	t.Helper()
	_, peer := twoBooks(t)
	input := ReconcileInput{Peer: peerBundle(t, peer), RecordType: "exchange", Compare: SamePayloadCommitments}
	return book.Close(context.Background(), CloseInput{Reconcile: input, Counterparty: "party-b", Period: period})
}

func TestAGracePolicyIsFixedAndShorterThanItsPeriod(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		period WindowPeriod
		grace  time.Duration
	}{{WindowDay, 24 * time.Hour}, {WindowWeek, 7 * 24 * time.Hour}, {WindowMonth, 28 * 24 * time.Hour}, {WindowMonth, -time.Second}, {WindowMonth, time.Millisecond}} {
		f := newShelfFixture(t)
		f.cfg.Window = &WindowPolicy{Period: c.period, Base: "acme", Since: c.period.WindowAt(utc(2026, 10, 1)).Start, Grace: c.grace}
		_, err := OpenSQLiteShelf(ctx, f.cfg)
		assert.ErrorIs(t, err, ErrInvalid, "%s grace %s", c.period, c.grace)
	}
	f := newGraceFixture(t)
	require.NoError(t, f.open(t).Release())
	read, err := ReadShelfWindow(ctx, f.path)
	require.NoError(t, err)
	assert.Equal(t, 7*24*time.Hour, read.Grace)
	db, err := sql.Open("sqlite", "file:"+f.path)
	require.NoError(t, err)
	var version int
	require.NoError(t, db.QueryRow(`SELECT version FROM shelf_meta`).Scan(&version))
	assert.Equal(t, ShelfVersionWindowGrace, version, "a store with a grace is a version older builds refuse")
	require.NoError(t, db.Close())
	other := f.cfg
	policy := *f.cfg.Window
	policy.Grace = 24 * time.Hour
	other.Window = &policy
	_, err = OpenSQLiteShelf(ctx, other)
	assert.ErrorIs(t, err, ErrInvalid, "the grace is fixed with the window")
}

func TestAStoreFromBeforeGraceOpensWithNone(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	_, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	require.NoError(t, shelf.Release())
	db, err := sql.Open("sqlite", "file:"+f.path)
	require.NoError(t, err)
	for _, ddl := range []string{`ALTER TABLE shelf_window DROP COLUMN grace`, `ALTER TABLE shelf_window_books DROP COLUMN boundary_entries`, `ALTER TABLE shelf_window_books DROP COLUMN boundary_root`} {
		_, err := db.Exec(ddl)
		require.NoError(t, err)
	}
	require.NoError(t, db.Close())
	read, err := ReadShelfWindow(ctx, f.path)
	require.NoError(t, err)
	assert.Zero(t, read.Grace)
	shelf = f.open(t)
	setClock(f.clock, utc(2026, 11, 2))
	_, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-11", w.Label, "it rolls as before")
}

func TestAWindowInGraceTakesOnlyACloseForItsPeriod(t *testing.T) {
	ctx := context.Background()
	f := newGraceFixture(t)
	shelf := f.open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	mustAppend(t, oct, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Correlation: Correlation{ExchangeID: "x1", RequestDigest: "r1"}, Payloads: [][]byte{[]byte("p1")}})
	setClock(f.clock, utc(2026, 11, 3))
	nov, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-11", w.Label)
	windows, err := shelf.Windows(ctx)
	require.NoError(t, err)
	assert.False(t, windows[0].Closed, "October is in its grace")

	_, err = oct.Append(ctx, observation("deal", "late"))
	assert.ErrorIs(t, err, ErrWindowClosed, "an ordinary record goes to November")
	_, err = octoberClose(t, oct, ClosePeriod{Start: utc(2026, 11, 1), End: utc(2026, 11, 2)})
	assert.ErrorIs(t, err, ErrWindowClosed, "a period outside October")
	closed, err := octoberClose(t, oct, ClosePeriod{Start: utc(2026, 10, 1), End: utc(2026, 11, 1)})
	require.NoError(t, err, "October's monthly Close, sealed in its grace")
	mustAppend(t, nov, observation("deal", "on time"))

	_, err = shelf.WindowChain(ctx, "2026-10", "2026-10", WindowChainOptions{})
	require.ErrorIs(t, err, ErrInGrace)
	assert.Contains(t, err.Error(), "window 2026-10 is in grace until 2026-11-08T00:00:00Z; no final checkpoint yet")

	// Past the grace, without a roll, October takes nothing.
	setClock(f.clock, utc(2026, 11, 9))
	_, err = octoberClose(t, oct, ClosePeriod{Start: utc(2026, 10, 2), End: utc(2026, 10, 3)})
	assert.ErrorIs(t, err, ErrWindowClosed)
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 12, 9))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)

	chain, err := shelf.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	assert.Equal(t, WindowChainVersion, chain.V)
	require.NotNil(t, chain.Windows[0].Boundary)
	key := shelfCheckpointKey(f)
	verified, findings := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: key})
	require.Empty(t, findings)
	require.NotNil(t, verified[0].Boundary)
	var tailTypes []string
	for _, tail := range chain.Windows[0].Boundary.Tail {
		var h Header
		require.NoError(t, json.Unmarshal(tail.Record.Header, &h))
		tailTypes = append(tailTypes, h.RecordType)
	}
	assert.Contains(t, tailTypes, RecordTypeClose)
	var sawClose bool
	for _, tail := range chain.Windows[0].Boundary.Tail {
		sawClose = sawClose || tail.Record.RecordID == closed.RecordID
	}
	assert.True(t, sawClose, "the late Close is in the tail")

	texts := func(chain WindowChain) []string {
		_, findings := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: key})
		var out []string
		for _, f := range findings {
			out = append(out, f.Text)
		}
		return out
	}
	omitted := chain
	omitted.Windows = append([]ChainWindow{}, chain.Windows...)
	b := *chain.Windows[0].Boundary
	b.Tail = b.Tail[1:]
	omitted.Windows[0].Boundary = &b
	assert.NotEmpty(t, texts(omitted), "a tail leaf left out")
	rewritten := chain
	rewritten.Windows = append([]ChainWindow{}, chain.Windows...)
	b2 := *chain.Windows[0].Boundary
	b2.Consistency = append([]byte{}, chain.Windows[0].Boundary.Consistency[:len(chain.Windows[0].Boundary.Consistency)-1]...)
	rewritten.Windows[0].Boundary = &b2
	assert.Contains(t, texts(rewritten), "window 2026-10's final tree does not extend its boundary")
	stripped := chain
	stripped.Windows = append([]ChainWindow{}, chain.Windows...)
	stripped.Windows[0].Boundary = nil
	assert.Contains(t, texts(stripped), "window 2026-10's close record names a boundary the chain does not carry")
}

func TestAChainNamesAnOrdinaryRecordSlippedIntoAGrace(t *testing.T) {
	ctx := context.Background()
	f := newGraceFixture(t)
	shelf := f.open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	mustAppend(t, oct, observation("deal", "a"))
	setClock(f.clock, utc(2026, 11, 3))
	_, _, err = shelf.Current(ctx)
	require.NoError(t, err)
	// A holder that bypasses the admission rule: its record is still in the
	// tail, and named.
	oct.mu.Lock()
	window := oct.window
	oct.window = nil
	oct.mu.Unlock()
	mustAppend(t, oct, observation("deal", "slipped in"))
	oct.mu.Lock()
	oct.window = window
	oct.mu.Unlock()
	setClock(f.clock, utc(2026, 12, 9))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)
	chain, err := shelf.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	_, findings := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: shelfCheckpointKey(f)})
	require.NotEmpty(t, findings)
	assert.Contains(t, findings[0].Text, "window 2026-10 holds a late observation record at")
}

func TestAClockJumpPastAGraceClosesWindowsAtOnce(t *testing.T) {
	ctx := context.Background()
	f := newGraceFixture(t)
	shelf := f.open(t)
	_, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 12, 20))
	_, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-12", w.Label)
	windows, err := shelf.Windows(ctx)
	require.NoError(t, err)
	require.Len(t, windows, 3)
	assert.True(t, windows[0].Closed)
	assert.True(t, windows[1].Closed)
	chain, err := shelf.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, chain.V, "no window took a boundary")
	_, findings := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: shelfCheckpointKey(f)})
	assert.Empty(t, findings)
}

// graceChain is a shelf whose October took a boundary, received a late
// Close and closed, rolled into December; and October's chain alone.
func graceChain(t *testing.T, opts WindowChainOptions) (*shelfFixture, *Shelf, WindowChain) {
	t.Helper()
	ctx := context.Background()
	f := newGraceFixture(t)
	shelf := f.open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	mustAppend(t, oct, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Correlation: Correlation{ExchangeID: "x1", RequestDigest: "r1"}, Payloads: [][]byte{[]byte("p1")}})
	setClock(f.clock, utc(2026, 11, 3))
	_, _, err = shelf.Current(ctx)
	require.NoError(t, err)
	_, err = octoberClose(t, oct, ClosePeriod{Start: utc(2026, 10, 1), End: utc(2026, 11, 1)})
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 12, 9))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)
	chain, err := shelf.WindowChain(ctx, "2026-10", "2026-10", opts)
	require.NoError(t, err)
	return f, shelf, chain
}

func findingTexts(chain WindowChain, opts VerifyChainOptions) []string {
	_, findings := VerifyWindowChain(chain, opts)
	var out []string
	for _, f := range findings {
		out = append(out, f.Text)
	}
	return out
}

// The last window of a chain has no next window to pin its boundary: its
// close record does.
func TestTheCloseRecordPinsALastWindowsBoundary(t *testing.T) {
	f, shelf, chain := graceChain(t, WindowChainOptions{})
	key := VerifyChainOptions{CheckpointKey: shelfCheckpointKey(f)}
	require.Empty(t, findingTexts(chain, key))

	stripped := chain
	stripped.V = 1
	stripped.Windows = append([]ChainWindow{}, chain.Windows...)
	stripped.Windows[0].Boundary = nil
	assert.Contains(t, findingTexts(stripped, key), "window 2026-10's close record names a boundary the chain does not carry")

	// A swapped boundary: another checkpoint October signed, after the
	// real boundary, with its own consistency proof and tail, so only the
	// close record's naming tells it apart.
	ctx := context.Background()
	oct, _, err := shelf.WindowBook(ctx, "2026-10")
	require.NoError(t, err)
	checkpoints, err := oct.Checkpoints(ctx)
	require.NoError(t, err)
	real, err := checkpointFromStatement(chain.Windows[0].Boundary.Checkpoint)
	require.NoError(t, err)
	final, err := checkpointFromStatement(chain.Windows[0].FinalCheckpoint)
	require.NoError(t, err)
	var later *Checkpoint
	for i := range checkpoints {
		if checkpoints[i].Entries > real.Entries && checkpoints[i].Entries < final.Entries {
			later = &checkpoints[i]
		}
	}
	require.NotNil(t, later)
	prover, err := oct.commitments.(*CLLSubstrate).cllProver(ctx, final)
	require.NoError(t, err)
	consistency, err := prover.consistencyProof(later.TreeSize, final.TreeSize)
	require.NoError(t, err)
	swapped := chain
	swapped.Windows = append([]ChainWindow{}, chain.Windows...)
	b := *chain.Windows[0].Boundary
	b.Checkpoint, b.Consistency = later.Statement, consistency
	b.Tail = b.Tail[later.Entries-real.Entries:]
	swapped.Windows[0].Boundary = &b
	assert.Contains(t, findingTexts(swapped, key), "window 2026-10 carries a boundary other than the one its close record names")

	extra := chain
	extra.Windows = append([]ChainWindow{}, chain.Windows...)
	b2 := *chain.Windows[0].Boundary
	b2.Tail = append(append([]ChainTail{}, b2.Tail...), b2.Tail[0])
	extra.Windows[0].Boundary = &b2
	assert.NotEmpty(t, findingTexts(extra, key), "an extra tail leaf")
}

func TestAWitnessedGraceChainNeedsTheBoundarysReceipt(t *testing.T) {
	witnessKey, directory := testWitness()
	opts := WindowChainOptions{Receipts: func(_ context.Context, cp Checkpoint) ([]json.RawMessage, error) {
		return []json.RawMessage{rawReceipt(t, mintReceipt(t, cp.Statement, witnessKey))}, nil
	}}
	f, _, chain := graceChain(t, opts)
	key := VerifyChainOptions{CheckpointKey: shelfCheckpointKey(f), Witnesses: directory}
	assert.Empty(t, findingTexts(chain, key))
	unreceipted := chain
	unreceipted.Windows = append([]ChainWindow{}, chain.Windows...)
	b := *chain.Windows[0].Boundary
	b.Receipts = nil
	unreceipted.Windows[0].Boundary = &b
	assert.Contains(t, findingTexts(unreceipted, key), "window 2026-10 not witnessed: its boundary checkpoint carries no receipt")
}

func TestAWindowThatTookItsBoundaryRefusesOrdinaryRecords(t *testing.T) {
	ctx := context.Background()
	f := newGraceFixture(t)
	shelf := f.open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 11, 3))
	_, _, err = shelf.Current(ctx)
	require.NoError(t, err)
	windows, err := shelf.Windows(ctx)
	require.NoError(t, err)
	assert.True(t, windows[0].InGrace)
	assert.Equal(t, utc(2026, 11, 8), windows[0].ClosesAt)
	// A clock set back into October still cannot add to it.
	setClock(f.clock, utc(2026, 10, 30))
	_, err = oct.Append(ctx, observation("deal", "backdated"))
	assert.ErrorIs(t, err, ErrWindowClosed)
	// A legacy Close body is refused in grace.
	setClock(f.clock, utc(2026, 11, 4))
	_, err = oct.Append(ctx, Entry{RecordType: RecordTypeClose, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"counterparty":"b","profile":"p","reconciliation":{"pairs":[]}}`)})
	assert.ErrorIs(t, err, ErrWindowClosed)
}

func TestARollFinishesAfterACrashBetweenTheBoundaryAndTheNextWindow(t *testing.T) {
	ctx := context.Background()
	f := newGraceFixture(t)
	shelf := f.open(t)
	_, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 11, 3))
	shelf.mu.Lock()
	rows, err := shelf.windowRowsLocked(ctx)
	require.NoError(t, err)
	_, err = shelf.boundaryLocked(ctx, rows[0])
	shelf.mu.Unlock()
	require.NoError(t, err)
	require.NoError(t, shelf.Release())

	shelf = f.open(t)
	nov, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-11", w.Label)
	var open WindowOpenStatement
	require.NoError(t, json.Unmarshal(nov.logLocked()[0].Header.Statement, &open))
	windows, err := shelf.Windows(ctx)
	require.NoError(t, err)
	require.True(t, windows[0].InGrace)
	assert.Equal(t, rows[0].logID, open.Previous.LogID)
	shelf.mu.Lock()
	rows, err = shelf.windowRowsLocked(ctx)
	shelf.mu.Unlock()
	require.NoError(t, err)
	assert.Equal(t, rows[0].boundaryEntries, open.Previous.Entries, "the next window names the stored boundary")
}

func TestAShelfVersionThatDisagreesWithItsGraceIsRefused(t *testing.T) {
	f := newGraceFixture(t)
	require.NoError(t, f.open(t).Release())
	db, err := sql.Open("sqlite", "file:"+f.path)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE shelf_meta SET version = 3`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	_, err = OpenSQLiteShelf(context.Background(), f.cfg)
	assert.ErrorIs(t, err, ErrCorrupt)
}
