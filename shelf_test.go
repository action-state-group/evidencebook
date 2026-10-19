// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"errors"
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
