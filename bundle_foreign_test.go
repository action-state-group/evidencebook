// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// foreignOf is a book's records as a holder of that log received them, and
// the book's checkpoint statement over them.
func foreignOf(t *testing.T, book *Book) ([]ForeignRecord, []byte, []Record) {
	t.Helper()
	ctx := context.Background()
	cp, err := book.Checkpoint(ctx)
	require.NoError(t, err)
	recs, err := book.Query(ctx, Filter{FromSeq: 1, ToSeq: cp.Entries})
	require.NoError(t, err)
	out := make([]ForeignRecord, len(recs))
	for i, r := range recs {
		sealed, err := book.Sealed(ctx, r.RecordID)
		require.NoError(t, err)
		out[i] = ForeignRecord{Capsule: sealed.Capsule, Envelope: sealed.Envelope, Header: sealed.Header, Payloads: map[string][]byte{}}
		for _, d := range r.Header.PayloadCommitments {
			if out[i].Payloads[d], err = book.ResolvePayload(ctx, r.RecordID, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out, cp.Statement, recs
}

// A holder builds a bundle of a log it does not own, from the records it
// received and the owner's signed checkpoint: it verifies in full, anchored
// on the owner's key, every record carried and only the root disclosed.
func TestForeignBundle(t *testing.T) {
	book := checkpointedBook(t, "foreign", 7)
	records, statement, recs := foreignOf(t, book)
	data, err := ForeignBundle(context.Background(), statement, records, recs[0].RecordID)
	require.NoError(t, err)
	vb, err := VerifyBundle(data)
	require.NoError(t, err)
	require.NoError(t, vb.FullyVerified())
	assert.True(t, vb.AnchorAuthenticated)
	assert.Equal(t, statement, vb.AnchorStatement)
	assert.Equal(t, uint64(1), vb.IntervalFirst)
	assert.Equal(t, uint64(len(recs)), vb.IntervalLast)
	disclosed := 0
	for _, r := range vb.Records {
		if r.HeaderVerified {
			disclosed++
			assert.Equal(t, recs[0].RecordID, r.RecordID)
		}
	}
	assert.Equal(t, 1, disclosed, "only the root is disclosed")
	assert.Len(t, vb.Payloads, 1)
}

func TestForeignBundleRefusals(t *testing.T) {
	ctx := context.Background()
	book := checkpointedBook(t, "foreign", 7)
	records, statement, recs := foreignOf(t, book)
	other, _, _ := foreignOf(t, checkpointedBook(t, "other", 8))
	root := recs[0].RecordID

	_, err := ForeignBundle(ctx, statement, records[:1], root)
	assert.ErrorIs(t, err, ErrInvalid, "fewer records than the checkpoint")
	_, err = ForeignBundle(ctx, statement, []ForeignRecord{records[1], records[0]}, root)
	assert.ErrorIs(t, err, ErrInvalid, "records out of order do not recompute the root")
	_, err = ForeignBundle(ctx, statement, other, root)
	assert.ErrorIs(t, err, ErrInvalid, "another log's records")
	_, err = ForeignBundle(ctx, statement, records, recs[1].RecordID+"0")
	assert.ErrorIs(t, err, ErrInvalid, "a root not among the records")
	noPayload := append([]ForeignRecord{}, records...)
	noPayload[0].Payloads = nil
	_, err = ForeignBundle(ctx, statement, noPayload, root)
	assert.ErrorIs(t, err, ErrInvalid, "the root's payload not given")
	_, err = ForeignBundle(ctx, []byte("not a checkpoint"), records, root)
	assert.Error(t, err)
}
