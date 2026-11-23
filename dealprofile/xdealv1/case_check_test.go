// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const caseBuyerLog = "deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0"

// caseFixture is a started deal naming an adjudicator that checkpoints with
// its record key: the buyer holds both agreements, sealed a dispute on
// as_described at seq 2, and wrote its adjudicator copy. It returns the
// handshake, the adjudicator, the dispute's pair and the buyer's copy.
func caseFixture(t *testing.T) (*handshake, *party, []byte, CaseCopy) {
	t.Helper()
	return caseFixtureWith(t, nil)
}

// caseFixtureWith is caseFixture with the buyer's agreement edited first.
func caseFixtureWith(t *testing.T, edit func(*AgreeBody)) (*handshake, *party, []byte, CaseCopy) {
	t.Helper()
	h, adjudicator, _, _ := caseBookHolder(t)
	od, err := Digest(h.offer)
	require.NoError(t, err)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	adj := fxAdjudicator
	adj.KeyID = adjudicator.key()
	ab.Adjudicator = Some(adj)
	ab.Forums = []string{fxForum}
	if edit != nil {
		edit(ab)
	}
	bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	bb.Seq, bb.Seen = 1, Some(h.agreementID)
	agreement := Record{Block: bb, Body: ab}
	h.buyer.seal(caseBuyerLog, agreement)
	h.buyer.recUseStatus(map[string][]byte{od: h.offerPair})
	h.buyer.receive(h.agreementPair, &h.proof)

	ad, err := Digest(agreement)
	require.NoError(t, err)
	db := journalBlock(TypeDispute, "2026-11-02T10:00:00Z", true)
	db.Seq, db.Seen = 2, Some(h.agreementID)
	db.Prev, db.AgreementRef = ref(0), ref(0)
	*db.Prev, *db.AgreementRef = RefTo(ad), RefTo(ad)
	dispute := Record{Block: db, Body: &DisputeBody{Kind: KindDispute, Row: "as_described", Statements: []Ref{}, Adjudicator: adjudicator.key(), Forum: fxForum}}
	require.NoError(t, Check(dispute))
	require.NoError(t, CheckDispute(dispute, agreement))
	_, disputePair := h.buyer.seal(caseBuyerLog, dispute)
	_, err = h.buyer.recStampTick()
	require.NoError(t, err)
	_, pair, x := holderBundle(t, h.buyer, "buyer", caseBuyerLog, copyAt(h.buyer.now.Format(time.RFC3339), dealprofile.AudienceAdjudicator))
	return h, adjudicator, disputePair, CaseCopy{Report: pair, Extension: extJSON(t, x)}
}

