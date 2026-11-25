// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/internal/shelfscript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fourBooks is a deal where both sides keep a book, each stamp of each side
// witnessed at a fixed time by the witness both agreements name:
//
//	the seller seals its agreement (S1) and stamps;
//	the buyer seals its agreement (B1, seen S1), receives S1, stamps at 10:00;
//	the seller receives B1, seals a delivery (S2, seen B1), stamps at 11:00;
//	the buyer receives S2 with its proof, stamps at 12:00 (its link reaches S2);
//	the buyer seals a dispute of the late delivery (B2, seen S2), stamps at 12:30.
type fourBooks struct {
	h           *handshake
	adjudicator *party
	buyerLog    string
	b1, b2, s2  string
	dispute     []byte
	at          map[string]time.Time
}

// gatherAsOf is the copies' as_of, after every witnessed stamp.
const gatherAsOf = "2026-11-01T14:00:00Z"

var (
	gatherWitness = Witness{KeyID: hex.EncodeToString(shelfscript.WitnessKey.Public().(ed25519.PublicKey)), URL: shelfscript.WitnessURL}
	gatherAt      = func(hm string) time.Time {
		t, err := time.Parse(time.RFC3339, "2026-11-01T"+hm+":00Z")
		if err != nil {
			panic(err)
		}
		return t
	}
)

// witnessedStamp takes a stamp of p and records a receipt for its
// checkpoint registered at iat.
func witnessedStamp(t *testing.T, p *party, iat time.Time) {
	t.Helper()
	ctx := context.Background()
	p.tick()
	res, err := p.cadence.Stamp(ctx, p.now)
	require.NoError(t, err)
	receipt, err := shelfscript.MintReceiptAt(res.Tick.Checkpoint, iat)
	require.NoError(t, err)
	check, err := p.cadence.Witnessed(ctx, res.Tick.N, receipt)
	require.NoError(t, err)
	require.Equal(t, evidencebook.WitnessVerified, check.State, check.Reason)
}

func newFourBooks(t *testing.T) *fourBooks {
	t.Helper()
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	partyWitnesses = dir
	t.Cleanup(func() { partyWitnesses = aacwitness.Directory{} })
	adjudicator := newAdjudicator(t, "adjudicator")
	adj := fxAdjudicator
	adj.KeyID = adjudicator.key()
	h := newHandshake(t, func(a *AgreeBody) {
		a.Adjudicator, a.Witnesses = Some(adj), []Witness{gatherWitness}
	})
	f := &fourBooks{h: h, adjudicator: adjudicator, buyerLog: caseBuyerLog, s2: "", at: map[string]time.Time{}}
	od, err := Digest(h.offer)
	require.NoError(t, err)
	pairs := map[string][]byte{od: h.offerPair}
	h.buyer.recUseStatus(pairs)
	h.seller.recUseStatus(pairs)

	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	ab.Adjudicator, ab.Forums, ab.Witnesses = Some(adj), []string{fxForum}, []Witness{gatherWitness}
	bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	bb.Seq, bb.Seen = 1, Some(h.agreementID)
	buyerAg := Record{Block: bb, Body: ab}
	require.NoError(t, Check(buyerAg))
	var b1Pair []byte
	f.b1, b1Pair = h.buyer.seal(f.buyerLog, buyerAg)
	h.buyer.receive(h.agreementPair, &h.proof)
	witnessedStamp(t, h.buyer, gatherAt("10:00"))
	b1Proof, err := h.buyer.cadence.StampProof(context.Background(), f.b1, 0, evidencebook.AudienceCounterparty)
	require.NoError(t, err)

	h.seller.receive(b1Pair, &b1Proof)
	sd, err := Digest(h.agreement)
	require.NoError(t, err)
	pb := journalBlock(TypePerform, "2026-11-01T11:00:00Z", true)
	pb.Seq, pb.Seen, pb.Prev, pb.AgreementRef = 2, Some(f.b1), ref(0), ref(0)
	*pb.Prev, *pb.AgreementRef = RefTo(sd), RefTo(sd)
	perform := Record{Block: pb, Body: &PerformBody{Act: "deliver", Observed: "carrier_scan", Statements: statements(item("delivered", ValueMet))}}
	require.NoError(t, Check(perform))
	var s2Pair []byte
	f.s2, s2Pair = h.seller.seal(h.sellerLog, perform)
	witnessedStamp(t, h.seller, gatherAt("11:00"))
	s2Proof, err := h.seller.cadence.StampProof(context.Background(), h.agreementID, 0, evidencebook.AudienceCounterparty)
	require.NoError(t, err)

	h.buyer.receive(s2Pair, &s2Proof)
	witnessedStamp(t, h.buyer, gatherAt("12:00"))
	ad, err := Digest(buyerAg)
	require.NoError(t, err)
	db := journalBlock(TypeDispute, "2026-11-01T12:15:00Z", true)
	db.Seq, db.Seen, db.Prev, db.AgreementRef = 2, Some(f.s2), ref(0), ref(0)
	*db.Prev, *db.AgreementRef = RefTo(ad), RefTo(ad)
	dispute := Record{Block: db, Body: &DisputeBody{Kind: KindDispute, Row: "delivered", Statements: []Ref{}, Adjudicator: adjudicator.key(), Forum: fxForum}}
	require.NoError(t, Check(dispute))
	require.NoError(t, CheckDispute(dispute, buyerAg))
	f.b2, f.dispute = h.buyer.seal(f.buyerLog, dispute)
	witnessedStamp(t, h.buyer, gatherAt("12:30"))
	return f
}

