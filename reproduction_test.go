// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reproduced runs the same entries into a fresh reproduction book whose
// time is fixed, and returns the record ids and the checkpoint.
func reproduced(t *testing.T) ([]string, Checkpoint) {
	t.Helper()
	f := newFixture(t, "repro", 3)
	cfg := f.config(t)
	at := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	cfg.Now, cfg.Reproduction = func() time.Time { return at }, true
	book, err := Open(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Release() })
	var ids []string
	for _, payload := range []string{"one", "two"} {
		record := mustAppend(t, book, observation("s", payload))
		require.NotNil(t, record.Header.Reproduction)
		assert.Equal(t, ReproductionClockCaller, record.Header.Reproduction.Clock)
		ids = append(ids, record.RecordID)
	}
	checkpoint, err := book.Checkpoint(context.Background())
	require.NoError(t, err)
	return ids, checkpoint
}

func TestReproductionBooksAreByteIdentical(t *testing.T) {
	idsA, checkpointA := reproduced(t)
	idsB, checkpointB := reproduced(t)
	assert.Equal(t, idsA, idsB)
	assert.Equal(t, checkpointA, checkpointB)
}

func TestReproductionMarkerChangesTheRecordIdentity(t *testing.T) {
	ids, _ := reproduced(t)
	// The same entry in a live book at the same time is another record.
	f := newFixture(t, "repro", 3)
	cfg := f.config(t)
	at := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	cfg.Now = func() time.Time { return at }
	book, err := Open(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Release() })
	live := mustAppend(t, book, observation("s", "one"))
	assert.Nil(t, live.Header.Reproduction)
	assert.NotEqual(t, ids[0], live.RecordID)
}

func TestReproductionNeedsACallerTime(t *testing.T) {
	f := newFixture(t, "repro", 3)
	cfg := f.config(t)
	cfg.Now, cfg.Reproduction = nil, true
	_, err := Open(context.Background(), cfg)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestABookKeepsItsClockKind(t *testing.T) {
	for _, reproduction := range []bool{true, false} {
		f := newFixture(t, "mixed", 5)
		cfg := f.config(t)
		cfg.Reproduction = reproduction
		book, err := Open(context.Background(), cfg)
		require.NoError(t, err)
		mustAppend(t, book, observation("s", "one"))
		require.NoError(t, book.Release())

		cfg = f.config(t)
		cfg.Reproduction = !reproduction
		other, err := Open(context.Background(), cfg)
		require.NoError(t, err, "opening to read is allowed")
		_, err = other.Append(context.Background(), observation("s", "two"))
		assert.True(t, errors.Is(err, ErrInvalid), "reproduction=%v: %v", reproduction, err)
		require.NoError(t, other.Release())
	}
}

func TestReproductionClockIsValidated(t *testing.T) {
	h := Header{Version: HeaderVersion, BookID: "b", Seq: 1, RecordType: "r", EpistemicType: ObservedEvent, CommittedAt: "2026-10-31T00:00:00Z", Links: []Link{}, Reproduction: &Reproduction{Clock: "wall"}}
	assert.ErrorIs(t, validateHeader(h), ErrInvalid)
	h.Reproduction.Clock = ReproductionClockCaller
	assert.NoError(t, validateHeader(h))
}
