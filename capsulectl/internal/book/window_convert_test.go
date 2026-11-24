package book

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A JSONL book continues in a store split by window (evidencebook#808):
// the book closes for good, the first window is partial and names it, a
// Close before the conversion is refused with its hint, and a chain from
// the first window carries the book as its predecessor.
func TestConvertingAJSONLBookToWindows(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	b, keys := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "none")
	require.NoError(t, err)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	var notice strings.Builder
	noticeOut = &notice
	t.Cleanup(func() { noticeOut = os.Stderr })

	_, err = invoke(t, "", "book", "window", "convert", "--profile", "b")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "one-way")
	_, statErr := os.Stat(shelfFile(b))
	require.ErrorIs(t, statErr, os.ErrNotExist, "nothing converted without --yes")

	out, err := invoke(t, "", "book", "window", "convert", "--profile", "b", "--yes")
	require.NoError(t, err, out)
	var converted windowConvertResult
	require.NoError(t, json.Unmarshal([]byte(out), &converted), out)
	assert.Equal(t, evidencebook.WindowMonth, converted.Period)
	assert.Equal(t, "2026-09-24T10:00:01Z", converted.Since)
	require.NotNil(t, converted.Predecessor)
	assert.Equal(t, b.LogID, converted.Predecessor.LogID)
	assert.False(t, converted.Witnessed)
	for _, want := range []string{"closes 7 days after it ends", "the first window 2026-09 starts at 2026-09-24T10:00:01Z (partial)", "this is not a clear", "unwitnessed", "stays at book/log.jsonl"} {
		assert.Contains(t, converted.Notice, want)
	}
	assert.Contains(t, notice.String(), "capsulectl: converted:")

	// Again: already converted, nothing changes.
	out, err = invoke(t, "", "book", "window", "convert", "--profile", "b", "--yes")
	require.NoError(t, err, out)
	assert.Contains(t, out, `"already_converted":true`)

	set(time.Date(2026, 9, 24, 10, 5, 0, 0, time.UTC))
	appendHalves(t, b, half{"x2", "r2", "p2"})
	out, err = invoke(t, "", "book", "window", "list", "--profile", "b")
	require.NoError(t, err, out)
	var windows windowListResult
	require.NoError(t, json.Unmarshal([]byte(out), &windows), out)
	require.Len(t, windows.Windows, 1)
	assert.True(t, windows.Windows[0].Partial)
	assert.Equal(t, time.Date(2026, 9, 24, 10, 0, 1, 0, time.UTC), windows.Windows[0].Start)

	// A Close over a period before the conversion is refused, with its hint.
	set(time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC))
	_, err = invoke(t, "", "close", "--profile", "b", "--counterparty", "a", "--period", "day", "--date", "2026-09-23")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "begins before this store's conversion")
	assert.Contains(t, err.Error(), "closes in two parts")

	// A chain from the first window carries the predecessor and verifies.
	set(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	chainPath := filepath.Join(t.TempDir(), "chain.json")
	out, err = invoke(t, "", "book", "window", "chain", "--profile", "b", "--from", "2026-09", "--to", "2026-10", "--out", chainPath)
	require.NoError(t, err, out)
	raw, err := os.ReadFile(chainPath)
	require.NoError(t, err)
	var chain evidencebook.WindowChain
	require.NoError(t, json.Unmarshal(raw, &chain))
	require.NotNil(t, chain.Predecessor)
	assert.Equal(t, evidencebook.WindowChainVersionPredecessor, chain.V)
	verified, findings := evidencebook.VerifyWindowChain(chain, evidencebook.VerifyChainOptions{CheckpointKey: hex.EncodeToString(keys.checkpoint)})
	require.Empty(t, findings)
	assert.True(t, verified[0].Partial)
	require.NotNil(t, verified[0].Predecessor)

	// The core owns the partial first window's log.
	assert.True(t, BookBackend{}.OwnsLogID(b, b.LogID+"/2026-09"))
	assert.False(t, BookBackend{}.OwnsLogID(b, b.LogID+"/2026-08"))
}

