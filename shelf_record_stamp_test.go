// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/action-state-group/checkpointed-local-log/go/mmr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A record's stamp is the tick of the first fact that covers it: the
// migration's genesis fact for the first record, the second stamp's for the
// record appended after it, none for a record no fact covers yet.
func TestARecordStampIsTheFirstCoveringFact(t *testing.T) {
	ctx := context.Background()
	_, shelf, _, bookID, first, second := recordTimeShelf(t)
	var size, genesis uint64
	require.NoError(t, shelf.db.QueryRow(`SELECT json_extract(doc, '$.size'), tick FROM shelf_entries WHERE log_id = 'deal/a' ORDER BY entry_seq LIMIT 1`).Scan(&size, &genesis))
	inFirst, _ := mmr.LeafCount(size)
	last := leavesOf(t, shelf, "deal/a")
	require.Less(t, genesis, first.N)

	for _, c := range []struct {
		position uint64
		n        uint64
		ok       bool
	}{{inFirst, genesis, true}, {last, second.N, true}, {last + 1, 0, false}} {
		n, ok, err := shelf.RecordStamp(ctx, bookID, c.position)
		require.NoError(t, err)
		assert.Equal(t, c.ok, ok, c.position)
		assert.Equal(t, c.n, n, c.position)
	}
	_, ok, err := shelf.RecordStamp(ctx, "no-such-book", 1)
	require.NoError(t, err)
	assert.False(t, ok)
	_, _, err = shelf.RecordStamp(ctx, bookID, 0)
	assert.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, shelf.Release())
	_, _, err = shelf.RecordStamp(ctx, bookID, 1)
	assert.ErrorIs(t, err, ErrReleased)
}

// A received record's stamp is the tick of the first fact whose link value
// reaches it, as ReceivedRecordTime finds that fact.
func TestAReceivedRecordStampIsTheFirstFactThatLinksIt(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, values := newStampCadence(t, shelf)
	values.set("deal/a", `{"row":"open"}`)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	link := func(size uint64) {
		values.mu.Lock()
		defer values.mu.Unlock()
		values.link["deal/a"] = LinkValue{LastReceived: &ReceivedPosition{LogID: "deal/theirs", Size: size, RecordDigest: dg(fmt.Sprint("theirs", size))}, CheckedOver: []string{}}
	}
	link(2)
	mustAppend(t, a, observation("a", "2"))
	s1, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	link(5)
	mustAppend(t, a, observation("a", "3"))
	s2, err := c.Stamp(ctx, tickTime.Add(3*time.Minute))
	require.NoError(t, err)
	var bookID string
	require.NoError(t, shelf.db.QueryRow(`SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' LIMIT 1`).Scan(&bookID))

	for _, c := range []struct {
		log      string
		position uint64
		n        uint64
		ok       bool
	}{{"deal/theirs", 1, s1.Tick.N, true}, {"deal/theirs", 2, s1.Tick.N, true}, {"deal/theirs", 3, s2.Tick.N, true},
		{"deal/theirs", 5, s2.Tick.N, true}, {"deal/theirs", 6, 0, false}, {"deal/other", 1, 0, false}} {
		n, ok, err := shelf.ReceivedRecordStamp(ctx, bookID, c.log, c.position)
		require.NoError(t, err)
		assert.Equal(t, c.ok, ok, "%s %d", c.log, c.position)
		assert.Equal(t, c.n, n, "%s %d", c.log, c.position)
	}
	_, _, err = shelf.ReceivedRecordStamp(ctx, bookID, "", 1)
	assert.ErrorIs(t, err, ErrInvalid)
	_, _, err = shelf.ReceivedRecordStamp(ctx, bookID, "deal/theirs", 0)
	assert.ErrorIs(t, err, ErrInvalid)
}

// stampedValues is statusValues that is told each stamp's tick.
type stampedValues struct {
	statusValues
	mu    sync.Mutex
	ticks []uint64
}

