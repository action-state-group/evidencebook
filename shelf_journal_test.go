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

// migratedWithJournal is a migrated shelf with one deal book and a journal
// holding one record, stamped once after the journal's first record.
func migratedWithJournal(t *testing.T) (*Shelf, *Cadence, *Book) {
	t.Helper()
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	journal, err := shelf.Journal(ctx)
	require.NoError(t, err)
	mustAppend(t, journal, observation("journal", "1"))
	_, err = c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	return shelf, c, journal
}

func journalFacts(t *testing.T, shelf *Shelf) []BookFact {
	t.Helper()
	rows, err := shelf.db.Query(`SELECT doc FROM shelf_entries WHERE kind = ? ORDER BY entry_seq`, FactKindJournal)
	require.NoError(t, err)
	defer rows.Close()
	var out []BookFact
	for rows.Next() {
		var doc []byte
		require.NoError(t, rows.Scan(&doc))
		var f BookFact
		require.NoError(t, json.Unmarshal(doc, &f))
		out = append(out, f)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestTheJournalGetsAJournalFactThatHidesItsSize(t *testing.T) {
	shelf, _, _ := migratedWithJournal(t)
	facts := journalFacts(t, shelf)
	require.Len(t, facts, 1)
	f := facts[0]
	assert.Equal(t, JournalLogID, f.LogID)
	assert.Zero(t, f.Size)
	assert.Empty(t, f.CheckpointSHA256)
	assert.Zero(t, f.PrevFactSize)
	assert.Equal(t, ProfileNone, f.Profile)

	// The owner's opening holds the true size and checkpoint.
	got, err := shelf.Openings(context.Background(), f.BookID, AudienceOwner)
	require.NoError(t, err)
	require.Len(t, got, 1)
	var status JournalStatus
	require.NoError(t, json.Unmarshal(got[0].StatusOpening.Value, &status))
	assert.NotZero(t, status.Size)
	assert.True(t, isDigest(status.CheckpointSHA256))
	require.NoError(t, VerifyOpenings(got[0].Fact, got[0].LinkOpening, got[0].StatusOpening))
}

// The journal follows the write rule: a fact only when it grew.
func TestTheJournalGetsAFactOnlyWhenItGrows(t *testing.T) {
	ctx := context.Background()
	shelf, c, journal := migratedWithJournal(t)
	_, err := c.Stamp(ctx, tickTime.Add(3*time.Minute))
	require.NoError(t, err)
	assert.Len(t, journalFacts(t, shelf), 1, "a quiet journal gets no fact")
	mustAppend(t, journal, observation("journal", "2"))
	_, err = c.Stamp(ctx, tickTime.Add(4*time.Minute))
	require.NoError(t, err)
	assert.Len(t, journalFacts(t, shelf), 2)

	got, err := shelf.Openings(ctx, journalFacts(t, shelf)[0].BookID, AudienceOwner)
	require.NoError(t, err)
	require.Len(t, got, 2)
	var first, second JournalStatus
	require.NoError(t, json.Unmarshal(got[0].StatusOpening.Value, &first))
	require.NoError(t, json.Unmarshal(got[1].StatusOpening.Value, &second))
	assert.Equal(t, first.Size, second.PrevFactSize)
	assert.Greater(t, second.Size, first.Size)
}

// The journal counts toward no completeness and no tally: it is not one of
// the shelf's books, has no extent on a card, and a handover returns no
// latest fact for it.
func TestTheJournalIsInNoCountAndNoHandover(t *testing.T) {
	ctx := context.Background()
	shelf, c, _ := migratedWithJournal(t)
	books, err := shelf.Books(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"deal/a"}, books)
	last, ok, err := c.Last(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	anchor, err := c.StandardHistoryAnchor(ctx, last.N)
	require.NoError(t, err)
	assert.NotContains(t, anchor.Extents, JournalLogID)
	assert.Contains(t, anchor.Extents, "deal/a")

	h, err := c.Handover(ctx, 0)
	require.NoError(t, err)
	cp, err := checkpointFromStatement(h.Anchor)
	require.NoError(t, err)
	got, err := VerifyHandover(h, cp.KeyID, testWitnesses(t))
	require.NoError(t, err)
	for _, l := range got.Latest {
		assert.NotEqual(t, JournalLogID, l.Fact.LogID)
	}
	for _, d := range h.Documents {
		var f BookFact
		require.NoError(t, json.Unmarshal(d.Doc, &f))
		if f.Kind == FactKindJournal {
			assert.Nil(t, d.StatusOpening, "the journal's opening is never handed over")
		}
	}
}

func TestTheJournalIsOpenedOnlyAsTheJournal(t *testing.T) {
	ctx := context.Background()
	shelf, _, journal := migratedWithJournal(t)
	again, err := shelf.Journal(ctx)
	require.NoError(t, err)
	assert.Same(t, journal, again)
	_, err = shelf.Open(ctx, JournalLogID)
	assert.ErrorIs(t, err, ErrInvalid, "Open refuses the journal")
	_, err = shelf.HostLog(ctx, JournalLogID, true)
	assert.ErrorIs(t, err, ErrInvalid, "a host log cannot take the journal's id")
}

func TestTheJournalNeedsAShelfOfBookFacts(t *testing.T) {
	shelf := newShelfFixture(t).open(t)
	_, err := shelf.Journal(context.Background())
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestTheJournalRefusesALogThatAlreadyHasItsID(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, JournalLogID), observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	_, err := shelf.Journal(ctx)
	assert.ErrorIs(t, err, ErrInvalid)
}

// A file whose shelf_logs predates the journal's kind is rebuilt with the
// same rows when the journal is first opened.
func TestAnOlderShelfLogsTableTakesTheJournal(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	for _, statement := range []string{
		`CREATE TABLE shelf_logs_old (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('book', 'host')))`,
		`INSERT INTO shelf_logs_old SELECT log_id, kind FROM shelf_logs`,
		`DROP TABLE shelf_logs`,
		`ALTER TABLE shelf_logs_old RENAME TO shelf_logs`,
	} {
		_, err := shelf.db.Exec(statement)
		require.NoError(t, err)
	}
	_, err := shelf.Journal(ctx)
	require.NoError(t, err)
	kind, err := shelf.logKind(ctx, "deal/a")
	require.NoError(t, err)
	assert.Equal(t, logKindBook, kind)
	kind, err = shelf.logKind(ctx, JournalLogID)
	require.NoError(t, err)
	assert.Equal(t, logKindJournal, kind)
}

// A shelf whose cadence log is named as the journal refuses the journal and
// changes nothing, in this process and after a reopen with no cadence open.
func TestTheJournalRefusesTheCadenceLogsID(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	c, err := shelf.Cadence(ctx, CadenceConfig{LogID: JournalLogID,
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t)})
	require.NoError(t, err)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	_, err = shelf.Journal(ctx)
	assert.ErrorIs(t, err, ErrInvalid, "the cadence open in this process")
	require.NoError(t, shelf.Release())

	reopened := fixture.open(t)
	_, err = reopened.Journal(ctx)
	assert.ErrorIs(t, err, ErrInvalid, "the cadence's entries on disk")
	kind, err := reopened.logKind(ctx, JournalLogID)
	require.NoError(t, err)
	assert.Empty(t, kind, "nothing was claimed")
	_, err = reopened.Cadence(ctx, CadenceConfig{LogID: JournalLogID,
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t)})
	assert.NoError(t, err, "the cadence still opens")
}
