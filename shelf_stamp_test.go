// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusValues is a test StatusProvider: a fixed profile and status per log.
type statusValues struct {
	mu     sync.Mutex
	status map[string]json.RawMessage
	link   map[string]LinkValue
}

func (v *statusValues) Values(_ context.Context, _, logID string) (string, LinkValue, json.RawMessage, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	st, ok := v.status[logID]
	return "deal", v.link[logID], st, ok, nil
}

func (v *statusValues) set(logID, status string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.status[logID] = json.RawMessage(status)
}

func newStampCadence(t *testing.T, shelf *Shelf) (*Cadence, *statusValues) {
	t.Helper()
	values := &statusValues{status: map[string]json.RawMessage{}, link: map[string]LinkValue{}}
	c, err := shelf.Cadence(context.Background(), CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t), Status: values})
	require.NoError(t, err)
	return c, values
}

// entriesOfTick is the shelf_entries rows a tick appended, by kind.
func entriesOfTick(t *testing.T, shelf *Shelf, n uint64) map[string][]string {
	t.Helper()
	rows, err := shelf.db.Query(`SELECT kind, log_id FROM shelf_entries WHERE tick = ? ORDER BY entry_seq`, n)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var kind, log string
		require.NoError(t, rows.Scan(&kind, &log))
		out[kind] = append(out[kind], log)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestMigrateThenStampWritesFactsOnlyOnChange(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	c, values := newStampCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	b := mustOpenBook(t, shelf, "deal/b")
	mustAppend(t, b, observation("b", "1"))
	old := mustTick(t, c, tickTime)

	// Stamp before Migrate is refused; Migrate writes the marker, a genesis
	// fact for each log and a stamp entry, as the tick after the last one.
	_, err := c.Stamp(ctx, tickTime)
	require.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	migrated, err := c.Migrated(ctx)
	require.NoError(t, err)
	require.True(t, migrated)
	assert.Equal(t, ShelfVersionFacts, shelfVersion(t, shelf.db))
	genesis := entriesOfTick(t, shelf, old.N+1)
	assert.Equal(t, []string{""}, genesis[EntryKindFormatSwitch])
	assert.ElementsMatch(t, []string{"deal/a", "deal/b"}, genesis[FactKindBook])
	assert.Len(t, genesis[EntryKindStamp], 1)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(2*time.Minute)), "Migrate again is a no-op")

	// The backup is the version 1 file an older build opens.
	bak := rawShelfDB(t, f.path+".v1.bak")
	assert.Equal(t, ShelfVersion, shelfVersion(t, bak))
	var rows int
	require.NoError(t, bak.QueryRow(`SELECT COUNT(*) FROM shelf_migration`).Scan(&rows))
	assert.Zero(t, rows)

	// A quiet stamp: no fact, but a stamp entry and a new checkpoint.
	quiet, err := c.Stamp(ctx, tickTime.Add(3*time.Minute))
	require.NoError(t, err)
	got := entriesOfTick(t, shelf, quiet.Tick.N)
	assert.Empty(t, got[FactKindBook])
	assert.Len(t, got[EntryKindStamp], 1)
	previous, err := c.Get(ctx, quiet.Tick.N-1)
	require.NoError(t, err)
	assert.Greater(t, quiet.Tick.CheckpointSize, previous.CheckpointSize, "an empty stamp still grows the log")

	// A grown book gets a fact; a quiet one does not.
	mustAppend(t, a, observation("a", "2"))
	grown, err := c.Stamp(ctx, tickTime.Add(4*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, []string{"deal/a"}, entriesOfTick(t, shelf, grown.Tick.N)[FactKindBook])

	// A changed status value with no new record (received material) gets a
	// fact with the same size and checkpoint.
	values.set("deal/b", `{"rows":[{"id":"paid_as_agreed"}]}`)
	received, err := c.Stamp(ctx, tickTime.Add(5*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, []string{"deal/b"}, entriesOfTick(t, shelf, received.Tick.N)[FactKindBook])
	last, err := shelf.lastFactOf(ctx, "deal/b")
	require.NoError(t, err)
	assert.Equal(t, last.fact.Size, last.fact.PrevFactSize)
	again, err := c.Stamp(ctx, tickTime.Add(6*time.Minute))
	require.NoError(t, err)
	assert.Empty(t, entriesOfTick(t, shelf, again.Tick.N)[FactKindBook], "the same values again: no fact")

	// The heartbeat stamps after the switch.
	beat := mustTick(t, c, again.Tick.DueNext)
	assert.NotNil(t, beat.Leaves.Stamp)

	// /1's anchor still counts every book, the record added after migration
	// included.
	before, err := c.StandardHistoryAnchor(ctx, old.N)
	require.NoError(t, err)
	anchor, err := c.StandardHistoryAnchor(ctx, 0)
	require.NoError(t, err)
	for _, book := range []*Book{a, b} {
		leaves, err := book.commitments.Size(ctx)
		require.NoError(t, err)
		assert.Equal(t, leaves, anchor.Extents[book.id], "%s: every leaf the book holds", book.id)
	}
	assert.Greater(t, anchor.Extents["deal/a"], before.Extents["deal/a"], "the record after migration is counted")
	assert.Equal(t, anchor.Extents["deal/b"], before.Extents["deal/b"])
}

func TestAStampStoredButNotFinishedIsFinishedFirst(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	require.NoError(t, c.Migrate(ctx, tickTime))
	mustAppend(t, a, observation("a", "2"))

	// Store a stamp and append only its first entry, as a crash would leave it.
	shelf.mu.Lock()
	last, _, err := c.lastLocked(ctx)
	require.NoError(t, err)
	logs, err := shelf.cadenceLogIDs(ctx)
	require.NoError(t, err)
	log, err := shelf.cadenceLogLocked(ctx, logs[0])
	require.NoError(t, err)
	prev, err := shelf.lastFactOf(ctx, logs[0])
	require.NoError(t, err)
	f, err := c.factFor(ctx, last.N+1, log, tickTime, &prev)
	require.NoError(t, err)
	tick, err := c.buildStampLocked(ctx, last.N+1, tickTime, tickTime, []pendingFact{f}, nil)
	require.NoError(t, err)
	encoded, err := json.Marshal(tick.Leaves)
	require.NoError(t, err)
	_, err = shelf.db.Exec(`INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, 0, x'', ?)`,
		tick.N, tick.At.Format(time.RFC3339), tick.DueNext.Format(time.RFC3339), string(encoded))
	require.NoError(t, err)
	first := tick.Leaves.Stamp.Entries[0]
	id, _, err := EntryID(json.RawMessage(first.Doc))
	require.NoError(t, err)
	_, err = c.log.Append(ctx, hex.EncodeToString(id[:]), tick.At)
	require.NoError(t, err)
	shelf.mu.Unlock()

	// The next stamp finishes that one, appending only what is missing.
	result, err := c.Stamp(ctx, tickTime.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, tick.N, result.Tick.N)
	got := entriesOfTick(t, shelf, tick.N)
	assert.Equal(t, []string{"deal/a"}, got[FactKindBook])
	assert.Len(t, got[EntryKindStamp], 1)
	entries, err := c.log.Entries(ctx, 0, 1000)
	require.NoError(t, err)
	seen := map[string]int{}
	for _, e := range entries {
		seen[e.RecordID]++
	}
	for id, n := range seen {
		assert.Equal(t, 1, n, "entry %s appended once", id)
	}
}

func TestMigrateResumesAfterABackupAndRefusesAForeignOne(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))

	// A foreign <path>.v1.bak: Migrate refuses and writes nothing past the
	// version switch.
	require.NoError(t, os.WriteFile(f.path+".v1.bak", []byte("not this shelf"), 0o600))
	err := c.Migrate(ctx, tickTime)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), f.path+".v1.bak", "the refusal names the file to move aside")
	assert.Equal(t, ShelfVersionFacts, shelfVersion(t, shelf.db), "the version switch comes before the backup")
	state, err := migrationState(ctx, shelf.db)
	require.NoError(t, err)
	assert.Equal(t, migrationBackingUp, state)

	// Moved aside, Migrate resumes from where it stopped; a leftover
	// temporary file from an unfinished attempt is replaced.
	require.NoError(t, os.Remove(f.path+".v1.bak"))
	require.NoError(t, os.WriteFile(f.path+".v1.bak.tmp", []byte("partial"), 0o600))
	require.NoError(t, c.Migrate(ctx, tickTime))
	migrated, err := c.Migrated(ctx)
	require.NoError(t, err)
	assert.True(t, migrated)
	_, err = os.Stat(f.path + ".v1.bak.tmp")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestAMigratedShelfReopensAndAnOldVersionIsStillOpened(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	require.NoError(t, c.Migrate(ctx, tickTime))
	require.NoError(t, shelf.Release())
	reopened, err := OpenSQLiteShelf(ctx, f.cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Release() })
	assert.Equal(t, ShelfVersionFacts, shelfVersion(t, reopened.db))
	c2, _ := newStampCadence(t, reopened)
	_, err = c2.Stamp(ctx, tickTime.Add(time.Minute))
	require.NoError(t, err)
}

