// SPDX-License-Identifier: Apache-2.0

package book

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answerPair is two JSONL books that exchanged on day0, and b's Close for
// that day naming a, disclosed in b's Close bundle.
type answerPair struct {
	a, b         cli.Profile
	aKeys, bKeys bookKeys
	bundle       string
	bClose       string
	set          func(time.Time)
}

func newAnswerPair(t *testing.T) answerPair {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0.Add(-48*time.Hour))
	a, aKeys := bookProfile(t, "a")
	b, bKeys := bookProfile(t, "b")
	appendHalves(t, a, half{"x0", "r0", "p0"})
	appendHalves(t, b, half{"x0", "r0", "p0"})
	set(day0)
	appendHalves(t, a, half{"x1", "r1", "p1"})
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(day1)
	path := filepath.Join(t.TempDir(), "b-close-bundle.json")
	closed, err := runClose(t, "--profile", "b", "--period", "day", "--date", "2026-09-24", "--counterparty", "a", "--bundle-out", path)
	require.NoError(t, err)
	return answerPair{a: a, b: b, aKeys: aKeys, bKeys: bKeys, bundle: path, bClose: closed.RecordID, set: set}
}

func (p answerPair) args(verb string, extra ...string) []string {
	return append([]string{verb, "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--peer", p.bundle, "--peer-checkpoint-key", hex.EncodeToString(p.bKeys.checkpoint)}, extra...)
}

func runAnswer(t *testing.T, args ...string) (answerResult, error) {
	t.Helper()
	out, err := invoke(t, "", args...)
	var result answerResult
	if err == nil {
		require.NoError(t, json.Unmarshal([]byte(out), &result), out)
	}
	return result, err
}

// statusOf is b's Close's status as b reads it from a's bundle.
func (p answerPair) statusOf(t *testing.T) evidencebook.CloseStatus {
	t.Helper()
	status, err := evidencebook.StatusOfClose(p.bClose, "a", hex.EncodeToString(p.aKeys.checkpoint), fullBundle(t, p.a))
	require.NoError(t, err)
	return status
}

func TestAcknowledgeMakesThePeerCloseAgreed(t *testing.T) {
	p := newAnswerPair(t)
	assert.Equal(t, evidencebook.Unilateral, p.statusOf(t))

	acked, err := runAnswer(t, p.args("acknowledge")...)
	require.NoError(t, err)
	assert.Equal(t, evidencebook.RecordTypeAcknowledgement, acked.RecordType)
	assert.Equal(t, p.bClose, acked.PeerClose)
	assert.Equal(t, "a", acked.Book)
	assert.Equal(t, "day:2026-09-24", acked.Period)
	assert.False(t, acked.AlreadyRecorded)
	withBook(t, p.a, func(book *evidencebook.Book) {
		record, err := book.Get(t.Context(), acked.RecordID)
		require.NoError(t, err)
		assert.Equal(t, []evidencebook.Link{{Type: evidencebook.Acknowledges, Target: p.bClose}}, record.Header.Links)
		assert.Equal(t, "b", record.Header.CounterpartyRef)
		covered, err := book.CheckpointCovers(t.Context(), record.Seq)
		require.NoError(t, err)
		assert.True(t, covered, "a checkpoint covers it, so b can count it")
	})
	assert.Equal(t, evidencebook.Agreed, p.statusOf(t))

	again, err := runAnswer(t, p.args("acknowledge")...)
	require.NoError(t, err)
	assert.True(t, again.AlreadyRecorded)
	assert.Equal(t, acked.RecordID, again.RecordID, "a repeat adds nothing")

	// A later dispute: a rebuttal outranks the acknowledgement.
	rebutted, err := runAnswer(t, p.args("rebut", "--reason", "x1 differs on our side")...)
	require.NoError(t, err)
	assert.Equal(t, evidencebook.Contested, p.statusOf(t))
	withBook(t, p.a, func(book *evidencebook.Book) {
		record, err := book.Get(t.Context(), rebutted.RecordID)
		require.NoError(t, err)
		assert.JSONEq(t, `{"reason":"x1 differs on our side"}`, string(record.Header.Statement))
		assert.Equal(t, []evidencebook.Link{{Type: evidencebook.Rebuts, Target: p.bClose}}, record.Header.Links)
	})
}

