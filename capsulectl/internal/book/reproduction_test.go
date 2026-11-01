package book

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reproProfile is a profile with fixed keys, so two scratch books built from
// the same inputs can be compared by digest.
func reproProfile(t *testing.T, name string) cli.Profile {
	t.Helper()
	seed := func(b byte) []byte {
		s := make([]byte, ed25519.SeedSize)
		for i := range s {
			s[i] = b + byte(i)
		}
		return s
	}
	record, checkpoint := ed25519.NewKeyFromSeed(seed(1)), ed25519.NewKeyFromSeed(seed(100))
	p := cli.Profile{Name: name, Type: "jsonl", LogID: "repro", Log: Name, Namespace: "capsule", Operator: "repro-operator"}
	p.Connection.Database = filepath.Join(t.TempDir(), name)
	p.Signing.Value = hex.EncodeToString(record.Seed())
	p.TrustedKeys = []string{hex.EncodeToString(record.Public().(ed25519.PublicKey))}
	p.Checkpoint.Signing.Value = hex.EncodeToString(checkpoint.Seed())
	p.Checkpoint.TrustedKeys = []string{hex.EncodeToString(checkpoint.Public().(ed25519.PublicKey))}
	require.NoError(t, cli.SaveProfile(p, false))
	_, err := invoke(t, "", "store", "init", "--profile", name)
	require.NoError(t, err)
	return p
}

const windowEnd = "1793404800" // 2026-10-31T00:00:00Z

func reproduceOnce(t *testing.T, name string) (publishedOut, string) {
	t.Helper()
	t.Setenv(SourceDateEpochEnv, windowEnd)
	reproProfile(t, name)
	published := publishFile(t, name, sealRequestFile(t, "judge-1"))
	checkpoint, err := invoke(t, "", "cll", "checkpoint", "create", "--profile", name)
	require.NoError(t, err)
	return published, checkpoint
}

func TestReproducedBooksAreByteIdentical(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	first, firstCheckpoint := reproduceOnce(t, "one")
	// A later wall clock changes nothing: the time is the caller's.
	bookClock(t, day0.Add(48*time.Hour))
	second, secondCheckpoint := reproduceOnce(t, "two")
	assert.Equal(t, first, second)
	assert.Equal(t, firstCheckpoint, secondCheckpoint)

	p, _ := cli.LoadProfile("one")
	opened, err := openBook(t.Context(), p, false)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.release()) }()
	records, err := opened.book.Query(t.Context(), evidencebook.Filter{})
	require.NoError(t, err)
	require.NotEmpty(t, records)
	for _, r := range records {
		require.NotNil(t, r.Header.Reproduction, "record %d", r.Seq)
		assert.Equal(t, "2026-10-31T00:00:00Z", r.Header.CommittedAt)
	}
}

func TestAReproducedBookIsADeadEnd(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	reproduceOnce(t, "one")
	bookClock(t, time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) // the wall clock is past the book's time
	require.NoError(t, os.Unsetenv(SourceDateEpochEnv))        // reproduceOnce set it with t.Setenv, which restores it
	// A marked book is read as any other: it can be listed and compared.
	assert.Len(t, listLog(t, "one").Entries, 1)
	// Without the caller's time the marked book cannot be extended or cut.
	_, err := invoke(t, "", "publish", "--profile", "one", "--request", sealRequestFile(t, "judge-2"))
	assert.ErrorContains(t, err, "cannot be written as a live")
	_, err = invoke(t, "", "cll", "checkpoint", "create", "--profile", "one")
	assert.ErrorContains(t, err, "cannot be written as a live")
}

func TestALiveBookRefusesACallerTime(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := storedProfile(t, "live")
	publishFile(t, p.Name, sealRequestFile(t, "live-1"))
	t.Setenv(SourceDateEpochEnv, windowEnd)
	_, err := invoke(t, "", "publish", "--profile", p.Name, "--request", sealRequestFile(t, "live-2"))
	assert.ErrorContains(t, err, "live")
}

func TestCallerTimesOnlyMoveForward(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	reproduceOnce(t, "one")
	t.Setenv(SourceDateEpochEnv, "1793404799") // one second earlier
	_, err := invoke(t, "", "publish", "--profile", "one", "--request", sealRequestFile(t, "judge-2"))
	assert.ErrorContains(t, err, "only move forward")
	t.Setenv(SourceDateEpochEnv, "1793404801") // later is fine
	_, err = invoke(t, "", "publish", "--profile", "one", "--request", sealRequestFile(t, "judge-2"))
	require.NoError(t, err)
}

func TestAnUnreadableSourceDateEpochIsRefused(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := storedProfile(t, "a")
	for _, value := range []string{"yesterday", "-5", "1.5", "253402300800"} {
		t.Setenv(SourceDateEpochEnv, value)
		_, err := invoke(t, "", "publish", "--profile", p.Name, "--request", sealRequestFile(t, "x"))
		assert.ErrorContains(t, err, SourceDateEpochEnv, value)
	}
}

// book result build seals the Result through the book's clock: under
// SOURCE_DATE_EPOCH its record is the caller's time and carries the marker.
func TestResultBuildTakesTheCallersTime(t *testing.T) {
	t.Setenv(SourceDateEpochEnv, windowEnd)
	b := newResultBook(t)
	built, _, err := runResultBuild(t, b.profile, resultDoc(testClaim("claim-1", "met", b.capsules[0])))
	require.NoError(t, err)
	withBook(t, b.profile, func(book *evidencebook.Book) {
		record, err := book.Get(t.Context(), built.RecordID)
		require.NoError(t, err)
		assert.Equal(t, "2026-10-31T00:00:00Z", record.Header.CommittedAt)
		require.NotNil(t, record.Header.Reproduction)
	})
}

func TestWallClockVerbsDoNotRunInAReproduction(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day0)
	p, _ := storedProfile(t, "a")
	t.Setenv(SourceDateEpochEnv, windowEnd)
	_, err := invoke(t, "", "book", "migrate", "--profile", p.Name)
	assert.ErrorContains(t, err, SourceDateEpochEnv)
	_, err = invoke(t, "", "backfill", "run", "--profile", p.Name, "--source", "-")
	assert.ErrorContains(t, err, SourceDateEpochEnv)
}