// A dispute and one adjudicator copy are a case: the other side's key comes
// from the copy's agreement, the side without a copy was not read, and the
// case log is the deal's.
func TestCheckCase(t *testing.T) {
	h, adjudicator, dispute, buyerCopy := caseFixture(t)
	c, err := CheckCase(dispute, []CaseCopy{buyerCopy}, adjudicator.key())
	require.NoError(t, err)
	assert.Equal(t, fxDealID, c.DealID)
	assert.Equal(t, "as_described", c.Row)
	assert.Equal(t, RulingKeys{Buyer: h.buyer.key(), Seller: h.seller.key()}, c.Keys)
	require.NotNil(t, c.Read.Buyer.V)
	assert.Nil(t, c.Read.Seller.V, "no seller copy: not read")
	assert.Equal(t, fxForum, c.Forum)
	assert.Equal(t, caseLogOf(t, h, adjudicator), c.LogID)

	// Refusals.
	_, err = CheckCase(dispute, []CaseCopy{buyerCopy}, h.seller.key())
	assert.ErrorContains(t, err, "another adjudicator")
	_, err = CheckCase(dispute, nil, adjudicator.key())
	assert.ErrorContains(t, err, "one or two adjudicator copies")
	_, err = CheckCase(dispute, []CaseCopy{buyerCopy, buyerCopy}, adjudicator.key())
	assert.ErrorContains(t, err, "both copies are the buyer's")
	_, err = CheckCase(h.agreementPair, []CaseCopy{buyerCopy}, adjudicator.key())
	assert.ErrorContains(t, err, "not a dispute")

	other := buyerCopy
	_, err = CheckCase(dispute, []CaseCopy{other, other}, adjudicator.key())
	assert.ErrorContains(t, err, "both copies")

	// A keep copy is not an adjudicator copy.
	_, keepPair, keepX := holderBundle(t, h.buyer, "buyer", caseBuyerLog, keepCopy(h.buyer.now.Format(time.RFC3339)))
	_, err = CheckCase(dispute, []CaseCopy{{Report: keepPair, Extension: extJSON(t, keepX)}}, adjudicator.key())
	assert.ErrorContains(t, err, "not an adjudicator copy")

	// A dispute signed by a key that is neither party's is refused.
	stranger := newParty(t, "stranger")
	read, err := VerifyBook(dispute)
	require.NoError(t, err)
	strangerLog := "deal/" + hex.EncodeToString(seed("stranger log")[:16])
	stranger.seal(strangerLog, readBack(t, mustOpen(t, h.buyer, caseBuyerLog), 1).Record)
	_, forged := stranger.seal(strangerLog, read.Record)
	_, err = CheckCase(forged, []CaseCopy{buyerCopy}, adjudicator.key())
	assert.ErrorContains(t, err, "signed by neither party")
}

// SealRuling seals a ruling once: the same body again is the existing
// record, a different final ruling on the row is refused, and a non-final
// one is sealed after it.
func TestSealRuling(t *testing.T) {
	ctx := context.Background()
	_, adjudicator, dispute, buyerCopy := caseFixture(t)
	c, err := CheckCase(dispute, []CaseCopy{buyerCopy}, adjudicator.key())
	require.NoError(t, err)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	body := c.RulingOf(ValueNotMet, Some(RoleSeller), nil, true, "")
	first, err := SealRuling(ctx, adjudicator.shelf, c, body, at)
	require.NoError(t, err)
	assert.False(t, first.Existing)
	assert.Equal(t, uint64(1), first.Seq)
	assert.Equal(t, first.RecordID, first.BookID)
	read, err := VerifyBook(first.Pair)
	require.NoError(t, err)
	assert.Equal(t, adjudicator.key(), read.KeyID)

	again, err := SealRuling(ctx, adjudicator.shelf, c, body, at.Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, again.Existing)
	assert.Equal(t, first.Digest, again.Digest)

	_, err = SealRuling(ctx, adjudicator.shelf, c, c.RulingOf(ValueMet, NullOf[string](), nil, true, ""), at)
	assert.ErrorIs(t, err, ErrRulingConflict)

	// After a final ruling the row takes no other ruling, final or not: the
	// first final ruling applies and is never replaced.
	_, err = SealRuling(ctx, adjudicator.shelf, c, c.RulingOf(ValueMet, NullOf[string](), nil, false, ""), at)
	assert.ErrorIs(t, err, ErrRulingConflict)

	// A body that is not the case's is refused before anything is sealed.
	other := c.RulingOf(ValueMet, NullOf[string](), nil, false, "")
	other.Row = "delivered"
	_, err = SealRuling(ctx, adjudicator.shelf, c, other, at)
	assert.ErrorContains(t, err, "the case on row as_described")
	other = c.RulingOf(ValueMet, NullOf[string](), nil, false, "")
	other.Keys.Seller = adjudicator.key()
	_, err = SealRuling(ctx, adjudicator.shelf, c, other, at)
	assert.ErrorContains(t, err, "other keys")
	other = c.RulingOf(ValueMet, NullOf[string](), nil, false, "")
	other.Read.Seller = Some("someone else's record")
	_, err = SealRuling(ctx, adjudicator.shelf, c, other, at)
	assert.ErrorContains(t, err, "read")
	other = c.RulingOf(ValueMet, NullOf[string](), nil, false, "")
	other.RulesDigest = hx('7')
	_, err = SealRuling(ctx, adjudicator.shelf, c, other, at)
	assert.ErrorContains(t, err, "other rules")
}