func TestRebutRefusesAcknowledgingAfterwardsAndAReasonChange(t *testing.T) {
	p := newAnswerPair(t)
	_, err := runAnswer(t, p.args("rebut")...)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "--reason is required")
	_, err = runAnswer(t, p.args("rebut", "--reason", "  ")...)
	require.ErrorIs(t, err, cli.ErrInput)

	rebutted, err := runAnswer(t, p.args("rebut", "--reason", "x1 is not ours")...)
	require.NoError(t, err)
	assert.Equal(t, evidencebook.RecordTypeRebuttal, rebutted.RecordType)
	assert.Equal(t, evidencebook.Contested, p.statusOf(t))

	again, err := runAnswer(t, p.args("rebut", "--reason", "x1 is not ours")...)
	require.NoError(t, err)
	assert.True(t, again.AlreadyRecorded)
	assert.Equal(t, rebutted.RecordID, again.RecordID)

	_, err = runAnswer(t, p.args("rebut", "--reason", "another reason")...)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "already rebutted Close "+p.bClose+" at position")
	assert.Contains(t, err.Error(), `with: "x1 is not ours". That rebuttal is sealed and is not replaced`)

	_, err = runAnswer(t, p.args("acknowledge")...)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "already rebutted Close "+p.bClose)
	assert.Equal(t, evidencebook.Contested, p.statusOf(t))
}

func TestAcknowledgeRefusesWhatIsNotTheCounterpartysClose(t *testing.T) {
	p := newAnswerPair(t)
	size := bookSize(t, p.a)
	other, otherKeys := bookProfile(t, "c")
	appendHalves(t, other, half{"x9", "r9", "p9"})
	otherBundle := filepath.Join(t.TempDir(), "c.json")
	p.set(day1.AddDate(0, 0, 1))
	_, err := runClose(t, "--profile", "c", "--period", "day", "--date", "2026-09-25", "--counterparty", "a", "--bundle-out", otherBundle)
	require.NoError(t, err)

	for name, args := range map[string][]string{
		"no peer bundle":          {"acknowledge", "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b"},
		"no pinned key":           {"acknowledge", "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--peer", p.bundle},
		"another key":             {"acknowledge", "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--peer", p.bundle, "--peer-checkpoint-key", hex.EncodeToString(otherKeys.checkpoint)},
		"another book's bundle":   {"acknowledge", "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--peer", otherBundle, "--peer-checkpoint-key", hex.EncodeToString(otherKeys.checkpoint)},
		"no Close for the period": {"acknowledge", "--profile", "a", "--period", "day", "--date", "2026-09-23", "--counterparty", "b", "--peer", p.bundle, "--peer-checkpoint-key", hex.EncodeToString(p.bKeys.checkpoint)},
	} {
		_, err := runAnswer(t, args...)
		require.ErrorIs(t, err, cli.ErrInput, name)
	}
	_, err = runAnswer(t, "acknowledge", "--profile", "a", "--period", "day", "--date", "2026-09-23", "--counterparty", "b", "--peer", p.bundle, "--peer-checkpoint-key", hex.EncodeToString(p.bKeys.checkpoint))
	assert.Contains(t, err.Error(), "discloses no Close of b's for day:2026-09-23 naming this book (a)")
	assert.Equal(t, size, bookSize(t, p.a), "nothing was written")
}

// An acknowledgement is a receipt of the Close: the bundle need not disclose
// the record the Close names as its head, which only a reconcile needs.
func TestAcknowledgeNeedsOnlyTheClose(t *testing.T) {
	p := newAnswerPair(t)
	path := filepath.Join(t.TempDir(), "close-only.json")
	withBook(t, p.b, func(book *evidencebook.Book) {
		anchor, err := book.Checkpoint(t.Context())
		require.NoError(t, err)
		bundle, err := book.Bundle(t.Context(), evidencebook.BundleRequest{Root: p.bClose, Include: []string{p.bClose}, At: &anchor, WithholdPayloads: true})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, bundle.JSON, 0o600))
	})
	p.bundle = path
	acked, err := runAnswer(t, p.args("acknowledge")...)
	require.NoError(t, err)
	assert.Equal(t, p.bClose, acked.PeerClose)
}

