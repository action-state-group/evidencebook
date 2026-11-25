// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests stop or race the store's writers through its seams. They came
// from capsulectl-deal's own tests when the writers moved here
// (evidencebook#899), and test the same cases.

// The shelf's SealSent can fail without sealing (an empty Record: retry), or
// after the record is stored (a Record with the error: its Capsule is kept
// and the pair made, never a second seal).
func TestTheSendSurvivesTheShelfsPartialFailures(t *testing.T) {
	ctx := t.Context()
	buyer := newSide(t, "buyer")
	sk := newSide(t, "seller").keyID()
	real := sealSent
	t.Cleanup(func() { sealSent = real })

	sealSent = func(context.Context, *evidencebook.Shelf, evidencebook.Entry, string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		return evidencebook.Record{}, evidencebook.SentCapsule{}, errors.New("the journal is busy")
	}
	_, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.Error(t, err)
	var dealID, state string
	require.NoError(t, buyer.db.QueryRowContext(ctx, `SELECT deal_id, state FROM deal_sent`).Scan(&dealID, &state))
	assert.Equal(t, sentSealing, state)
	sealSent = real
	got, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)
	assert.Equal(t, dealID, got.DealID, "the stored record is sealed, not a new one")
	_, err = xdealv1.VerifySent(got.Pair, nil)
	require.NoError(t, err)

	sealSent = func(ctx context.Context, shelf *evidencebook.Shelf, e evidencebook.Entry, id string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		rec, sent, err := real(ctx, shelf, e, id)
		require.NoError(t, err)
		return rec, sent, errors.New("the log append is pending")
	}
	second, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o2")
	require.NoError(t, err, "a record stored with an error is sent as it is")
	sealSent = real
	_, err = xdealv1.VerifySent(second.Pair, nil)
	require.NoError(t, err, "the pair is made from the Capsule that came with the error")
	var n int
	require.NoError(t, buyer.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deal_sent`).Scan(&n))
	assert.Equal(t, 2, n)
}

// A decline that never reached the journal is not sealed once the deal moved
// on: run again after the offer's cancellation was received, it is refused
// and nothing is sealed.
func TestANeverSealedDeclineIsRefusedAfterACancellation(t *testing.T) {
	ctx := t.Context()
	buyer, seller := newSide(t, "buyer"), newSide(t, "seller")
	sk := seller.keyID()
	offer, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)
	seller.receiveSent(offer.Pair, nil)
	real := sealSent
	t.Cleanup(func() { sealSent = real })
	sealSent = func(context.Context, *evidencebook.Shelf, evidencebook.Entry, string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		return evidencebook.Record{}, evidencebook.SentCapsule{}, errors.New("the journal is busy")
	}
	_, err = seller.store.DeclineOffer(ctx, offer.Digest, "", "d")
	require.Error(t, err)
	sealSent = real
	_, err = seller.db.ExecContext(ctx, `UPDATE deal_versions SET state='cancelled' WHERE deal_id=?`, offer.DealID)
	require.NoError(t, err)

	_, err = seller.store.DeclineOffer(ctx, offer.Digest, "", "d")
	assert.Contains(t, inputReason(t, err), "this offer is cancelled")
	var digest string
	var rows int
	require.NoError(t, seller.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(record_digest) FROM deal_sent WHERE kind=?`, xdealv1.KindDecline).Scan(&rows, &digest))
	assert.Equal(t, 1, rows, "no repeat stored")
	held, err := seller.store.journalHolds(ctx, digest)
	require.NoError(t, err)
	assert.False(t, held, "nothing sealed in the journal")
	d, err := seller.store.Deal(ctx, offer.DealID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", d.State)
}

// A stopped offer with a payment row is sealed once more from its stored
// payload, and its terms leg is computed from it again.
func TestAStoppedPaymentOfferIsSealedAgainWithItsTermsLeg(t *testing.T) {
	ctx := t.Context()
	buyer := newSide(t, "buyer")
	sk := newSide(t, "seller").keyID()
	// A payment row the payee recomputes is one the terms leg carries.
	payment := offerTo(t, &sk)
	payment.TermsRows.Rows[0].Recomputed = true
	real := sealSent
	t.Cleanup(func() { sealSent = real })
	sealSent = func(context.Context, *evidencebook.Shelf, evidencebook.Entry, string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		return evidencebook.Record{}, evidencebook.SentCapsule{}, errors.New("the journal is busy")
	}
	_, err := buyer.store.Offer(ctx, payment, "o")
	require.Error(t, err)
	sealSent = real
	got, err := buyer.store.Offer(ctx, payment, "o")
	require.NoError(t, err)
	_, err = xdealv1.VerifySent(got.Pair, nil)
	require.NoError(t, err)
	assert.Contains(t, string(got.Pair), `"leg":"terms"`)
}

// agreedAlone is a deal the buyer agreed to alone (an offer to a party with
// no layer), with its book open.
func agreedAlone(t *testing.T) (*side, string, *evidencebook.Book) {
	t.Helper()
	ctx := t.Context()
	buyer := newSide(t, "buyer")
	offer, err := buyer.store.Offer(ctx, offerTo(t, nil), "o")
	require.NoError(t, err)
	buyer.admit(offer.DealID)
	_, err = buyer.store.Agree(ctx, buyer.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)
	d, err := buyer.store.Deal(ctx, offer.DealID)
	require.NoError(t, err)
	book, err := buyer.shelf.Open(ctx, d.OwnLogID)
	require.NoError(t, err)
	return buyer, offer.DealID, book
}

var metStatement = xdealv1.StatementBody{Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "met"}}}

// A write that lands in the deal book between reading its tail and appending
// is not overwritten: the record is refused its place, read again and rebuilt
// at the next one, citing the record that landed.
func TestARecordWhosePlaceWasTakenIsRebuiltAtTheNextOne(t *testing.T) {
	ctx := t.Context()
	buyer, dealID, book := agreedAlone(t)
	var interloper string
	calls := 0
	t.Cleanup(func() { bookAppendHook = func() {} })
	bookAppendHook = func() {
		calls++
		if calls > 1 {
			return
		}
		payload := []byte(`{"interloper":true}`)
		sum := sha256.Sum256(payload)
		interloper = hex.EncodeToString(sum[:])
		_, err := book.Append(ctx, evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}})
		require.NoError(t, err)
	}
	sealed, err := buyer.store.SealInBook(ctx, dealID, BookRecord{Body: metStatement})
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "built twice: the first was refused its place")
	assert.Equal(t, uint64(3), sealed.Seq, "after the agreement and the record that landed")
	require.NotNil(t, sealed.Record.Block.Prev)
	assert.Equal(t, interloper, sealed.Record.Block.Prev.Digest, "it cites the record that landed")
	read, err := xdealv1.VerifyBook(sealed.Pair)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), read.Seq)
}

// A book that keeps changing is not retried for ever.
func TestAWriterGivesUpWhenTheBookKeepsChanging(t *testing.T) {
	ctx := t.Context()
	buyer, dealID, book := agreedAlone(t)
	n := 0
	t.Cleanup(func() { bookAppendHook = func() {} })
	bookAppendHook = func() {
		n++
		_, err := book.Append(ctx, evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{[]byte(`{"n":` + strconv.Itoa(n) + `}`)}})
		require.NoError(t, err)
	}
	_, err := buyer.store.SealInBook(ctx, dealID, BookRecord{Body: metStatement})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kept changing")
	assert.Equal(t, bookAppendTries, n)
}