// answer is p's full answer for its deal book logID: its full adjudicator
// copy and its book's evidence, trimmed to the copy.
func (f *fourBooks) answer(t *testing.T, p *party, name, logID string) CaseAnswer {
	t.Helper()
	c := copyAt(gatherAsOf, dealprofile.AudienceAdjudicator)
	c.Full = true
	_, pair, x := holderBundle(t, p, name, logID, c)
	cc := CaseCopy{Report: pair, Extension: extJSON(t, x)}
	_, full, err := VerifyFullCopy(cc.Report, cc.Extension)
	require.NoError(t, err)
	e, err := p.cadence.BookEvidence(context.Background(), full.Own[0].RecordID, 0)
	require.NoError(t, err)
	return CaseAnswer{Copy: cc, Evidence: TrimBookEvidence(e, full)}
}

// Acceptance 5 (evidencebook#861): the buyer disputes a late delivery and
// the seller does not answer. The case reads three sources of four: the
// buyer's own book and the copy of the seller's book the buyer holds. The
// ruling names them and the missing one, and the seller's delivery is timed
// by the buyer's linking fact (12:00), which the case says.
func TestGatherCaseSellerSilent(t *testing.T) {
	f := newFourBooks(t)
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	buyer := f.answer(t, f.h.buyer, "buyer", f.buyerLog)
	g, err := GatherCase(f.dispute, nil, []CaseAnswer{buyer}, f.adjudicator.key(), dir)
	require.NoError(t, err)

	want := RulingSources{
		Buyer:  BuyerSources{Own: Some(f.b2), HeldBySeller: NullOf[string]()},
		Seller: SellerSources{Own: NullOf[string](), HeldByBuyer: Some(f.s2)},
	}
	require.NotNil(t, g.Sources)
	assert.True(t, sameSources(&want, g.Sources), "%+v", *g.Sources)
	assert.Equal(t, f.b2, *g.Read.Buyer.V)
	assert.Equal(t, f.s2, *g.Read.Seller.V)

	seller := g.Seller
	require.Len(t, seller.Book, 2)
	for _, r := range seller.Book {
		assert.Equal(t, f.h.seller.key(), r.Read.KeyID, "a side's book holds only records it signed")
	}
	assert.Equal(t, f.s2, seller.Book[1].Read.RecordID)
	assert.Equal(t, TimedByHolderLink, seller.Book[1].TimedBy)
	assert.Equal(t, "2026-11-01T12:00:00Z", seller.Book[1].At)
	buyerBook := g.Buyer.Book
	require.Len(t, buyerBook, 2)
	assert.Equal(t, TimedByOwnStamp, buyerBook[1].TimedBy)
	assert.Equal(t, "2026-11-01T12:30:00Z", buyerBook[1].At)

	// The ruling carries the sources, and SealRuling seals it.
	body := g.RulingOf(ValueNotMet, Some(RoleSeller), nil, true, "")
	require.NotNil(t, body.Sources)
	rec := Record{Block: journalBlock(TypeRuling, "2026-11-02T10:00:00Z", true), Body: &body}
	rec.Block.Seq = 1
	require.NoError(t, Check(rec), "a ruling with sources passes the schema")
	sealed, err := SealRuling(context.Background(), f.adjudicator.shelf, g.Case, body, gatherAt("13:00"))
	require.NoError(t, err)
	assert.False(t, sealed.Existing)

	// A ruling whose sources are not the case's is refused.
	other := body
	other.Sources = nil
	_, err = SealRuling(context.Background(), f.adjudicator.shelf, g.Case, other, gatherAt("13:00"))
	assert.ErrorContains(t, err, "sources")
}

// Acceptance 1: both sides answer. Each held copy checks against the other
// side's own book; the seller's delivery is timed by its own stamp (11:00),
// earlier than the buyer's linking fact.
func TestGatherCaseBothAnswer(t *testing.T) {
	f := newFourBooks(t)
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	buyer := f.answer(t, f.h.buyer, "buyer", f.buyerLog)
	seller := f.answer(t, f.h.seller, "seller", f.h.sellerLog)
	g, err := GatherCase(f.dispute, nil, []CaseAnswer{buyer, seller}, f.adjudicator.key(), dir)
	require.NoError(t, err)
	want := RulingSources{
		Buyer:  BuyerSources{Own: Some(f.b2), HeldBySeller: Some(f.b1)},
		Seller: SellerSources{Own: Some(f.s2), HeldByBuyer: Some(f.s2)},
	}
	assert.True(t, sameSources(&want, g.Sources), "%+v", *g.Sources)
	assert.Equal(t, f.b2, *g.Read.Buyer.V, "the buyer's own book is longer than the seller's copy of it")
	require.Len(t, g.Seller.Book, 2)
	assert.Equal(t, TimedByOwnStamp, g.Seller.Book[1].TimedBy)
	assert.Equal(t, "2026-11-01T11:00:00Z", g.Seller.Book[1].At)
	assert.Zero(t, g.Buyer.ForkAt)
	assert.Zero(t, g.Seller.ForkAt)
}

// Acceptance 3: the buyer's case-opening selective copy and its later full
// answer are both read; they agree, so the case is the same as with the
// answer alone.
func TestGatherCaseKeepsTheOpeningCopy(t *testing.T) {
	f := newFourBooks(t)
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	_, pair, x := holderBundle(t, f.h.buyer, "buyer", f.buyerLog, copyAt(gatherAsOf, dealprofile.AudienceAdjudicator))
	opening := CaseCopy{Report: pair, Extension: extJSON(t, x)}
	buyer := f.answer(t, f.h.buyer, "buyer", f.buyerLog)
	g, err := GatherCase(f.dispute, []CaseCopy{opening}, []CaseAnswer{buyer}, f.adjudicator.key(), dir)
	require.NoError(t, err)
	assert.Zero(t, g.Buyer.ForkAt)
	assert.Equal(t, f.b2, *g.Read.Buyer.V)

	// The opening copy alone, as CheckCase reads it.
	g, err = GatherCase(f.dispute, []CaseCopy{opening}, nil, f.adjudicator.key(), dir)
	require.NoError(t, err)
	assert.Equal(t, f.b2, *g.Read.Buyer.V)
	assert.Nil(t, g.Read.Seller.V)
	assert.Nil(t, g.Sources.Seller.HeldByBuyer.V, "no full answer: no held copy")
}