// Before any final ruling, a non-final ruling is sealed, and a later one
// follows it; a forum the agreement does not accept is refused.
func TestSealRulingNonFinal(t *testing.T) {
	ctx := context.Background()
	_, adjudicator, dispute, buyerCopy := caseFixture(t)
	c, err := CheckCase(dispute, []CaseCopy{buyerCopy}, adjudicator.key())
	require.NoError(t, err)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	first, err := SealRuling(ctx, adjudicator.shelf, c, c.RulingOf(ValueNotMet, Some(RoleSeller), nil, false, ""), at)
	require.NoError(t, err)
	_, err = SealRuling(ctx, adjudicator.shelf, c, c.RulingOf(ValueMet, NullOf[string](), nil, false, "external:elsewhere"), at)
	assert.ErrorContains(t, err, "not one the agreement accepts")
	later, err := SealRuling(ctx, adjudicator.shelf, c, c.RulingOf(ValueMet, NullOf[string](), nil, true, ""), at)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), later.Seq)
	assert.Equal(t, first.RecordID, later.BookID)
}

// sellerCopy has the seller of caseFixture hold the buyer's agreement with
// its stamp proof and write its adjudicator copy.
func sellerCopy(t *testing.T, h *handshake) CaseCopy {
	t.Helper()
	buyerBook := mustOpen(t, h.buyer, caseBuyerLog)
	agreementPair := bookPairAt(t, buyerBook, 1)
	proof := h.buyer.stamp(readBack(t, buyerBook, 1).RecordID)
	od, err := Digest(h.offer)
	require.NoError(t, err)
	_, err = h.seller.shelf.Journal(context.Background()) // a report reads the offer's journal
	require.NoError(t, err)
	h.seller.recUseStatus(map[string][]byte{od: h.offerPair})
	h.seller.receive(agreementPair, &proof)
	_, err = h.seller.recStampTick()
	require.NoError(t, err)
	_, pair, x := holderBundle(t, h.seller, "seller", h.sellerLog, copyAt(h.seller.now.Format(time.RFC3339), dealprofile.AudienceAdjudicator))
	return CaseCopy{Report: pair, Extension: extJSON(t, x)}
}

// Both sides' copies make a case read from both books, whichever order they
// come in; a copy under a key the other agreement does not name is refused.
func TestCheckCaseTwoCopies(t *testing.T) {
	h, adjudicator, dispute, buyerCopy := caseFixture(t)
	seller := sellerCopy(t, h)
	c, err := CheckCase(dispute, []CaseCopy{buyerCopy, seller}, adjudicator.key())
	require.NoError(t, err)
	require.NotNil(t, c.Read.Buyer.V)
	require.NotNil(t, c.Read.Seller.V)
	assert.Equal(t, RulingKeys{Buyer: h.buyer.key(), Seller: h.seller.key()}, c.Keys)
	reversed, err := CheckCase(dispute, []CaseCopy{seller, buyerCopy}, adjudicator.key())
	require.NoError(t, err)
	assert.Equal(t, c, reversed, "the order of the copies does not matter")

	// The seller's copy alone: the dispute is the buyer's, so only its forum
	// is read against the seller's agreement.
	alone, err := CheckCase(dispute, []CaseCopy{seller}, adjudicator.key())
	require.NoError(t, err)
	assert.Nil(t, alone.Read.Buyer.V)
	assert.Equal(t, h.buyer.key(), alone.Keys.Buyer)

}

