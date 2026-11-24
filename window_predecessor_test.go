// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// convertedShelf closes a JSONL book of two records into a month-windowed
// store and opens that store with the clock a minute after the close
// (evidencebook#808).
func convertedShelf(t *testing.T, grace time.Duration) (*bookFixture, *Book, *shelfFixture, *Shelf, *WindowPolicy) {
	t.Helper()
	ctx := context.Background()
	bf := newFixture(t, "acme", 7)
	old := bf.open(t)
	mustAppend(t, old, observation("deal", "before-1"))
	mustAppend(t, old, observation("deal", "before-2"))
	policy, err := old.CloseForWindows(ctx, "acme", WindowMonth, grace)
	if err != nil {
		t.Fatal(err)
	}
	sf := newShelfFixture(t)
	sf.cfg.Window = policy
	setClock(sf.clock, policy.Since.Add(time.Minute))
	return bf, old, sf, sf.open(t), policy
}

func TestABookClosesIntoAWindowedStore(t *testing.T) {
	ctx := context.Background()
	_, old, sf, shelf, policy := convertedShelf(t, 0)
	if !old.Closed() {
		t.Fatal("the book is not closed")
	}
	// Since is the first whole second after the close record.
	log := old.logLocked()
	closeRecord := log[len(log)-1]
	if closeRecord.Header.RecordType != RecordTypeBookClose {
		t.Fatalf("last record %s", closeRecord.Header.RecordType)
	}
	closedAt, _ := time.Parse(time.RFC3339Nano, closeRecord.Header.CommittedAt)
	if !policy.Since.After(closedAt) || policy.Since.Sub(closedAt) > time.Second || !policy.Since.Equal(policy.Since.Truncate(time.Second)) {
		t.Fatalf("since %s after a close at %s", policy.Since, closedAt)
	}
	if policy.Predecessor.LogID != "acme-log" || policy.Predecessor.Entries != uint64(len(log)) {
		t.Fatalf("predecessor %+v", policy.Predecessor)
	}
	// The book takes nothing after its close.
	if _, err := old.Append(ctx, observation("deal", "after")); !errors.Is(err, ErrBookClosed) {
		t.Fatalf("append after close: %v", err)
	}
	// Closing again resumes with the same policy; another successor is refused.
	again, err := old.CloseForWindows(ctx, "acme", WindowMonth, 0)
	if err != nil || !again.sameAs(policy) {
		t.Fatalf("resume: %v %+v", err, again)
	}
	if _, err := old.CloseForWindows(ctx, "acme", WindowWeek, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another successor: %v", err)
	}

	// The first window is partial, from Since, and names the predecessor.
	book, w, err := shelf.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if w.Label != WindowMonth.WindowAt(policy.Since).Label || !w.Start.Equal(policy.Since) || !WindowMonth.Partial(w) {
		t.Fatalf("first window %+v, since %s", w, policy.Since)
	}
	var open WindowOpenStatement
	if err := json.Unmarshal(book.logLocked()[0].Header.Statement, &open); err != nil {
		t.Fatal(err)
	}
	if open.Previous == nil || *open.Previous != *policy.Predecessor || open.Start != windowTime(policy.Since) {
		t.Fatalf("open record %+v", open)
	}
	windows, err := shelf.Windows(ctx)
	if err != nil || len(windows) != 1 || !windows[0].Partial {
		t.Fatalf("windows %+v %v", windows, err)
	}
	// The stored policy reads back whole, and the file is version 5.
	if err := shelf.Release(); err != nil {
		t.Fatal(err)
	}
	stored, err := ReadShelfWindow(ctx, sf.path)
	if err != nil || !stored.sameAs(policy) {
		t.Fatalf("stored policy %+v %v", stored, err)
	}
	if v := shelfVersion(t, rawShelfDB(t, sf.path)); v != ShelfVersionWindowPredecessor {
		t.Fatalf("version %d", v)
	}
	changed := *policy
	changed.Predecessor = &WindowTree{LogID: "acme-log", Entries: 1, Root: policy.Predecessor.Root}
	sf.cfg.Window = &changed
	if _, err := OpenSQLiteShelf(ctx, sf.cfg); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another predecessor: %v", err)
	}
}

// The first window refuses a record dated before the conversion, though
// its label's period began earlier.
func TestAPartialWindowRefusesARecordBeforeItsStart(t *testing.T) {
	ctx := context.Background()
	_, _, sf, shelf, policy := convertedShelf(t, 0)
	book, _, err := shelf.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, book, observation("deal", "after-1"))
	setClock(sf.clock, policy.Since.Add(-2*time.Second))
	if _, err := book.Append(ctx, observation("deal", "backdated")); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("a record before since: %v", err)
	}
}

func TestAPredecessorPolicyIsValidated(t *testing.T) {
	base := WindowPolicy{Period: WindowMonth, Base: "acme", Since: time.Date(2026, 10, 19, 12, 0, 1, 0, time.UTC)}
	root := "ab" + string(make([]byte, 0))
	for i := 0; i < 31; i++ {
		root += "ab"
	}
	for name, p := range map[string]WindowPolicy{
		"since off a period start without a predecessor": base,
		"sub-second since": func() WindowPolicy {
			p := base
			p.Since = p.Since.Add(time.Millisecond)
			p.Predecessor = &WindowTree{LogID: "acme-log", Entries: 3, Root: root}
			return p
		}(),
		"predecessor inside the shelf": func() WindowPolicy {
			p := base
			p.Predecessor = &WindowTree{LogID: "acme/2026-09", Entries: 3, Root: root}
			return p
		}(),
		"policy without predecessor": func() WindowPolicy {
			p := base
			p.Since = utc(2026, 10, 1)
			p.PredecessorPolicy = &DisclosurePolicy{}
			return p
		}(),
	} {
		if err := p.validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	ok := base
	ok.Predecessor = &WindowTree{LogID: "acme-log", Entries: 3, Root: root}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
}
