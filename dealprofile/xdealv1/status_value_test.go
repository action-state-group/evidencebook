// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/rowstatus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusBooksOf is an Appendix A book as status records, every time known
// as its at, except where times overrides by index.
func statusBooksOf(t *testing.T, a *appendixA, path string, times map[int]StatusTime) []StatusRecord {
	t.Helper()
	var out []StatusRecord
	for i, r := range appendixABooks(t, a, path) {
		st := KnownTime(*r.Time)
		if o, ok := times[i]; ok {
			st = o
		}
		out = append(out, StatusRecord{Record: r.Record, RecordID: r.RecordID, Time: st})
	}
	return out
}

func appendixAStatus(t *testing.T) (*appendixA, StatusBooks) {
	t.Helper()
	a := buildAppendixA(t)
	return a, StatusBooks{Offer: a.record("appendix-a/buyer-journal/05-offer.json"),
		Own:         statusBooksOf(t, a, "appendix-a/buyer-book", nil),
		Counterpart: statusBooksOf(t, a, "appendix-a/seller-book", nil),
		CaseBook:    statusBooksOf(t, a, "appendix-a/case-book", nil), ProofHeld: true}
}

func TestStatusTimeEncoding(t *testing.T) {
	for want, st := range map[string]StatusTime{`"2026-11-01T10:00:00Z"`: KnownTime("2026-11-01T10:00:00Z"), `null`: NoTime, `{"stamp":3}`: StampTime(3)} {
		got, err := json.Marshal(st)
		require.NoError(t, err)
		assert.Equal(t, want, string(got))
		var back StatusTime
		require.NoError(t, json.Unmarshal(got, &back))
		assert.Equal(t, st, back)
	}
	for _, bad := range []string{`{"stamp":0}`, `{}`, `{"stamp":1,"x":2}`, `3`, `{"stamp":"1"}`} {
		var st StatusTime
		assert.Error(t, json.Unmarshal([]byte(bad), &st), bad)
	}
}

// Appendix A's buyer: the status value holds the deal's inputs, the rows,
// and the one dispute record once (Steven's #95 ruling as Sherlock2 read it:
// a contest is the dispute record itself, counted once).
func TestBuildStatusAppendixA(t *testing.T) {
	_, b := appendixAStatus(t)
	v, err := BuildStatus(b)
	require.NoError(t, err)
	assert.Equal(t, 1, v.V)
	assert.Equal(t, RoleBuyer, v.Role)
	assert.Equal(t, SideOfferer, v.Side)
	assert.True(t, v.CounterpartyLayer)
	assert.True(t, v.CounterpartyProofHeld)
	require.NotNil(t, v.ValueBand)
	assert.Equal(t, ValueBand{Currency: "USD", Decade: 1}, *v.ValueBand, "60 dollars: 10^1")
	require.NotNil(t, v.Adjudicator)
	require.Len(t, v.Rows, 3)
	disputes := 0
	for _, side := range [][]StatusRecord{b.Own, b.Counterpart} {
		for _, r := range side {
			if d, ok := bodyOf[DisputeBody](r.Record.Body); ok && d.Kind == KindDispute {
				disputes++
			}
		}
	}
	require.Equal(t, 1, disputes, "Appendix A has one dispute record")
	require.Len(t, v.Disputes, 1, "one dispute record is one entry")
	assert.Equal(t, "as_described", v.Disputes[0].Row)
	assert.Equal(t, RoleBuyer, v.Disputes[0].Claimant)
	row := v.Rows[2]
	assert.Equal(t, "as_described", row.ID)
	assert.Len(t, row.Input.Disputes, 1, "and one rowstatus dispute")
	assert.Len(t, row.StatementNamedByOther, len(row.Input.Statements))

	// No shared identifier: no record id, digest, deal id or log id.
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	for _, rs := range [][]StatusRecord{b.Own, b.Counterpart, b.CaseBook} {
		for _, r := range rs {
			assert.NotContains(t, string(raw), r.RecordID)
			d, err := Digest(r.Record)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), d)
		}
	}
	assert.NotContains(t, string(raw), fxDealID)
	od, err := Digest(b.Offer)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), od)
}

