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
	assert.Contains(t, texts(stripped), "chain breaks at 2026-11: its open record names a previous tree other than window 2026-10's final tree")
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