// A full copy's refusals.
func TestVerifyFullCopyRefusals(t *testing.T) {
	f := newFourBooks(t)
	buyer := f.answer(t, f.h.buyer, "buyer", f.buyerLog)
	var x ReportBundle
	require.NoError(t, json.Unmarshal(buyer.Copy.Extension, &x))
	_, full, err := VerifyFullCopy(buyer.Copy.Report, buyer.Copy.Extension)
	require.NoError(t, err)
	require.Len(t, full.Own, 2)
	require.Len(t, full.Received, 2)
	require.NotEmpty(t, x.Book.Received.Proofs)

	clone := func() ReportBundle {
		raw, err := json.Marshal(x)
		require.NoError(t, err)
		var out ReportBundle
		require.NoError(t, json.Unmarshal(raw, &out))
		return out
	}
	for name, mutate := range map[string]func(*ReportBundle){
		"a held copy short of an own record's seen": func(b *ReportBundle) {
			delete(b.Book.Received.Records, f.s2)
		},
		"an own record left out": func(b *ReportBundle) {
			delete(b.Book.Records, f.b1)
		},
		"a received record not signed by the counterparty": func(b *ReportBundle) {
			b.Book.Received.Records[f.b1] = b.Book.Records[f.b1]
		},
		"no received": func(b *ReportBundle) { b.Book.Received = nil },
		"received without records": func(b *ReportBundle) {
			b.Book.Records = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := clone()
			mutate(&b)
			_, _, err := VerifyFullCopy(buyer.Copy.Report, extJSON(t, b))
			assert.Error(t, err)
		})
	}
	// A selective copy is not a full answer.
	_, pair, sel := holderBundle(t, f.h.buyer, "buyer", f.buyerLog, copyAt(gatherAsOf, dealprofile.AudienceAdjudicator))
	_, _, err = VerifyFullCopy(pair, extJSON(t, sel))
	assert.ErrorContains(t, err, "not a full adjudicator copy")
	// Only an adjudicator's copy is full.
	c := keepCopy(gatherAsOf)
	c.Full = true
	_, _, err = f.h.buyer.status.ReportWithBook(context.Background(), f.buyerLog, c)
	assert.ErrorContains(t, err, "only an adjudicator's copy is full")
}

// The answer's book evidence must agree with its copy: a link value naming
// a record the held copy does not carry there, and a fact its records do not
// recompute, are refused; a receipt left out only makes a time later.
func TestGatherCaseEvidenceRefusals(t *testing.T) {
	f := newFourBooks(t)
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	buyer := f.answer(t, f.h.buyer, "buyer", f.buyerLog)
	require.Len(t, buyer.Evidence.Facts, 3, "the buyer's stamps at 10:00, 12:00 and 12:30")

	bad := buyer
	bad.Evidence.BookID = f.h.agreementID
	_, err = GatherCase(f.dispute, nil, []CaseAnswer{bad}, f.adjudicator.key(), dir)
	assert.ErrorContains(t, err, "not its deal book")

	// The receipt of the stamp whose link first reached the delivery left
	// out: the delivery is timed by the next one (12:30), later, never
	// earlier.
	late := buyer
	late.Evidence.Facts = append([]evidencebook.BookFactEvidence{}, buyer.Evidence.Facts...)
	late.Evidence.Facts[1].Receipts = nil
	g, err := GatherCase(f.dispute, nil, []CaseAnswer{late}, f.adjudicator.key(), dir)
	require.NoError(t, err)
	assert.Equal(t, "2026-11-01T12:30:00Z", g.Seller.Book[1].At)

	_, full, err := VerifyFullCopy(buyer.Copy.Report, buyer.Copy.Extension)
	require.NoError(t, err)
	// A link value naming a record the held copy does not carry at that
	// position: the answer could otherwise time a record it never held.
	noS2 := full
	noS2.Received = map[string]BookRead{}
	for id, b := range full.Received {
		if id != f.s2 {
			noS2.Received[id] = b
		}
	}
	_, err = readAnswer(noS2, buyer.Evidence, dir)
	assert.ErrorContains(t, err, "does not carry there")
	// A fact its records do not recompute: another record at seq 2.
	other := full
	other.Own = append([]BookRead{}, full.Own...)
	other.Own[1] = full.Received[f.s2]
	_, err = readAnswer(other, buyer.Evidence, dir)
	assert.ErrorContains(t, err, "not recomputed by its records")
}

// br is a record of a book for the fork tests: its id, seq and the id
// before it.
func br(id string, seq uint64, prev string) BookRead {
	return BookRead{RecordID: id, Seq: seq, Prev: prev, KeyID: "k", LogID: "deal/x"}
}

func held(rs ...BookRead) map[string]BookRead {
	out := map[string]BookRead{}
	for _, r := range rs {
		out[r.RecordID] = r
	}
	return out
}

func ids(book []CaseRecord) []string {
	var out []string
	for _, r := range book {
		out = append(out, r.Read.RecordID)
	}
	return out
}

