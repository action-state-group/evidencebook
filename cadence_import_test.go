// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

// legacyFixture is a profile cut over from a pre-shelf writer: legacy logs
// in the shelf's file, and the ticks that writer cut on the profile's
// cadence log. The ticks are cut here by the cadence itself (its tree and
// rows are capsule-cli's, byte for byte: cadence_tree_test.go), read back,
// and then the cadence's own state is cleared, as a store that only ever
// had deal_cadence rows would be.
type legacyFixture struct {
	shelf  *Shelf
	c      *Cadence
	ended  *ended
	legacy map[string]*LegacyLog
	ticks  []CadenceTick
}

func newLegacyFixture(t *testing.T, logs ...string) *legacyFixture {
	t.Helper()
	ctx := context.Background()
	f := newShelfFixture(t)
	for i, id := range logs {
		old, err := OpenCLLSQLite(f.path, id, f.cfg.CheckpointKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.Append(ctx, hex.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32)), tickTime); err != nil {
			t.Fatal(err)
		}
		if err := old.Release(); err != nil {
			t.Fatal(err)
		}
	}
	l := &legacyFixture{shelf: f.open(t), legacy: map[string]*LegacyLog{}}
	l.c, l.ended = newCadence(t, l.shelf)
	for _, id := range logs {
		legacy, err := l.shelf.Legacy(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		l.legacy[id] = legacy
	}
	first := mustTick(t, l.c, tickTime)
	second := mustTick(t, l.c, first.DueNext)
	l.ticks = []CadenceTick{first, second}
	// The pre-shelf writer's receipt for its first tick.
	l.ticks[0].Witnessed, l.ticks[0].Receipt = true, &testReceipt
	for _, table := range []string{"cadence_ticks", "cadence_witnessed", "cadence_live", "cadence_logs"} {
		if _, err := l.shelf.db.Exec(`DELETE FROM ` + table); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func (l *legacyFixture) logs() []string {
	var out []string
	for id := range l.legacy {
		out = append(out, id)
	}
	return out
}

func countRows(t *testing.T, s *Shelf, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The cadence continues from the imported ticks: the next tick is numbered
// after them and due at the last one's due time, the witnessed tick's leaves
// are indexed, every legacy log counts into H, and a bundle anchored on an
// imported witnessed tick gets its chain.
func TestImportLegacyContinuesTheCadence(t *testing.T) {
	ctx := context.Background()
	l := newLegacyFixture(t, "deal/a", "deal/b")
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks}); err != nil {
		t.Fatal(err)
	}
	if h := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_logs`); h != 2 {
		t.Fatalf("H %d, want 2", h)
	}
	if n := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_witnessed WHERE tick = 1`); n != 2 {
		t.Fatalf("witnessed rows for tick 1: %d, want 2", n)
	}
	if n := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_witnessed WHERE tick = 2`); n != 0 {
		t.Fatalf("an unwitnessed tick was indexed: %d", n)
	}
	if live, _ := l.c.Live(ctx); len(live) != 2 {
		t.Fatalf("live %v: neither log has ended", live)
	}
	early, err := l.c.Tick(ctx, l.ticks[1].DueNext.Add(-1))
	if err != nil || early.Ticked || !early.DueNext.Equal(l.ticks[1].DueNext) {
		t.Fatalf("before the imported due time: %+v %v", early, err)
	}
	next := mustTick(t, l.c, l.ticks[1].DueNext)
	if next.N != 3 || next.EntrySeq <= l.ticks[1].EntrySeq {
		t.Fatalf("next tick %d at entry %d after entry %d", next.N, next.EntrySeq, l.ticks[1].EntrySeq)
	}
	leaf := l.ticks[0].Leaves.Deals["deal/a"]
	statement, err := base64.RawURLEncoding.DecodeString(leaf.Statement)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := l.c.Chain(ctx, statement)
	if err != nil || chain["extent"] != "all" {
		t.Fatalf("chain on an imported witnessed tick: %v %v", chain["extent"], err)
	}
	extent, tick, _, err := l.c.WitnessedTick(ctx, "deal/a", leaf.CheckpointSHA256, leaf.Size)
	if err != nil || extent != "all" || tick.N != 1 {
		t.Fatalf("index: %s %d %v", extent, tick.N, err)
	}
}

// Importing the same state again changes nothing; a tick delivered since it
// was imported is promoted to witnessed; a tick a pre-shelf writer added
// after the cadence's own is numbered after them; and the live rule is
// applied to every log on every run.
func TestImportLegacyIsRerunnable(t *testing.T) {
	ctx := context.Background()
	l := newLegacyFixture(t, "deal/a", "deal/b")
	state := LegacyCadence{Logs: l.logs(), Ticks: l.ticks}
	if err := l.c.ImportLegacy(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := l.c.ImportLegacy(ctx, state); err != nil {
		t.Fatalf("the same import again: %v", err)
	}
	if n := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_ticks`); n != 2 {
		t.Fatalf("ticks %d after a repeated import", n)
	}
	// The second tick's receipt arrives through the pre-shelf writer.
	l.ticks[1].Witnessed, l.ticks[1].Receipt = true, &testReceipt
	l.ended.set("deal/a")
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks}); err != nil {
		t.Fatal(err)
	}
	second, err := l.c.Get(ctx, 2)
	if err != nil || !second.Witnessed || second.Receipt == nil {
		t.Fatalf("tick 2 not promoted: %+v %v", second, err)
	}
	// Its leaves are indexed. An unchanged log names the same checkpoint in
	// both ticks, so its row may be tick 1's: the checkpoint is what counts.
	for id, leaf := range l.ticks[1].Leaves.Deals {
		if n := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_witnessed WHERE log_id = ? AND checkpoint_sha256 = ?`, id, leaf.CheckpointSHA256); n != 1 {
			t.Fatalf("%s's checkpoint in the promoted tick is not indexed", id)
		}
	}
	if live, _ := l.c.Live(ctx); len(live) != 1 || live[0] != "deal/b" {
		t.Fatalf("live %v: deal/a ended and its final checkpoint is witnessed", live)
	}
	// deal/a grows again (a released build's report on the ended deal): the
	// next run puts it back.
	if _, err := l.legacy["deal/a"].log.Append(ctx, hex.EncodeToString(bytes.Repeat([]byte{9}, 32)), tickTime); err != nil {
		t.Fatal(err)
	}
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks}); err != nil {
		t.Fatal(err)
	}
	if live, _ := l.c.Live(ctx); len(live) != 2 {
		t.Fatalf("live %v: a grown log is live again", live)
	}
	// The cadence ticks on its own, then a pre-shelf writer adds a tick
	// later in the log: it is numbered after the cadence's.
	native := mustTick(t, l.c, l.ticks[1].DueNext)
	foreign := native
	foreign.N, foreign.EntrySeq, foreign.Witnessed, foreign.Receipt = 99, native.EntrySeq+5, false, nil
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: append(l.ticks, foreign)}); err != nil {
		t.Fatal(err)
	}
	last, err := l.c.Get(ctx, native.N+1)
	if err != nil || last.EntrySeq != foreign.EntrySeq {
		t.Fatalf("the later tick: %+v %v", last, err)
	}
}

// What an import may not do: rewrite a held tick, insert one older than the
// cadence's newest, carry a log it does not register, take an unfinished or
// receiptless tick, or treat a book as a legacy log.
func TestImportLegacyRefuses(t *testing.T) {
	ctx := context.Background()
	l := newLegacyFixture(t, "deal/a")
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks}); err != nil {
		t.Fatal(err)
	}
	other := l.ticks[0]
	other.Checkpoint = append([]byte{}, other.Checkpoint...)
	other.Checkpoint[len(other.Checkpoint)-1] ^= 1
	unfinished := l.ticks[1]
	unfinished.CheckpointSize = 0
	receiptless := l.ticks[1]
	receiptless.Witnessed, receiptless.Receipt = true, nil
	older := l.ticks[1]
	older.EntrySeq = l.ticks[0].EntrySeq + 1
	older.Checkpoint = []byte("not held")
	cases := map[string]LegacyCadence{
		"another checkpoint at a held position": {Logs: l.logs(), Ticks: []CadenceTick{other}},
		"an unregistered log":                   {Logs: nil, Ticks: l.ticks},
		"an unfinished tick":                    {Logs: l.logs(), Ticks: []CadenceTick{unfinished}},
		"a witnessed tick without a receipt":    {Logs: l.logs(), Ticks: []CadenceTick{receiptless}},
		"out of order":                          {Logs: l.logs(), Ticks: []CadenceTick{l.ticks[1], l.ticks[0]}},
		"the cadence log itself":                {Logs: []string{"deal-cadence/0123456789abcdef"}},
	}
	if older.EntrySeq < l.ticks[1].EntrySeq {
		cases["older than the newest held tick"] = LegacyCadence{Logs: l.logs(), Ticks: []CadenceTick{older}}
	}
	for name, legacy := range cases {
		if err := l.c.ImportLegacy(ctx, legacy); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	book := mustOpenBook(t, l.shelf, "deal/book")
	mustAppend(t, book, observation("s", "1"))
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: []string{"deal/book"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a book as a legacy log: %v", err)
	}
}

// Records lists committed records in book then seq order, without opening a
// book: a record stored above its book's entry count is counted as pending
// and not listed, and a book filter narrows the listing.
func TestShelfRecordsReadsWithoutOpening(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	a, b := mustOpenBook(t, shelf, "deal/a"), mustOpenBook(t, shelf, "deal/b")
	ra := mustAppend(t, a, observation("s", "1"))
	mustAppend(t, a, observation("s", "2"))
	rb := mustAppend(t, b, observation("s", "3"))
	// A pending tail: a record stored past the log, as a crash between
	// PutRecord and the append leaves it.
	header, err := shelf.db.Query(`SELECT header FROM book_records WHERE record_id = ?`, ra.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	var h []byte
	if !header.Next() {
		t.Fatal("no stored header")
	}
	if err := header.Scan(&h); err != nil {
		t.Fatal(err)
	}
	_ = header.Close()
	if _, err := shelf.db.Exec(`INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES ('deal/a', 3, 'pending', ?, x'', x'')`, h); err != nil {
		t.Fatal(err)
	}
	all, err := shelf.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range all.Records {
		got = append(got, r.BookID+"#"+string(rune('0'+r.Seq)))
	}
	if len(got) != 3 || got[0] != "deal/a#1" || got[1] != "deal/a#2" || got[2] != "deal/b#1" {
		t.Fatalf("records %v", got)
	}
	if all.Pending["deal/a"] != 1 || len(all.Pending) != 1 {
		t.Fatalf("pending %v", all.Pending)
	}
	if all.Records[0].RecordID != ra.RecordID || all.Records[0].Header.BookID != "deal/a" {
		t.Fatalf("first record %+v", all.Records[0])
	}
	if all.Records[0].Retention != Available {
		t.Fatalf("retention %q, want AVAILABLE", all.Records[0].Retention)
	}
	only, err := shelf.Records(ctx, "deal/b")
	if err != nil || len(only.Records) != 1 || only.Records[0].RecordID != rb.RecordID {
		t.Fatalf("filtered %+v %v", only, err)
	}
}