func (v *stampedValues) StampValues(ctx context.Context, n uint64, bookID, logID string) (string, LinkValue, json.RawMessage, bool, error) {
	v.mu.Lock()
	v.ticks = append(v.ticks, n)
	v.mu.Unlock()
	p, l, st, _, err := v.statusValues.Values(ctx, bookID, logID)
	return p, l, json.RawMessage(fmt.Sprintf(`{"stamp":%d,"was":%s}`, n, st)), true, err
}

// A StampStatusProvider is told the tick of the stamp it gives values for,
// the migration's included, and the fact commits what it returns.
func TestAStampStatusProviderIsToldTheStamp(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	values := &stampedValues{statusValues: statusValues{status: map[string]json.RawMessage{}, link: map[string]LinkValue{}}}
	values.set("deal/a", `{"row":"open"}`)
	c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t), Status: values})
	require.NoError(t, err)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	mustAppend(t, a, observation("a", "2"))
	s, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)

	var ticks []uint64
	values.mu.Lock()
	ticks = append(ticks, values.ticks...)
	values.mu.Unlock()
	require.NotEmpty(t, ticks)
	assert.Equal(t, s.Tick.N, ticks[len(ticks)-1], "the stamp's own tick")
	assert.Equal(t, s.Tick.N-1, ticks[0], "the migration's stamp is the one before")
	var status []byte
	require.NoError(t, shelf.db.QueryRow(`SELECT status_opening FROM shelf_entries WHERE log_id = 'deal/a' AND tick = ?`, s.Tick.N).Scan(&status))
	assert.Contains(t, string(status), fmt.Sprintf(`"stamp":%d`, s.Tick.N))
}

// bookValues reads through the handles it is handed, as a status provider
// must while a stamp holds the shelf's lock.
type bookValues struct {
	statusValues
	mu    sync.Mutex
	calls []string
}

func (v *bookValues) BookValues(ctx context.Context, in StatusInput) (string, LinkValue, json.RawMessage, bool, error) {
	recs, err := in.Book.Query(ctx, Filter{FromSeq: 1, ToSeq: in.Book.Size()})
	if err != nil {
		return "", LinkValue{}, nil, false, err
	}
	journal := -1
	if in.Journal != nil {
		js, err := in.Journal.Query(ctx, Filter{FromSeq: 1, ToSeq: in.Journal.Size()})
		if err != nil {
			return "", LinkValue{}, nil, false, err
		}
		journal = len(js)
	}
	v.mu.Lock()
	v.calls = append(v.calls, fmt.Sprintf("%d %s %d %d", in.N, in.LogID, len(recs), journal))
	v.mu.Unlock()
	return "deal", LinkValue{CheckedOver: []string{}}, json.RawMessage(fmt.Sprintf(`{"records":%d,"journal":%d}`, len(recs), journal)), true, nil
}

func (v *bookValues) last() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls[len(v.calls)-1]
}

// withinOrAbort runs f and, if it has not returned in 10 s, ends the whole
// test binary from a watchdog goroutine: a panic there is not recovered by
// the test runner, so no cleanup runs, where a deadlocked shelf's Release
// would hang too.
func withinOrAbort(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			panic("a stamp deadlocked on a provider reading its handles")
		}
	}()
	f()
	close(done)
}

