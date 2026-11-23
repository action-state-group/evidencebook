// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The case log id is one per offer and adjudicator, hides the deal id, and
// is the prefix and 32 hex characters.
func TestCaseLogID(t *testing.T) {
	offer, other := strings.Repeat("a", 64), strings.Repeat("b", 64)
	key, otherKey := strings.Repeat("1", 64), strings.Repeat("2", 64)
	id := CaseLogID(offer, key)
	assert.Equal(t, id, CaseLogID(offer, key))
	assert.True(t, strings.HasPrefix(id, CaseLogPrefix))
	assert.Len(t, strings.TrimPrefix(id, CaseLogPrefix), 32)
	_, err := hex.DecodeString(strings.TrimPrefix(id, CaseLogPrefix))
	assert.NoError(t, err)
	assert.NotEqual(t, id, CaseLogID(other, key), "another offer")
	assert.NotEqual(t, id, CaseLogID(offer, otherKey), "another adjudicator")
	// The parts are separated: moving a character across the boundary changes the id.
	assert.NotEqual(t, CaseLogID("ab", "c"), CaseLogID("a", "bc"))
	// A pinned value, so an implementation in another language can check its own.
	assert.Equal(t, "case/fe02461334fc17ef81a8494d07d28608", id)
}

// A ruling from the agreement's adjudicator on any log but the deal's case
// log is refused, so a ruling counts only from that log.
func TestRulingOnAnotherLogIsRefused(t *testing.T) {
	h, adjudicator, ruling, _ := rulingCase(t)
	_, pair := adjudicator.seal("case/"+hex.EncodeToString(seed("another case log")[:16]), ruling)
	_, err := h.seller.receiveRaw(pair, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused)
}

// caseBookHolder is a handshake whose agreement names an adjudicator that
// checkpoints with its record key, and that adjudicator's final ruling
// against the seller, ready to seal on the deal's case log.
func caseBookHolder(t *testing.T) (*handshake, *party, Record, string) {
	t.Helper()
	adjudicator := newAdjudicator(t, "adjudicator")
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
	return h, adjudicator, ruling, caseLogOf(t, h, adjudicator)
}

// A case book's stamp proof is bound to the adjudicator key the agreement
// names: the adjudicator checkpoints with that key, and the holder keeps the
// proof beside the ruling.
func TestCaseBookProofBindsToTheAdjudicatorKey(t *testing.T) {
	h, adjudicator, ruling, caseLog := caseBookHolder(t)
	id, pair := adjudicator.seal(caseLog, ruling)
	proof := adjudicator.stamp(id)
	res := h.seller.receive(pair, &proof)
	require.NotNil(t, res.Proof)
	assert.True(t, res.Proof.Accepted, "refused: %v", res.Proof.Refused)
}

// A case book proof under a checkpoint key that is not the adjudicator's
// record key binds to nothing and is refused; the ruling itself is kept.
func TestCaseBookProofUnderAnotherKeyIsRefused(t *testing.T) {
	adjudicator := newParty(t, "adjudicator") // checkpoints with a key of its own
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
	id, pair := adjudicator.seal(caseLogOf(t, h, adjudicator), ruling)
	proof := adjudicator.stamp(id)
	_, err := h.seller.receiveRaw(pair, &proof)
	assert.ErrorIs(t, err, evidencebook.ErrRefused)
	assert.ErrorContains(t, err, "may not push a stamp proof")
	// The ruling itself was kept: sent again without the proof, it is held.
	res := h.seller.receive(pair, nil)
	require.NotNil(t, res.Record)
	assert.True(t, res.Record.Stored || res.Record.Duplicate)
	assert.True(t, res.Record.Counted)
}

// A judge that gives each side another ruling at the same place of the case
// log shows as a fork once one side forwards what it received: the holder
// then holds both, its proofs for that log are refused, and the row has no
// effective ruling (conflicting_final_rulings).
func TestForwardedRulingShowsAFork(t *testing.T) {
	h, adjudicator, ruling, caseLog := caseBookHolder(t)
	ctx := context.Background()
	od, err := Digest(h.offer)
	require.NoError(t, err)
	// The buyer holds both agreements, so its deal has started (rulingHolder).
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	adj := fxAdjudicator
	adj.KeyID = adjudicator.key()
	ab.Adjudicator = Some(adj)
	bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	bb.Seq, bb.Seen = 1, Some(h.agreementID)
	buyerLog := "deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0"
	h.buyer.seal(buyerLog, Record{Block: bb, Body: ab})
	h.buyer.recUseStatus(map[string][]byte{od: h.offerPair})
	h.buyer.receive(h.agreementPair, &h.proof)

	idA, pairA := adjudicator.seal(caseLog, ruling)
	proofA := adjudicator.stamp(idA)
	res := h.buyer.receive(pairA, &proofA)
	require.True(t, res.Proof.Accepted, "refused: %v", res.Proof.Refused)
	_, err = h.buyer.recStampTick()
	require.NoError(t, err)
	r, err := h.buyer.status.Report(ctx, buyerLog, keepCopy(h.buyer.now.Format("2006-01-02T15:04:05Z")))
	require.NoError(t, err)
	require.NotNil(t, rowOf(t, r, "as_described").Ruling, "one ruling held: it is effective")

	// The seller got ruling B at the same place, and forwards it.
	other := *ruling.Body.(*RulingBody)
	other.Against, other.Value = Some(RoleBuyer), ValueMet
	_, pairB := adjudicator.sealOnNewShelf(caseLog, Record{Block: ruling.Block, Body: &other})
	h.buyer.receive(pairB, nil)
	_, err = h.buyer.recStampTick()
	require.NoError(t, err)
	r, err = h.buyer.status.Report(ctx, buyerLog, keepCopy(h.buyer.now.Format("2006-01-02T15:04:05Z")))
	require.NoError(t, err)
	assert.Nil(t, rowOf(t, r, "as_described").Ruling, "two different first final rulings: none is effective")

	// A forked case book makes no checkpoint claim.
	sender, ok, err := h.buyer.profile.ProofSender(ctx, adjudicator.key(), caseLog)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, sender)
}

