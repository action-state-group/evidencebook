// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cadenceCard2 checks a cadence staple for holder and returns the check,
// the card's positions and its window (cardPositions, history_card2_test.go).
func cadenceCard2(t *testing.T, staple Staple, holder string) (StapleCheck, []uint64, uint64) {
	t.Helper()
	got, err := CheckStaple(staple, holder)
	require.NoError(t, err)
	require.Equal(t, DerivationHistoryCard2, got.Derivation)
	window, positions := cardPositions(t, got.Artifact)
	return got, positions, window
}

// The cadence's history_card/2 is a staple CheckStaple accepts at any
// length (u539 removes the 35-tick cap): it lists the sampled positions
// P(n-1, window) of the finished ticks, as of tick n.
func TestCadenceHistoryCard2SamplesTheTicks(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	c, _ := newCadence(t, shelf)
	holder := KeyID(fixture.cfg.Signer)

	_, err := c.HistoryCard2(ctx, 0, 0)
	require.ErrorIs(t, err, ErrNotFound, "no tick yet")

	book := mustOpenBook(t, shelf, "deal/a")
	ticks := make([]CadenceTick, 0, 40)
	at := tickTime
	for i := range 40 {
		mustAppend(t, book, observation("a", string(rune('A'+i%26))+string(rune('a'+i/26))))
		tick := mustTick(t, c, at)
		ticks = append(ticks, tick)
		at = tick.DueNext
	}

	for _, tc := range []struct {
		n, window uint64
	}{{0, 16}, {40, 4}, {20, 1}, {1, 16}, {40, 64}} {
		staple, err := c.HistoryCard2(ctx, tc.n, int(tc.window))
		require.NoError(t, err, "tick %d window %d", tc.n, tc.window)
		n := tc.n
		if n == 0 {
			n = 40
		}
		got, positions, window := cadenceCard2(t, staple, holder)
		assert.Equal(t, digestBytes(ticks[n-1].Checkpoint), got.Anchor, "as of tick %d", n)
		assert.Equal(t, evidencerequest.HistoryPositions(n-1, tc.window), positions, "tick %d window %d", n, tc.window)
		assert.Equal(t, tc.window, window)
	}

	// window 0 is the default window.
	staple, err := c.HistoryCard2(ctx, 0, 0)
	require.NoError(t, err)
	_, _, window := cadenceCard2(t, staple, holder)
	assert.Equal(t, uint64(DefaultHistoryWindow), window)
}

func TestCadenceHistoryCard2Refusals(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	c, _ := newCadence(t, shelf)
	first := mustTick(t, c, tickTime)

	for _, w := range []int{-1, MaxHistoryWindow + 1} {
		_, err := c.HistoryCard2(ctx, 1, w)
		assert.ErrorIs(t, err, ErrInvalid, "window %d", w)
	}
	_, err := c.HistoryCard2(ctx, 1, MaxHistoryWindow)
	require.NoError(t, err, "the largest window")

	_, err = c.HistoryCard2(ctx, 2, 0)
	assert.ErrorIs(t, err, ErrNotFound, "a tick not yet cut")

	// An unfinished newest tick is not an anchor; n=0 takes the newest finished one.
	second := mustTick(t, c, first.DueNext)
	_, err = shelf.db.Exec(`UPDATE cadence_ticks SET checkpoint_size = 0 WHERE tick = ?`, second.N)
	require.NoError(t, err)
	_, err = c.HistoryCard2(ctx, 2, 0)
	assert.ErrorIs(t, err, ErrNotFound, "an unfinished tick")
	staple, err := c.HistoryCard2(ctx, 0, 0)
	require.NoError(t, err)
	got, _, _ := cadenceCard2(t, staple, KeyID(fixture.cfg.Signer))
	assert.Equal(t, digestBytes(first.Checkpoint), got.Anchor, "n=0 skips the unfinished tick")

	// An unfinished tick at a sampled position before the anchor is stored
	// state that cannot arise.
	_, err = shelf.db.Exec(`UPDATE cadence_ticks SET checkpoint_size = 0 WHERE tick = 1`)
	require.NoError(t, err)
	_, err = shelf.db.Exec(`UPDATE cadence_ticks SET checkpoint_size = 7 WHERE tick = 2`)
	require.NoError(t, err)
	_, err = c.HistoryCard2(ctx, 2, 0)
	assert.ErrorIs(t, err, ErrCorrupt)

	require.NoError(t, shelf.Release())
	_, err = c.HistoryCard2(ctx, 1, 0)
	assert.ErrorIs(t, err, ErrReleased)
}

// After the store moves to the new layout, ticks are stamps cut on the same
// cadence log: the card spans the move and still checks.
func TestCadenceHistoryCard2SpansTheMove(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	c, _ := newStampCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	mustAppend(t, a, observation("a", "2"))
	_, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	last, err := c.Stamp(ctx, tickTime.Add(3*time.Minute))
	require.NoError(t, err)

	staple, err := c.HistoryCard2(ctx, 0, 0)
	require.NoError(t, err)
	got, positions, _ := cadenceCard2(t, staple, KeyID(fixture.cfg.Signer))
	assert.Equal(t, digestBytes(last.Tick.Checkpoint), got.Anchor)
	assert.Equal(t, evidencerequest.HistoryPositions(last.Tick.N-1, DefaultHistoryWindow), positions)
}
