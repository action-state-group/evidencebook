// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/action-state-group/checkpointed-local-log/go/cll"
	cllsqlite "github.com/action-state-group/checkpointed-local-log/go/store/sqlite"

	"github.com/action-state-group/evidencebook/internal/detrand"
)

// The shelf contract's additions (evidencebook#254 §2.2, §2.4, §2.7, §5).

// rawShelfDB opens the shelf's file beside the shelf, as another process
// would, to read or plant rows.
func rawShelfDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		t.Fatal(err)
	}
	return db
}

func shelfLogs(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT log_id, kind FROM shelf_logs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			t.Fatal(err)
		}
		out[id] = kind
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func shelfVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var version int
	if err := db.QueryRow(`SELECT version FROM shelf_meta WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestAFreshShelfIsVersionOne(t *testing.T) {
	f := newShelfFixture(t)
	shelf := f.open(t)
	book := mustOpenBook(t, shelf, "deal/a")
	if got := shelfLogs(t, rawShelfDB(t, f.path)); len(got) != 0 {
		t.Fatalf("a book with no record is not on shelf_logs yet: %v", got)
	}
	mustAppend(t, book, observation("s", "1"))
	db := rawShelfDB(t, f.path)
	if v := shelfVersion(t, db); v != ShelfVersion {
		t.Fatalf("version %d", v)
	}
	if got := shelfLogs(t, db); !reflect.DeepEqual(got, map[string]string{"deal/a": "book"}) {
		t.Fatalf("shelf_logs %v", got)
	}
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	f.open(t)
	if v := shelfVersion(t, db); v != ShelfVersion {
		t.Fatalf("version after reopen %d", v)
	}
}

func TestAShelfRefusesAVersionItDoesNotKnow(t *testing.T) {
	f := newShelfFixture(t)
	if err := f.open(t).Release(); err != nil {
		t.Fatal(err)
	}
	db := rawShelfDB(t, f.path)
	if _, err := db.Exec(`UPDATE shelf_meta SET version = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLiteShelf(context.Background(), f.cfg); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a version 2 file: %v", err)
	}
	if v := shelfVersion(t, db); v != 2 {
		t.Fatalf("a refused file is not rewritten: version %d", v)
	}
}

