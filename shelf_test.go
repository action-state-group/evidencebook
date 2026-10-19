// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

type shelfFixture struct {
	path  string
	clock *testClock
	cfg   ShelfConfig
}

func newShelfFixture(t *testing.T) *shelfFixture {
	t.Helper()
	dir := t.TempDir()
	signer, err := NewEd25519Signer(seededKey(7))
	if err != nil {
		t.Fatal(err)
	}
	originals, err := OpenOriginalsFile(context.Background(), filepath.Join(dir, "originals.jsonl"), "profile", signer.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	path := filepath.Join(dir, "deal.db")
	return &shelfFixture{path: path, clock: clock, cfg: ShelfConfig{
		Path: path, Operator: "profile-operator", Signer: signer, CheckpointKey: seededKey(107),
		Originals: originals, Now: clock.Now,
	}}
}

func (f *shelfFixture) open(t *testing.T) *Shelf {
	t.Helper()
	shelf, err := OpenSQLiteShelf(context.Background(), f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shelf.Release() })
	return shelf
}

func mustOpenBook(t *testing.T, shelf *Shelf, logID string) *Book {
	t.Helper()
	book, err := shelf.Open(context.Background(), logID)
	if err != nil {
		t.Fatal(err)
	}
	return book
}

func TestAShelfKeepsEachDealInItsOwnLog(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	a, b := mustOpenBook(t, shelf, "deal/deal-a"), mustOpenBook(t, shelf, "deal/deal-b")
	if again := mustOpenBook(t, shelf, "deal/deal-a"); again != a {
		t.Fatal("one log id is one *Book for the shelf's lifetime")
	}
	ra := []Record{mustAppend(t, a, observation("a", "a1")), mustAppend(t, a, observation("a", "a2"))}
	rb := mustAppend(t, b, observation("b", "b1"))
	if ra[0].Seq != 1 || ra[1].Seq != 2 || rb.Seq != 1 {
		t.Fatalf("each log counts its own positions: %d %d %d", ra[0].Seq, ra[1].Seq, rb.Seq)
	}
	anchor, err := a.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := a.Bundle(ctx, BundleRequest{Root: ra[1].RecordID, Include: []string{ra[0].RecordID}, At: &anchor})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if verified.LogID != "deal/deal-a" {
		t.Fatalf("bundle log %q", verified.LogID)
	}
	for _, record := range verified.Records {
		if record.RecordID == rb.RecordID {
			t.Fatal("a deal's bundle must carry no other deal's records")
		}
	}
	ids, err := shelf.Books(ctx)
	if err != nil || len(ids) != 2 || ids[0] != "deal/deal-a" || ids[1] != "deal/deal-b" {
		t.Fatalf("books %v %v", ids, err)
	}
}

