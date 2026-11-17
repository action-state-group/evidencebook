// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordTimeShelf is a migrated shelf whose book deal/a has its genesis fact
// in the migration's stamp and a second fact, after another append, in the
// second of two stamps after it. It returns the cadence, the book id of
// deal/a, and the two stamps.
func recordTimeShelf(t *testing.T) (*shelfFixture, *Shelf, *Cadence, string, CadenceTick, CadenceTick) {
	t.Helper()
	ctx := context.Background()
	f := newShelfFixture(t)
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

// leavesOf is the number of records a book's log holds.
func leavesOf(t *testing.T, shelf *Shelf, logID string) uint64 {
	t.Helper()
	return uint64(countRows(t, shelf, `SELECT COUNT(*) FROM book_records WHERE book_id = ?`, logID))
}

func witnessAt(t *testing.T, c *Cadence, tick CadenceTick, url string, iat time.Time) {
	t.Helper()
	check, err := c.Witnessed(context.Background(), tick.N, receiptAt(t, tick.Checkpoint, url, iat))
	require.NoError(t, err)
	require.Equal(t, WitnessVerified, check.State, check.Reason)
}

func recordTime(t *testing.T, shelf *Shelf, bookID string, position uint64, witnesses ...string) (time.Time, bool) {
	t.Helper()
	at, ok, err := shelf.RecordTime(context.Background(), bookID, position, witnesses)
	require.NoError(t, err)
	return at, ok
}

// A record's time is the earliest registration time, from a named witness,
// of a shelf checkpoint covering the first fact whose size covers it: a
// later checkpoint covers earlier facts too, and a witness not named gives
// nothing.
func TestARecordTimeIsTheEarliestNamedRegistration(t *testing.T) {
	_, shelf, c, bookID, _, second := recordTimeShelf(t)
	// The first fact of deal/a (the migration's genesis fact), its tick, and
	// the last record it covers: its size's leaves.
	var size, n uint64
	require.NoError(t, shelf.db.QueryRow(`SELECT json_extract(doc, '$.size'), tick FROM shelf_entries WHERE log_id = 'deal/a' ORDER BY entry_seq LIMIT 1`).Scan(&size, &n))
	inFirst, _ := mmr.LeafCount(size)
	first, err := c.Get(context.Background(), n)
	require.NoError(t, err)
	require.Less(t, inFirst, leavesOf(t, shelf, "deal/a"))
	last := leavesOf(t, shelf, "deal/a")
	t1, t2 := tickTime.Add(10*time.Minute), tickTime.Add(20*time.Minute)

	_, ok := recordTime(t, shelf, bookID, inFirst, testWitnessURL)
	assert.False(t, ok, "no receipt yet")

	witnessAt(t, c, second, testWitnessURL, t2)
	at, ok := recordTime(t, shelf, bookID, inFirst, testWitnessURL)
	require.True(t, ok, "the second stamp's checkpoint covers the first fact")
	assert.Equal(t, t2, at)
	at, ok = recordTime(t, shelf, bookID, last, testWitnessURL)
	require.True(t, ok)
	assert.Equal(t, t2, at)

	witnessAt(t, c, first, otherWitnessURL, t1)
	at, ok = recordTime(t, shelf, bookID, inFirst, testWitnessURL, otherWitnessURL)
	require.True(t, ok)
	assert.Equal(t, t1, at, "the earliest among the named witnesses")
	at, _ = recordTime(t, shelf, bookID, inFirst, testWitnessURL)
	assert.Equal(t, t2, at, "a witness not named gives nothing")
	_, ok = recordTime(t, shelf, bookID, last, otherWitnessURL)
	assert.False(t, ok, "the first stamp's checkpoint does not cover the second fact")
	_, ok = recordTime(t, shelf, bookID, inFirst, "https://unnamed.test")
	assert.False(t, ok)
	_, ok = recordTime(t, shelf, bookID, inFirst)
	assert.False(t, ok, "no witness named")
	_, ok = recordTime(t, shelf, bookID, last+1, testWitnessURL)
	assert.False(t, ok, "no fact covers the position")
	_, ok = recordTime(t, shelf, "no-such-book", 1, testWitnessURL)
	assert.False(t, ok)

	_, _, err = shelf.RecordTime(context.Background(), bookID, 0, []string{testWitnessURL})
	assert.ErrorIs(t, err, ErrInvalid)
}

// A receipt with no registration time gives no time; the same witness's
// later receipt with a time does, and of two times the earlier is kept.
func TestARecordTimeNeedsARegistrationTime(t *testing.T) {
	_, shelf, c, bookID, _, second := recordTimeShelf(t)
	last := leavesOf(t, shelf, "deal/a")
	witnessAt(t, c, second, testWitnessURL, time.Time{})
	_, ok := recordTime(t, shelf, bookID, last, testWitnessURL)
	assert.False(t, ok, "a receipt with no registration time")
	sum := sha256.Sum256(second.Checkpoint)
	cp := hex.EncodeToString(sum[:])
	assert.Equal(t, 1, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ? AND registered_at IS NULL`, cp))

	later, earlier := tickTime.Add(30*time.Minute), tickTime.Add(20*time.Minute)
	witnessAt(t, c, second, testWitnessURL, later)
	at, ok := recordTime(t, shelf, bookID, last, testWitnessURL)
	require.True(t, ok)
	assert.Equal(t, later, at)
	witnessAt(t, c, second, testWitnessURL, earlier)
	witnessAt(t, c, second, testWitnessURL, later.Add(time.Hour))
	witnessAt(t, c, second, testWitnessURL, time.Time{})
	at, _ = recordTime(t, shelf, bookID, last, testWitnessURL)
	assert.Equal(t, earlier, at, "the witness's earliest registration is kept")
	assert.Equal(t, 1, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ?`, cp), "one row per witness")
}