// A side's book from its sources (Steven's ruling 3, design 861 §6): where its own
// book and the copy the other side held differ, the held copy governs and
// both are kept; where one extends the other, the longer is read; with no
// held copy, a fork between the side's opening copy and its full answer
// reads neither branch past it.
func TestGatherSideForks(t *testing.T) {
	a1, a2, b1 := br("a1", 1, ""), br("a2", 2, "a1"), br("b1", 1, "")
	x3, y3, x4 := br("x3", 3, "a2"), br("y3", 3, "a2"), br("x4", 4, "x3")
	own := func(rs ...BookRead) answerRead { return answerRead{full: FullCopy{Own: rs}} }
	holder := func(rs ...BookRead) answerRead {
		return answerRead{branch: rs, full: FullCopy{LogID: "deal/x", Own: []BookRead{br("h1", 1, "")}}}
	}
	for _, tc := range []struct {
		name              string
		own, holder       answerRead
		hasOwn, hasHolder bool
		op                openingRead
		hasOpening        bool
		book, forked      []string
		forkAt            uint64
		ownLast, heldLast string
		shown             map[uint64][]BookRead
		past              []string
	}{
		{name: "own and held fork at 3: the held copy governs", own: own(a1, a2, x3), hasOwn: true, holder: holder(a1, a2, y3), hasHolder: true,
			book: []string{"a1", "a2", "y3"}, forked: []string{"x3"}, forkAt: 3, ownLast: "x3", heldLast: "y3"},
		{name: "the held copy extends the own book", own: own(a1, a2), hasOwn: true, holder: holder(a1, a2, y3), hasHolder: true,
			book: []string{"a1", "a2", "y3"}, ownLast: "a2", heldLast: "y3"},
		{name: "the own book extends the held copy", own: own(a1, a2, x3), hasOwn: true, holder: holder(a1), hasHolder: true,
			book: []string{"a1", "a2", "x3"}, ownLast: "x3", heldLast: "a1"},
		{name: "the held copy forked; the own book agrees with the branch read and goes further", own: own(a1, a2, y3), hasOwn: true,
			holder: answerRead{branch: []BookRead{a1, a2}, forked: []BookRead{x3}, forkAt: 3, full: FullCopy{LogID: "deal/x", Own: []BookRead{br("h1", 1, "")}}}, hasHolder: true,
			book: []string{"a1", "a2"}, forked: []string{"x3", "y3"}, forkAt: 3, ownLast: "y3", heldLast: "a2"},
		{name: "the own copies differ past the held copy: neither is read past the fork", own: own(a1, a2, x3, x4), hasOwn: true,
			holder: holder(a1, a2), hasHolder: true,
			op: openingRead{records: map[uint64]BookRead{1: a1, 3: y3}, size: 3, last: "y3"}, hasOpening: true,
			book: []string{"a1", "a2"}, forked: []string{"x3", "y3", "x4"}, forkAt: 3, ownLast: "x4", heldLast: "a2"},
		{name: "the sender forked at its first record and the holder named neither", own: own(a1, a2), hasOwn: true,
			holder: answerRead{forked: []BookRead{a1, b1}, forkAt: 1, full: FullCopy{LogID: "deal/x", Own: []BookRead{br("h1", 1, "")}}}, hasHolder: true,
			book: nil, forked: []string{"a1", "b1", "a2"}, forkAt: 1, ownLast: "a2"},
		{name: "an opening copy carrying two records at one seq", hasOpening: true,
			op:   openingRead{records: map[uint64]BookRead{1: a1}, size: 3, last: "x3", forkAt: 3, forked: []BookRead{x3, y3}},
			book: []string{"a1"}, forked: []string{"x3", "y3"}, forkAt: 3, ownLast: "x3"},
		{name: "the held copy alone", holder: holder(a1, a2), hasHolder: true, book: []string{"a1", "a2"}, heldLast: "a2"},
		{name: "the opening copy and the full answer differ, nothing held: neither past it", own: own(a1, a2, x3), hasOwn: true,
			op: openingRead{records: map[uint64]BookRead{1: a1, 3: y3}, size: 3, last: "y3"}, hasOpening: true,
			book: []string{"a1", "a2"}, forked: []string{"x3", "y3"}, forkAt: 3, ownLast: "x3"},
		{name: "the opening copy differs from the held copy: the held copy governs", holder: holder(a1, a2, y3), hasHolder: true,
			op: openingRead{records: map[uint64]BookRead{1: a1, 3: x3}, size: 3, last: "x3"}, hasOpening: true,
			book: []string{"a1", "a2", "y3"}, forked: []string{"x3"}, forkAt: 3, ownLast: "x3", heldLast: "y3"},
		{name: "neither source", book: nil},
		// evidencebook#944: the other side's opening copy shows this side's records
		{name: "shown records the sources agree with change nothing", own: own(a1, a2, x3), hasOwn: true, holder: holder(a1, a2), hasHolder: true,
			shown: map[uint64][]BookRead{2: {a2}, 3: {x3}}, book: []string{"a1", "a2", "x3"}, ownLast: "x3", heldLast: "a2"},
		{name: "a shown record past the held copy differs from the own book: neither is read past it", own: own(a1, a2, x3, x4), hasOwn: true,
			holder: holder(a1, a2), hasHolder: true, shown: map[uint64][]BookRead{3: {y3}},
			book: []string{"a1", "a2"}, forked: []string{"x3", "y3", "x4"}, forkAt: 3, ownLast: "x4", heldLast: "a2"},
		{name: "a shown record differs where the held copy reaches: the held copy governs", own: own(a1, a2, y3), hasOwn: true,
			holder: holder(a1, a2, y3), hasHolder: true, shown: map[uint64][]BookRead{3: {x3}},
			book: []string{"a1", "a2", "y3"}, forked: []string{"x3"}, forkAt: 3, ownLast: "y3", heldLast: "y3"},
		{name: "the own book alone and a shown fork: the book stops before it", own: own(a1, a2, x3, x4), hasOwn: true,
			shown: map[uint64][]BookRead{2: {br("a2x", 2, "a1")}},
			book:  []string{"a1"}, forked: []string{"a2", "a2x", "x3", "x4"}, forkAt: 2, ownLast: "x4"},
		{name: "two shown records at one seq", own: own(a1, a2), hasOwn: true, shown: map[uint64][]BookRead{3: {x3, y3}},
			book: []string{"a1", "a2"}, forked: []string{"x3", "y3"}, forkAt: 3, ownLast: "a2"},
		{name: "a shown record past the book read", own: own(a1, a2), hasOwn: true, shown: map[uint64][]BookRead{3: {x3}},
			book: []string{"a1", "a2"}, ownLast: "a2", past: []string{"x3"}},
		{name: "another signer's record at a seq", own: own(a1, a2), hasOwn: true,
			shown: map[uint64][]BookRead{2: {{RecordID: "ruling", Seq: 2, KeyID: "adjudicator"}}}, book: []string{"a1", "a2"}, ownLast: "a2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := gatherSide(RoleSeller, "k", tc.own, tc.hasOwn, tc.holder, tc.hasHolder, tc.op, tc.hasOpening, tc.shown)
			assert.Equal(t, tc.book, ids(s.Book))
			assert.Equal(t, tc.forkAt, s.ForkAt)
			var forked []string
			for _, b := range s.Forked {
				forked = append(forked, b.RecordID)
			}
			assert.Equal(t, tc.forked, forked)
			var past []string
			for _, b := range s.ShownPast {
				past = append(past, b.RecordID)
			}
			assert.Equal(t, tc.past, past)
			for _, c := range []struct {
				got  Null[string]
				want string
			}{{s.Own, tc.ownLast}, {s.Held, tc.heldLast}} {
				if c.want == "" {
					assert.Nil(t, c.got.V)
				} else {
					require.NotNil(t, c.got.V)
					assert.Equal(t, c.want, *c.got.V)
				}
			}
		})
	}
}