// Two Closes for the period, the second superseding the first: each is
// answered on its own, and the superseded one is no longer offered.
func TestAnswersAreKeyedByThePeerClose(t *testing.T) {
	p := newAnswerPair(t)
	first, err := runAnswer(t, p.args("acknowledge")...)
	require.NoError(t, err)
	second := sealPeerClose(t, p.b, p.bClose)
	bundle, raw := fullBundleJSON(t, p.b)
	require.NotEmpty(t, bundle.Records)
	path := filepath.Join(t.TempDir(), "superseded.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	p.bundle = path
	acked, err := runAnswer(t, p.args("acknowledge")...)
	require.NoError(t, err)
	assert.Equal(t, second, acked.PeerClose)
	assert.False(t, acked.AlreadyRecorded, "an acknowledgement of the superseded Close is not this one's")
	assert.NotEqual(t, first.RecordID, acked.RecordID)
}

// A repeat after a run that wrote the record and failed before its
// checkpoint checkpoints it, writing nothing else.
func TestARepeatCheckpointsAnUncoveredAnswer(t *testing.T) {
	p := newAnswerPair(t)
	var id string
	withBook(t, p.a, func(book *evidencebook.Book) {
		record, err := book.Acknowledge(t.Context(), p.bClose, "b")
		require.NoError(t, err)
		id = record.RecordID
	})
	again, err := runAnswer(t, p.args("acknowledge")...)
	require.NoError(t, err)
	assert.True(t, again.AlreadyRecorded)
	assert.Equal(t, id, again.RecordID)
	withBook(t, p.a, func(book *evidencebook.Book) {
		covered, err := book.CheckpointCovers(t.Context(), again.Seq)
		require.NoError(t, err)
		assert.True(t, covered)
		acks, err := book.Query(t.Context(), evidencebook.Filter{RecordType: evidencebook.RecordTypeAcknowledgement})
		require.NoError(t, err)
		assert.Len(t, acks, 1, "no second acknowledgement")
	})
}

// On a windowed store the peer Close names the window book its period is
// in; the answer goes in the current window, and a repeat after the store
// rolls on finds it there.
func TestAnswersOnAWindowedStore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC))
	a, _ := bookProfile(t, "a")
	b, bKeys := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "a")
	require.NoError(t, err)
	appendHalves(t, a, half{"x1", "r1", "p1"})
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC))
	path := filepath.Join(t.TempDir(), "b.json")
	closed, err := runClose(t, "--profile", "b", "--period", "day", "--date", "2026-09-10", "--counterparty", "a/2026-09", "--bundle-out", path)
	require.NoError(t, err)
	args := []string{"acknowledge", "--profile", "a", "--period", "day", "--date", "2026-09-10", "--counterparty", "b", "--peer", path, "--peer-checkpoint-key", hex.EncodeToString(bKeys.checkpoint)}

	// September is closed by November; the acknowledgement goes in November.
	set(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC))
	_, err = invoke(t, "", "book", "window", "roll", "--profile", "a")
	require.NoError(t, err)
	acked, err := runAnswer(t, args...)
	require.NoError(t, err)
	assert.Equal(t, closed.RecordID, acked.PeerClose)
	assert.Equal(t, "a/2026-11", acked.Book)
	assert.Equal(t, "2026-11", acked.Window)

	set(time.Date(2026, 12, 9, 10, 0, 0, 0, time.UTC))
	_, err = invoke(t, "", "book", "window", "roll", "--profile", "a")
	require.NoError(t, err)
	again, err := runAnswer(t, args...)
	require.NoError(t, err)
	assert.True(t, again.AlreadyRecorded, "found in November's book")
	assert.Equal(t, acked.RecordID, again.RecordID)
	assert.Equal(t, "2026-11", again.Window)

	rebuttal := append(append([]string{"rebut"}, args[1:]...), "--reason", "late")
	rebutted, err := runAnswer(t, rebuttal...)
	require.NoError(t, err)
	assert.Equal(t, "a/2026-12", rebutted.Book)
	_, err = runAnswer(t, args...)
	require.NoError(t, err, "the acknowledgement is already recorded")

	// A Close naming the current window's book is not this period's.
	wrong := filepath.Join(t.TempDir(), "wrong.json")
	_, err = runClose(t, "--profile", "b", "--period", "day", "--date", "2026-09-10", "--counterparty", "a/2026-12", "--bundle-out", wrong)
	require.NoError(t, err)
	_, err = runAnswer(t, "acknowledge", "--profile", "a", "--period", "day", "--date", "2026-09-10", "--counterparty", "b", "--peer", wrong, "--peer-checkpoint-key", hex.EncodeToString(bKeys.checkpoint))
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "naming this book (a/2026-09)")
}