// A BookStatusProvider is handed the stamp's tick, the book and the journal,
// and reads them without the shelf's lock. The migration's stamp has no
// journal; a stamp before the host opens the journal has none either (a
// stamp never creates it); once it exists, and after a reopen, the provider
// reads its records.
func TestABookStatusProviderReadsTheHandles(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	values := &bookValues{statusValues: statusValues{status: map[string]json.RawMessage{}, link: map[string]LinkValue{}}}
	cadence := func(shelf *Shelf) *Cadence {
		c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
			Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t), Status: values})
		require.NoError(t, err)
		return c
	}
	c := cadence(shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	at := tickTime
	step := func() time.Time { at = at.Add(time.Minute); return at }
	var size uint64
	var s TickResult
	withinOrAbort(t, func() {
		mustAppend(t, a, observation("a", "1"))
		mustTick(t, c, tickTime)
		require.NoError(t, c.Migrate(ctx, step()))
	})
	assert.Contains(t, values.last(), " -1", "the migration's stamp: no journal")

	withinOrAbort(t, func() {
		mustAppend(t, a, observation("a", "2"))
		size = a.Size()
		var err error
		s, err = c.Stamp(ctx, step())
		require.NoError(t, err)
	})
	assert.Equal(t, fmt.Sprintf("%d deal/a %d -1", s.Tick.N, size), values.last(), "no journal opened yet, and the stamp does not create one")
	kind, err := shelf.logKind(ctx, JournalLogID)
	require.NoError(t, err)
	assert.Empty(t, kind, "the stamp claimed no journal log")

	journal, err := shelf.Journal(ctx)
	require.NoError(t, err)
	mustAppend(t, journal, observation("j", "1"))
	require.NoError(t, shelf.Release())

	// Reopened: the journal is not open yet, so the stamp opens it.
	shelf = fixture.open(t)
	c = cadence(shelf)
	a = mustOpenBook(t, shelf, "deal/a")
	withinOrAbort(t, func() {
		mustAppend(t, a, observation("a", "3"))
		size = a.Size()
		var err error
		s, err = c.Stamp(ctx, step())
		require.NoError(t, err)
	})
	assert.Regexp(t, fmt.Sprintf(`^%d deal/a %d [1-9][0-9]*$`, s.Tick.N, size), values.last(), "the provider reads the journal's records")
	var status []byte
	require.NoError(t, shelf.db.QueryRow(`SELECT status_opening FROM shelf_entries WHERE log_id = 'deal/a' AND tick = ?`, s.Tick.N).Scan(&status))
	assert.Contains(t, string(status), fmt.Sprintf(`"records":%d`, size), "the fact commits what the provider read")
}

// failingValues fails for the books named, as a provider whose data another
// party or retention controls might.
type failingValues struct {
	statusValues
	fail map[string]bool
}

func (v *failingValues) BookValues(ctx context.Context, in StatusInput) (string, LinkValue, json.RawMessage, bool, error) {
	if v.fail[in.LogID] {
		return "", LinkValue{}, nil, false, fmt.Errorf("the deal's offer is not held")
	}
	return v.statusValues.Values(ctx, in.BookID, in.LogID)
}

// One book's provider error never stops the shelf's stamp: that book keeps
// its last fact and is reported Unstated, the others get theirs, and the
// next stamp asks again.
func TestAProviderErrorLeavesTheBooksLastFact(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	values := &failingValues{statusValues: statusValues{status: map[string]json.RawMessage{}, link: map[string]LinkValue{}}, fail: map[string]bool{}}
	values.set("deal/a", `{"row":"a"}`)
	values.set("deal/b", `{"row":"b"}`)
	c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t), Status: values})
	require.NoError(t, err)
	a, b := mustOpenBook(t, shelf, "deal/a"), mustOpenBook(t, shelf, "deal/b")
	mustAppend(t, a, observation("a", "1"))
	mustAppend(t, b, observation("b", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	factsOf := func(log string) int {
		return countRows(t, shelf, `SELECT COUNT(*) FROM shelf_entries WHERE log_id = ? AND kind = ?`, log, FactKindBook)
	}
	beforeA, beforeB := factsOf("deal/a"), factsOf("deal/b")

	values.fail["deal/a"] = true
	mustAppend(t, a, observation("a", "2"))
	mustAppend(t, b, observation("b", "2"))
	s, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err, "the stamp goes on")
	assert.Equal(t, map[string]string{"deal/a": "the deal's offer is not held"}, s.Unstated)
	assert.Equal(t, beforeA, factsOf("deal/a"), "deal/a keeps its last fact")
	assert.Equal(t, beforeB+1, factsOf("deal/b"), "deal/b gets its new fact")

	values.fail["deal/a"] = false
	s, err = c.Stamp(ctx, tickTime.Add(3*time.Minute))
	require.NoError(t, err)
	assert.Empty(t, s.Unstated)
	assert.Equal(t, beforeA+1, factsOf("deal/a"), "the next stamp asks again")
}