// The journal's facts carry its size in their status opening, which a
// record time reads.
func TestARecordTimeCoversTheJournal(t *testing.T) {
	ctx := context.Background()
	_, shelf, c, _, _, _ := recordTimeShelf(t)
	journal, err := shelf.Journal(ctx)
	require.NoError(t, err)
	mustAppend(t, journal, observation("journal", "1"))
	stamp, err := c.Stamp(ctx, tickTime.Add(4*time.Minute))
	require.NoError(t, err)
	var bookID string
	require.NoError(t, shelf.db.QueryRow(`SELECT book_id FROM shelf_entries WHERE log_id = ? LIMIT 1`, JournalLogID).Scan(&bookID))
	iat := tickTime.Add(time.Hour)
	witnessAt(t, c, stamp.Tick, testWitnessURL, iat)
	held := leavesOf(t, shelf, JournalLogID)
	at, ok := recordTime(t, shelf, bookID, held, testWitnessURL)
	require.True(t, ok)
	assert.Equal(t, iat, at)
	_, ok = recordTime(t, shelf, bookID, held+1, testWitnessURL)
	assert.False(t, ok)
}

// Every verified receipt is kept, a tree tick's included; a witnessed tick
// whose receipt is missing from shelf_receipts (a shelf written before the
// table) gets it back at the cadence's next open, unless it no longer
// verifies under the configured directory.
func TestReceiptsOfWitnessedTicksAreKeptAndBackfilled(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	tree := mustTick(t, c, tickTime)
	witnessAt(t, c, tree, testWitnessURL, tickTime.Add(time.Minute))
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	stamp, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	witnessAt(t, c, stamp.Tick, testWitnessURL, tickTime.Add(3*time.Minute))
	require.Equal(t, 2, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts WHERE registered_at IS NOT NULL`))
	_, err = shelf.db.Exec(`DELETE FROM shelf_receipts`)
	require.NoError(t, err)
	require.NoError(t, shelf.Release())

	// A directory that no longer lists the witness backfills nothing.
	shelf = f.open(t)
	_, err = shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: aacwitness.Directory{}})
	require.NoError(t, err)
	assert.Equal(t, 0, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts`))
	require.NoError(t, shelf.Release())

	shelf = f.open(t)
	newStampCadence(t, shelf)
	assert.Equal(t, 2, countRows(t, shelf, `SELECT COUNT(*) FROM shelf_receipts WHERE registered_at IS NOT NULL AND witness_id = ?`, testWitnessURL))
}

// A handover carries every witness's receipt for its anchor, and is
// witnessed only by one with a registration time.
func TestAHandoverIsWitnessedOnlyWithARegistrationTime(t *testing.T) {
	ctx := context.Background()
	_, _, c, _, _, second := recordTimeShelf(t)
	witnessAt(t, c, second, testWitnessURL, time.Time{})
	h, err := c.Handover(ctx, 0)
	require.NoError(t, err)
	require.Len(t, h.Receipts, 1)
	anchor, err := checkpointFromStatement(h.Anchor)
	require.NoError(t, err)
	got, err := VerifyHandover(h, anchor.KeyID, testWitnesses(t))
	require.NoError(t, err)
	assert.False(t, got.Witnessed, "a receipt with no registration time")

	witnessAt(t, c, second, otherWitnessURL, tickTime.Add(time.Hour))
	h, err = c.Handover(ctx, 0)
	require.NoError(t, err)
	require.Len(t, h.Receipts, 2)
	assert.Equal(t, otherWitnessURL, h.Receipts[0].TSURL, "in witness URL order")
	got, err = VerifyHandover(h, anchor.KeyID, testWitnesses(t))
	require.NoError(t, err)
	assert.True(t, got.Witnessed)
}

func TestARecordTimeOnAReleasedShelf(t *testing.T) {
	_, shelf, _, bookID, _, _ := recordTimeShelf(t)
	require.NoError(t, shelf.Release())
	_, _, err := shelf.RecordTime(context.Background(), bookID, 1, []string{testWitnessURL})
	assert.ErrorIs(t, err, ErrReleased)
}
