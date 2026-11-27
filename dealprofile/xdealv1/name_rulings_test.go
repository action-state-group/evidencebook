// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A counted ruling of the deal's adjudicator that the holder's book does not
// name gets a naming statement sealed by the receive call itself, after the
// record before it, once (PROFILE §5; evidencebook#527); the sealed record
// verifies as a book pair, and a later call seals nothing.
func testNameRulings(t *testing.T) {
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
	caseLog := caseLogOf(t, h, adjudicator)
	_, pair := adjudicator.seal(caseLog, ruling)
	res := h.seller.receive(pair, nil)
	require.True(t, res.Record.Stored)
	assert.Contains(t, res.Changes.Grew, fxDealID)
	h.seller.recBook(h.sellerLog)

	// The receive call itself named it, before it returned: a holder ruled
	// against cannot skip it by not calling anything.
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	require.Equal(t, uint64(2), book.Size(), "the ruling against the seller, named in the seller's own book by Receive")
	recs, err := book.Query(ctx, evidencebook.Filter{FromSeq: 2, ToSeq: 2})
	require.NoError(t, err)
	assert.Equal(t, dealprofile.StepRecordType, recs[0].Header.RecordType, "the sealed header's record_type is deal_step (design §3.1)")
	assert.Equal(t, evidencebook.ProducerClaim, recs[0].Header.EpistemicType)
	sealed, err := book.Sealed(ctx, recs[0].RecordID)
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

	again, err := h.seller.recNameRulings([]string{fxDealID})
	require.NoError(t, err)
	assert.Empty(t, again, "named once: a later call, or the next Receive, seals nothing")
	h.seller.receive(pair, nil)
	assert.Equal(t, uint64(2), book.Size(), "the same ruling again names nothing")
	h.seller.recBook(h.sellerLog)
	other, err := h.seller.recNameRulings([]string{"deal-ffffffffffffffff"})
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
	assert.Equal(t, dealprofile.StepRecordType, e.RecordType, "the header's record_type is deal_step (design §3.1)")
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

// rulingCase is a handshake whose agreements name an adjudicator, the
// adjudicator, and a final ruling of the deal against the seller.
func rulingCase(t *testing.T) (*handshake, *party, Record, string) {
	t.Helper()
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
	return h, adjudicator, ruling, caseLogOf(t, h, adjudicator)
}

// readBack is the book's record at seq, verified as a book pair.
func readBack(t *testing.T, book *evidencebook.Book, seq uint64) BookRead {
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
	read, err := VerifyBook(pair)
	require.NoError(t, err)
	return read
}

// The naming statement's block: after the agreement, bound to it, and with
// seen the last counted record of the counterparty's book (the buyer holds
// the seller's agreement here).
func testNameRulingsBlock(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	od, err := Digest(h.offer)
	require.NoError(t, err)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	adj := fxAdjudicator
	adj.KeyID = adjudicator.key()
	ab.Adjudicator = Some(adj)
	bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	bb.Seq, bb.Seen = 1, Some(h.agreementID)
	buyerAg := Record{Block: bb, Body: ab}
	require.NoError(t, Check(buyerAg))
	h.buyer.seal("deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0", buyerAg)
	h.buyer.receive(h.agreementPair, &h.proof)
	_, pair := adjudicator.seal(caseLog, ruling)
	h.buyer.receive(pair, nil)
	h.buyer.recBook("deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0")
	book, err := h.buyer.shelf.Open(ctx, "deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0")
	require.NoError(t, err)
	require.Equal(t, uint64(2), book.Size())
	read := readBack(t, book, 2)
	agDigest, err := Digest(buyerAg)
	require.NoError(t, err)
	require.NotNil(t, read.Record.Block.Prev)
	assert.Equal(t, agDigest, read.Record.Block.Prev.Digest)
	require.NotNil(t, read.Record.Block.AgreementRef)
	assert.Equal(t, agDigest, read.Record.Block.AgreementRef.Digest)
	require.NotNil(t, read.Record.Block.Seen.V)
	assert.Equal(t, h.agreementID, *read.Record.Block.Seen.V, "seen: the seller's agreement, the last of its counted prefix")
	assert.Equal(t, int64(2), read.Record.Block.Seq)
}

// A write landing between the read and the append moves the statement to
// the next place: it is built again, and lands valid.
func TestNameRulingsAfterAConcurrentWrite(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	once := false
	nameRulingsBeforeAppend = func() error {
		if once {
			return nil
		}
		once = true
		next := settleRecord(t, 2, h.agreement, h.agreement, h.agreementID)
		e, err := BookEntry(next, h.agreementID)
		require.NoError(t, err)
		_, err = book.Append(ctx, e)
		require.NoError(t, err)
		return nil
	}
	t.Cleanup(func() { nameRulingsBeforeAppend = nil })
	_, pair := adjudicator.seal(caseLog, ruling)
	h.seller.receive(pair, nil)
	require.True(t, once)
	require.Equal(t, uint64(3), book.Size(), "the host's record, then the naming statement")
	read := readBack(t, book, 3)
	assert.Equal(t, int64(3), read.Record.Block.Seq, "built again for its place")
	_, ok := bodyOf[StatementBody](read.Record.Body)
	assert.True(t, ok)
}

// A book with a record not readable whole (here a lifecycle record a
// retention change appends) is left for later: nothing invalid is sealed.
func testNameRulingsLeavesAPartBook(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	next := settleRecord(t, 2, h.agreement, h.agreement, h.agreementID)
	h.seller.seal(h.sellerLog, next)
	h.seller.recRetentionIn(h.sellerLog, 2, evidencebook.Withheld)
	size := book.Size()
	_, pair := adjudicator.seal(caseLog, ruling)
	h.seller.receive(pair, nil)
	assert.Equal(t, size, book.Size(), "no statement from a part read")
	h.seller.recBook(h.sellerLog)
}

// A ruling of another deal in the same case log is not this deal's.
func testNameRulingsOnlyTheDealsRulings(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	other := ruling
	other.Block.DealID = "deal-ffffffffffffffff"
	_, pair := adjudicator.seal(caseLog, other)
	h.seller.receiveRaw(pair, nil) //nolint:errcheck // refused or stored, it is not this deal's
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), book.Size())
	h.seller.recBook(h.sellerLog)
}

