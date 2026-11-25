// SPDX-License-Identifier: Apache-2.0

package placeappend

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testBook(t *testing.T) *evidencebook.Book {
	t.Helper()
	seed := sha256.Sum256([]byte("placeappend test"))
	signer, err := evidencebook.NewEd25519Signer(ed25519.NewKeyFromSeed(seed[:]))
	require.NoError(t, err)
	dir := t.TempDir()
	originals, err := evidencebook.OpenOriginalsFile(t.Context(), filepath.Join(dir, "originals.jsonl"), "test", signer.PublicKey())
	require.NoError(t, err)
	shelf, err := evidencebook.OpenSQLiteShelf(t.Context(), evidencebook.ShelfConfig{
		Path: filepath.Join(dir, "shelf.db"), Operator: "test", Signer: signer, CheckpointKey: ed25519.NewKeyFromSeed(seed[:]),
		Originals: originals, Now: func() time.Time { return time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = shelf.Release() })
	book, err := shelf.Open(t.Context(), "test/book")
	require.NoError(t, err)
	return book
}

func entry() evidencebook.Entry {
	return evidencebook.Entry{RecordType: "test", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{[]byte(`{"n":1}`)}}
}

func TestAppendAtTheNextPlace(t *testing.T) {
	book := testBook(t)
	var got evidencebook.Record
	err := Append(t.Context(), book, func(size uint64) (Try, error) {
		assert.Equal(t, uint64(0), size)
		return Try{Entry: entry(), Done: func(r evidencebook.Record) error { got = r; return nil }}, nil
	}, Tries)
	require.NoError(t, err)
	assert.NotEmpty(t, got.RecordID)
	assert.Equal(t, uint64(1), book.Size())
}

func TestATakenPlaceIsBuiltAgainThenGivenUp(t *testing.T) {
	book := testBook(t)
	builds := 0
	err := Append(t.Context(), book, func(uint64) (Try, error) {
		builds++
		return Try{Entry: entry(), Place: func(func(*evidencebook.Book) error) error { return evidencebook.ErrBusy }}, nil
	}, 3)
	assert.ErrorIs(t, err, ErrKeptChanging)
	assert.ErrorIs(t, err, evidencebook.ErrBusy)
	assert.Equal(t, 3, builds)
	assert.Equal(t, uint64(0), book.Size())
}

func TestBuildAndDoneErrorsAreNotRetried(t *testing.T) {
	book := testBook(t)
	builds := 0
	err := Append(t.Context(), book, func(uint64) (Try, error) {
		builds++
		return Try{}, evidencebook.ErrBusy
	}, Tries)
	assert.ErrorIs(t, err, evidencebook.ErrBusy)
	assert.NotErrorIs(t, err, ErrKeptChanging)
	assert.Equal(t, 1, builds, "a busy error from the build is not a taken place")

	stop := errors.New("done failed")
	err = Append(t.Context(), book, func(uint64) (Try, error) {
		return Try{Entry: entry(), Done: func(evidencebook.Record) error { return stop }}, nil
	}, Tries)
	assert.ErrorIs(t, err, stop)
	assert.Equal(t, uint64(1), book.Size(), "the record was appended once, never again")
}

func TestSkipAppendsNothing(t *testing.T) {
	book := testBook(t)
	require.NoError(t, Append(t.Context(), book, func(uint64) (Try, error) { return Try{Skip: true}, nil }, Tries))
	assert.Equal(t, uint64(0), book.Size())
}
