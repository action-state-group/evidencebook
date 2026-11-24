// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A shelf whose windows are its journal (evidencebook#797,
// docs/design/797-deal-window.md).

func journalMonthly(since time.Time) *WindowPolicy {
	return &WindowPolicy{Period: WindowMonth, Base: JournalLogID, Since: since, Journal: true}
}

// newJournalWindowFixture is a shelf fixture whose journal is windowed by
// month from October 2026, its clock on 2026-10-19.
func newJournalWindowFixture(t *testing.T) *shelfFixture {
	t.Helper()
	f := newShelfFixture(t)
	f.cfg.Window = journalMonthly(utc(2026, 10, 1))
	return f
}

func TestAJournalWindowPolicyIsFixedAndCreatedOnFacts(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []*WindowPolicy{
		{Period: WindowMonth, Base: "acme", Since: utc(2026, 10, 1), Journal: true},
		{Period: WindowMonth, Base: JournalLogID, Since: utc(2026, 10, 1), Journal: true, Grace: 24 * time.Hour},
	} {
		f := newShelfFixture(t)
		f.cfg.Window = bad
		_, err := OpenSQLiteShelf(ctx, f.cfg)
		assert.ErrorIs(t, err, ErrInvalid, "base %q grace %s", bad.Base, bad.Grace)
	}

	f := newJournalWindowFixture(t)
	shelf := f.open(t)
	var version int64
	require.NoError(t, shelf.db.QueryRowContext(ctx, `SELECT version FROM shelf_meta WHERE id = 1`).Scan(&version))
	assert.Equal(t, int64(ShelfVersionWindowJournal), version)
	cadence, err := shelf.Cadence(ctx, CadenceConfig{LogID: "cadence/acme", NoJitter: true, Ended: shelf.WindowEnded})
	require.NoError(t, err)
	migrated, err := cadence.Migrated(ctx)
	require.NoError(t, err)
	assert.True(t, migrated, "created on book facts")
	assert.NoError(t, cadence.Migrate(ctx, f.clock.Now()), "nothing to migrate")
	require.NoError(t, shelf.Release())

	stored, err := ReadShelfWindow(ctx, f.path)
	require.NoError(t, err)
	assert.True(t, stored.Journal)
	cfg := f.cfg
	cfg.Window = &WindowPolicy{Period: WindowMonth, Base: JournalLogID, Since: utc(2026, 10, 1)}
	_, err = OpenSQLiteShelf(ctx, cfg)
	assert.ErrorIs(t, err, ErrInvalid, "reopened without its journal flag")
	cfg.Window = stored
	reopened, err := OpenSQLiteShelf(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, reopened.Release())

	// A #689 windowed shelf still refuses the facts migration.
	g := newWindowFixture(t)
	other := g.open(t)
	c, err := other.Cadence(ctx, CadenceConfig{LogID: "cadence/acme", NoJitter: true, Ended: func(context.Context, string) (bool, error) { return false, nil }})
	require.NoError(t, err)
	assert.ErrorIs(t, c.Migrate(ctx, g.clock.Now()), ErrUnsupported)
}

