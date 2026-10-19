// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/action-state-group/checkpointed-local-log/go/cll"
	cllsqlite "github.com/action-state-group/checkpointed-local-log/go/store/sqlite"
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
	if last, ok, err := l.c.Last(ctx); err != nil || !ok || last.N != 2 || last.EntrySeq != l.ticks[1].EntrySeq {
		t.Fatalf("last after import: %+v %v %v", last, ok, err)
	}
	next := mustTick(t, l.c, l.ticks[1].DueNext)
	if next.N != 3 || next.EntrySeq <= l.ticks[1].EntrySeq {
		t.Fatalf("next tick %d at entry %d after entry %d", next.N, next.EntrySeq, l.ticks[1].EntrySeq)
	}
	// The imported ticks have a released build's shape: every log was in
	// every tick, so only the one stored filler and no derived ones
	// (capsule-cli 7a6b101 dealTickLeaves), and Chain rebuilds them.
	for _, tick := range l.ticks {
		if tick.Leaves.FillerSeed != "" || tick.Leaves.FillerCount != 0 || tick.Leaves.FillerNonce == "" {
			t.Fatalf("tick at entry %d is not a released build's shape: %+v", tick.EntrySeq, tick.Leaves)
		}
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

// An import never strands the cadence's own unfinished tick: it is refused
// until a tick finishes it, then imports after it.
func TestImportLegacyWaitsForAnUnfinishedTick(t *testing.T) {
	ctx := context.Background()
	l := newLegacyFixture(t, "deal/a")
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks}); err != nil {
		t.Fatal(err)
	}
	native := mustTick(t, l.c, l.ticks[1].DueNext)
	// A crash after the native tick's row: unfinished.
	if _, err := l.shelf.db.Exec(`UPDATE cadence_ticks SET checkpoint_size = 0, entry_seq = 0, checkpoint = x'' WHERE tick = ?`, native.N); err != nil {
		t.Fatal(err)
	}
	foreign := native
	foreign.EntrySeq = native.EntrySeq + 5
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: append(l.ticks, foreign)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("import over an unfinished tick: %v", err)
	}
	resumed, err := l.c.Tick(ctx, native.DueNext)
	if err != nil || !resumed.Ticked || resumed.Tick.N != native.N {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	foreign.EntrySeq = resumed.Tick.EntrySeq + 5
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: append(l.ticks, foreign)}); err != nil {
		t.Fatal(err)
	}
	if last, err := l.c.Get(ctx, native.N+1); err != nil || last.EntrySeq != foreign.EntrySeq {
		t.Fatalf("after the finished tick: %+v %v", last, err)
	}
}

// Promotion indexes the held tick's own leaves, whatever the incoming copy
// of it carries.
func TestImportLegacyPromotesTheHeldLeaves(t *testing.T) {
	ctx := context.Background()
	l := newLegacyFixture(t, "deal/a", "deal/b")
	// Nothing witnessed yet: the index starts empty.
	l.ticks[0].Witnessed, l.ticks[0].Receipt = false, nil
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: l.ticks}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_witnessed`); n != 0 {
		t.Fatalf("index before promotion: %d rows", n)
	}
	held := l.ticks[1]
	incoming := held
	incoming.Witnessed, incoming.Receipt = true, &testReceipt
	incoming.Leaves = TickLeaves{Deals: map[string]TickLeaf{}}
	if err := l.c.ImportLegacy(ctx, LegacyCadence{Logs: l.logs(), Ticks: []CadenceTick{l.ticks[0], incoming}}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_witnessed WHERE tick = 2`); n != len(held.Leaves.Deals) || n != 2 {
		t.Fatalf("tick 2's held leaves indexed: %d rows", n)
	}
	for id, leaf := range held.Leaves.Deals {
		if n := countRows(t, l.shelf, `SELECT COUNT(*) FROM cadence_witnessed WHERE log_id = ? AND checkpoint_sha256 = ? AND tick = 2`, id, leaf.CheckpointSHA256); n != 1 {
			t.Fatalf("%s's held checkpoint is not indexed at tick 2", id)
		}
	}
	stored, err := l.c.Get(ctx, 2)
	if err != nil || len(stored.Leaves.Deals) != 2 || !stored.Witnessed {
		t.Fatalf("the held tick: %+v %v", stored, err)
	}
}