func TestAStampIsWitnessedLikeATick(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	require.NoError(t, c.Migrate(ctx, tickTime))
	mustAppend(t, a, observation("a", "2"))
	stamp, err := c.Stamp(ctx, tickTime.Add(time.Minute))
	require.NoError(t, err)
	mustWitness(t, c, stamp.Tick.N)
	got, err := c.Get(ctx, stamp.Tick.N)
	require.NoError(t, err)
	assert.True(t, got.Witnessed)
	leaf := stamp.Tick.Leaves.Deals["deal/a"]
	var n int
	require.NoError(t, shelf.db.QueryRow(`SELECT COUNT(*) FROM cadence_witnessed WHERE log_id = ? AND checkpoint_sha256 = ?`, "deal/a", leaf.CheckpointSHA256).Scan(&n))
	assert.Zero(t, n, "a stamp leaves the tree's witnessed index alone")
}

// Acceptance 2: each audience gets exactly the openings OpeningsFor gives it,
// and every opening it gets verifies.
func TestEachAudienceGetsExactlyItsOpenings(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, values := newStampCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	values.set("deal/a", `{"rows":[]}`)
	require.NoError(t, c.Migrate(ctx, tickTime))
	last, err := shelf.lastFactOf(ctx, "deal/a")
	require.NoError(t, err)
	for _, audience := range []Audience{AudienceCounterparty, AudienceAdjudicator, AudienceAsker, AudienceChosenReader, AudienceOwner, AudienceAuditor} {
		facts, err := shelf.Openings(ctx, last.fact.BookID, audience)
		require.NoError(t, err)
		require.Len(t, facts, 1, audience)
		wantLink, wantStatus, err := OpeningsFor(FactKindBook, audience)
		require.NoError(t, err)
		f := facts[0]
		assert.Equal(t, wantLink, f.LinkOpening != nil, "%s link", audience)
		assert.Equal(t, wantStatus, f.StatusOpening != nil, "%s status", audience)
		require.NoError(t, VerifyOpenings(f.Fact, f.LinkOpening, f.StatusOpening), audience)
	}
	_, err = shelf.Openings(ctx, last.fact.BookID, "stranger")
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = shelf.Openings(ctx, "no-such-book", "stranger")
	assert.ErrorIs(t, err, ErrInvalid, "an unknown audience is refused even for a book with no facts")
}