// A file a shelf from v0.12.0 or earlier wrote has no shelf_meta and no
// shelf_logs: it gets version 1, its books are books and its other cadence
// logs host logs, and the kinds hold from then on.
func TestAPreVersionShelfIsFilledOnOpen(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	mustAppend(t, mustOpenBook(t, shelf, "deal/book"), observation("s", "1"))
	provisionLegacy(t, f.path, "deal/old")
	if _, err := shelf.Legacy(ctx, "deal/old"); err != nil {
		t.Fatal(err)
	}
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	db := rawShelfDB(t, f.path)
	if _, err := db.Exec(`DROP TABLE shelf_meta; DROP TABLE shelf_logs`); err != nil {
		t.Fatal(err)
	}

	shelf = f.open(t)
	if v := shelfVersion(t, db); v != 1 {
		t.Fatalf("version %d", v)
	}
	want := map[string]string{"deal/book": "book", "deal/old": "host"}
	if got := shelfLogs(t, db); !reflect.DeepEqual(got, want) {
		t.Fatalf("filled shelf_logs %v, want %v", got, want)
	}
	if _, err := shelf.Open(ctx, "deal/old"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Open of a filled host id: %v", err)
	}
	if _, err := shelf.Legacy(ctx, "deal/book"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Legacy of a filled book id: %v", err)
	}

	// A file whose shelf_meta lost its row but kept shelf_logs keeps the
	// kinds it holds and gets version 1 again.
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM shelf_meta`); err != nil {
		t.Fatal(err)
	}
	f.open(t)
	if v := shelfVersion(t, db); v != 1 {
		t.Fatalf("version %d", v)
	}
	if got := shelfLogs(t, db); !reflect.DeepEqual(got, want) {
		t.Fatalf("shelf_logs %v", got)
	}
}

// provisionLegacy writes a CLL log of one entry into the shelf's file, as a
// pre-shelf writer left it.
func provisionLegacy(t *testing.T, path, logID string) {
	t.Helper()
	if err := cllsqlite.Init(path, logID); err != nil {
		t.Fatal(err)
	}
	log, err := OpenCLLSQLite(path, logID, seededKey(107))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(context.Background(), hex.EncodeToString(bytes.Repeat([]byte{4}, 32)), tickTime); err != nil {
		t.Fatal(err)
	}
	if err := log.Release(); err != nil {
		t.Fatal(err)
	}
}

// Book and host ids stay apart in this process and across reopens.
func TestBookAndHostLogIdsStayApart(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	mustOpenBook(t, shelf, "deal/empty")
	if _, err := shelf.HostLog(ctx, "deal/empty", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a host log on an open book with no record: %v", err)
	}
	mustAppend(t, mustOpenBook(t, shelf, "deal/book"), observation("s", "1"))
	host, err := shelf.HostLog(ctx, "node/chain", false)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := shelf.HostLog(ctx, "node/chain", false); err != nil || again != host {
		t.Fatalf("one host log per id: %v", err)
	}
	if _, err := shelf.HostLog(ctx, "node/chain", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a host log reopened in the other mode: %v", err)
	}
	if _, err := shelf.Open(ctx, "node/chain"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Open of a host id: %v", err)
	}
	for _, open := range []func(string) error{
		func(id string) error { _, err := shelf.Legacy(ctx, id); return err },
		func(id string) error { _, err := shelf.HostLog(ctx, id, true); return err },
	} {
		if err := open("deal/book"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("a host log on a book: %v", err)
		}
	}
	cadenceID := "deal-cadence/0123456789abcdef"
	noEnd := func(context.Context, string) (bool, error) { return false, nil }
	for _, id := range []string{"node/chain", "deal/book", "deal/empty"} {
		if _, err := shelf.Cadence(ctx, CadenceConfig{LogID: id, Ended: noEnd}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("a cadence on %s: %v", id, err)
		}
	}
	c, _ := newCadence(t, shelf)
	if _, err := shelf.HostLog(ctx, cadenceID, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a host log on the cadence log: %v", err)
	}
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}

	shelf = f.open(t)
	if _, err := shelf.Open(ctx, "node/chain"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Open of a host id after reopen: %v", err)
	}
	if _, err := shelf.HostLog(ctx, "deal/book", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a host log on a book after reopen: %v", err)
	}
	if _, err := shelf.Cadence(ctx, CadenceConfig{LogID: "node/chain", Ended: noEnd}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a cadence on a recorded host log after reopen: %v", err)
	}
	// The store refuses too: a record under a host id never lands.
	store := &sqliteStore{db: shelf.db, bookID: "node/chain"}
	err = store.PutRecord(ctx, StoredRecord{Seq: 1, RecordID: "r", Header: []byte(`{}`), Capsule: []byte(`{}`), Envelope: []byte{0}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a record under a host id: %v", err)
	}
	var rows int
	if err := shelf.db.QueryRow(`SELECT COUNT(*) FROM book_records WHERE book_id = 'node/chain'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("the refused record was stored: %d %v", rows, err)
	}
	// ImportLegacy keeps them apart as well.
	c, _ = newCadence(t, shelf)
	if err := c.ImportLegacy(ctx, LegacyCadence{Logs: []string{"deal/book"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("importing a book as a legacy log: %v", err)
	}
	if _, err := shelf.HostLog(ctx, "node/chain", false); err != nil {
		t.Fatal(err)
	}
	if err := c.ImportLegacy(ctx, LegacyCadence{Logs: []string{"node/chain"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("importing a host log outside the cadence: %v", err)
	}
}

// The mode of a host log holds across processes: the file records it, as
// cadence_logs membership, so a reopen cannot switch it either way.
func TestAHostLogKeepsItsModeAcrossReopens(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	if _, err := shelf.HostLog(ctx, "node/in", true); err != nil {
		t.Fatal(err)
	}
	if _, err := shelf.HostLog(ctx, "node/out", false); err != nil {
		t.Fatal(err)
	}
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}

	shelf = f.open(t)
	c, _ := newCadence(t, shelf)
	if _, err := shelf.HostLog(ctx, "node/in", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a cadence host log reopened outside it: %v", err)
	}
	for _, open := range []func() error{
		func() error { _, err := shelf.HostLog(ctx, "node/out", true); return err },
		func() error { _, err := shelf.Legacy(ctx, "node/out"); return err },
		func() error { return c.ImportLegacy(ctx, LegacyCadence{Logs: []string{"node/out"}}) },
	} {
		if err := open(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("a host log outside the cadence enrolled in it after a reopen: %v", err)
		}
	}
	// Every tick still runs: the refused opens left the live set alone, and
	// node/in, opened in the cadence but never written, has no checkpoint
	// to carry, so the tick skips it and a derived filler stands in for it.
	if tick := mustTick(t, c, tickTime); len(tick.Leaves.Deals) != 0 || tick.Leaves.FillerCount != 1 {
		t.Fatalf("the tick's leaves %+v", tick.Leaves)
	}
	if live, _ := c.Live(ctx); !reflect.DeepEqual(live, []string{"node/in"}) {
		t.Fatalf("live %v", live)
	}
}

// A host log outside the cadence is the host's own: the cadence never
// counts or checkpoints it, and its writes run on the log's backend and the
// shelf's artifact store under the shelf's lock.
func TestAHostLogOutsideTheCadenceIsTheHosts(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	c, _ := newCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("s", "1"))
	host, err := shelf.HostLog(ctx, "node/chain", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := byte(1); i <= 3; i++ {
		err := host.Write(ctx, func(stores HostLogStores) error {
			if stores.Originals != f.cfg.Originals {
				return fmt.Errorf("not the shelf's artifact store")
			}
			_, err := stores.Log.Append(ctx, cll.AppendInput{Value: bytes.Repeat([]byte{i}, 32), AppendedAt: tickTime})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	live, err := c.Live(ctx)
	if err != nil || !reflect.DeepEqual(live, []string{"deal/a"}) {
		t.Fatalf("live %v %v", live, err)
	}
	tick := mustTick(t, c, tickTime)
	if _, carried := tick.Leaves.Deals["node/chain"]; carried || len(tick.Leaves.Deals) != 1 || tick.Leaves.FillerCount != 0 {
		t.Fatalf("the cadence carried or counted the host log: %+v", tick.Leaves)
	}
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	if err := host.Write(ctx, func(HostLogStores) error { return nil }); !errors.Is(err, ErrReleased) {
		t.Fatalf("a write after release: %v", err)
	}

	// The entries are in the shelf's file under the log's id.
	shelf = f.open(t)
	host, err = shelf.HostLog(ctx, "node/chain", false)
	if err != nil {
		t.Fatal(err)
	}
	var count uint64
	if err := host.Write(ctx, func(stores HostLogStores) error {
		entries, err := stores.Log.ScanEntries(ctx, 0, 10)
		count = uint64(len(entries))
		return err
	}); err != nil || count != 3 {
		t.Fatalf("entries after reopen: %d %v", count, err)
	}
}

// A host log in the cadence is a legacy log: live on open and on every
// write, and checkpointed by each tick.
func TestAHostLogInTheCadenceIsTicked(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	c, e := newCadence(t, shelf)
	host, err := shelf.HostLog(ctx, "node/chain", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Write(ctx, func(stores HostLogStores) error {
		_, err := stores.Log.Append(ctx, cll.AppendInput{Value: bytes.Repeat([]byte{1}, 32), AppendedAt: tickTime})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tick := mustTick(t, c, tickTime)
	leaf, ok := tick.Leaves.Deals["node/chain"]
	if !ok || leaf.Size != 1 {
		t.Fatalf("the tick's leaf %+v %v", leaf, ok)
	}
	e.set("node/chain")
	mustWitness(t, c, tick.N)
	if live, _ := c.Live(ctx); len(live) != 0 {
		t.Fatalf("an ended, witnessed host log stays live: %v", live)
	}
	if err := host.Write(ctx, func(HostLogStores) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if live, _ := c.Live(ctx); !reflect.DeepEqual(live, []string{"node/chain"}) {
		t.Fatalf("a write puts the log back: %v", live)
	}
	// Legacy opens the same log as the same handle.
	if legacy, err := shelf.Legacy(ctx, "node/chain"); err != nil || legacy != host {
		t.Fatalf("Legacy of an open host log: %v", err)
	}
}

type heldLock struct{ err error }

func (l heldLock) Held() error { return l.err }

// A host that holds the store's lock passes it: the shelf takes none of its
// own, so it neither waits on the host's lock nor releases it.
func TestAShelfOnAHostHeldLockTakesNone(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	f.cfg.LockPath = f.path + ".host.lock"
	unlock, err := lockWriter(ctx, f.cfg.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	defaultUnlock, err := lockWriter(ctx, f.path+".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = defaultUnlock() }()

	if _, err := OpenSQLiteShelf(ctx, ShelfConfig{Path: f.path, LockPath: f.cfg.LockPath, HeldLock: heldLock{}, Operator: "o",
		Signer: f.cfg.Signer, CheckpointKey: f.cfg.CheckpointKey, Originals: f.cfg.Originals}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a held lock with a lock path: %v", err)
	}
	cfg := f.cfg
	cfg.LockPath, cfg.HeldLock = "", heldLock{err: errors.New("not ours")}
	if _, err := OpenSQLiteShelf(ctx, cfg); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a lock the host does not hold: %v", err)
	}

	// Both the host's lock and the default lock file are held, so a shelf
	// that took either would wait until ctx ends.
	cfg.HeldLock = heldLock{}
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	shelf, err := OpenSQLiteShelf(short, cfg)
	if err != nil {
		t.Fatalf("a shelf on a held lock: %v", err)
	}
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("s", "1"))
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	// The host's lock is still the host's.
	waiting, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	defer stop()
	if _, err := lockWriter(waiting, f.cfg.LockPath); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the shelf released the host's lock: %v", err)
	}
}

// fixedStream is a random source that returns its bytes once.
type fixedStream struct{ b []byte }

func (s *fixedStream) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, errors.New("exhausted")
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

func be64(values ...uint64) []byte {
	var out []byte
	for _, v := range values {
		out = binary.BigEndian.AppendUint64(out, v)
	}
	return out
}

func TestTheRandomSourceDrawsAreDefinedOnItsBytes(t *testing.T) {
	// A power of two never redraws: x mod 2^16.
	got, err := newRandomSource(&fixedStream{be64(1<<64 - 1)}).uniform(1 << 16)
	if err != nil || got != 1<<16-1 {
		t.Fatalf("uniform(2^16) %d %v", got, err)
	}
	// For n = 3, 2^64 mod 3 = 1, so 2^64 - 1 is redrawn.
	got, err = newRandomSource(&fixedStream{be64(1<<64-1, 7)}).uniform(3)
	if err != nil || got != 1 {
		t.Fatalf("uniform(3) %d %v", got, err)
	}
	got, err = newRandomSource(&fixedStream{be64(1<<64 - 2)}).uniform(3)
	if err != nil || got != (1<<64-2)%3 {
		t.Fatalf("uniform(3) below the limit %d %v", got, err)
	}
	if _, err := newRandomSource(&fixedStream{}).uniform(0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uniform(0): %v", err)
	}
	// A taken position is drawn again.
	used := positions{5: true}
	if got, err := used.random(newRandomSource(&fixedStream{be64(5, 1<<16+9)})); err != nil || got != 9 || !used[9] {
		t.Fatalf("position %d %v", got, err)
	}
	// A short stream is an error, not a short value.
	if _, err := newRandomSource(&fixedStream{[]byte{1, 2, 3}}).hex(32); err == nil {
		t.Fatal("a short stream drew a value")
	}
}

// One injected source makes a cadence run deterministic: two shelves with
// the same stream, clock and records store the same ticks byte for byte.
func TestAnInjectedRandomSourceMakesTicksDeterministic(t *testing.T) {
	run := func(stream string) (string, []string) {
		ctx := context.Background()
		f := newShelfFixture(t)
		f.cfg.Random = detrand.New(stream)
		f.cfg.Now = func() time.Time { return tickTime }
		shelf := f.open(t)
		c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef", PadBucket: 4, Ended: func(context.Context, string) (bool, error) { return false, nil }})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"deal/a", "deal/b"} {
			mustAppend(t, mustOpenBook(t, shelf, id), observation(id, "1"))
		}
		var dues []string
		now := tickTime
		for range 3 {
			tick := mustTick(t, c, now)
			dues = append(dues, tick.DueNext.Format(time.RFC3339))
			now = tick.DueNext
		}
		var leaves []byte
		var entries uint64
		rows, err := shelf.db.Query(`SELECT leaves FROM cadence_ticks ORDER BY tick`)
		if err != nil {
			t.Fatal(err)
		}
		var all []byte
		for rows.Next() {
			if err := rows.Scan(&leaves); err != nil {
				t.Fatal(err)
			}
			all = append(append(all, leaves...), '\n')
		}
		// The shelf's handle has one connection: close before the next query.
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		if err := shelf.db.QueryRow(`SELECT COUNT(*) FROM cll_entries WHERE log_id = ?`, c.cfg.LogID).Scan(&entries); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%s%d", all, entries), dues
	}
	a, dueA := run("one")
	b, dueB := run("one")
	other, _ := run("two")
	if a != b || !reflect.DeepEqual(dueA, dueB) {
		t.Fatalf("one stream, two runs:\n%s\n%s", a, b)
	}
	if a == other {
		t.Fatal("another stream drew the same ticks")
	}
}

// A shelf transaction that reads and then writes takes the write lock when
// it begins (BEGIN IMMEDIATE). Under WAL a deferred one would read a
// snapshot, and another connection's commit before its first write (a
// book's CLL append, another process) would fail that write at once with
// SQLITE_BUSY_SNAPSHOT, which the busy timeout does not cover.
func TestAShelfTransactionHoldsTheWriteLockFromItsStart(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	other := rawShelfDB(t, f.path)
	committed := make(chan error, 1)
	err := shelf.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cadence_logs`).Scan(&n); err != nil {
			return err
		}
		go func() {
			_, err := other.ExecContext(ctx, `INSERT INTO cadence_logs (log_id) VALUES ('other')`)
			committed <- err
		}()
		// A deferred transaction lets the other write commit here; an
		// immediate one holds it back until this one commits.
		select {
		case err := <-committed:
			committed <- err
		case <-time.After(300 * time.Millisecond):
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO cadence_logs (log_id) VALUES ('mine')`)
		return err
	})
	if err != nil {
		t.Fatalf("the transaction's write after another connection's commit: %v", err)
	}
	if err := <-committed; err != nil {
		t.Fatalf("the other connection's write: %v", err)
	}
	var n int
	if err := other.QueryRow(`SELECT COUNT(*) FROM cadence_logs WHERE log_id IN ('mine', 'other')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("both writes landed: %d %v", n, err)
	}
}

// The same race through the book API: one book checkpoints while another
// appends, each append committing on its own CLL connection between the
// first book's reads and writes.
func TestABookCheckpointsWhileAnotherAppends(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	a, b := mustOpenBook(t, shelf, "deal/a"), mustOpenBook(t, shelf, "deal/b")
	done := make(chan error, 1)
	go func() {
		for i := range 60 {
			if _, err := b.Append(ctx, observation("b", fmt.Sprint(i))); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := range 30 {
		if _, err := a.Append(ctx, observation("a", fmt.Sprint(i))); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if _, err := a.Checkpoint(ctx); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("the other book's appends: %v", err)
	}
}