// Times (design §8.1, times-three-cases): a record with a known time, one
// first covered by a stamp whose time is not known, and no such record.
func TestBuildStatusTimes(t *testing.T) {
	a, b := appendixAStatus(t)
	b.Own = statusBooksOf(t, a, "appendix-a/buyer-book", map[int]StatusTime{1: StampTime(4)})
	v, err := BuildStatus(b)
	require.NoError(t, err)
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `{"stamp":4}`, "the payment, first covered by stamp 4")
	assert.Contains(t, string(raw), `"`+b.Own[0].Record.Block.At+`"`, "the agreement's known time")
	var named map[string]StatusNamed
	for _, r := range v.Rows {
		if r.Input.Named != nil {
			named = r.Input.Named
		}
	}
	require.NotNil(t, named)

	// Without the receipt record, received is not written: its time is null.
	b2 := b
	for i, r := range b2.Own {
		if strings.Contains(strings.Join(r.Record.Block.Marks, ","), "received") {
			b2.Own = b2.Own[:i]
			break
		}
	}
	v2, err := BuildStatus(b2)
	require.NoError(t, err)
	for _, r := range v2.Rows {
		if n, ok := r.Input.Named["received"]; ok {
			assert.True(t, n.Time.IsNone(), "a named record not written yet")
		}
	}
}