func TestTheJournalIsReadInOrderAcrossItsWindows(t *testing.T) {
	ctx := context.Background()
	f := newJournalWindowFixture(t)
	shelf := f.open(t)

	oct, err := shelf.Journal(ctx)
	require.NoError(t, err)
	assert.Equal(t, "journal/2026-10", oct.id)
	a := mustAppend(t, oct, observation("deal", "a"))

	setClock(f.clock, utc(2026, 12, 3))
	dec, err := shelf.Journal(ctx)
	require.NoError(t, err)
	assert.Equal(t, "journal/2026-12", dec.id, "the journal is the current window's book")
	mustAppend(t, dec, observation("deal", "c"))

	_, err = shelf.Open(ctx, "journal/2026-10")
	assert.ErrorIs(t, err, ErrInvalid, "a journal window opens only as the journal")
	_, err = shelf.ExistingJournal(ctx)
	assert.ErrorIs(t, err, ErrInvalid)
	books, err := shelf.Books(ctx)
	require.NoError(t, err)
	assert.Empty(t, books, "journal windows count among no books")

	view, err := shelf.JournalView(ctx)
	require.NoError(t, err)
	octSize, decSize := oct.Size(), dec.Size()
	nov, _, err := shelf.WindowBook(ctx, "2026-11")
	require.NoError(t, err)
	assert.Equal(t, octSize+nov.Size()+decSize, view.Size(), "one seq space over three windows, one empty")
	all, err := view.Query(ctx, Filter{FromSeq: 1, ToSeq: view.Size()})
	require.NoError(t, err)
	require.Len(t, all, int(view.Size()))
	assert.Equal(t, RecordTypeWindowOpen, all[0].Header.RecordType)
	assert.Equal(t, "journal/2026-12", all[len(all)-1].Header.BookID)
	var observations []string
	for _, r := range all {
		if r.Header.RecordType == "observation" {
			observations = append(observations, r.Header.BookID)
		}
	}
	assert.Equal(t, []string{"journal/2026-10", "journal/2026-12"}, observations, "in journal order")

	// A range across a boundary maps onto each window's own seqs.
	cut, err := view.Query(ctx, Filter{FromSeq: octSize, ToSeq: octSize + 1})
	require.NoError(t, err)
	require.Len(t, cut, 2)
	assert.Equal(t, "journal/2026-10", cut[0].Header.BookID)
	assert.Equal(t, "journal/2026-11", cut[1].Header.BookID)
	assert.Equal(t, uint64(1), cut[1].Header.Seq)

	payload, err := view.ResolvePayload(ctx, a.RecordID, a.Header.PayloadCommitments[0])
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), payload, "a payload resolves from a closed window")

	existing, err := shelf.ExistingJournalView(ctx)
	require.NoError(t, err)
	assert.Equal(t, view.Size(), existing.Size())

	ended, err := shelf.WindowEnded(ctx, "journal/2026-10")
	require.NoError(t, err)
	assert.True(t, ended, "a closed window has ended")
	ended, err = shelf.WindowEnded(ctx, "journal/2026-12")
	require.NoError(t, err)
	assert.False(t, ended)
}

func TestAnEmptyJournalViewBeforeTheFirstWindow(t *testing.T) {
	ctx := context.Background()
	shelf := newJournalWindowFixture(t).open(t)
	view, err := shelf.ExistingJournalView(ctx)
	require.NoError(t, err)
	require.NotNil(t, view)
	assert.Equal(t, uint64(0), view.Size())
}