// The branch of a held copy read: the one the holder named as received,
// extended while it does not fork; the other branch is the sender's fork,
// kept as evidence. A holder naming both branches is refused.
func TestHeldBranch(t *testing.T) {
	a1, a2 := br("a1", 1, ""), br("a2", 2, "a1")
	y3, z3, y4 := br("y3", 3, "a2"), br("z3", 3, "a2"), br("y4", 4, "y3")
	full := FullCopy{Role: RoleBuyer, Received: held(a1, a2, y3, z3, y4)}

	branch, forked, at, err := heldBranch(full, []string{"a2", "y3"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a1", "a2", "y3", "y4"}, recordIDs(branch))
	assert.Equal(t, []string{"z3"}, recordIDs(forked))
	assert.Equal(t, uint64(3), at)

	branch, _, at, err = heldBranch(full, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a1", "a2"}, recordIDs(branch), "nothing named: up to the fork")
	assert.Equal(t, uint64(3), at)

	_, _, _, err = heldBranch(full, []string{"y3", "z3"})
	assert.ErrorContains(t, err, "two branches")

	// A fork at the first record, neither named: no branch, every held
	// record kept as evidence.
	b1 := br("b1", 1, "")
	branch, forked, at, err = heldBranch(FullCopy{Received: held(a1, a2, b1)}, nil)
	require.NoError(t, err)
	assert.Empty(t, branch)
	assert.Equal(t, []string{"a1", "b1", "a2"}, recordIDs(forked))
	assert.Equal(t, uint64(1), at)

	branch, forked, at, err = heldBranch(FullCopy{Received: held(a1, a2)}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a1", "a2"}, recordIDs(branch))
	assert.Empty(t, forked)
	assert.Zero(t, at)
}

// A gathered ruling may not go against a side none of whose records the
// case read (Steven's ruling 2, design 861 §6), and a ruling with a remedy amount
// seals.
func TestSealGatheredRuling(t *testing.T) {
	f := newFourBooks(t)
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	g, err := GatherCase(f.dispute, nil, []CaseAnswer{f.answer(t, f.h.buyer, "buyer", f.buyerLog)}, f.adjudicator.key(), dir)
	require.NoError(t, err)
	c := g.Case
	c.Read.Seller = NullOf[string]()
	none := *c.Sources
	none.Seller = SellerSources{Own: NullOf[string](), HeldByBuyer: NullOf[string]()}
	c.Sources = &none
	_, err = SealRuling(context.Background(), f.adjudicator.shelf, c, c.RulingOf(ValueNotMet, Some(RoleSeller), nil, true, ""), gatherAt("13:00"))
	assert.ErrorContains(t, err, "none of whose records the case read")
	// A side that forked at its first record, its branches kept as evidence,
	// may be ruled against: those are its own records.
	forked := none
	forked.Seller.ForkAt = 1
	c.Sources = &forked
	sealed, err := SealRuling(context.Background(), f.adjudicator.shelf, c, c.RulingOf(ValueNotMet, Some(RoleSeller), nil, false, ""), gatherAt("13:00"))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), sealed.Seq)

	amount := int64(500)
	remedy := &RulingRemedy{Kind: "refund", AmountMinor: &amount, Currency: "USD", Due: "2026-11-10T00:00:00Z"}
	sealed, err = SealRuling(context.Background(), f.adjudicator.shelf, g.Case, g.RulingOf(ValueNotMet, Some(RoleSeller), remedy, true, ""), gatherAt("13:00"))
	require.NoError(t, err)
	assert.Equal(t, uint64(2), sealed.Seq)
}

const caseGatherDir = "../../testdata/deal-v1/case-gather"

// gatherCopy and gatherAnswer carry the bytes as text: the extension and
// the evidence exactly as a host hands them over (an indented file would
// reformat their members).
type gatherCopy struct {
	Report    string `json:"report"`
	Extension string `json:"extension"`
}

type gatherAnswer struct {
	gatherCopy
	Evidence string `json:"evidence"`
}

type gatherRecord struct {
	Record  string `json:"record"`
	At      string `json:"at"`
	TimedBy string `json:"timed_by"`
}

type gatherWant struct {
	Sources RulingSources  `json:"sources"`
	Read    RulingRead     `json:"read"`
	Buyer   []gatherRecord `json:"buyer"`
	Seller  []gatherRecord `json:"seller"`
}

type gatherVector struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Dispute        string         `json:"dispute"`
	Opening        []gatherCopy   `json:"opening"`
	Answers        []gatherAnswer `json:"answers"`
	AdjudicatorKey string         `json:"adjudicator_key"`
	Want           *gatherWant    `json:"want,omitempty"`
	Error          string         `json:"error,omitempty"`
}

func (v gatherVector) inputs(t *testing.T) ([]byte, []CaseCopy, []CaseAnswer) {
	t.Helper()
	dispute, err := hex.DecodeString(v.Dispute)
	require.NoError(t, err)
	cc := func(c gatherCopy) CaseCopy {
		r, err := hex.DecodeString(c.Report)
		require.NoError(t, err)
		return CaseCopy{Report: r, Extension: []byte(c.Extension)}
	}
	var opening []CaseCopy
	for _, c := range v.Opening {
		opening = append(opening, cc(c))
	}
	var answers []CaseAnswer
	for _, a := range v.Answers {
		var e evidencebook.BookEvidence
		require.NoError(t, json.Unmarshal([]byte(a.Evidence), &e))
		answers = append(answers, CaseAnswer{Copy: cc(a.gatherCopy), Evidence: e})
	}
	return dispute, opening, answers
}

func gatherRecords(s CaseSide) []gatherRecord {
	out := []gatherRecord{}
	for _, r := range s.Book {
		out = append(out, gatherRecord{Record: r.Read.RecordID, At: r.At, TimedBy: r.TimedBy})
	}
	return out
}

// replayGather runs one vector: GatherCase gives want, or refuses with error
// in its text.
func replayGather(t *testing.T, v gatherVector) {
	t.Helper()
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	dispute, opening, answers := v.inputs(t)
	g, err := GatherCase(dispute, opening, answers, v.AdjudicatorKey, dir)
	if v.Error != "" {
		require.Error(t, err, v.Name)
		assert.Contains(t, err.Error(), v.Error, v.Name)
		return
	}
	require.NoError(t, err, v.Name)
	got := gatherWant{Sources: *g.Sources, Read: g.Read, Buyer: gatherRecords(g.Buyer), Seller: gatherRecords(g.Seller)}
	wantJSON, err := json.Marshal(v.Want)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), string(gotJSON), v.Name)
}

