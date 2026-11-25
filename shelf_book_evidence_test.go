// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bookEvidenceShelf is recordTimeShelf with a third record and stamp, the
// ticks of the book's three facts (the migration's genesis fact, the second
// and third stamps'), the book's evidence up to the third, its checkpoint key
// and shelf log id. The first stamp wrote no fact of the book.
func bookEvidenceShelf(t *testing.T) (*Shelf, *Cadence, string, [3]CadenceTick, BookEvidence, string, string) {
	t.Helper()
	ctx := context.Background()
	_, shelf, c, bookID, _, second := recordTimeShelf(t)
	a, err := shelf.Open(ctx, "deal/a")
	require.NoError(t, err)
	mustAppend(t, a, observation("a", "3"))
	third, err := c.Stamp(ctx, tickTime.Add(4*time.Minute))
	require.NoError(t, err)
	e, err := c.BookEvidence(ctx, bookID, 0)
	require.NoError(t, err)
	cp, err := checkpointFromStatement(third.Tick.Checkpoint)
	require.NoError(t, err)
	var genesisTick uint64
	require.NoError(t, shelf.db.QueryRow(`SELECT tick FROM shelf_entries WHERE log_id = 'deal/a' ORDER BY entry_seq LIMIT 1`).Scan(&genesisTick))
	genesis, err := c.Get(ctx, genesisTick)
	require.NoError(t, err)
	return shelf, c, bookID, [3]CadenceTick{genesis, second, third.Tick}, e, cp.KeyID, cp.LogID
}

// A book's evidence verifies on its own and gives the record times the
// shelf's RecordTime gives when each fact's own stamp is witnessed.
func TestBookEvidenceGivesTheShelfsRecordTimes(t *testing.T) {
	shelf, c, bookID, ticks, e, key, shelfLog := bookEvidenceShelf(t)
	require.Len(t, e.Facts, 3, "the genesis fact and one per stamp that grew the book")
	require.Len(t, e.Consistency, 2)
	for i, f := range e.Facts {
		assert.Nil(t, f.Proof.StatusOpening, "fact %d: an adjudicator gets no status opening", i+1)
	}
	// Each stamp's own checkpoint witnessed, later stamps later.
	for i, tick := range ticks {
		witnessAt(t, c, tick, testWitnessURL, tickTime.Add(time.Duration(10*(i+1))*time.Minute))
	}
	e, err := c.BookEvidence(context.Background(), bookID, 0)
	require.NoError(t, err)
	times, err := VerifyBookEvidence(e, key, shelfLog, testWitnesses(t))
	require.NoError(t, err)
	last := leavesOf(t, shelf, "deal/a")
	for p := uint64(1); p <= last; p++ {
		want, wantOK := recordTime(t, shelf, bookID, p, testWitnessURL)
		got, ok, err := times.RecordTime(bookID, p, []string{testWitnessURL})
		require.NoError(t, err)
		require.Equal(t, wantOK, ok, p)
		assert.Equal(t, want, got, "position %d", p)
	}
	_, ok, err := times.RecordTime(bookID, last+1, []string{testWitnessURL})
	require.NoError(t, err)
	assert.False(t, ok, "past the book")
	_, ok, err = times.ReceivedRecordTime(bookID, "deal/b", 1, []string{testWitnessURL})
	require.NoError(t, err)
	assert.False(t, ok, "this book received nothing")
}

