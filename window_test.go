// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Window books (evidencebook#689, docs/design/689-window-books.md).

func setClock(c *testClock, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func utc(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func monthly(base string, since time.Time) *WindowPolicy {
	return &WindowPolicy{Period: WindowMonth, Base: base, Since: since}
}

// newWindowFixture is a shelf fixture windowed by month from October 2026,
// its clock on 2026-10-19.
func newWindowFixture(t *testing.T) *shelfFixture {
	t.Helper()
	f := newShelfFixture(t)
	f.cfg.Window = monthly("acme", utc(2026, 10, 1))
	return f
}

func TestWindowPeriodsLabelAndBound(t *testing.T) {
	for _, c := range []struct {
		period     WindowPeriod
		at         time.Time
		label      string
		start, end time.Time
	}{
		{WindowDay, time.Date(2028, 2, 29, 23, 59, 0, 0, time.UTC), "2028-02-29", utc(2028, 2, 29), utc(2028, 3, 1)},
		{WindowWeek, utc(2026, 12, 31), "2026-W53", utc(2026, 12, 28), utc(2027, 1, 4)},
		{WindowWeek, utc(2027, 1, 3), "2026-W53", utc(2026, 12, 28), utc(2027, 1, 4)},
		{WindowWeek, utc(2027, 1, 4), "2027-W01", utc(2027, 1, 4), utc(2027, 1, 11)},
		{WindowMonth, time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), "2026-12", utc(2026, 12, 1), utc(2027, 1, 1)},
		{WindowMonth, utc(2028, 2, 29), "2028-02", utc(2028, 2, 1), utc(2028, 3, 1)},
		{WindowQuarter, utc(2026, 11, 23), "2026-Q4", utc(2026, 10, 1), utc(2027, 1, 1)},
		{WindowYear, utc(2026, 11, 23), "2026", utc(2026, 1, 1), utc(2027, 1, 1)},
	} {
		w := c.period.WindowAt(c.at)
		assert.Equal(t, Window{Label: c.label, Start: c.start, End: c.end}, w, "%s at %s", c.period, c.at)
		parsed, err := c.period.ParseWindow(c.label)
		require.NoError(t, err)
		assert.Equal(t, w, parsed, "a label alone gives its bounds")
		assert.Equal(t, c.end, c.period.Next(w).Start)
	}
	// A time in another zone is placed by its UTC instant.
	assert.Equal(t, "2026-12", WindowMonth.WindowAt(time.Date(2026, 11, 30, 20, 0, 0, 0, time.FixedZone("CST", -6*3600))).Label)
	for period, label := range map[WindowPeriod]string{WindowMonth: "2026-13", WindowWeek: "2026-W54", WindowDay: "2026-02-30", WindowQuarter: "2026-Q5", WindowYear: "26"} {
		_, err := period.ParseWindow(label)
		assert.ErrorIs(t, err, ErrInvalid, "%s %s", period, label)
	}
	_, err := WindowPeriod("fortnight").ParseWindow("2026-01")
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestWindowPolicyIsFixedWhenTheShelfIsCreated(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	require.NoError(t, shelf.Release())
	reopen := func(policy *WindowPolicy) error {
		cfg := f.cfg
		cfg.Window = policy
		s, err := OpenSQLiteShelf(ctx, cfg)
		if err == nil {
			require.NoError(t, s.Release())
		}
		return err
	}
	assert.NoError(t, reopen(monthly("acme", utc(2026, 10, 1))))
	assert.ErrorIs(t, reopen(&WindowPolicy{Period: WindowYear, Base: "acme", Since: utc(2026, 1, 1)}), ErrInvalid)
	assert.ErrorIs(t, reopen(monthly("other", utc(2026, 10, 1))), ErrInvalid)
	assert.ErrorIs(t, reopen(nil), ErrInvalid, "a windowed shelf opens only with its window")

	var version int
	db, err := sql.Open("sqlite", "file:"+f.path)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT version FROM shelf_meta`).Scan(&version))
	assert.Equal(t, ShelfVersionWindow, version, "a windowed file carries a version older builds refuse")
	require.NoError(t, db.Close())

	plain := newShelfFixture(t)
	require.NoError(t, plain.open(t).Release())
	plain.cfg.Window = monthly("acme", utc(2026, 10, 1))
	_, err = OpenSQLiteShelf(ctx, plain.cfg)
	assert.ErrorIs(t, err, ErrInvalid, "a file created without a window never gains one")

	unaligned := newShelfFixture(t)
	unaligned.cfg.Window = monthly("acme", utc(2026, 10, 15))
	_, err = OpenSQLiteShelf(ctx, unaligned.cfg)
	assert.ErrorIs(t, err, ErrInvalid, "since must be a period's start")
}

func TestWindowedShelfRefusesTheFactsMigrationAndOtherBooks(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	_, err := shelf.Open(ctx, "deal/x")
	assert.ErrorIs(t, err, ErrInvalid, "a windowed shelf holds window books only")
	cadence, err := shelf.Cadence(ctx, CadenceConfig{LogID: "cadence/acme", NoJitter: true, Ended: func(context.Context, string) (bool, error) { return false, nil }})
	require.NoError(t, err)
	assert.ErrorIs(t, cadence.Migrate(ctx, f.clock.Now()), ErrUnsupported)
}

func TestWindowsRollWithNoHole(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	oct, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-10", w.Label)
	assert.Equal(t, "acme/2026-10", oct.id)
	first := oct.logLocked()[0]
	assert.Equal(t, RecordTypeWindowOpen, first.Header.RecordType)
	var open WindowOpenStatement
	require.NoError(t, json.Unmarshal(first.Header.Statement, &open))
	assert.Nil(t, open.Previous, "the first window names no previous one")
	mustAppend(t, oct, observation("deal", "a"))
	mustAppend(t, oct, observation("deal", "b"))

	setClock(f.clock, utc(2027, 1, 5))
	jan, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2027-01", w.Label)
	windows, err := shelf.Windows(ctx)
	require.NoError(t, err)
	var labels []string
	for _, ws := range windows {
		labels = append(labels, ws.Label)
	}
	assert.Equal(t, []string{"2026-10", "2026-11", "2026-12", "2027-01"}, labels, "empty windows get their books")
	for _, ws := range windows[:3] {
		assert.True(t, ws.Closed, ws.Label)
	}
	assert.False(t, windows[3].Closed)

	// October: the close is the last leaf, and its final checkpoint ends there.
	octLog := oct.logLocked()
	last := octLog[len(octLog)-1]
	assert.Equal(t, RecordTypeWindowClose, last.Header.RecordType)
	var closing WindowCloseStatement
	require.NoError(t, json.Unmarshal(last.Header.Statement, &closing))
	assert.Equal(t, 2, closing.CountsByKind["observation"])
	assert.Equal(t, uint64(len(octLog)), windows[0].Final.Entries)
	final, err := oct.Checkpoint(ctx)
	require.NoError(t, err)
	assert.Equal(t, windows[0].Final.Root, final.Root, "a closed book gives its final checkpoint")
	assert.Equal(t, uint64(len(octLog)), oct.Size(), "and appends nothing for it")
	_, err = oct.Append(ctx, observation("deal", "late"))
	assert.ErrorIs(t, err, ErrWindowClosed)

	// November is empty: its open and its close.
	nov, _, err := shelf.WindowBook(ctx, "2026-11")
	require.NoError(t, err)
	novLog := nov.logLocked()
	assert.Equal(t, RecordTypeWindowOpen, novLog[0].Header.RecordType)
	assert.Equal(t, RecordTypeWindowClose, novLog[len(novLog)-1].Header.RecordType)
	for _, record := range novLog[1 : len(novLog)-1] {
		assert.Equal(t, RecordTypeIndexRoot, record.Header.RecordType, "an empty window holds only its own records")
	}
	require.NoError(t, json.Unmarshal(novLog[0].Header.Statement, &open))
	assert.Equal(t, &WindowTree{LogID: "acme/2026-10", Entries: windows[0].Final.Entries, Root: windows[0].Final.Root}, open.Previous)

	// January takes records; the clock stepping back over the boundary is refused.
	mustAppend(t, jan, observation("deal", "c"))
	setClock(f.clock, utc(2026, 12, 20))
	_, _, err = shelf.Current(ctx)
	assert.ErrorIs(t, err, ErrClockBehind)
	_, err = jan.Append(ctx, observation("deal", "d"))
	assert.ErrorIs(t, err, ErrWindowClosed, "a record is placed by its commit time")
}

func TestWindowRecordsAreTheShelfsOwn(t *testing.T) {
	ctx := context.Background()
	shelf := newWindowFixture(t).open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	for _, recordType := range []string{RecordTypeWindowOpen, RecordTypeWindowClose} {
		_, err := oct.Append(ctx, Entry{RecordType: recordType, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{}`)})
		assert.ErrorIs(t, err, ErrInvalid, recordType)
	}
	plain := newFixture(t, "plain", 3).open(t)
	_, err = plain.Append(ctx, Entry{RecordType: RecordTypeWindowOpen, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{}`)})
	assert.ErrorIs(t, err, ErrInvalid, "only a windowed shelf writes window records")
}

func TestARecordOutsideItsWindowIsRefused(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 11, 1))
	_, err = oct.Append(ctx, observation("deal", "late"))
	assert.ErrorIs(t, err, ErrWindowClosed, "not yet rolled, but past its window")
	nov, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-11", w.Label)
	mustAppend(t, nov, observation("deal", "on time"))
}

func TestARollACrashInterruptedIsFinished(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	mustAppend(t, oct, observation("deal", "a"))
	// A crash after the close record, before the final checkpoint.
	setClock(f.clock, utc(2026, 11, 2))
	_, err = oct.Checkpoint(ctx)
	require.NoError(t, err)
	_, err = oct.appendWindowRecord(ctx, RecordTypeWindowClose, WindowCloseStatement{
		RecordVersion: WindowCloseVersion, Period: WindowMonth, Label: "2026-10",
		Start: "2026-10-01T00:00:00Z", End: "2026-11-01T00:00:00Z", CountsByKind: map[string]int{},
	})
	require.NoError(t, err)
	size := oct.Size()
	require.NoError(t, shelf.Release())

	shelf = f.open(t)
	_, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-11", w.Label)
	windows, err := shelf.Windows(ctx)
	require.NoError(t, err)
	require.True(t, windows[0].Closed)
	assert.Equal(t, size, windows[0].Final.Entries, "the close was not written twice")

	// A crash after November's row, before its book's open record.
	setClock(f.clock, utc(2026, 12, 2))
	_, err = shelf.db.Exec(`UPDATE shelf_window_books SET state = 'open', final_entries = NULL, final_root = NULL WHERE label = '2026-11'`)
	require.NoError(t, err)
	require.NoError(t, shelf.Release())
	shelf = f.open(t)
	_, w, err = shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-12", w.Label)
	chain, err := shelf.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	_, findings := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: shelfCheckpointKey(f)})
	assert.Empty(t, findings)
}

func TestTheDisclosurePolicyAndPacksCarryAcrossWindows(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	id, err := shelf.RegisterDerivation(DerivationDefinition{
		Document: []byte(packDocument), Recipients: []string{"requester-1"}, Proofs: true,
		Forms: []SubjectKind{SubjectFullHistory}, Fields: []string{"record_type", "committed_at"},
	})
	require.NoError(t, err)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	adopted, _, err := oct.AdoptPolicy(ctx, "person")
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 12, 3))
	dec, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	carried, err := dec.DisclosurePolicy()
	require.NoError(t, err)
	assert.Equal(t, adopted, carried)
	dec.mu.Lock()
	_, ok := dec.derivations[id]
	dec.mu.Unlock()
	assert.True(t, ok, "a pack registered on the shelf is answerable in a later window")
	assert.Same(t, oct.unsignedAsks, dec.unsignedAsks, "one unsigned-ask limit across windows")
}

func shelfCheckpointKey(f *shelfFixture) string {
	return hex.EncodeToString(f.cfg.CheckpointKey[32:])
}

// windowReport is a holder with records in October and November 2026 and
// an empty December, its clock in January, and a requester book.
type windowReport struct {
	f         *shelfFixture
	holder    *Shelf
	requester *Book
	pack      string
	keys      ResponderKeys
}

func newWindowReport(t *testing.T) windowReport {
	t.Helper()
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	pack, err := shelf.RegisterDerivation(DerivationDefinition{
		Document: []byte(packDocument), Recipients: []string{"requester-1"}, Proofs: true,
		Forms: []SubjectKind{SubjectFullHistory}, Fields: []string{"record_type", "committed_at"},
	})
	require.NoError(t, err)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	mustAppend(t, oct, observation("deal", "a"))
	mustAppend(t, oct, Entry{RecordType: "claim", EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"c":1}`)})
	setClock(f.clock, utc(2026, 11, 10))
	nov, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	mustAppend(t, nov, Entry{RecordType: "ruling", EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"r":1}`)})
	setClock(f.clock, utc(2027, 1, 2))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)
	return windowReport{
		f: f, holder: shelf, requester: newFixture(t, "requester", 30).open(t), pack: pack,
		keys: ResponderKeys{Signer: KeyID(f.cfg.Signer), Checkpoint: shelfCheckpointKey(f)},
	}
}

// answer asks the holder for the pack over one window, pinned to its final
// checkpoint, and records the answer.
func (r windowReport) answer(t *testing.T, label string, n int) Record {
	t.Helper()
	ctx := context.Background()
	book, _, err := r.holder.WindowBook(ctx, label)
	require.NoError(t, err)
	final, err := book.Checkpoint(ctx)
	require.NoError(t, err)
	sent, err := r.requester.Request(ctx, EvidenceRequest{
		Subject: Subject{Kind: SubjectFullHistory}, Derivation: r.pack,
		Coverage: Coverage{ExpectedPin: anchorDigest(final)}, Nonce: fmt.Sprintf("%032x", n),
	}, "acme")
	require.NoError(t, err)
	reply, _, err := r.holder.Respond(ctx, sent.Message(), RespondOptions{AuthenticatedRequester: "requester-1", Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	recorded, err := r.requester.RecordResponse(ctx, sent.RecordID, reply.Message, r.keys)
	require.NoError(t, err)
	var statement ResponseStatement
	require.NoError(t, json.Unmarshal(recorded.Header.Statement, &statement))
	require.Equal(t, OutcomeArtifact, statement.Outcome, statement.Failure)
	return recorded
}

// evaluateWindows is a one-claim Result citing every answer.
func evaluateWindows(responses []string) func([]WindowInputs) (json.RawMessage, error) {
	return func(inputs []WindowInputs) (json.RawMessage, error) {
		var evidence []any
		for _, id := range responses {
			evidence = append(evidence, map[string]any{"digest": id, "digest_alg": "SHA-256"})
		}
		rows := 0
		for _, in := range inputs {
			rows += len(in.Records)
		}
		claim := map[string]any{
			"id": "has-records", "contract_ref": packContract, "requirement_ref": "has-observation", "tier": "recomputed", "grade": "self-attested",
			"sufficiency": "SATISFIED", "verdict": map[bool]string{true: "met", false: "not_met"}[rows > 0], "evidence": evidence, "proofs": []any{},
			"presentation": map[string]any{"kind": "disclosure", "status": "SATISFIED", "evidence": evidence},
		}
		verdict := claim["verdict"].(string)
		buckets := map[string]any{"met": []any{}, "not_met": []any{}, "not_evaluable": []any{}}
		buckets[verdict] = []any{"has-records"}
		return json.Marshal(map[string]any{
			"result_version": "evidence-result-v0", "generated_at": "2027-01-02T00:00:00Z", "claims": []any{claim},
			"aggregate": map[string]any{"buckets": buckets, "coverage": map[string]any{"evaluated_population": 1, "excluded_not_applicable": 0, "unknown_count": 0}},
		})
	}
}

func TestAReportOverWindowsCoversItsRange(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	chain, err := r.holder.WindowChain(ctx, "2026-10", "2026-12", WindowChainOptions{})
	require.NoError(t, err)
	verified, findings := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	require.Empty(t, findings)
	require.Len(t, verified, 3)

	chainRecord, err := r.requester.RecordWindowChain(ctx, chain, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	require.NoError(t, err)
	jan, _, err := r.holder.WindowBook(ctx, "2027-01")
	require.NoError(t, err)
	janBefore := jan.Size()
	closedSizes := map[string]uint64{}
	var responses []string
	for i, label := range []string{"2026-10", "2026-11", "2026-12"} {
		book, _, err := r.holder.WindowBook(ctx, label)
		require.NoError(t, err)
		closedSizes[label] = book.Size()
		responses = append(responses, r.answer(t, label, i+1).RecordID)
		assert.Equal(t, closedSizes[label], book.Size(), "a closed window answers and is not written")
	}
	assert.Equal(t, janBefore+3, jan.Size(), "each answer is recorded in the window open now")

	report, err := r.requester.ReportFromWindows(ctx, chainRecord.RecordID, responses, evaluateWindows(responses),
		ReportOptions{Derivation: r.pack}, VerifyChainOptions{})
	require.NoError(t, err)
	coverage, err := VerifyReportCoverage(report.Bundle.JSON, CoverageOptions{})
	require.NoError(t, err)
	assert.Empty(t, coverage.Findings)
	assert.Equal(t, "2026-10", coverage.From)
	assert.Equal(t, "2026-12", coverage.To)
	require.Len(t, coverage.Windows, 3)
	assert.Equal(t, "2026-11", coverage.Windows[1].Label)

	// A window without its answer is named, and no Result is sealed over it.
	_, err = r.requester.ReportFromWindows(ctx, chainRecord.RecordID, []string{responses[0], responses[2]}, evaluateWindows([]string{responses[0], responses[2]}),
		ReportOptions{Derivation: r.pack}, VerifyChainOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "window 2026-11 has no answer")
	// The same answer twice is named too.
	_, err = r.requester.ReportFromWindows(ctx, chainRecord.RecordID, []string{responses[0], responses[1], responses[1], responses[2]}, evaluateWindows(responses),
		ReportOptions{Derivation: r.pack}, VerifyChainOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "window 2026-11 has 2 answers")
	// A claim citing something else is refused.
	_, err = r.requester.ReportFromWindows(ctx, chainRecord.RecordID, responses, evaluateWindows(append([]string{chainRecord.RecordID}, responses[:1]...)[:1]),
		ReportOptions{Derivation: r.pack}, VerifyChainOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cites none of the answers")
}

func TestCoverageNamesAnAnswerNotAtItsFinalTree(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	chain, err := r.holder.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	payload, err := canonicalJSON(chain)
	require.NoError(t, err)
	body, err := canonicalJSON(WindowChainStatement{RecordVersion: WindowChainRecordVersion, Base: "acme", Period: WindowMonth, From: "2026-10", To: "2026-12", ChainDigest: digestBytes(payload)})
	require.NoError(t, err)
	verified, _ := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	oct := verified[0].Final
	early := oct
	early.Entries--
	_, findings, err := coverage(Header{Statement: body}, payload, []windowAnswer{
		{key: r.keys.Checkpoint, tree: early},
		{key: "another", tree: verified[1].Final},
	}, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	require.NoError(t, err)
	var texts []string
	for _, f := range findings {
		texts = append(texts, f.Text)
	}
	assert.Contains(t, texts, "windows do not tile 2026-10..2026-12: the chain covers 2026-10..2026-11")
	assert.Contains(t, texts, fmt.Sprintf("window 2026-10's answer is anchored at a tree of %d records, not its final tree of %d", early.Entries, oct.Entries))
	assert.Contains(t, texts, "window 2026-10 has no answer")
	assert.Contains(t, texts, "an answer at acme/2026-11 is under another holder checkpoint key than the others")
}

func TestAWindowChainNamesEachBreak(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	chain, err := r.holder.WindowChain(ctx, "2026-10", "2026-12", WindowChainOptions{})
	require.NoError(t, err)
	opts := VerifyChainOptions{CheckpointKey: r.keys.Checkpoint}
	texts := func(findings []ChainFinding) []string {
		var out []string
		for _, f := range findings {
			out = append(out, f.Text)
		}
		return out
	}

	dropped := chain
	dropped.Windows = []ChainWindow{chain.Windows[0], chain.Windows[2]}
	_, findings := VerifyWindowChain(dropped, opts)
	assert.Contains(t, texts(findings), "chain breaks at 2026-11: the next window carried is 2026-12")

	short := chain
	short.Windows = chain.Windows[:2]
	_, findings = VerifyWindowChain(short, opts)
	assert.Contains(t, texts(findings), "chain breaks at 2026-12: the chain ends before 2026-12")

	swapped := chain
	swapped.Windows = append([]ChainWindow{}, chain.Windows...)
	swapped.Windows[1].Close, swapped.Windows[1].CloseProof = chain.Windows[0].Close, chain.Windows[0].CloseProof
	_, findings = VerifyWindowChain(swapped, opts)
	require.NotEmpty(t, findings)
	assert.Equal(t, "2026-11", findings[0].Window)

	_, findings = VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: "00"})
	require.NotEmpty(t, findings)
	assert.Contains(t, findings[0].Text, "window 2026-10's final checkpoint")

	_, findings = VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint, Witnesses: []WitnessRow{{Endpoint: "https://witness.example"}}})
	assert.Contains(t, texts(findings), "window 2026-10 not witnessed: its final checkpoint carries no receipt")

	// A second shelf under the same base and keys, begun in November: its
	// November names no previous window, so joining it to October's chain
	// breaks there as a new chain.
	other := newShelfFixture(t)
	other.cfg.Signer, other.cfg.CheckpointKey = r.f.cfg.Signer, r.f.cfg.CheckpointKey
	other.cfg.Window = monthly("acme", utc(2026, 11, 1))
	setClock(other.clock, utc(2026, 11, 5))
	restarted := other.open(t)
	setClock(other.clock, utc(2026, 12, 2))
	_, err = restarted.Roll(ctx)
	require.NoError(t, err)
	theirs, err := restarted.WindowChain(ctx, "2026-11", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	joined := chain
	joined.Windows = []ChainWindow{chain.Windows[0], theirs.Windows[0], chain.Windows[2]}
	_, findings = VerifyWindowChain(joined, opts)
	assert.Contains(t, texts(findings), "chain breaks at 2026-11: a new chain began")
}

func TestShelfRespondRoutesByPin(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	// A freshness floor is answered by the current window.
	sent, err := r.requester.Request(ctx, EvidenceRequest{
		Subject: Subject{Kind: SubjectFullHistory}, Derivation: r.pack,
		Coverage: Coverage{MinFreshness: &Freshness{Size: ptr(uint64(1))}}, Nonce: fmt.Sprintf("%032x", 99),
	}, "acme")
	require.NoError(t, err)
	reply, _, err := r.holder.Respond(ctx, sent.Message(), RespondOptions{AuthenticatedRequester: "requester-1", Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	recorded, err := r.requester.RecordResponse(ctx, sent.RecordID, reply.Message, r.keys)
	require.NoError(t, err)
	answer, err := readWindowAnswer(recorded.Header, reply.Message.Octets)
	require.NoError(t, err)
	assert.Equal(t, "acme/2027-01", answer.tree.LogID)

	// A pin no window issued is refused as any book refuses it.
	sent, err = r.requester.Request(ctx, EvidenceRequest{
		Subject: Subject{Kind: SubjectFullHistory}, Derivation: r.pack,
		Coverage: Coverage{ExpectedPin: digestBytes([]byte("nothing"))}, Nonce: fmt.Sprintf("%032x", 98),
	}, "acme")
	require.NoError(t, err)
	reply, _, err = r.holder.Respond(ctx, sent.Message(), RespondOptions{AuthenticatedRequester: "requester-1", Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	require.NotNil(t, reply.Refusal)
}

func ptr[T any](v T) *T { return &v }

func TestShelfRespondRoutesAHalfPinToTheBookCitingIt(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	half := digestBytes([]byte("the requester's half"))
	// October's book, closed since, holds the record citing the half.
	f := newWindowFixture(t)
	shelf := f.open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	mustAppend(t, oct, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: half}}})
	setClock(f.clock, utc(2026, 12, 2))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)
	sent, err := r.requester.Request(ctx, EvidenceRequest{
		Subject: Subject{Kind: SubjectExchange, Digest: half}, Coverage: Coverage{ExpectedPin: half}, Nonce: fmt.Sprintf("%032x", 7),
	}, "acme")
	require.NoError(t, err)
	shelf.mu.Lock()
	book, err := shelf.answeringBookLocked(ctx, sent.Message())
	shelf.mu.Unlock()
	require.NoError(t, err)
	require.NotNil(t, book)
	assert.Equal(t, "acme/2026-10", book.id)
}

func TestAPastWindowAnswersUnderThePolicyInForceNow(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	oct, _, err := r.holder.WindowBook(ctx, "2026-10")
	require.NoError(t, err)
	final, err := oct.Checkpoint(ctx)
	require.NoError(t, err)
	// October held no policy; January, the window open now, adopts one
	// that limits unsigned askers to three a week.
	jan, _, err := r.holder.Current(ctx)
	require.NoError(t, err)
	_, _, err = jan.AdoptPolicy(ctx, HolderPerson)
	require.NoError(t, err)
	ask := func() string {
		req := EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: digestBytes([]byte("x"))}, Coverage: Coverage{ExpectedPin: anchorDigest(final)}}
		decided := ""
		reply, _, err := r.holder.Respond(ctx, jsonRequest(t, req), RespondOptions{Policy: DefaultSharePolicy(), OnRefusal: func(_, d, _ string) { decided = d }})
		require.NoError(t, err)
		require.NotNil(t, reply.Refusal)
		return decided
	}
	for i := range 3 {
		assert.Equal(t, ReasonNotAuthorized, ask(), "ask %d", i+1)
	}
	assert.NotEqual(t, ReasonNotAuthorized, ask(), "January's limit decides an answer from October")
}

func TestOnlyACheckpointCommitsAnIndexRootOutsideTheWindow(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	oct, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 11, 1))
	_, err = oct.Append(ctx, Entry{RecordType: RecordTypeIndexRoot, EpistemicType: DerivedMetric, Statement: json.RawMessage(`{}`)})
	assert.ErrorIs(t, err, ErrWindowClosed)
}

func TestAnEmptyChainRangeIsAFinding(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	chain, err := r.holder.WindowChain(ctx, "2026-10", "2026-10", WindowChainOptions{})
	require.NoError(t, err)
	chain.From, chain.To, chain.Windows = "2026-12", "2026-10", nil
	verified, findings := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	assert.Empty(t, verified)
	require.NotEmpty(t, findings)
	assert.Equal(t, "the chain's range 2026-12..2026-10 is empty", findings[0].Text)
	_, err = r.requester.RecordWindowChain(ctx, chain, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestCoverageNamesForeignDuplicateAndMixedAnswers(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	chain, err := r.holder.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	payload, err := canonicalJSON(chain)
	require.NoError(t, err)
	body, err := canonicalJSON(WindowChainStatement{RecordVersion: WindowChainRecordVersion, Base: "acme", Period: WindowMonth, From: "2026-10", To: "2026-11", ChainDigest: digestBytes(payload)})
	require.NoError(t, err)
	verified, _ := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	key := r.keys.Checkpoint
	_, findings, err := coverage(Header{Statement: body}, payload, []windowAnswer{
		{key: key, tree: verified[0].Final, derivation: r.pack},
		{key: key, tree: verified[1].Final, derivation: r.pack},
		{key: key, tree: verified[1].Final, derivation: digestBytes([]byte("another pack"))},
		{key: key, tree: WindowTree{LogID: "elsewhere/2026-11", Entries: 3, Root: "00"}, derivation: r.pack},
	}, VerifyChainOptions{CheckpointKey: key})
	require.NoError(t, err)
	var texts []string
	for _, f := range findings {
		texts = append(texts, f.Text)
	}
	assert.Contains(t, texts, "window 2026-11 has 2 answers")
	assert.Contains(t, texts, "an answer anchored in log elsewhere/2026-11 is outside the chain's windows")
	assert.Contains(t, texts, fmt.Sprintf("an answer at acme/2026-11 answers derivation %s, not %s as the others", digestBytes([]byte("another pack")), r.pack))

	_, _, err = coverage(Header{Statement: body}, append([]byte(" "), payload...), nil, VerifyChainOptions{CheckpointKey: key})
	assert.ErrorIs(t, err, ErrInvalid, "a payload that is not the chain the record names")
}

func TestAChainNamesAPreviousTreeOtherThanItsNeighbours(t *testing.T) {
	ctx := context.Background()
	r := newWindowReport(t)
	chain, err := r.holder.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	// A second shelf under the same base, keys and start, with other
	// records in October: its November names its own October.
	other := newShelfFixture(t)
	other.cfg.Signer, other.cfg.CheckpointKey = r.f.cfg.Signer, r.f.cfg.CheckpointKey
	other.cfg.Window = monthly("acme", utc(2026, 10, 1))
	forked := other.open(t)
	oct, _, err := forked.Current(ctx)
	require.NoError(t, err)
	mustAppend(t, oct, observation("deal", "another"))
	setClock(other.clock, utc(2026, 12, 2))
	_, err = forked.Roll(ctx)
	require.NoError(t, err)
	theirs, err := forked.WindowChain(ctx, "2026-11", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	joined := chain
	joined.Windows = []ChainWindow{chain.Windows[0], theirs.Windows[0]}
	_, findings := VerifyWindowChain(joined, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	require.NotEmpty(t, findings)
	assert.Equal(t, "chain breaks at 2026-11: its open record names a previous tree other than window 2026-10's final tree", findings[0].Text)
}

func TestAWindowWhoseOpenRecordACrashLostGetsIt(t *testing.T) {
	ctx := context.Background()
	f := newWindowFixture(t)
	shelf := f.open(t)
	_, _, err := shelf.Current(ctx)
	require.NoError(t, err)
	setClock(f.clock, utc(2026, 11, 3))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)
	// November closes and December's row is written, then the process
	// stops before December's book gets its open record.
	setClock(f.clock, utc(2026, 12, 3))
	shelf.mu.Lock()
	rows, err := shelf.windowRowsLocked(ctx)
	require.NoError(t, err)
	_, err = shelf.closeWindowLocked(ctx, rows[1])
	require.NoError(t, err)
	_, err = shelf.db.Exec(`INSERT INTO shelf_window_books (label, log_id, start, state) VALUES ('2026-12', 'acme/2026-12', '2026-12-01T00:00:00Z', 'open')`)
	require.NoError(t, err)
	shelf.mu.Unlock()
	require.NoError(t, shelf.Release())

	shelf = f.open(t)
	dec, w, err := shelf.Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2026-12", w.Label)
	first := dec.logLocked()[0]
	require.Equal(t, RecordTypeWindowOpen, first.Header.RecordType)
	var open WindowOpenStatement
	require.NoError(t, json.Unmarshal(first.Header.Statement, &open))
	windows, err := shelf.Windows(ctx)
	require.NoError(t, err)
	assert.Equal(t, windows[1].Final, open.Previous)
}

func TestAPolicyNotStartedYetSaysSo(t *testing.T) {
	f := newShelfFixture(t)
	f.cfg.Window = monthly("acme", utc(2027, 1, 1))
	_, _, err := f.open(t).Current(context.Background())
	require.ErrorIs(t, err, ErrClockBehind)
	assert.Contains(t, err.Error(), "the window policy starts at 2027-01-01T00:00:00Z")
}
