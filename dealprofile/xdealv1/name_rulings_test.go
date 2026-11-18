// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A counted ruling of the deal's adjudicator that the holder's book does not
// name gets a naming statement sealed after the record before it, once
// (PROFILE §5; evidencebook#527); the sealed record verifies as a book pair
// and a second call seals nothing.
func TestNameRulings(t *testing.T) {
	ctx := context.Background()
	adjudicator := newParty(t, "adjudicator")
	h := newHandshake(t, func(a *AgreeBody) {
		adj := fxAdjudicator
		adj.KeyID = adjudicator.key()
		a.Adjudicator = Some(adj)
	})
	rb := journalBlock(TypeRuling, "2026-11-13T10:00:00Z", true)
	rb.Seq = 1
	ruling := Record{Block: rb, Body: &RulingBody{Row: "as_described", Keys: RulingKeys{Buyer: h.buyer.key(), Seller: h.seller.key()},
		Against: Some(RoleSeller), Value: ValueNotMet, Read: RulingRead{Buyer: NullOf[string](), Seller: Some(h.agreementID)},
		Forum: fxForum, RulesDigest: fxAdjudicator.RulesDigest, Final: true}}
	require.NoError(t, Check(ruling))
	caseLog := "case/" + hex.EncodeToString(seed("case log")[:16])
	_, pair := adjudicator.seal(caseLog, ruling)
	res := h.seller.receive(pair, nil)
	require.True(t, res.Record.Stored)
	assert.Contains(t, res.Changes.Grew, fxDealID)

	ids, err := NameRulings(ctx, h.seller.shelf, res.Changes.Grew, h.seller.now)
	require.NoError(t, err)
	require.Len(t, ids, 1, "the ruling against the seller, named in the seller's own book")
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	require.Equal(t, uint64(2), book.Size())
	sealed, err := book.Sealed(ctx, ids[0])
	require.NoError(t, err)
	recs, err := book.Query(ctx, evidencebook.Filter{FromSeq: 2, ToSeq: 2})
	require.NoError(t, err)
	rec, kind, err := v1Payload(ctx, book, recs[0])
	require.NoError(t, err)
	require.Equal(t, payloadV1, kind)
	payload, _, err := Encode(rec)
	require.NoError(t, err)
	bp, err := BookPair(sealed, payload)
	require.NoError(t, err)
	read, err := VerifyBook(bp)
	require.NoError(t, err, "the naming statement is a valid deal-book record")
	st, ok := bodyOf[StatementBody](read.Record.Body)
	require.True(t, ok)
	rd, err := Digest(ruling)
	require.NoError(t, err)
	assert.Equal(t, rd, st.Names.Ruling.Digest)
	assert.Equal(t, h.agreementID, read.Prev)

	again, err := NameRulings(ctx, h.seller.shelf, []string{fxDealID}, h.seller.now)
	require.NoError(t, err)
	assert.Empty(t, again, "named once")
	other, err := NameRulings(ctx, h.seller.shelf, []string{"deal-ffffffffffffffff"}, h.seller.now)
	require.NoError(t, err)
	assert.Empty(t, other, "another deal's book is passed over")
}

func TestBookEntry(t *testing.T) {
	a := buildAppendixA(t)
	books := appendixABooks(t, a, "appendix-a/buyer-book")
	r := books[1].Record
	_, err := BookEntry(r, "")
	assert.Error(t, err, "past seq 1 a record cites the one before it")
	e, err := BookEntry(r, books[0].RecordID)
	require.NoError(t, err)
	require.Len(t, e.Links, 1)
	assert.Equal(t, evidencebook.Cites, e.Links[0].Type)
	assert.Equal(t, books[0].RecordID, e.Links[0].Target)
	assert.Equal(t, r.Block.RecordType, e.RecordType)
	assert.Equal(t, fxDealID, e.SubjectRef)
	payload, digest, err := Encode(r)
	require.NoError(t, err)
	assert.Equal(t, payload, e.Payloads[0])
	d, err := Digest(r)
	require.NoError(t, err)
	assert.Equal(t, d, digest, "the one payload's digest is the record digest")
	e, err = BookEntry(books[0].Record, "")
	require.NoError(t, err)
	assert.Empty(t, e.Links, "the agreement at seq 1 cites nothing")
}