// A Close citing a peer Close holds the --peer bundle as received, cites
// it, and reports it; a repeat reports the same held_bundle, and the Close's
// bundle withholds its payload.
func TestCloseHoldsThePeerBundleItReliesOn(t *testing.T) {
	p := newAnswerPair(t)
	raw, err := os.ReadFile(p.bundle)
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "a-close-bundle.json")
	closed, err := runClose(t, "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--peer", p.bundle, "--peer-checkpoint-key", hex.EncodeToString(p.bKeys.checkpoint), "--bundle-out", out)
	require.NoError(t, err)
	require.Equal(t, p.bClose, closed.PeerClose)
	require.NotEmpty(t, closed.HeldBundle)
	withBook(t, p.a, func(book *evidencebook.Book) {
		held, err := book.Get(t.Context(), closed.HeldBundle)
		require.NoError(t, err)
		assert.Equal(t, evidencebook.RecordTypeHeldBundle, held.Header.RecordType)
		sum := sha256.Sum256(raw)
		require.Equal(t, []string{hex.EncodeToString(sum[:])}, held.Header.PayloadCommitments)
		payload, err := book.ResolvePayload(t.Context(), held.RecordID, held.Header.PayloadCommitments[0])
		require.NoError(t, err)
		assert.Equal(t, raw, payload, "held in full, as received")
		var statement evidencebook.HeldBundleStatement
		require.NoError(t, json.Unmarshal(held.Header.Statement, &statement))
		assert.Equal(t, evidencebook.HeldBundleStatement{BundleDigest: closed.PeerBundle, Peer: "b", ReliedBy: evidencebook.HeldBundleReliedByClose}, statement)
		sealed, err := book.Get(t.Context(), closed.RecordID)
		require.NoError(t, err)
		assert.Contains(t, sealed.Header.LinksTo(evidencebook.Cites), closed.HeldBundle)
	})

	written, err := os.ReadFile(out)
	require.NoError(t, err)
	verified, err := evidencebook.VerifyBundle(written)
	require.NoError(t, err)
	for _, payload := range verified.Payloads {
		assert.NotEqual(t, raw, payload, "the Close's bundle does not carry b's bundle back")
	}

	again, err := runClose(t, "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--peer", p.bundle, "--peer-checkpoint-key", hex.EncodeToString(p.bKeys.checkpoint))
	require.NoError(t, err)
	assert.True(t, again.AlreadyClosed)
	assert.Equal(t, closed.HeldBundle, again.HeldBundle)

	// A Close citing no peer Close holds nothing.
	plain, err := runClose(t, "--profile", "b", "--period", "day", "--date", "2026-09-24", "--counterparty", "a")
	require.NoError(t, err)
	assert.Empty(t, plain.HeldBundle)
}

// A Close citing a peer Close over last month, sealed while last month's
// window is in its grace, holds the peer bundle there (#1009's regression,
// fixed in #116).
func TestACitingCloseSealsInAWindowsGrace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC))
	a, _ := bookProfile(t, "a")
	b, bKeys := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "a")
	require.NoError(t, err)
	appendHalves(t, a, half{"x1", "r1", "p1"})
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC))
	appendHalves(t, a, half{"x2", "r2", "p2"})
	appendHalves(t, b, half{"x2", "r2", "p2"})
	path := filepath.Join(t.TempDir(), "b.json")
	_, err = runClose(t, "--profile", "b", "--counterparty", "a/2026-09", "--period", "month", "--date", "2026-09-01", "--bundle-out", path)
	require.NoError(t, err)
	closed, err := runClose(t, "--profile", "a", "--counterparty", "b", "--period", "month", "--date", "2026-09-01", "--peer", path, "--peer-checkpoint-key", hex.EncodeToString(bKeys.checkpoint))
	require.NoError(t, err)
	assert.Equal(t, "2026-09", closed.Window)
	assert.NotEmpty(t, closed.PeerClose)
	assert.NotEmpty(t, closed.HeldBundle)
}

// After a JSONL book is converted to a windowed store, an answer the old
// book recorded still counts: a repeat finds it, and acknowledging a Close
// it rebutted is refused.
func TestAnswersBeforeAConversionStillCount(t *testing.T) {
	p := newAnswerPair(t)
	rebutted, err := runAnswer(t, p.args("rebut", "--reason", "x1 is not ours")...)
	require.NoError(t, err)
	out, err := invoke(t, "", "book", "window", "convert", "--profile", "a", "--yes")
	require.NoError(t, err, out)
	p.set(day1.Add(time.Hour))

	again, err := runAnswer(t, p.args("rebut", "--reason", "x1 is not ours")...)
	require.NoError(t, err)
	assert.True(t, again.AlreadyRecorded)
	assert.Equal(t, rebutted.RecordID, again.RecordID)
	assert.Equal(t, "a", again.Book, "the predecessor JSONL book holds it")
	_, err = runAnswer(t, p.args("rebut", "--reason", "another")...)
	require.ErrorIs(t, err, cli.ErrInput)
	_, err = runAnswer(t, p.args("acknowledge")...)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "already rebutted Close "+p.bClose)
}