func TestNoTickWhileAMigrationIsUnderWay(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	require.NoError(t, os.WriteFile(f.path+".v1.bak", []byte("not this shelf"), 0o600))
	require.ErrorIs(t, c.Migrate(ctx, tickTime), ErrInvalid)
	_, err := c.Tick(ctx, tickTime.Add(time.Hour))
	require.ErrorIs(t, err, ErrInvalid, "no tree tick on a file whose migration started")
	require.NoError(t, os.Remove(f.path+".v1.bak"))
	require.NoError(t, c.Migrate(ctx, tickTime))
	beat := mustTick(t, c, tickTime.Add(2*time.Hour))
	assert.NotNil(t, beat.Leaves.Stamp)
}

// A batch longer than one runner scan (cll.MaxScanLimit) gets one
// checkpoint, never one over part of it: one pending witness delivery, at the
// full size.
func TestCheckpointWholeCutsOneCheckpointOverALongBatch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "log.db")
	log, err := openCLLSQLite(path, "deal-cadence/0123456789abcdef", seededKey(11), []string{"https://witness.example"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = log.Release() })
	for i := 0; i < 2500; i++ {
		_, err := log.Append(ctx, fmt.Sprintf("%064x", i+1), tickTime)
		require.NoError(t, err)
	}
	cp, err := log.CheckpointWhole(ctx, tickTime)
	require.NoError(t, err)
	assert.Equal(t, mmrSize(2500), cp.TreeSize)
	pending, err := log.log.(interface {
		PendingWitnesses(context.Context, time.Time, int) ([]cll.WitnessState, error)
	}).PendingWitnesses(ctx, tickTime.Add(time.Hour), 1)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, cp.TreeSize, pending[0].CheckpointSize, "the first delivery queued is the whole batch's checkpoint, not one over part of it")
}

// A crash after the backup's digest is recorded and before its rename: the
// next Migrate renames the matching temporary file and goes on; a recorded
// backup already renamed is reused as it is.
func TestMigrateReusesABackupItRecorded(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	// Steps 1 and 2 by hand, stopping before the rename.
	_, err := shelf.db.Exec(`UPDATE shelf_meta SET version = ? WHERE id = 1`, ShelfVersionFacts)
	require.NoError(t, err)
	_, err = shelf.db.Exec(`INSERT INTO shelf_migration (id, state, backup_sha256) VALUES (1, ?, '')`, migrationBackingUp)
	require.NoError(t, err)
	tmp := f.path + ".v1.bak.tmp"
	_, err = shelf.db.Exec(`VACUUM INTO ?`, tmp)
	require.NoError(t, err)
	require.NoError(t, resetBackup(ctx, tmp))
	digest, err := fileSHA256(tmp)
	require.NoError(t, err)
	_, err = shelf.db.Exec(`UPDATE shelf_migration SET backup_sha256 = ? WHERE id = 1`, digest)
	require.NoError(t, err)

	require.NoError(t, c.Migrate(ctx, tickTime))
	got, err := fileSHA256(f.path + ".v1.bak")
	require.NoError(t, err)
	assert.Equal(t, digest, got, "the recorded backup is the one in place")
	_, err = os.Stat(tmp)
	assert.ErrorIs(t, err, os.ErrNotExist)
	migrated, err := c.Migrated(ctx)
	require.NoError(t, err)
	assert.True(t, migrated)
}