// The §3.3 vectors of docs/design/861-four-books.md, from real shelves:
// GatherCase over a dispute, opening copies and full answers. Written with
// EB_WRITE_DEAL_V1_VECTORS=1; replayed from the file by
// TestCaseGatherVectorsReplay.
func TestCaseGatherVectors(t *testing.T) {
	f := newFourBooks(t)
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	copyOf := func(c CaseCopy) gatherCopy {
		return gatherCopy{Report: hex.EncodeToString(c.Report), Extension: string(c.Extension)}
	}
	answerOf := func(a CaseAnswer) gatherAnswer {
		e, err := json.Marshal(a.Evidence)
		require.NoError(t, err)
		return gatherAnswer{gatherCopy: copyOf(a.Copy), Evidence: string(e)}
	}
	buyer := f.answer(t, f.h.buyer, "buyer", f.buyerLog)
	seller := f.answer(t, f.h.seller, "seller", f.h.sellerLog)
	_, pair, x := holderBundle(t, f.h.buyer, "buyer", f.buyerLog, copyAt(gatherAsOf, dealprofile.AudienceAdjudicator))
	opening := CaseCopy{Report: pair, Extension: extJSON(t, x)}

	var cases []gatherVector
	add := func(name, desc string, op []CaseCopy, as []CaseAnswer, mustErr string) {
		t.Helper()
		v := gatherVector{Name: name, Description: desc, Dispute: hex.EncodeToString(f.dispute), Opening: []gatherCopy{},
			Answers: []gatherAnswer{}, AdjudicatorKey: f.adjudicator.key()}
		for _, c := range op {
			v.Opening = append(v.Opening, copyOf(c))
		}
		for _, a := range as {
			v.Answers = append(v.Answers, answerOf(a))
		}
		g, err := GatherCase(f.dispute, op, as, f.adjudicator.key(), dir)
		if mustErr != "" {
			require.ErrorContains(t, err, mustErr, name)
			v.Error = mustErr
		} else {
			require.NoError(t, err, name)
			v.Want = &gatherWant{Sources: *g.Sources, Read: g.Read, Buyer: gatherRecords(g.Buyer), Seller: gatherRecords(g.Seller)}
		}
		cases = append(cases, v)
	}
	add("acceptance-5-seller-silent", "the buyer disputes a late delivery and the seller does not answer: the buyer's own book and its copy of the seller's are read, the seller's own book is not shown, and the delivery is timed by the buyer's linking fact",
		nil, []CaseAnswer{buyer}, "")
	add("acceptance-1-both-answer", "both answer: each held copy checks against the other side's own book; the buyer's own book extends the seller's copy of it (the longer is read, fork_at 0), and the delivery is timed by the seller's own stamp, earlier than the buyer's linking fact",
		nil, []CaseAnswer{buyer, seller}, "")
	add("acceptance-3-opening-kept", "the buyer's case-opening selective copy and its later full answer are both read and agree",
		[]CaseCopy{opening}, []CaseAnswer{buyer}, "")
	late := buyer
	late.Evidence.Facts = append([]evidencebook.BookFactEvidence{}, buyer.Evidence.Facts...)
	late.Evidence.Facts[1].Receipts = []evidencebook.WitnessReceipt{}
	add("holder-receipt-left-out", "the buyer leaves out the receipt of the stamp whose link first reached the delivery: the delivery is timed later (12:30), never earlier",
		nil, []CaseAnswer{late}, "")

	var bx ReportBundle
	require.NoError(t, json.Unmarshal(buyer.Copy.Extension, &bx))
	mutated := func(edit func(*ReportBundle)) CaseAnswer {
		raw, err := json.Marshal(bx)
		require.NoError(t, err)
		var b ReportBundle
		require.NoError(t, json.Unmarshal(raw, &b))
		edit(&b)
		a := buyer
		a.Copy.Extension = extJSON(t, b)
		return a
	}
	add("refuse-held-short-of-seen", "a held copy short of an own record's seen",
		nil, []CaseAnswer{mutated(func(b *ReportBundle) {
			delete(b.Book.Received.Records, f.s2)
			b.Book.Received.Proofs = b.Book.Received.Proofs[:1] // the proof covering the delivery goes with it
		})}, "which received does not carry")
	add("refuse-received-not-counterparty", "a received record not signed by the agreement's counterparty_key",
		nil, []CaseAnswer{mutated(func(b *ReportBundle) { b.Book.Received.Records[f.b1] = b.Book.Records[f.b1] })}, "not the counterparty's record")
	skip := buyer
	skip.Evidence.Facts = append([]evidencebook.BookFactEvidence{buyer.Evidence.Facts[0]}, buyer.Evidence.Facts[2:]...)
	add("holder-fact-left-out", "the buyer leaves out its fact whose link first reached the delivery (its book's size did not change there, so the chain still holds): the delivery is timed later (12:30), never earlier",
		nil, []CaseAnswer{skip}, "")
	first := buyer
	first.Evidence.Facts = buyer.Evidence.Facts[1:]
	add("refuse-first-fact-left-out", "the holder's facts without the first: the chain does not start at the book's first fact",
		nil, []CaseAnswer{first}, "the first fact names a previous size")
	other := buyer
	other.Evidence.BookID = f.h.agreementID
	add("refuse-evidence-of-another-book", "book evidence of another book than the answer's deal book",
		nil, []CaseAnswer{other}, "not its deal book")

	for _, c := range cases {
		replayGather(t, c)
	}
	got, err := json.MarshalIndent(map[string]any{
		"description": "the adjudicator gathers four books (evidencebook#861, docs/design/861-four-books.md §2.3, §3.3; PROFILE §5 and §10): GatherCase(dispute, opening, answers, adjudicator_key, the shelf script's witness directory) gives want (the ruling's sources and read, and each side's book read with each record's time and what timed it), or refuses with error in its text. dispute and each report are hex; extension is the x-deal-v1 extension's text and evidence the text of an evidencebook.BookEvidence, as a host hands them over. Written by dealprofile/xdealv1's TestCaseGatherVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := caseGatherDir + "/vectors.json"
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(caseGatherDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "case-gather vectors.json differs from this build's; regenerate only for an intended change")
}

func TestCaseGatherVectorsReplay(t *testing.T) {
	raw, err := os.ReadFile(caseGatherDir + "/vectors.json")
	require.NoError(t, err)
	var file struct {
		Cases []gatherVector `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.NotEmpty(t, file.Cases)
	for _, c := range file.Cases {
		replayGather(t, c)
	}
}

// readOpening reads an opening copy that carries two of its signer's
// records at one seq as a fork there: both, and every record after them, are
// fork evidence, and the records read stop before it (round 2 of #877).
func TestReadOpeningForked(t *testing.T) {
	f := newFourBooks(t)
	_, pair, x := holderBundle(t, f.h.buyer, "buyer", f.buyerLog, copyAt(gatherAsOf, dealprofile.AudienceAdjudicator))
	// Another record at the buyer's seq 2, sealed on a fresh shelf under the
	// buyer's key: the buyer contradicting itself.
	q := newParty(t, "buyer fork")
	q.cfg.Signer = f.h.buyer.cfg.Signer
	originals, err := evidencebook.OpenOriginalsFile(context.Background(), filepath.Join(q.dir, "originals2.jsonl"), "x-deal-v1", f.h.buyer.cfg.Signer.PublicKey())
	require.NoError(t, err)
	q.cfg.Originals, q.cfg.Path = originals, filepath.Join(q.dir, "fork.db")
	q.open()
	book := mustOpen(t, f.h.buyer, f.buyerLog)
	q.seal(f.buyerLog, readBack(t, book, 1).Record)
	other := readBack(t, book, 2).Record
	other.Block.At = "2026-11-01T12:20:00Z"
	otherID, otherPair := q.seal(f.buyerLog, other)
	require.NotEqual(t, f.b2, otherID)
	x.Book.ReliedOn[f.b2] = f.dispute
	x.Book.ReliedOn[otherID] = otherPair

	cc, err := readCaseCopy(CaseCopy{Report: pair, Extension: extJSON(t, x)}, f.adjudicator.key())
	require.NoError(t, err)
	op, err := readOpening(CaseCopy{Report: pair, Extension: extJSON(t, x)}, cc)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), op.forkAt)
	assert.ElementsMatch(t, []string{f.b2, otherID}, recordIDs(op.forked))
	assert.Contains(t, op.records, uint64(1))
	assert.NotContains(t, op.records, uint64(2))
}

// evidencebook#944, on the path #943's review found: the seller states
// not_met at seq 3; the buyer's full answer holds the seller's book only
// through seq 2, and its opening copy relies on a different seller record at
// seq 3 (met). The seller's held copy is read, so only the reconciliation of
// the opening copy finds the fork; without it DecideCase would decide on the
// seller's own not_met.
func TestGatherCaseFindsAForkInTheOtherSidesOpeningCopy(t *testing.T) {
	f := newFourBooks(t)
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	h := f.h
	book := mustOpen(t, h.seller, h.sellerLog)
	s2 := readBack(t, book, 2).Record
	s2d, err := Digest(s2)
	require.NoError(t, err)
	ad, err := Digest(h.agreement)
	require.NoError(t, err)
	stmt := func(at, value string) Record {
		b := journalBlock(TypeStatement, at, true)
		b.Seq, b.Seen, b.Prev, b.AgreementRef = 3, Some(f.b1), ref(0), ref(0)
		*b.Prev, *b.AgreementRef = RefTo(s2d), RefTo(ad)
		r := Record{Block: b, Body: &StatementBody{Statements: statements(item("delivered", value))}}
		require.NoError(t, Check(r))
		return r
	}
	s3, _ := h.seller.seal(h.sellerLog, stmt("2026-11-01T12:40:00Z", ValueNotMet))
	witnessedStamp(t, h.seller, gatherAt("12:45"))

	// The same seller key seals a different seq 3 (met) on another shelf.
	q := newParty(t, "seller fork")
	q.cfg.Signer = h.seller.cfg.Signer
	originals, err := evidencebook.OpenOriginalsFile(context.Background(), filepath.Join(q.dir, "originals2.jsonl"), "x-deal-v1", h.seller.cfg.Signer.PublicKey())
	require.NoError(t, err)
	q.cfg.Originals, q.cfg.Path = originals, filepath.Join(q.dir, "fork.db")
	q.open()
	q.seal(h.sellerLog, readBack(t, book, 1).Record)
	q.seal(h.sellerLog, s2)
	otherID, otherPair := q.seal(h.sellerLog, stmt("2026-11-01T12:41:00Z", ValueMet))
	require.NotEqual(t, s3, otherID)

	_, pair, x := holderBundle(t, h.buyer, "buyer", f.buyerLog, copyAt(gatherAsOf, dealprofile.AudienceAdjudicator))
	x.Book.ReliedOn[otherID] = otherPair
	opening := CaseCopy{Report: pair, Extension: extJSON(t, x)}
	buyer := f.answer(t, h.buyer, "buyer", f.buyerLog)
	seller := f.answer(t, h.seller, "seller", h.sellerLog)

	g, err := GatherCase(f.dispute, []CaseCopy{opening}, []CaseAnswer{buyer, seller}, f.adjudicator.key(), dir)
	require.NoError(t, err)
	assert.Equal(t, f.s2, *g.Seller.Held.V, "the buyer holds the seller's book through seq 2")
	assert.Equal(t, s3, *g.Seller.Own.V)
	assert.Equal(t, uint64(3), g.Seller.ForkAt)
	assert.Contains(t, recordIDs(g.Seller.Forked), otherID)
	assert.Equal(t, int64(3), g.Sources.Seller.ForkAt, "the ruling's sources name the fork")
	d := DecideCase(g, Null[string]{})
	assert.Nil(t, d.Decisive)
	assert.Equal(t, "the seller's book has two versions from record 3", d.NeedsPerson)

	// Without the opening copy, the same answers decide on the seller's own not_met.
	g, err = GatherCase(f.dispute, nil, []CaseAnswer{buyer, seller}, f.adjudicator.key(), dir)
	require.NoError(t, err)
	d = DecideCase(g, Null[string]{})
	require.NotNil(t, d.Decisive, d.NeedsPerson)
	assert.Equal(t, s3, d.Decisive.RecordID)
}

// A fork found through the other side's opening copy caps the book read as
// any other fork no held copy settles (#943 review, round 3): the seller
// answers in full, the buyer gives no full answer, and its opening copy shows
// a different seller seq 2. The read stops at seq 1, the ruling's read with it.
func TestGatherCaseCapsTheReadAtAShownFork(t *testing.T) {
	f := newFourBooks(t)
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	h := f.h
	book := mustOpen(t, h.seller, h.sellerLog)
	q := newParty(t, "seller fork")
	q.cfg.Signer = h.seller.cfg.Signer
	originals, err := evidencebook.OpenOriginalsFile(context.Background(), filepath.Join(q.dir, "originals2.jsonl"), "x-deal-v1", h.seller.cfg.Signer.PublicKey())
	require.NoError(t, err)
	q.cfg.Originals, q.cfg.Path = originals, filepath.Join(q.dir, "fork.db")
	q.open()
	q.seal(h.sellerLog, readBack(t, book, 1).Record)
	other := readBack(t, book, 2).Record
	other.Block.At = "2026-11-01T11:05:00Z"
	otherID, otherPair := q.seal(h.sellerLog, other)
	_, pair, x := holderBundle(t, h.buyer, "buyer", f.buyerLog, copyAt(gatherAsOf, dealprofile.AudienceAdjudicator))
	x.Book.ReliedOn[otherID] = otherPair
	opening := CaseCopy{Report: pair, Extension: extJSON(t, x)}

	seller := f.answer(t, h.seller, "seller", h.sellerLog)
	g, err := GatherCase(f.dispute, []CaseCopy{opening}, []CaseAnswer{seller}, f.adjudicator.key(), dir)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), g.Seller.ForkAt)
	require.Len(t, g.Seller.Book, 1, "the seller's book is read only up to the fork")
	assert.Equal(t, g.Seller.Book[0].Read.RecordID, *g.Read.Seller.V)
	assert.ElementsMatch(t, []string{f.s2, otherID}, recordIDs(g.Seller.Forked))
}
