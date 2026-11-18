// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook"
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
	ctx := context.Background()
	d := newPaymentDeal(t, func(o *ProposeBody) { o.Witnesses = []Witness{} }, true)
	d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
	d.buyer.status = &StatusProvider{OfferPair: func(_ context.Context, digest string) ([]byte, error) {
		if digest == d.offerDigest {
			return d.offerPair, nil
		}
		return nil, nil
	}}
	d.buyer.reopen()
	proof := d.seller.stamp(d.sellerAgID)
	d.buyer.receive(d.sellerAgPair, &proof)
	res := d.buyer.receive(payee, nil)
	require.True(t, res.Record.Stored)

	d.buyer.tick()
	tick, err := d.buyer.cadence.Stamp(ctx, d.buyer.now)
	require.NoError(t, err)
	facts, err := d.buyer.shelf.Openings(ctx, d.buyerAgID, evidencebook.AudienceCounterparty)
	require.NoError(t, err)
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