// A crash after part of a long batch is indexed and before its checkpoint:
// the next CheckpointWhole indexes the rest and cuts one checkpoint.
func TestCheckpointWholeAfterAPartialIndex(t *testing.T) {
	ctx := context.Background()
	log, err := openCLLSQLite(filepath.Join(t.TempDir(), "log.db"), "deal-cadence/0123456789abcdef", seededKey(11), []string{"https://witness.example"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = log.Release() })
	for i := 0; i < 2500; i++ {
		_, err := log.Append(ctx, fmt.Sprintf("%064x", i+1), tickTime)
		require.NoError(t, err)
	}
	changed, err := log.indexer.RunOnce(ctx, tickTime) // one scan, then the crash
	require.NoError(t, err)
	require.True(t, changed)
	cp, err := log.CheckpointWhole(ctx, tickTime)
	require.NoError(t, err)
	assert.Equal(t, mmrSize(2500), cp.TreeSize)
	pending, err := log.log.(interface {
		PendingWitnesses(context.Context, time.Time, int) ([]cll.WitnessState, error)
	}).PendingWitnesses(ctx, tickTime.Add(time.Hour), 1)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, cp.TreeSize, pending[0].CheckpointSize)
}

// A crash after a stamp's checkpoint is cut and before its row records it:
// recovery records the checkpoint already cut, with no new one.
func TestAStampCheckpointedButNotRecordedKeepsItsCheckpoint(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	require.NoError(t, c.Migrate(ctx, tickTime))
	last, ok, err := c.Last(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = shelf.db.Exec(`UPDATE cadence_ticks SET entry_seq = 0, checkpoint_size = 0, checkpoint = x'' WHERE tick = ?`, last.N)
	require.NoError(t, err)
	_, err = shelf.db.Exec(`UPDATE shelf_migration SET state = ? WHERE id = 1`, migrationMigrating)
	require.NoError(t, err)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Hour)))
	again, err := c.Get(ctx, last.N)
	require.NoError(t, err)
	assert.Equal(t, last.Checkpoint, again.Checkpoint, "the checkpoint already cut")
	assert.Equal(t, last.CheckpointSize, again.CheckpointSize)
	assert.Equal(t, last.EntrySeq, again.EntrySeq)
	latest, err := c.log.Latest(ctx)
	require.NoError(t, err)
	assert.Equal(t, last.Checkpoint, latest.Statement, "no checkpoint after it")
}

// beforeStampValues is statusValues that counts BeforeStamp calls and fails
// the first when told to.
type beforeStampValues struct {
	*statusValues
	calls int
	fail  bool
}

func (v *beforeStampValues) BeforeStamp(context.Context, time.Time) error {
	v.calls++
	if v.fail {
		v.fail = false
		return errors.New("naming failed")
	}
	return nil
}

// BeforeStamp runs before each stamp Stamp takes and before a due Tick's
// stamp, never before a Tick that cuts nothing; its error is reported and
// the stamp is still taken (evidencebook#527).
func TestBeforeStampRunsBeforeEachStamp(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	values := &beforeStampValues{statusValues: &statusValues{status: map[string]json.RawMessage{}, link: map[string]LinkValue{}}}
	c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef",
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Witnesses: testWitnesses(t), Status: values})
	require.NoError(t, err)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	mustTick(t, c, tickTime)
	assert.Zero(t, values.calls, "no BeforeStamp before the shelf is migrated")
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))

	values.fail = true
	res, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, values.calls)
	assert.Equal(t, "naming failed", res.BeforeStamp, "reported, and the stamp taken")
	require.True(t, res.Ticked)

	early, err := c.Tick(ctx, tickTime.Add(3*time.Minute))
	require.NoError(t, err)
	assert.False(t, early.Ticked)
	assert.Equal(t, 1, values.calls, "a Tick that cuts nothing runs no BeforeStamp")

	due, err := c.Tick(ctx, res.Tick.DueNext)
	require.NoError(t, err)
	require.True(t, due.Ticked)
	assert.Equal(t, 2, values.calls, "a due Tick's stamp runs it")
	assert.Empty(t, due.BeforeStamp)
}
