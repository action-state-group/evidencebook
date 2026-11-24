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

// convertedChain converts a book, writes into the partial first window,
// rolls two windows on and returns the chain from the predecessor through
// the first two windows.
func convertedChain(t *testing.T, receipts func(context.Context, Checkpoint) ([]json.RawMessage, error)) (*Book, *shelfFixture, *Shelf, WindowChain) {
	t.Helper()
	ctx := context.Background()
	_, old, sf, shelf, policy := convertedShelf(t, 0)
	book, _, err := shelf.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, book, observation("deal", "after-1"))
	first := WindowMonth.WindowAt(policy.Since)
	second := WindowMonth.Next(first)
	setClock(sf.clock, WindowMonth.Next(second).Start.Add(48*time.Hour))
	if _, err := shelf.Roll(ctx); err != nil {
		t.Fatal(err)
	}
	predecessor, err := ChainPredecessorFromBook(ctx, old, receipts)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := shelf.WindowChain(ctx, first.Label, second.Label, WindowChainOptions{Receipts: receipts, Predecessor: predecessor})
	if err != nil {
		t.Fatal(err)
	}
	return old, sf, shelf, chain
}

// Sherlock2's acceptance (evidencebook#808): a chain from the predecessor
// through two windows verifies; a dropped predecessor, an altered
// predecessor tree and a record after the book's close are named.
func TestAChainCarriesThePredecessorAcrossTheConversion(t *testing.T) {
	ctx := context.Background()
	old, sf, shelf, chain := convertedChain(t, nil)
	key := VerifyChainOptions{CheckpointKey: shelfCheckpointKey(sf)}
	if chain.V != WindowChainVersionPredecessor || chain.Predecessor == nil {
		t.Fatalf("chain v %d predecessor %v", chain.V, chain.Predecessor != nil)
	}
	verified, findings := VerifyWindowChain(chain, key)
	if len(findings) > 0 || len(verified) != 2 {
		t.Fatalf("findings %v", findings)
	}
	if verified[0].Predecessor == nil || verified[0].Predecessor.LogID != "acme-log" || !WindowMonth.Partial(verified[0].Window) {
		t.Fatalf("first window %+v", verified[0])
	}

	dropped := chain
	dropped.Predecessor = nil
	assertFinding(t, dropped, key, "chain breaks at "+chain.From+": its open record names a predecessor book the chain does not carry")

	// Another checkpoint of the old book, of a tree other than the one the
	// first window names: an earlier one.
	checkpoints, err := old.Checkpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	altered := chain
	p := *chain.Predecessor
	p.FinalCheckpoint = checkpoints[0].Statement
	altered.Predecessor = &p
	if texts := findingTexts(altered, key); len(texts) == 0 {
		t.Fatal("an altered predecessor tree passed")
	}

	// A record appended after the close: the holder bypasses the book's
	// refusal, checkpoints the longer tree and hands it over.
	old.mu.Lock()
	last := &old.log[len(old.log)-1]
	last.Header.RecordType = "deal"
	old.mu.Unlock()
	mustAppend(t, old, observation("deal", "after-close"))
	old.mu.Lock()
	old.log[len(old.log)-2].Header.RecordType = RecordTypeBookClose
	old.mu.Unlock()
	longer, err := old.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	appended := chain
	q := *chain.Predecessor
	q.FinalCheckpoint = longer.Statement
	appended.Predecessor = &q
	if texts := findingTexts(appended, key); len(texts) == 0 {
		t.Fatal("a record after the close passed")
	}

	// A chain from the second window does not include the predecessor and
	// says so.
	later, err := shelf.WindowChain(ctx, chain.To, chain.To, WindowChainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	verified, findings = VerifyWindowChain(later, key)
	if len(findings) > 0 || verified[0].Note == "" || later.Predecessor != nil {
		t.Fatalf("later chain %+v %v", verified, findings)
	}
	// A chain from the first window without the predecessor is refused.
	if _, err := shelf.WindowChain(ctx, chain.From, chain.To, WindowChainOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no predecessor passed: %v", err)
	}
	// A version 2 chain with this since is refused.
	v2 := chain
	v2.V = WindowChainVersion
	if texts := findingTexts(v2, key); len(texts) == 0 {
		t.Fatal("a v2 chain with a partial window passed")
	}
}

func TestAWitnessedConvertedChainNeedsThePredecessorsReceipt(t *testing.T) {
	witnessKey, directory := testWitness()
	receipts := func(_ context.Context, cp Checkpoint) ([]json.RawMessage, error) {
		return []json.RawMessage{rawReceipt(t, mintReceipt(t, cp.Statement, witnessKey))}, nil
	}
	_, sf, _, chain := convertedChain(t, receipts)
	key := VerifyChainOptions{CheckpointKey: shelfCheckpointKey(sf), Witnesses: directory}
	if texts := findingTexts(chain, key); len(texts) > 0 {
		t.Fatalf("findings %v", texts)
	}
	unwitnessed := chain
	p := *chain.Predecessor
	p.Receipts = nil
	unwitnessed.Predecessor = &p
	assertFinding(t, unwitnessed, key, "predecessor not witnessed: its final checkpoint carries no receipt")
}

func assertFinding(t *testing.T, chain WindowChain, opts VerifyChainOptions, want string) {
	t.Helper()
	for _, text := range findingTexts(chain, opts) {
		if text == want {
			return
		}
	}
	t.Fatalf("no finding %q in %v", want, findingTexts(chain, opts))
}

// A report over the conversion's month names the part of it in the
// predecessor book: no window answers for it.
func TestCoverageNamesThePartOfThePartialMonthBeforeTheConversion(t *testing.T) {
	ctx := context.Background()
	_, sf, shelf, chain := convertedChain(t, nil)
	key := VerifyChainOptions{CheckpointKey: shelfCheckpointKey(sf)}
	book, _, err := shelf.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	record, err := book.RecordWindowChain(ctx, chain, key)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := canonicalJSON(chain)
	if err != nil {
		t.Fatal(err)
	}
	_, findings, err := coverage(record.Header, payload, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	first := chain.Windows[0].Label
	since, _ := time.Parse(time.RFC3339, chain.Since)
	want := "window " + first + " starts at " + chain.Since + "; [" + windowTime(WindowMonth.start(since)) + ", " + chain.Since + ") is in the predecessor book, not covered"
	for _, f := range findings {
		if f.Text == want {
			return
		}
	}
	t.Fatalf("no finding %q in %v", want, findings)
}

// The predecessor's close must be signed by the windows' key.
func TestAPredecessorSignedByAnotherKeyIsNamed(t *testing.T) {
	ctx := context.Background()
	bf := newFixture(t, "acme", 9) // another record key, and another checkpoint key
	old := bf.open(t)
	mustAppend(t, old, observation("deal", "before-1"))
	policy, err := old.CloseForWindows(ctx, "acme", WindowMonth, 0)
	if err != nil {
		t.Fatal(err)
	}
	sf := newShelfFixture(t)
	sf.cfg.Window = policy
	setClock(sf.clock, policy.Since.Add(time.Minute))
	shelf := sf.open(t)
	book, _, err := shelf.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, book, observation("deal", "after-1"))
	first := WindowMonth.WindowAt(policy.Since)
	setClock(sf.clock, WindowMonth.Next(first).Start.Add(48*time.Hour))
	predecessor, err := ChainPredecessorFromBook(ctx, old, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := shelf.WindowChain(ctx, first.Label, first.Label, WindowChainOptions{Predecessor: predecessor})
	if err != nil {
		t.Fatal(err)
	}
	if texts := findingTexts(chain, VerifyChainOptions{CheckpointKey: shelfCheckpointKey(sf)}); len(texts) == 0 {
		t.Fatal("a predecessor under other keys passed")
	}
}