// unnamedCount is the seller's count of counted rulings neither book names,
// as its status provider reads it.
func unnamedCount(t *testing.T, h *handshake, adjudicator *party) int {
	t.Helper()
	ctx := context.Background()
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	own, whole, err := readOwnBook(ctx, book)
	require.NoError(t, err)
	require.True(t, whole)
	held, err := h.seller.shelf.HeldByDeal(ctx, fxDealID)
	require.NoError(t, err)
	_, _, n, err := caseBook(ctx, h.seller.shelf, held, adjudicator.key(), own, nil)
	require.NoError(t, err)
	return n
}

// A naming that fails is Receive's error, the ruling stays held, and the
// status value counts it unnamed; the same ruling sent again (a duplicate,
// so nothing grew) names it (Codex, the stand-in and Sherlock2, #528 round
// 2), and the count is back to zero.
func testNameRulingsRetriedOnADuplicate(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	recNamingFails(t, true)
	_, pair := adjudicator.seal(caseLog, ruling)
	res, err := h.seller.receiveRaw(pair, nil)
	require.ErrorContains(t, err, "after receive")
	require.NotNil(t, res.Record)
	assert.True(t, res.Record.Stored, "the ruling stays committed")
	assert.Equal(t, fxDealID, res.Record.DealID)
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), book.Size())
	assert.Equal(t, 1, unnamedCount(t, h, adjudicator), "held, counted, named by neither book")

	h.seller.recBook(h.sellerLog)
	recNamingFails(t, false)
	again := h.seller.receive(pair, nil)
	assert.True(t, again.Record.Duplicate)
	assert.Empty(t, again.Changes.Grew, "nothing grew")
	assert.Equal(t, uint64(2), book.Size(), "the duplicate retried the naming")
	h.seller.recBook(h.sellerLog)
	assert.Equal(t, 0, unnamedCount(t, h, adjudicator))
}