// A payment check's result reaches the row: its check, its reason and how
// many settle records it left out, never which.
func TestBuildStatusCarriesThePaymentCheck(t *testing.T) {
	_, b := appendixAStatus(t)
	b.Payments = map[string]PaymentResult{"paid_as_agreed": {Reason: ReasonUnjoined, Excluded: []string{label("a"), label("b")}}}
	v, err := BuildStatus(b)
	require.NoError(t, err)
	assert.Empty(t, v.Rows[0].Input.Check)
	assert.Equal(t, ReasonUnjoined, v.Rows[0].CheckReason)
	assert.Equal(t, 2, v.Rows[0].CheckExcluded)
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), label("a"))

	// A passing payment beside a refund it did not check says how many
	// (evidencebook#722), never which.
	b.Payments = map[string]PaymentResult{"paid_as_agreed": {Check: PaymentPass, NotChecked: []string{label("r")}}}
	v, err = BuildStatus(b)
	require.NoError(t, err)
	assert.Equal(t, 1, v.Rows[0].CheckNotChecked)
	raw, err = json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"check_not_checked":1`)
	assert.NotContains(t, string(raw), label("r"))

	b.Payments = map[string]PaymentResult{"paid_as_agreed": {Check: PaymentPass}}
	v, err = BuildStatus(b)
	require.NoError(t, err)
	assert.Equal(t, PaymentPass, v.Rows[0].Input.Check)
}

func TestBuildStatusRefuses(t *testing.T) {
	_, b := appendixAStatus(t)
	for name, edit := range map[string]func(*StatusBooks){
		"an offer that is not one": func(b *StatusBooks) { b.Offer = b.Own[0].Record },
		"an empty book":            func(b *StatusBooks) { b.Own = nil },
		"no agreement first":       func(b *StatusBooks) { b.Own = b.Own[1:] },
	} {
		c := b
		edit(&c)
		_, err := BuildStatus(c)
		assert.Error(t, err, name)
	}
}

// The provider at a stamp, on two real shelves: the buyer's fact commits a
// v1 status value built from its book, the seller's counted prefix and the
// payment check, and a link value naming the seller's last record; no
// witness is named, so every time is a stamp.
func TestStatusProviderAtAStamp(t *testing.T) {
	d := newPaymentDeal(t, func(o *ProposeBody) { o.Witnesses = []Witness{} }, true)
	d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
	d.buyer.recUseStatus(map[string][]byte{d.offerDigest: d.offerPair})
	proof := d.seller.stamp(d.sellerAgID)
	d.buyer.receive(d.sellerAgPair, &proof)
	res := d.buyer.receive(payee, nil)
	require.True(t, res.Record.Stored)

	tick, err := d.buyer.recStampTick()
	require.NoError(t, err)
	facts := d.buyer.recOpenings(d.buyerAgID, evidencebook.AudienceCounterparty)
	require.NotEmpty(t, facts)
	last := facts[len(facts)-1]
	require.NotNil(t, last.StatusOpening)
	require.NotNil(t, last.LinkOpening)
	assert.Equal(t, Profile, last.Fact.Profile)
	var v StatusValue
	require.NoError(t, json.Unmarshal(last.StatusOpening.Value, &v))
	assert.Equal(t, RoleBuyer, v.Role)
	assert.True(t, v.CounterpartyProofHeld, "the seller's proof covers its agreement")
	assert.Equal(t, PaymentPass, v.Rows[0].Input.Check, "the payment legs agree, settled at the terms")
	raw := string(last.StatusOpening.Value)
	assert.Contains(t, raw, fmt.Sprintf(`{"stamp":%d}`, tick.Tick.N))
	assert.NotContains(t, raw, d.sellerAgID)
	assert.NotContains(t, raw, d.buyerAgID)

	var link evidencebook.LinkValue
	require.NoError(t, json.Unmarshal(last.LinkOpening.Value, &link))
	require.NotNil(t, link.LastReceived)
	assert.Equal(t, d.sellerLog, link.LastReceived.LogID)
	assert.Equal(t, uint64(2), link.LastReceived.Size)
	payeeRead, err := VerifyBook(payee)
	require.NoError(t, err)
	assert.Equal(t, payeeRead.RecordID, link.LastReceived.RecordDigest)
	assert.Contains(t, link.CheckedOver, payeeRead.Digest, "the checked records, by record digest, in the link value only")
}

// forkedCaseBook seals a case book on two copies of the adjudicator's shelf
// that share position 1 and then diverge, and returns every record as held,
// with the adjudicator's key and the log id.
func forkedCaseBook(t *testing.T, common []*RulingBody, left, right []*RulingBody) ([]evidencebook.HeldRecord, string, string) {
	t.Helper()
	a, b := newParty(t, "adjudicator"), newParty(t, "adjudicator")
	log := "case/" + hex.EncodeToString(seed("case log")[:16])
	var held []evidencebook.HeldRecord
	seq := int64(0)
	last := map[*party]Record{}
	add := func(p *party, body *RulingBody, seq int64) {
		blk := journalBlock(TypeRuling, "2026-11-13T10:00:00Z", true)
		blk.Seq = seq
		if prev, ok := last[p]; ok {
			pr := RefTo(mustDigest(t, prev))
			blk.Prev = &pr
		}
		r := Record{Block: blk, Body: body}
		last[p] = r
		require.NoError(t, Check(r))
		_, pair := p.seal(log, r)
		read, err := VerifyBook(pair)
		require.NoError(t, err)
		for _, h := range held {
			if h.Digest == read.RecordID {
				return
			}
		}
		held = append(held, evidencebook.HeldRecord{ReceivedRecord: evidencebook.ReceivedRecord{Digest: read.RecordID, SenderKey: read.KeyID, LogID: log,
			Position: read.Seq, Prev: read.Prev, DealID: fxDealID}, Record: pair})
	}
	for _, c := range common {
		seq++
		add(a, c, seq)
		add(b, c, seq)
	}
	for i, l := range left {
		add(a, l, seq+int64(i)+1)
	}
	for i, r := range right {
		add(b, r, seq+int64(i)+1)
	}
	require.Equal(t, a.key(), b.key())
	return held, a.key(), log
}

func ruling(row string, against string, value string, final bool, remedy *RulingRemedy) *RulingBody {
	ag := NullOf[string]()
	if against != "" {
		ag = Some(against)
	}
	return &RulingBody{Row: row, Keys: RulingKeys{Buyer: fxBuyerKey, Seller: fxSellerKey}, Against: ag, Value: value, Remedy: remedy,
		Read: RulingRead{Buyer: NullOf[string](), Seller: NullOf[string]()}, Forum: fxForum, RulesDigest: fxAdjudicator.RulesDigest, Final: final}
}

// A forked case book conflicts on a row only when two branches' first final
// rulings differ in their decision: against, value or remedy (Codex, PR
// 4c-2b-2 review); a later final after a common first one does not.
func TestForkedFinals(t *testing.T) {
	refund := &RulingRemedy{Kind: "refund", Due: "2026-11-20T23:59:59Z"}
	replace := &RulingRemedy{Kind: "replace", Due: "2026-11-20T23:59:59Z"}
	// A gap in one straight case book: the first final at position 1, a
	// later different final at position 3, position 2 not held. No fork.
	held, key, log := forkedCaseBook(t, []*RulingBody{
		ruling("as_described", RoleSeller, ValueNotMet, true, nil),
		ruling("delivered", "", ValueMet, false, nil),
		ruling("as_described", RoleBuyer, ValueMet, true, nil)}, nil, nil)
	var gapped []evidencebook.HeldRecord
	for _, h := range held {
		if h.Position != 2 {
			gapped = append(gapped, h)
		}
	}
	got, err := forkedFinals(gapped, key, log)
	require.NoError(t, err)
	assert.Empty(t, got, "a run after a gap is not a branch")

	for name, c := range map[string]struct {
		common, left, right []*RulingBody
		want                []string
	}{
		"a linear book": {[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil)}, nil, nil, nil},
		"against differs, value the same": {nil,
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil)},
			[]*RulingBody{ruling("as_described", RoleBuyer, ValueNotMet, true, nil)}, []string{"as_described"}},
		"remedy differs": {nil,
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, refund)},
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, replace)}, []string{"as_described"}},
		"a common first final, later finals differ": {[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil)},
			[]*RulingBody{ruling("as_described", RoleBuyer, ValueMet, true, nil)},
			[]*RulingBody{ruling("as_described", "", ValueMet, true, nil)}, nil},
		"one branch has no final": {nil,
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, nil)},
			[]*RulingBody{ruling("as_described", RoleBuyer, ValueMet, false, nil)}, nil},
		"the same decision on both": {nil,
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, refund), ruling("delivered", "", ValueMet, false, nil)},
			[]*RulingBody{ruling("as_described", RoleSeller, ValueNotMet, true, refund)}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			held, key, log := forkedCaseBook(t, c.common, c.left, c.right)
			got, err := forkedFinals(held, key, log)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// The side a ruling goes against cannot keep it off its own status by not
// naming it: the counterparty's naming times it (Sherlock2's twin, PR
// 4c-2b-2 review). Appendix A's final ruling goes against the seller; here
// the seller is the holder and never names it, the buyer does.
func TestRulingNamedByTheCounterparty(t *testing.T) {
	a := buildAppendixA(t)
	seller := statusBooksOf(t, a, "appendix-a/seller-book", nil)
	buyer := statusBooksOf(t, a, "appendix-a/buyer-book", nil)
	rulings := statusBooksOf(t, a, "appendix-a/case-book", map[int]StatusTime{0: NoTime, 1: NoTime})
	// The seller's records that named the rulings name nothing: it stays
	// silent, its book otherwise unchanged.
	var silent []StatusRecord
	dropped := 0
	for _, r := range seller {
		if st, ok := bodyOf[StatementBody](r.Record.Body); ok && st.Names != nil && st.Names.Ruling != nil {
			r.Record.Body = &StatementBody{Statements: st.Statements}
			dropped++
		}
		silent = append(silent, r)
	}
	require.Positive(t, dropped, "the seller's book named the rulings")
	named, err := namedRulings(rulings, silent, buyer)
	require.NoError(t, err)
	require.Len(t, named, 2, "the buyer named both")
	for _, r := range named {
		assert.False(t, r.Time.IsNone())
	}
	v, err := BuildStatus(StatusBooks{Offer: a.record("appendix-a/buyer-journal/05-offer.json"), Own: silent, Counterpart: buyer, CaseBook: named})
	require.NoError(t, err)
	for _, r := range v.Rows {
		if r.ID == "as_described" {
			require.NotNil(t, r.RulingAgainst)
			assert.Equal(t, RoleSeller, *r.RulingAgainst, "the silent holder's own status shows the ruling against it")
			in, err := ResolveRow(r, "2026-11-26T12:00:00Z", nil)
			require.NoError(t, err)
			res, err := rowstatus.Compute(in)
			require.NoError(t, err)
			assert.Equal(t, rowstatus.Ruled, res.Status)
		}
	}
	none, err := namedRulings(rulings, silent, nil)
	require.NoError(t, err)
	assert.Empty(t, none, "named by neither: not an input yet (#527)")

	// Earliest naming wins, a known time before a stamp.
	assert.True(t, earlier(KnownTime("2026-11-20T00:00:00Z"), StampTime(1)))
	assert.True(t, earlier(KnownTime("2026-11-19T00:00:00Z"), KnownTime("2026-11-20T00:00:00Z")))
	assert.True(t, earlier(StampTime(2), StampTime(3)))
	assert.False(t, earlier(StampTime(3), KnownTime("2026-11-20T00:00:00Z")))
}

// The counted walk stops, never fails, at a counted record not held for the
// deal (another deal's in the same log, one received after the held records
// were read, or one forgotten): the stamp goes on (Claude stand-in for Kimi,
// PR 4c-2b-2 review).
func TestCountedStopsAtARecordNotHeld(t *testing.T) {
	ctx := context.Background()
	d := newPaymentDeal(t, nil, true)
	d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
	proof := d.seller.stamp(d.sellerAgID)
	d.buyer.receive(d.sellerAgPair, &proof)
	d.buyer.receive(payee, nil)
	held, err := d.buyer.shelf.HeldByDeal(ctx, fxDealID)
	require.NoError(t, err)
	all, err := counted(ctx, d.buyer.shelf, held, d.seller.key(), d.sellerLog)
	require.NoError(t, err)
	require.Len(t, all, 2)
	var first []evidencebook.HeldRecord
	for _, h := range held {
		if h.Position == 1 {
			first = append(first, h)
		}
	}
	part, err := counted(ctx, d.buyer.shelf, first, d.seller.key(), d.sellerLog)
	require.NoError(t, err, "a counted record missing from the held records stops the walk")
	require.Len(t, part, 1)
	assert.Equal(t, d.sellerAgID, part[0].rec.RecordID)
	assert.Equal(t, []string{d.sellerLog}, logsOf(held, d.seller.key(), func(r Record) bool {
		a, ok := asAgree(r.Body)
		return ok && a.Kind == KindAgreement
	}))
}