// A checkpoint times a record only through its own fact: without the whole
// shelf log, a checkpoint's size does not say it includes a later fact. So a
// time from evidence is never earlier than the shelf's: a stamp that wrote
// no fact of the book gives the shelf a time and the evidence none, and a
// fact left out can only make a time later.
func TestBookEvidenceTimesARecordOnlyThroughItsOwnFact(t *testing.T) {
	ctx := context.Background()
	shelf, c, bookID, ticks, _, key, shelfLog := bookEvidenceShelf(t)
	// The first stamp (after the genesis fact's) wrote no fact of the book.
	var firstStamp uint64
	require.NoError(t, shelf.db.QueryRow(`SELECT MIN(tick) FROM cadence_ticks WHERE tick > ? AND checkpoint_size > 0`, ticks[0].N).Scan(&firstStamp))
	require.Less(t, firstStamp, ticks[1].N)
	noFact, err := c.Get(ctx, firstStamp)
	require.NoError(t, err)
	witnessAt(t, c, noFact, testWitnessURL, tickTime.Add(10*time.Minute))
	witnessAt(t, c, ticks[2], testWitnessURL, tickTime.Add(30*time.Minute))
	e, err := c.BookEvidence(ctx, bookID, 0)
	require.NoError(t, err)
	times, err := VerifyBookEvidence(e, key, shelfLog, testWitnesses(t))
	require.NoError(t, err)
	shelfTime, ok := recordTime(t, shelf, bookID, 1, testWitnessURL)
	require.True(t, ok)
	assert.Equal(t, tickTime.Add(10*time.Minute), shelfTime, "the shelf counts the checkpoint with no fact of the book")
	got, ok, err := times.RecordTime(bookID, 1, []string{testWitnessURL})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, tickTime.Add(30*time.Minute), got, "the evidence times it through the third fact only")

	// The last fact left out: the chain still verifies (it is a prefix),
	// and no record is timed earlier, the last one not at all.
	short := e
	short.Facts = e.Facts[:2]
	short.Consistency = e.Consistency[:1]
	times, err = VerifyBookEvidence(short, key, shelfLog, testWitnesses(t))
	require.NoError(t, err)
	last := leavesOf(t, shelf, "deal/a")
	_, ok, err = times.RecordTime(bookID, last, []string{testWitnessURL})
	require.NoError(t, err)
	assert.False(t, ok)
	for p := uint64(1); p <= last; p++ {
		full, fullOK := recordTime(t, shelf, bookID, p, testWitnessURL)
		got, ok, err := times.RecordTime(bookID, p, []string{testWitnessURL})
		require.NoError(t, err)
		if ok {
			require.True(t, fullOK)
			assert.False(t, got.Before(full), "position %d is never earlier than the shelf's time", p)
		}
	}
}

func TestBookEvidenceRefusals(t *testing.T) {
	_, _, _, _, e, key, shelfLog := bookEvidenceShelf(t)
	clone := func() BookEvidence {
		raw, err := json.Marshal(e)
		require.NoError(t, err)
		var out BookEvidence
		require.NoError(t, json.Unmarshal(raw, &out))
		return out
	}
	_, err := VerifyBookEvidence(clone(), key, shelfLog, testWitnesses(t))
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		mutate        func(*BookEvidence)
		key, shelfLog string
	}{
		"another checkpoint key": {key: "another-key"},
		"another shelf log":      {shelfLog: "another-shelf"},
		"another book":           {mutate: func(b *BookEvidence) { b.BookID = "another-book" }},
		"the first fact left out": {mutate: func(b *BookEvidence) {
			b.Facts, b.Consistency = b.Facts[1:], b.Consistency[1:]
		}},
		"a middle fact left out": {mutate: func(b *BookEvidence) { b.Facts = append(b.Facts[:1], b.Facts[2:]...) }},
		"two facts swapped":      {mutate: func(b *BookEvidence) { b.Facts[1], b.Facts[2] = b.Facts[2], b.Facts[1] }},
		"a consistency proof left out": {mutate: func(b *BookEvidence) {
			b.Consistency = b.Consistency[:1]
		}},
		"a status opening": {mutate: func(b *BookEvidence) {
			b.Facts[0].Proof.StatusOpening = &FactOpening{}
		}},
		"a link opening of another value": {mutate: func(b *BookEvidence) {
			b.Facts[0].LinkOpening.Value = json.RawMessage(`{"checked_over":["x"],"last_received":null}`)
		}},
		"a link opening of another fact": {mutate: func(b *BookEvidence) {
			b.Facts[0].LinkOpening = b.Facts[1].LinkOpening
		}},
		"a tampered fact": {mutate: func(b *BookEvidence) {
			b.Facts[1].Proof.Fact = append([]byte{}, b.Facts[0].Proof.Fact...)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			b := clone()
			if tc.mutate != nil {
				tc.mutate(&b)
			}
			k, l := key, shelfLog
			if tc.key != "" {
				k = tc.key
			}
			if tc.shelfLog != "" {
				l = tc.shelfLog
			}
			_, err := VerifyBookEvidence(b, k, l, testWitnesses(t))
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

// BookEvidence hands the journal to nobody, and stops at the stamp asked.
func TestBookEvidenceUpToAStamp(t *testing.T) {
	_, c, bookID, ticks, _, _, _ := bookEvidenceShelf(t)
	e, err := c.BookEvidence(context.Background(), bookID, ticks[1].N)
	require.NoError(t, err)
	assert.Len(t, e.Facts, 2)
	none, err := c.BookEvidence(context.Background(), "no-such-book", 0)
	require.NoError(t, err)
	assert.Empty(t, none.Facts)
}