// Where the profile names a witness, the predecessor's final checkpoint is
// queued in book/witness.json at conversion.
func TestConvertingQueuesThePredecessorForTheWitness(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "none")
	require.NoError(t, err)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	witnessKey := make([]byte, 32)
	witnessKey[0] = 1
	b.Checkpoint.Endpoint = "https://witness.example.invalid"
	b.Checkpoint.PublicKey = hex.EncodeToString(witnessKey)
	require.NoError(t, cli.SaveProfile(b, true))
	out, err := invoke(t, "", "book", "window", "convert", "--profile", "b", "--yes", "--period", "week")
	require.NoError(t, err, out)
	assert.Contains(t, out, `"witnessed":true`)
	states, err := newBookWitnessStore(b).load()
	require.NoError(t, err)
	require.NotEmpty(t, states)
}

// An interrupted conversion (the book closed, the store not made) resumes
// with the same window, refuses another, and says so to a writing verb.
func TestAnInterruptedConversionResumes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "none")
	require.NoError(t, err)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	opened, err := openBookKeyedAt(t.Context(), b, false, nil, false, true)
	require.NoError(t, err)
	_, err = opened.book.CloseForWindows(t.Context(), b.LogID, evidencebook.WindowMonth, 7*24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, opened.release())
	require.NoError(t, os.WriteFile(shelfFile(b)+".converting", []byte("half made"), 0o600))

	// A writing verb is told how to finish.
	opened, err = openBookKeyed(t.Context(), b, false, nil, false)
	require.NoError(t, err)
	_, err = opened.book.Append(t.Context(), evidencebook.Entry{RecordType: "deal", EpistemicType: evidencebook.ObservedEvent, Links: []evidencebook.Link{}, Statement: json.RawMessage(`{"a":1}`)})
	require.NoError(t, opened.release())
	require.ErrorIs(t, bookError(err), cli.ErrInput)
	assert.Contains(t, bookError(err).Error(), "run it again with the same --period and --grace")
	_, err = invoke(t, "", "book", "window", "convert", "--profile", "b", "--yes", "--period", "week")
	require.Error(t, err, "another window than the close names")
	out, err := invoke(t, "", "book", "window", "convert", "--profile", "b", "--yes")
	require.NoError(t, err, out)
	assert.FileExists(t, shelfFile(b))
	assert.NoFileExists(t, shelfFile(b)+".converting")
	opened, err = openBookKeyedAt(t.Context(), b, false, nil, true, true)
	require.NoError(t, err)
	closes := 0
	held, err := opened.book.Query(t.Context(), evidencebook.Filter{RecordType: evidencebook.RecordTypeBookClose})
	require.NoError(t, err)
	closes = len(held)
	require.NoError(t, opened.release())
	assert.Equal(t, 1, closes)
	_, err = invoke(t, "", "book", "window", "convert", "--profile", "b", "--yes", "--period", "week")
	require.ErrorIs(t, err, cli.ErrInput, "a converted store's window cannot change")
}

func TestConvertRefuses(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	empty, _ := bookProfile(t, "empty")
	_, err := invoke(t, "", "book", "window", "convert", "--profile", "empty", "--yes")
	require.ErrorIs(t, err, cli.ErrInput, "no JSONL book")
	_, statErr := os.Stat(shelfFile(empty))
	assert.ErrorIs(t, statErr, os.ErrNotExist)

	_, _ = bookProfile(t, "windowed")
	_, err = invoke(t, "", "book", "window", "init", "--profile", "windowed")
	require.NoError(t, err)
	_, err = invoke(t, "", "book", "window", "convert", "--profile", "windowed", "--yes")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "created empty")

	b, _ := bookProfile(t, "b")
	_, err = invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "none")
	require.NoError(t, err)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	t.Setenv(SourceDateEpochEnv, "1790000000")
	_, err = invoke(t, "", "book", "window", "convert", "--profile", "b", "--yes")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "reproduced")
}

// A record id of the old book gets the predecessor's hint.
func TestAnOldRecordIDNamesThePredecessor(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "none")
	require.NoError(t, err)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set := bookClock(t, time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	_, err = invoke(t, "", "book", "window", "convert", "--profile", "b", "--yes")
	require.NoError(t, err)
	set(time.Date(2026, 9, 24, 10, 5, 0, 0, time.UTC))
	appendHalves(t, b, half{"x2", "r2", "p2"})
	opened, err := openBookForReading(t.Context(), b)
	require.NoError(t, err)
	defer opened.release()
	_, err = opened.store.GetRecord(t.Context(), strings.Repeat("ab", 32))
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "predecessor book "+b.LogID)
}
