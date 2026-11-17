// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// earliest-receipt-not-anchor (docs/design/461-deal-profile.md §8.1,
// invariant C): the earliest named receipt for a record belongs to a shelf
// checkpoint before the anchor. The handover carries that checkpoint with its
// receipt, VerifyHandover checks it against the handed-over entries, and the
// reader's time equals the holder's RecordTime.
func TestAHandoverReaderGetsTheEarliestReceiptBeforeTheAnchor(t *testing.T) {
	ctx := context.Background()
	_, shelf, c, bookID, first, second := recordTimeShelf(t)
	anchor, err := c.Stamp(ctx, tickTime.Add(4*time.Minute))
	require.NoError(t, err)
	early, late := tickTime.Add(10*time.Minute), tickTime.Add(40*time.Minute)
	witnessAt(t, c, first, testWitnessURL, early)
	witnessAt(t, c, second, testWitnessURL, tickTime.Add(30*time.Minute))
	witnessAt(t, c, anchor.Tick, testWitnessURL, late)

	h, err := c.Handover(ctx, 0)
	require.NoError(t, err)
	require.Len(t, h.Witnessed, 2, "the two earlier witnessed stamps, not the anchor")
	key := checkpointKeyOf(t, h)
	times, err := VerifyHandoverTimes(h, key, testWitnesses(t))
	require.NoError(t, err)

	last := leavesOf(t, shelf, "deal/a")
	for p := uint64(1); p <= last; p++ {
		want, wantOK := recordTime(t, shelf, bookID, p, testWitnessURL)
		got, ok, err := times.RecordTime(bookID, p, []string{testWitnessURL})
		require.NoError(t, err)
		require.Equal(t, wantOK, ok, p)
		assert.Equal(t, want, got, "position %d", p)
	}
	got, ok, err := times.RecordTime(bookID, 1, []string{testWitnessURL})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, early, got, "the first record's time is the first stamp's receipt, not the anchor's")

	// Without the earlier checkpoints a reader sees only the anchor's time.
	anchorOnly := h
	anchorOnly.Witnessed = nil
	times, err = VerifyHandoverTimes(anchorOnly, key, testWitnesses(t))
	require.NoError(t, err)
	got, ok, err = times.RecordTime(bookID, 1, []string{testWitnessURL})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, late, got)

	for name, ok := range map[string]bool{
		"a witness not named": func() bool { _, ok, _ := times.RecordTime(bookID, 1, []string{"https://other.test"}); return ok }(),
		"another book":        func() bool { _, ok, _ := times.RecordTime("no-such-book", 1, []string{testWitnessURL}); return ok }(),
		"past the book":       func() bool { _, ok, _ := times.RecordTime(bookID, last+1, []string{testWitnessURL}); return ok }(),
	} {
		assert.False(t, ok, name)
	}
	_, _, err = times.RecordTime(bookID, 0, []string{testWitnessURL})
	assert.ErrorIs(t, err, ErrInvalid)
}

func checkpointKeyOf(t *testing.T, h Handover) string {
	t.Helper()
	anchor, err := checkpointFromStatement(h.Anchor)
	require.NoError(t, err)
	return anchor.KeyID
}

func TestAHandoverRefusesABadWitnessedCheckpoint(t *testing.T) {
	ctx := context.Background()
	_, _, c, _, first, second := recordTimeShelf(t)
	_, err := c.Stamp(ctx, tickTime.Add(4*time.Minute))
	require.NoError(t, err)
	witnessAt(t, c, first, testWitnessURL, tickTime.Add(10*time.Minute))
	witnessAt(t, c, second, testWitnessURL, tickTime.Add(20*time.Minute))
	h, err := c.Handover(ctx, 0)
	require.NoError(t, err)
	require.Len(t, h.Witnessed, 2)
	key := checkpointKeyOf(t, h)
	_, err = VerifyHandover(h, key, testWitnesses(t))
	require.NoError(t, err)

	// Another shelf's checkpoint: signed by another key, of another log.
	_, _, oc, _, ofirst, _ := recordTimeShelfKeyed(t, 108)
	witnessAt(t, oc, ofirst, testWitnessURL, tickTime.Add(10*time.Minute))
	foreign, err := oc.Handover(ctx, 0)
	require.NoError(t, err)
	require.NotEmpty(t, foreign.Witnessed)

	for name, edit := range map[string]func(h *Handover){
		"the anchor again":              func(h *Handover) { h.Witnessed[0].Checkpoint = h.Anchor },
		"one checkpoint twice":          func(h *Handover) { h.Witnessed[1] = h.Witnessed[0] },
		"no receipt":                    func(h *Handover) { h.Witnessed[0].Receipts = nil },
		"another holder's":              func(h *Handover) { h.Witnessed[0] = foreign.Witnessed[0] },
		"not a checkpoint":              func(h *Handover) { h.Witnessed[0].Checkpoint = []byte("x") },
		"the holder's key, another log": func(h *Handover) { h.Witnessed[0].Checkpoint = h.BookCheckpoints[0] },
	} {
		bad := h
		bad.Witnessed = append([]HandoverCheckpoint{}, h.Witnessed...)
		edit(&bad)
		_, err := VerifyHandover(bad, key, testWitnesses(t))
		assert.ErrorIs(t, err, ErrInvalid, name)
		_, err = VerifyHandoverTimes(bad, key, testWitnesses(t))
		assert.ErrorIs(t, err, ErrInvalid, name)
	}

	// A checkpoint past the anchor: the handover runs to an earlier stamp.
	earlier, err := c.Handover(ctx, second.N)
	require.NoError(t, err)
	past := earlier
	past.Witnessed = append([]HandoverCheckpoint{}, h.Witnessed...)
	past.Witnessed[1] = HandoverCheckpoint{Checkpoint: h.Anchor, Receipts: h.Witnessed[0].Receipts}
	_, err = VerifyHandover(past, key, testWitnesses(t))
	assert.ErrorIs(t, err, ErrInvalid, "a checkpoint past the anchor")
}

// recordTimeShelfKeyed is recordTimeShelf's shelf under another checkpoint
// key.
func recordTimeShelfKeyed(t *testing.T, key byte) (*shelfFixture, *Shelf, *Cadence, string, CadenceTick, CadenceTick) {
	t.Helper()
	ctx := context.Background()
	f := newShelfFixture(t)
	f.cfg.CheckpointKey = seededKey(key)
	shelf := f.open(t)
	c, _ := newStampCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	first, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	mustAppend(t, a, observation("a", "2"))
	second, err := c.Stamp(ctx, tickTime.Add(3*time.Minute))
	require.NoError(t, err)
	var bookID string
	require.NoError(t, shelf.db.QueryRow(`SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' LIMIT 1`).Scan(&bookID))
	return f, shelf, c, bookID, first.Tick, second.Tick
}
