// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sealed hands back a record as it was sealed: its Capsule commits its
// header and is the record id's Capsule, and its bytes are the caller's own.
func TestSealedIsTheRecordAsSealed(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	book := mustOpenBook(t, shelf, "deal/a")
	r := mustAppend(t, book, observation("a", "1"))

	sr, err := book.Sealed(ctx, r.RecordID)
	require.NoError(t, err)
	assert.Equal(t, r.RecordID, sr.RecordID)
	assert.Equal(t, r.Seq, sr.Seq)
	require.NoError(t, checkCapsuleCommitsHeader(sr.RecordID, sr.Capsule, sr.Header))
	assert.NotEmpty(t, sr.Envelope)

	sr.Capsule[0], sr.Envelope[0] = 'x', 'x'
	again, err := book.Sealed(ctx, r.RecordID)
	require.NoError(t, err)
	require.NoError(t, checkCapsuleCommitsHeader(again.RecordID, again.Capsule, again.Header), "a caller's change reaches nothing held")

	_, err = book.Sealed(ctx, "0000000000000000000000000000000000000000000000000000000000000000")
	assert.ErrorIs(t, err, ErrNotFound)
}

// AppendAt appends only as the book's next record: a stale place is refused
// with ErrBusy and writes nothing.
func TestAppendAtIsTheNextRecordOnly(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	book := mustOpenBook(t, shelf, "deal/a")
	r, err := book.AppendAt(ctx, 1, observation("a", "1"))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), r.Seq)
	_, err = book.AppendAt(ctx, 1, observation("a", "2"))
	assert.ErrorIs(t, err, ErrBusy, "record 1 is taken")
	_, err = book.AppendAt(ctx, 3, observation("a", "2"))
	assert.ErrorIs(t, err, ErrBusy, "record 3 is not next")
	assert.Equal(t, uint64(1), book.Size())
	r, err = book.AppendAt(ctx, 2, observation("a", "2"))
	require.NoError(t, err)
	assert.Equal(t, uint64(2), r.Seq)
}