func TestADealBookIsPlacedInTheWindowOfItsAgreement(t *testing.T) {
	ctx := context.Background()
	f := newJournalWindowFixture(t)
	shelf := f.open(t)

	node := mustOpenBook(t, shelf, "goose/node")
	mustAppend(t, node, observation("n", "1"))

	_, _, err := shelf.Place(ctx, "deal/a", utc(2026, 9, 30))
	assert.ErrorIs(t, err, ErrWindowClosed, "an agreement timed before the current window")
	_, _, err = shelf.Place(ctx, "journal/2026-10", f.clock.Now())
	assert.ErrorIs(t, err, ErrInvalid)

	a, w, err := shelf.Place(ctx, "deal/a", f.clock.Now())
	require.NoError(t, err)
	assert.Equal(t, "2026-10", w.Label)
	mustAppend(t, a, observation("deal", "agreement a"))
	placed, ok, err := shelf.Placement(ctx, "deal/a")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "2026-10", placed.Label)
	again, w, err := shelf.Place(ctx, "deal/a", f.clock.Now())
	require.NoError(t, err)
	assert.Same(t, a, again)
	assert.Equal(t, "2026-10", w.Label, "placing twice returns the first placement")

	// deal/b is placed and stays empty (a crash before its agreement).
	_, _, err = shelf.Place(ctx, "deal/b", f.clock.Now())
	require.NoError(t, err)

	setClock(f.clock, utc(2026, 11, 2))
	_, w, err = shelf.Place(ctx, "deal/a", f.clock.Now())
	require.NoError(t, err)
	assert.Equal(t, "2026-10", w.Label, "a placed, written book keeps its window as the deal runs on")
	mustAppend(t, a, observation("deal", "perform a"))
	b, w, err := shelf.Place(ctx, "deal/b", f.clock.Now())
	require.NoError(t, err)
	assert.Equal(t, "2026-11", w.Label, "an empty book is placed again in the current window")
	mustAppend(t, b, observation("deal", "agreement b"))

	setClock(f.clock, utc(2026, 12, 1))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)
	closeOf := func(label string) WindowCloseStatement {
		book, _, err := shelf.WindowBook(ctx, label)
		require.NoError(t, err)
		log := book.logLocked()
		var st WindowCloseStatement
		require.NoError(t, json.Unmarshal(log[len(log)-1].Header.Statement, &st))
		return st
	}
	oct, nov := closeOf("2026-10"), closeOf("2026-11")
	assert.Equal(t, WindowCloseVersionV2, oct.RecordVersion)
	assert.Equal(t, []string{"deal/a"}, oct.Placed, "deal/b was empty when October closed")
	assert.Equal(t, []string{"deal/b"}, nov.Placed)

	chain, err := shelf.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	verified, findings := VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey: shelfCheckpointKey(f)})
	require.Empty(t, findings)
	require.Len(t, verified, 2)
	assert.Equal(t, []string{"deal/a"}, verified[0].Placed)
	assert.Equal(t, []string{"deal/b"}, verified[1].Placed)

	// A placement record is the shelf's own.
	dec, err := shelf.Journal(ctx)
	require.NoError(t, err)
	_, err = dec.Append(ctx, Entry{RecordType: RecordTypeWindowPlace, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{"record_version":"window_place/v1","log_id":"deal/z"}`)})
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = node.Append(ctx, Entry{RecordType: RecordTypeWindowPlace, EpistemicType: ProducerClaim, Statement: json.RawMessage(`{}`)})
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestAPlacementACrashLeftPendingIsFinished(t *testing.T) {
	ctx := context.Background()
	f := newJournalWindowFixture(t)
	shelf := f.open(t)
	_, _, err := shelf.Place(ctx, "deal/a", f.clock.Now())
	require.NoError(t, err)
	// The row as a crash after the record leaves it: pending.
	_, err = shelf.db.ExecContext(ctx, `UPDATE shelf_window_placed SET state = ? WHERE log_id = ?`, placePending, "deal/a")
	require.NoError(t, err)
	_, ok, err := shelf.Placement(ctx, "deal/a")
	require.NoError(t, err)
	assert.False(t, ok)
	journal, err := shelf.Journal(ctx)
	require.NoError(t, err)
	before := journal.Size()
	_, w, err := shelf.Place(ctx, "deal/a", f.clock.Now())
	require.NoError(t, err)
	assert.Equal(t, "2026-10", w.Label)
	assert.Equal(t, before, journal.Size(), "the record already sealed is not sealed twice")
	_, ok, err = shelf.Placement(ctx, "deal/a")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestAJournalWindowsFactsAreJournalFacts(t *testing.T) {
	ctx := context.Background()
	f := newJournalWindowFixture(t)
	shelf := f.open(t)
	journal, err := shelf.Journal(ctx)
	require.NoError(t, err)
	mustAppend(t, journal, observation("deal", "a"))
	book, _, err := shelf.Place(ctx, "deal/a", f.clock.Now())
	require.NoError(t, err)
	mustAppend(t, book, observation("deal", "agreement"))
	var seen []StatusInput
	cadence, err := shelf.Cadence(ctx, CadenceConfig{LogID: "cadence/acme", NoJitter: true, Ended: shelf.WindowEnded,
		Status: recordingStatus(func(in StatusInput) { seen = append(seen, in) })})
	require.NoError(t, err)
	_, err = cadence.Stamp(ctx, f.clock.Now())
	require.NoError(t, err)
	require.Len(t, seen, 1, "only the deal book is described by the provider; the journal window is a journal fact")
	assert.Equal(t, "deal/a", seen[0].LogID)
	assert.Nil(t, seen[0].Journal, "no one journal book on this shelf")
	require.NotNil(t, seen[0].Journals)
	assert.Equal(t, journal.Size(), seen[0].Journals.Size())
	var kind string
	require.NoError(t, shelf.db.QueryRowContext(ctx, `SELECT kind FROM shelf_logs WHERE log_id = ?`, "journal/2026-10").Scan(&kind))
	assert.Equal(t, logKindJournal, kind)
}

// recordingStatus is a BookStatusProvider that records what it is handed.
type recordingStatus func(StatusInput)

func (r recordingStatus) Values(context.Context, string, string) (string, LinkValue, json.RawMessage, bool, error) {
	return "", LinkValue{}, nil, false, nil
}

func (r recordingStatus) BookValues(_ context.Context, in StatusInput) (string, LinkValue, json.RawMessage, bool, error) {
	r(in)
	return "", LinkValue{}, nil, false, nil
}

// answerBook asks the holder for the pack over book, pinned to cp, and
// records the answer.
func (r windowReport) answerBook(t *testing.T, cp Checkpoint, n int) Record {
	t.Helper()
	ctx := context.Background()
	sent, err := r.requester.Request(ctx, EvidenceRequest{
		Subject: Subject{Kind: SubjectFullHistory}, Derivation: r.pack,
		Coverage: Coverage{ExpectedPin: anchorDigest(cp)}, Nonce: fmt.Sprintf("%032x", n),
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

func TestAReportOverJournalWindowsCoversTheirDeals(t *testing.T) {
	ctx := context.Background()
	f := newJournalWindowFixture(t)
	shelf := f.open(t)
	pack, err := shelf.RegisterDerivation(DerivationDefinition{
		Document: []byte(packDocument), Recipients: []string{"requester-1"}, Proofs: true,
		Forms: []SubjectKind{SubjectFullHistory}, Fields: []string{"record_type", "committed_at"},
	})
	require.NoError(t, err)
	journal, err := shelf.Journal(ctx)
	require.NoError(t, err)
	mustAppend(t, journal, observation("deal", "offer a"))
	a, _, err := shelf.Place(ctx, "deal/a", f.clock.Now())
	require.NoError(t, err)
	mustAppend(t, a, observation("deal", "agreement a"))
	setClock(f.clock, utc(2026, 11, 10))
	b, _, err := shelf.Place(ctx, "deal/b", f.clock.Now())
	require.NoError(t, err)
	mustAppend(t, b, observation("deal", "agreement b"))
	setClock(f.clock, utc(2026, 12, 4))
	mustAppend(t, a, observation("deal", "perform a, after its window"))
	_, err = shelf.Roll(ctx)
	require.NoError(t, err)

	r := windowReport{f: f, holder: shelf, requester: newFixture(t, "requester", 30).open(t), pack: pack,
		keys: ResponderKeys{Signer: KeyID(f.cfg.Signer), Checkpoint: shelfCheckpointKey(f)}}
	chain, err := shelf.WindowChain(ctx, "2026-10", "2026-11", WindowChainOptions{})
	require.NoError(t, err)
	chainRecord, err := r.requester.RecordWindowChain(ctx, chain, VerifyChainOptions{CheckpointKey: r.keys.Checkpoint})
	require.NoError(t, err)
	var responses []string
	for i, label := range []string{"2026-10", "2026-11"} {
		responses = append(responses, r.answer(t, label, i+1).RecordID)
	}
	aCP, err := a.Checkpoint(ctx)
	require.NoError(t, err)
	bCP, err := b.Checkpoint(ctx)
	require.NoError(t, err)
	dealA := r.answerBook(t, aCP, 10).RecordID
	dealB := r.answerBook(t, bCP, 11).RecordID

	// Without deal/b's answer the report is refused, naming it.
	partial := append(append([]string{}, responses...), dealA)
	_, err = r.requester.ReportFromWindows(ctx, chainRecord.RecordID, partial, evaluateWindows(partial), ReportOptions{Derivation: pack}, VerifyChainOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deal deal/b of window 2026-11 has no answer")

	all := append(partial, dealB)
	var seen []WindowInputs
	evaluate := func(inputs []WindowInputs) (json.RawMessage, error) {
		seen = inputs
		return evaluateWindows(all)(inputs)
	}
	report, err := r.requester.ReportFromWindows(ctx, chainRecord.RecordID, all, evaluate, ReportOptions{Derivation: pack}, VerifyChainOptions{})
	require.NoError(t, err)
	require.Len(t, seen, 4)
	assert.Equal(t, "deal/a", seen[2].Deal)
	assert.Equal(t, "2026-10", seen[2].Window.Label)
	assert.Empty(t, seen[0].Deal)

	coverage, err := VerifyReportCoverage(report.Bundle.JSON, CoverageOptions{CheckpointKey: r.keys.Checkpoint})
	require.NoError(t, err)
	assert.Empty(t, coverage.Findings)
	assert.Equal(t, ReportLimit, coverage.Limit)
	require.Len(t, coverage.Deals, 2)
	assert.Equal(t, "deal/a", coverage.Deals[0].LogID)
	assert.Equal(t, "2026-10", coverage.Deals[0].Window)
	assert.Equal(t, aCP.Entries, coverage.Deals[0].Through.Entries, "deal/a's answer covers its perform after the window")
	assert.Contains(t, coverage.Deals[0].Line, fmt.Sprintf("deal deal/a, placed in window 2026-10, answered over its whole book, through its checkpoint of %d records issued 2026-12-04", aCP.Entries))
	assert.Contains(t, coverage.Deals[0].Line, "records after the window included")
	assert.Equal(t, "deal/b", coverage.Deals[1].LogID)
}