func rowOf(t *testing.T, r Report, id string) ReportRow {
	t.Helper()
	for _, row := range r.Rows {
		if row.ID == id {
			return row
		}
	}
	require.FailNow(t, "no row "+id)
	return ReportRow{}
}

// bookPairAt is the book pair of the record at seq of book, as it is sent.
func bookPairAt(t *testing.T, book *evidencebook.Book, seq uint64) []byte {
	t.Helper()
	ctx := context.Background()
	recs, err := book.Query(ctx, evidencebook.Filter{FromSeq: seq, ToSeq: seq})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	rec, kind, err := v1Payload(ctx, book, recs[0])
	require.NoError(t, err)
	require.Equal(t, payloadV1, kind)
	payload, _, err := Encode(rec)
	require.NoError(t, err)
	sealed, err := book.Sealed(ctx, recs[0].RecordID)
	require.NoError(t, err)
	pair, err := BookPair(sealed, payload)
	require.NoError(t, err)
	return pair
}

// The adjudicator, once it ruled on a deal, takes each party's book: the
// agreement with its stamp proof, and the statement naming its ruling, which
// is what it checks to see the ruling entered. Before it ruled, or for a deal
// it did not rule on, it takes nothing.
func TestAdjudicatorTakesThePartiesBooksAfterItsRuling(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := caseBookHolder(t)

	// Not yet ruled: the seller's agreement is refused.
	_, err := adjudicator.receiveRaw(h.agreementPair, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused, "no case book yet")

	_, pair := adjudicator.seal(caseLog, ruling)
	h.seller.receive(pair, nil) // names the ruling at seq 2 of the seller's book
	sellerBook, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	require.Equal(t, uint64(2), sellerBook.Size())

	res := adjudicator.receive(h.agreementPair, &h.proof)
	require.NotNil(t, res.Record)
	assert.True(t, res.Record.Stored)
	require.NotNil(t, res.Proof)
	assert.True(t, res.Proof.Accepted, "refused: %v", res.Proof.Refused)
	naming := adjudicator.receive(bookPairAt(t, sellerBook, 2), nil)
	require.NotNil(t, naming.Record)
	assert.True(t, naming.Record.Stored)
	read, err := VerifyBook(bookPairAt(t, sellerBook, 2))
	require.NoError(t, err)
	st, ok := bodyOf[StatementBody](read.Record.Body)
	require.True(t, ok)
	require.NotNil(t, st.Names)
	rd, err := Digest(ruling)
	require.NoError(t, err)
	assert.Equal(t, rd, st.Names.Ruling.Digest, "the seller entered this ruling")

	// The buyer's agreement and its proof are taken too: either party the
	// ruling names.
	od, err := Digest(h.offer)
	require.NoError(t, err)
	buyerAgreement := func(p *party) Record {
		ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
		ab.CheckpointKey, ab.ShelfLogID = p.checkpointKey(), testCadenceLog
		adj := fxAdjudicator
		adj.KeyID = adjudicator.key()
		ab.Adjudicator = Some(adj)
		bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
		bb.Seq, bb.Seen = 1, Some(h.agreementID)
		return Record{Block: bb, Body: ab}
	}
	buyerID, buyerPair := h.buyer.seal("deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0", buyerAgreement(h.buyer))
	buyerProof := h.buyer.stamp(buyerID)
	res = adjudicator.receive(buyerPair, &buyerProof)
	require.NotNil(t, res.Record)
	assert.True(t, res.Record.Stored)
	assert.True(t, res.Proof.Accepted, "refused: %v", res.Proof.Refused)

	// A third key's agreement of the same deal, naming the same adjudicator,
	// is no party's: refused, and so is its proof.
	intruder := newParty(t, "intruder")
	intruderID, intruderPair := intruder.seal("deal/c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0", buyerAgreement(intruder))
	intruderProof := intruder.stamp(intruderID)
	_, err = adjudicator.receiveRaw(intruderPair, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused, "a key the ruling does not name")
	_, err = adjudicator.receiveRaw(intruderPair, &intruderProof)
	assert.ErrorIs(t, err, evidencebook.ErrRefused)

	// Another adjudicator, which ruled on nothing, takes none of it.
	stranger := newAdjudicator(t, "another adjudicator")
	_, err = stranger.receiveRaw(h.agreementPair, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused)
}

// HeldRuling finds a received ruling by the record digest a statement
// names, and reports one the holder does not hold as unavailable.
func TestHeldRuling(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := caseBookHolder(t)
	rd, err := Digest(ruling)
	require.NoError(t, err)
	_, found, err := HeldRuling(ctx, h.seller.shelf, fxDealID, adjudicator.key(), rd)
	require.NoError(t, err)
	assert.False(t, found, "not received yet: unavailable")

	_, pair := adjudicator.seal(caseLog, ruling)
	h.seller.receive(pair, nil)
	read, found, err := HeldRuling(ctx, h.seller.shelf, fxDealID, adjudicator.key(), rd)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, TypeRuling, read.Record.Block.RecordType)

	_, found, err = HeldRuling(ctx, h.seller.shelf, fxDealID, h.buyer.key(), rd)
	require.NoError(t, err)
	assert.False(t, found, "from another key")
	_, found, err = HeldRuling(ctx, h.seller.shelf, "deal-ffffffffffffffff", adjudicator.key(), rd)
	require.NoError(t, err)
	assert.False(t, found, "of another deal")
}