func TestAShelfIsDurableAcrossReopen(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf, err := OpenSQLiteShelf(ctx, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	book := mustOpenBook(t, shelf, "deal/deal-a")
	first := mustAppend(t, book, observation("a", "a1"))
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Append(ctx, observation("a", "late")); !errors.Is(err, ErrReleased) {
		t.Fatalf("a write after the shelf's release: %v", err)
	}
	if _, err := shelf.Open(ctx, "deal/deal-a"); !errors.Is(err, ErrReleased) {
		t.Fatalf("open after release: %v", err)
	}

	reopened := f.open(t)
	book = mustOpenBook(t, reopened, "deal/deal-a")
	got, err := book.Get(ctx, first.RecordID)
	if err != nil || got.Seq != 1 {
		t.Fatalf("record after reopen: %v %+v", err, got)
	}
	latest, err := book.Checkpoint(ctx)
	if err != nil || latest.ID != cp.ID {
		t.Fatalf("an unchanged log keeps its checkpoint: %v %s %s", err, latest.ID, cp.ID)
	}
	held, err := book.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	// The first checkpoint committed index roots too, so the log holds more
	// than the one observation; positions continue after all of it.
	if second := mustAppend(t, book, observation("a", "a2")); second.Seq != uint64(len(held))+1 {
		t.Fatalf("positions continue: %d after %d records", second.Seq, len(held))
	}
}

func TestAShelfIsTheOnlyWriter(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	first := f.open(t)
	opened := make(chan error, 1)
	go func() {
		second, err := OpenSQLiteShelf(ctx, f.cfg)
		if err == nil {
			err = second.Release()
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		t.Fatalf("a second shelf opened while the first holds the lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second shelf never got the lock")
	}
}

func TestSQLiteStoreKeepsFileStoreSemantics(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	store := &sqliteStore{db: shelf.db, bookID: "deal/x"}
	other := &sqliteStore{db: shelf.db, bookID: "deal/y"}
	record := StoredRecord{Seq: 1, RecordID: "r1", Header: []byte(`{}`), Capsule: []byte(`{"c":1}`), Envelope: []byte{1}}
	if err := store.PutRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.PutRecord(ctx, record); err != nil {
		t.Fatalf("the same record again is a no-op: %v", err)
	}
	changed := record
	changed.Capsule = []byte(`{"c":2}`)
	if err := store.PutRecord(ctx, changed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("another record under the same id: %v", err)
	}
	if _, err := other.GetRecord(ctx, "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("books do not see each other's records")
	}
	for _, id := range []string{"cp-2", "cp-1", "cp-2"} {
		if err := store.PutCheckpoint(ctx, Checkpoint{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	cps, err := store.Checkpoints(ctx)
	if err != nil || len(cps) != 2 || cps[0].ID != "cp-2" || cps[1].ID != "cp-1" {
		t.Fatalf("checkpoints are kept once, in the order kept: %+v %v", cps, err)
	}
}

// A shelf waiting for the lock gives up when its context is done.
func TestAShelfStopsWaitingForTheLockWithItsContext(t *testing.T) {
	f := newShelfFixture(t)
	f.open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := OpenSQLiteShelf(ctx, f.cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a held lock past the deadline: %v", err)
	}
}

// A released book refuses every write, whether or not a shelf opened it,
// including a purge retry (SetRetention DELETED on a deleted record).
func TestAReleasedBookRefusesEveryWrite(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-released", 1).open(t)
	record := mustAppend(t, book, observation("s", "p"))
	if _, err := book.SetRetention(ctx, record.RecordID, Deleted, "expiry"); err != nil {
		t.Fatal(err)
	}
	if err := book.Release(); err != nil {
		t.Fatal(err)
	}
	if err := book.Release(); err != nil {
		t.Fatalf("Release is idempotent: %v", err)
	}
	if _, err := book.Append(ctx, observation("s", "late")); !errors.Is(err, ErrReleased) {
		t.Fatalf("append: %v", err)
	}
	if _, err := book.Checkpoint(ctx); !errors.Is(err, ErrReleased) {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, err := book.SetRetention(ctx, record.RecordID, Deleted, "retry"); !errors.Is(err, ErrReleased) {
		t.Fatalf("a purge retry after release: %v", err)
	}
}

// A host's own lock path is the shelf's: a holder of it keeps the shelf out.
func TestAShelfTakesTheHostsLockPath(t *testing.T) {
	f := newShelfFixture(t)
	f.cfg.LockPath = f.path + ".deal.lock"
	unlock, err := lockWriter(context.Background(), f.cfg.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := OpenSQLiteShelf(ctx, f.cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the host holds the lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	f.open(t)
}

// A log that has state but fails to open is reported, never provisioned
// over: OpenCLLSQLite does not write to it.
func TestACorruptLogIsNeverReprovisioned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deal.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// An entry with no meta row: not a log anyone provisioned this way.
	for _, statement := range []string{
		`CREATE TABLE cll_entries (log_id TEXT NOT NULL, seq INTEGER NOT NULL, value BLOB NOT NULL, appended_at TEXT NOT NULL, PRIMARY KEY(log_id, seq))`,
		`INSERT INTO cll_entries VALUES ('deal/x', 1, x'00', '2026-10-19T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCLLSQLite(path, "deal/x", seededKey(9)); err == nil {
		t.Fatal("a log with entries and no state must not open")
	}
	db, _ = sql.Open("sqlite", "file:"+path)
	defer db.Close()
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'cll_meta'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("OpenCLLSQLite wrote to the store: %d meta tables (%v)", tables, err)
	}
	// A log the file holds nothing of is provisioned beside it.
	if s, err := OpenCLLSQLite(path, "deal/y", seededKey(9)); err != nil {
		t.Fatalf("a fresh log: %v", err)
	} else {
		_ = s.Release()
	}
}

// Two books of one shelf append at once; each keeps its own positions.
func TestTwoBooksOfAShelfAppendConcurrently(t *testing.T) {
	shelf := newShelfFixture(t).open(t)
	a, b := mustOpenBook(t, shelf, "deal/a"), mustOpenBook(t, shelf, "deal/b")
	errs := make(chan error, 2)
	for _, book := range []*Book{a, b} {
		go func(book *Book) {
			for i := 0; i < 20; i++ {
				if _, err := book.Append(context.Background(), observation("s", fmt.Sprintf("p%d", i))); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}(book)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, book := range []*Book{a, b} {
		all, err := book.Query(context.Background(), Filter{})
		if err != nil || len(all) != 20 || all[19].Seq != 20 {
			t.Fatalf("%d records, %v", len(all), err)
		}
	}
}

// A shelf can give its books no authenticated index: their checkpoints then
// append no index_root records, and a deal's log holds its steps only.
func TestAShelfChoosesItsBooksIndexes(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	f.cfg.AuthenticatedIndexes = func(string) []AuthenticatedIndex { return []AuthenticatedIndex{} }
	shelf := f.open(t)
	book := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, book, observation("s", "1"))
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Entries != 1 {
		t.Fatalf("the checkpoint covers %d entries; index roots were appended", cp.Entries)
	}
	all, _ := book.Query(ctx, Filter{})
	for _, record := range all {
		if record.Header.RecordType == RecordTypeIndexRoot {
			t.Fatal("an index_root record in a book with no index")
		}
	}
}
