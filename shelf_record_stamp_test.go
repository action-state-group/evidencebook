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
	journal := uint64(0)
	if in.Journal != nil {
		journal = in.Journal.Size()
	}
	v.mu.Lock()
	v.calls = append(v.calls, fmt.Sprintf("%d %s %d %v", in.N, in.LogID, len(recs), in.Journal != nil))
	v.mu.Unlock()
	return "deal", LinkValue{CheckedOver: []string{}}, json.RawMessage(fmt.Sprintf(`{"records":%d,"journal":%d}`, len(recs), journal)), true, nil
}

// A BookStatusProvider is handed the stamp's tick, the book and the journal,
// and reads them without the shelf's lock: the stamp does not deadlock, the
// migration's stamp has no journal yet, and a later one has it.
func TestABookStatusProviderReadsTheHandles(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	values := &bookValues{statusValues: statusValues{status: map[string]json.RawMessage{}, link: map[string]LinkValue{}}}
	c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t), Status: values})
	require.NoError(t, err)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	_, err = shelf.Journal(ctx)
	require.NoError(t, err)
	mustAppend(t, a, observation("a", "2"))
	before := a.Size()
	done := make(chan error, 1)
	var s TickResult
	go func() {
		var err error
		s, err = c.Stamp(ctx, tickTime.Add(2*time.Minute))
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the stamp deadlocked on a provider reading its handles")
	}
	values.mu.Lock()
	calls := append([]string{}, values.calls...)
	values.mu.Unlock()
	require.Len(t, calls, 2)
	assert.Equal(t, fmt.Sprintf("%d deal/a %d false", s.Tick.N-1, before-1), calls[0], "the migration's stamp, a record ago: no journal yet")
	assert.Equal(t, fmt.Sprintf("%d deal/a %d true", s.Tick.N, before), calls[1])
	var status []byte
	require.NoError(t, shelf.db.QueryRow(`SELECT status_opening FROM shelf_entries WHERE log_id = 'deal/a' AND tick = ?`, s.Tick.N).Scan(&status))
	assert.Contains(t, string(status), fmt.Sprintf(`"records":%d`, before), "the fact commits what the provider read")
}