// Records reads a shelf whose books are not open: it lists committed
// records in book then seq order with the retention Book.Retention gives
// (a lifecycle record, an active and an expired hold, a field-only
// commitment), leaves a pending tail pending and unlisted, changes nothing
// stored, and a book filter narrows it.
func TestShelfRecordsReadsWithoutOpening(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	writer := f.open(t)
	a, b := mustOpenBook(t, writer, "deal/a"), mustOpenBook(t, writer, "deal/b")
	withheld := mustAppend(t, a, observation("s", "1"))
	held := mustAppend(t, a, observation("s", "2"))
	expired := mustAppend(t, a, observation("s", "3"))
	fields := mustAppend(t, a, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "s", Fields: map[string]string{"amount": "12.00"}})
	other := mustAppend(t, b, observation("s", "4"))
	if _, err := a.SetRetention(ctx, withheld.RecordID, Withheld, "not shared"); err != nil {
		t.Fatal(err)
	}
	now := f.clock.Now()
	if _, err := a.CommitRetention(ctx, Subject{Kind: SubjectRecord, Digest: held.RecordID}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CommitRetention(ctx, Subject{Kind: SubjectRecord, Digest: expired.RecordID}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	want := map[string]RetentionState{}
	for _, r := range []Record{withheld, held, expired, fields} {
		state, err := a.Retention(ctx, r.RecordID)
		if err != nil {
			t.Fatal(err)
		}
		want[r.RecordID] = state
	}
	if want[withheld.RecordID] != Withheld || want[held.RecordID] != LegalHold || want[expired.RecordID] != Available || want[fields.RecordID] != Available {
		t.Fatalf("fixture retention %v", want)
	}
	if err := writer.Release(); err != nil {
		t.Fatal(err)
	}

	shelf := f.open(t)
	// A pending tail on deal/a: a record stored past the log, as a crash
	// between PutRecord and the append leaves it. It is neither listed nor
	// recovered.
	var header []byte
	if err := shelf.db.QueryRow(`SELECT header FROM book_records WHERE record_id = ?`, withheld.RecordID).Scan(&header); err != nil {
		t.Fatal(err)
	}
	pendingSeq := countRows(t, shelf, `SELECT COUNT(*) FROM book_records WHERE book_id = 'deal/a'`) + 1
	if _, err := shelf.db.Exec(`INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES ('deal/a', ?, 'pending', ?, x'', x'')`, pendingSeq, header); err != nil {
		t.Fatal(err)
	}
	snapshot := func() [3]int {
		return [3]int{
			countRows(t, shelf, `SELECT COUNT(*) FROM book_records`),
			countRows(t, shelf, `SELECT COUNT(*) FROM cll_entries`),
			countRows(t, shelf, `SELECT COUNT(*) FROM cadence_live`),
		}
	}
	before := snapshot()
	all, err := shelf.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("Records changed the store: %v -> %v", before, after)
	}
	if len(shelf.books) != 0 {
		t.Fatalf("Records opened %d books", len(shelf.books))
	}
	if all.Pending["deal/a"] != 1 || len(all.Pending) != 1 {
		t.Fatalf("pending %v", all.Pending)
	}
	var lastA uint64
	byID := map[string]ShelfRecord{}
	for i, r := range all.Records {
		if i > 0 && (r.BookID < all.Records[i-1].BookID || (r.BookID == all.Records[i-1].BookID && r.Seq != all.Records[i-1].Seq+1)) {
			t.Fatalf("out of order at %d: %+v", i, r)
		}
		if r.BookID == "deal/a" {
			lastA = r.Seq
		}
		byID[r.RecordID] = r
	}
	if lastA != uint64(pendingSeq-1) {
		t.Fatalf("deal/a lists through seq %d, want %d", lastA, pendingSeq-1)
	}
	for id, state := range want {
		if byID[id].Retention != state {
			t.Errorf("%s: Records says %q, Book.Retention %q", id, byID[id].Retention, state)
		}
	}
	only, err := shelf.Records(ctx, "deal/b")
	if err != nil || len(only.Records) != 1 || only.Records[0].RecordID != other.RecordID {
		t.Fatalf("filtered %+v %v", only, err)
	}
}

// A cadence configured with a witness queues each cadence checkpoint for it
// in the same commit, so the host can deliver it (cll's PendingWitnesses);
// one without queues nothing.
func TestACadenceQueuesItsCheckpointForTheWitness(t *testing.T) {
	ctx := context.Background()
	f := newShelfFixture(t)
	shelf := f.open(t)
	e := &ended{logs: map[string]bool{}}
	c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef", Ended: e.check, WitnessIDs: []string{"witness-a"}})
	if err != nil {
		t.Fatal(err)
	}
	tick := mustTick(t, c, tickTime)
	log, err := cllsqlite.Open(f.path, "deal-cadence/0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	pending, err := log.PendingWitnesses(ctx, tickTime.Add(24*time.Hour), cll.MaxWitnesses)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].WitnessID != "witness-a" || pending[0].CheckpointSize != tick.CheckpointSize || !bytes.Equal(pending[0].Checkpoint, tick.Checkpoint) {
		t.Fatalf("pending deliveries %+v, want one for tick %d at size %d", pending, tick.N, tick.CheckpointSize)
	}
}

// NoJitter makes each tick due exactly the interval after the last; it
// cannot be combined with a jitter.
func TestNoJitterIsExact(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	e := &ended{logs: map[string]bool{}}
	if _, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef", Ended: e.check, NoJitter: true, Jitter: time.Second}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NoJitter with a jitter: %v", err)
	}
	c, err := shelf.Cadence(ctx, CadenceConfig{LogID: "deal-cadence/0123456789abcdef", Ended: e.check, Interval: time.Minute, NoJitter: true})
	if err != nil {
		t.Fatal(err)
	}
	first := mustTick(t, c, tickTime)
	if want := tickTime.Add(time.Minute).Truncate(time.Second); !first.DueNext.Equal(want) {
		t.Fatalf("due %s, want %s", first.DueNext, want)
	}
}