// NameAllRulings is the host's sweep: a naming that failed, with nothing
// sent again, is sealed by it; a second sweep seals nothing.
func testNameAllRulings(t *testing.T) {
	h, adjudicator, ruling, caseLog := rulingCase(t)
	recNamingFails(t, true)
	_, pair := adjudicator.seal(caseLog, ruling)
	_, err := h.seller.receiveRaw(pair, nil)
	require.Error(t, err)
	recNamingFails(t, false)
	sealed, err := h.seller.recNameRulings(nil)
	require.NoError(t, err)
	assert.Len(t, sealed, 1)
	assert.Equal(t, 0, unnamedCount(t, h, adjudicator))
	h.seller.recBook(h.sellerLog)
	sealed, err = h.seller.recNameRulings(nil)
	require.NoError(t, err)
	assert.Empty(t, sealed)
}

// The status value carries the count, with no ids, and leaves it out at
// zero, so a value with every ruling named is unchanged.
func TestStatusRulingsUnnamed(t *testing.T) {
	_, base := appendixAStatus(t)
	v, err := BuildStatus(base)
	require.NoError(t, err)
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "rulings_unnamed")
	base.RulingsUnnamed = 2
	v, err = BuildStatus(base)
	require.NoError(t, err)
	assert.Equal(t, 2, v.RulingsUnnamed)
	raw, err = json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"rulings_unnamed":2`)
}

// A naming that failed, with nothing received after it, is sealed by the
// holder's next stamp (the coordinator and Sherlock2, #528 round 2): the
// status provider's BeforeStamp runs before the stamp takes its values, so
// that stamp's status value counts no ruling unnamed.
func testNameRulingsAtTheNextStamp(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	h.seller.recUseStatus(nil)
	recNamingFails(t, true)
	_, pair := adjudicator.seal(caseLog, ruling)
	_, err := h.seller.receiveRaw(pair, nil)
	require.ErrorContains(t, err, "after receive")
	assert.Equal(t, 1, unnamedCount(t, h, adjudicator))

	recNamingFails(t, false)
	res, err := h.seller.recStampTick()
	require.NoError(t, err)
	assert.Empty(t, res.BeforeStamp)
	assert.Equal(t, 0, unnamedCount(t, h, adjudicator), "the stamp named it, no Receive needed")
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), book.Size())
	h.seller.recBook(h.sellerLog)
	facts := h.seller.recOpenings(h.agreementID, evidencebook.AudienceCounterparty)
	require.NotEmpty(t, facts)
	last := facts[len(facts)-1]
	require.NotNil(t, last.StatusOpening)
	assert.NotContains(t, string(last.StatusOpening.Value), "rulings_unnamed", "that stamp's value reads the ruling named")
}

// A book not readable whole (a retention change appended a lifecycle
// record, which no x-deal-v1 record can follow at its seq) is never written
// to, by Receive or by a stamp; the stamp's status value, read over the
// book's v1 prefix, counts the ruling unnamed, so the card does not show
// the dispute as unresolved.
func testNameRulingsUnreadableBookIsCounted(t *testing.T) {
	ctx := context.Background()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	h.seller.recUseStatus(nil)
	book, err := h.seller.shelf.Open(ctx, h.sellerLog)
	require.NoError(t, err)
	h.seller.seal(h.sellerLog, settleRecord(t, 2, h.agreement, h.agreement, h.agreementID))
	h.seller.recRetentionIn(h.sellerLog, 2, evidencebook.Withheld)
	size := book.Size()
	_, pair := adjudicator.seal(caseLog, ruling)
	h.seller.receive(pair, nil)
	res, err := h.seller.recStampTick()
	require.NoError(t, err)
	assert.Empty(t, res.BeforeStamp)
	assert.Equal(t, size, book.Size(), "nothing sealed into a book not readable whole")
	facts := h.seller.recOpenings(h.agreementID, evidencebook.AudienceCounterparty)
	require.NotEmpty(t, facts)
	last := facts[len(facts)-1]
	require.NotNil(t, last.StatusOpening)
	var v StatusValue
	require.NoError(t, json.Unmarshal(last.StatusOpening.Value, &v))
	assert.Equal(t, 1, v.RulingsUnnamed, "flagged, not shown open silently")
}