// Two copies must name each other: each agreement names exactly the other
// copy's signer as its layered counterparty; an agreement with no layer
// names nobody, so a second copy beside it is refused (CheckCase's
// cross-check).
func TestNamesCounterparty(t *testing.T) {
	yes := true
	no := false
	named := AgreeBody{CounterpartyLayer: &yes, CounterpartyKey: Some("seller-key")}
	assert.True(t, namesCounterparty(named, "seller-key"))
	assert.False(t, namesCounterparty(named, "stranger-key"), "a copy under a key the other agreement does not name")
	assert.False(t, namesCounterparty(AgreeBody{CounterpartyLayer: &no, CounterpartyKey: NullOf[string]()}, "any"), "no layer: no second book")
	assert.False(t, namesCounterparty(AgreeBody{}, "any"))
}

// CheckCaseRecord takes a party's withdrawal and a challenge of a ruling the
// case book holds, and refuses a challenge of any other ruling and a record
// signed by neither party.
func TestCheckCaseRecord(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, dispute, buyerCopy := caseFixture(t)
	c, err := CheckCase(dispute, []CaseCopy{buyerCopy}, adjudicator.key())
	require.NoError(t, err)
	sealed, err := SealRuling(ctx, adjudicator.shelf, c, c.RulingOf(ValueNotMet, Some(RoleSeller), nil, true, ""), time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	read, err := VerifyBook(dispute)
	require.NoError(t, err)
	disputeDigest, err := Digest(read.Record)
	require.NoError(t, err)
	challenge := func(ruling string) Record {
		b := read.Record.Block
		b.Seq = 3
		prev := RefTo(disputeDigest)
		b.Prev = &prev
		r := RefTo(ruling)
		return Record{Block: b, Body: &DisputeBody{Kind: KindChallenge, Ruling: &r, GroundsCommitment: hx('9')}}
	}
	_, pair := h.buyer.seal(caseBuyerLog, challenge(sealed.Digest))
	got, err := CheckCaseRecord(pair, c, []string{sealed.Digest})
	require.NoError(t, err)
	assert.Equal(t, KindChallenge, got.Record.Body.(*DisputeBody).Kind)

	_, err = CheckCaseRecord(pair, c, nil)
	assert.ErrorContains(t, err, "does not hold")

	stranger := newParty(t, "stranger")
	strangerLog := "deal/" + hex.EncodeToString(seed("stranger log")[:16])
	buyerBook := mustOpen(t, h.buyer, caseBuyerLog)
	stranger.seal(strangerLog, readBack(t, buyerBook, 1).Record)
	stranger.seal(strangerLog, readBack(t, buyerBook, 2).Record)
	_, forged := stranger.seal(strangerLog, challenge(sealed.Digest))
	_, err = CheckCaseRecord(forged, c, []string{sealed.Digest})
	assert.ErrorContains(t, err, "signed by neither party")

	_, err = CheckCaseRecord(dispute, c, nil)
	assert.ErrorContains(t, err, "neither a withdrawal nor a challenge")

	// A withdrawal of the case's row is taken; one of another row is not.
	prevDigest, seq := "", int64(3)
	if r, err := VerifyBook(pair); assert.NoError(t, err) {
		prevDigest, _ = Digest(r.Record)
	}
	withdrawal := func(row string) []byte {
		b := read.Record.Block
		seq++
		b.Seq = seq
		prev := RefTo(prevDigest)
		b.Prev = &prev
		rec := Record{Block: b, Body: &DisputeBody{Kind: KindWithdrawal, Row: row}}
		_, wp := h.buyer.seal(caseBuyerLog, rec)
		prevDigest, _ = Digest(rec)
		return wp
	}
	_, err = CheckCaseRecord(withdrawal("as_described"), c, nil)
	require.NoError(t, err)
	_, err = CheckCaseRecord(withdrawal("delivered"), c, nil)
	assert.ErrorContains(t, err, "the withdrawal is of row delivered")

	other := c
	other.DealID = "deal-ffffffffffffffff"
	_, err = CheckCaseRecord(pair, other, []string{sealed.Digest})
	assert.ErrorContains(t, err, "the case of deal deal-ffffffffffffffff")
}

func mustOpen(t *testing.T, p *party, logID string) *evidencebook.Book {
	t.Helper()
	book, err := p.shelf.Open(context.Background(), logID)
	require.NoError(t, err)
	return book
}
