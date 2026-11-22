// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ReportSnapshot gives the report and the book ReportWithBook gives, with the
// payment checks the same read ran; a host that gives no offer pair sees the
// row listed as not run, with why.
func TestReportSnapshotCarriesThePaymentChecks(t *testing.T) {
	d := newBundleDeal(t)
	c := keepCopy(d.at)
	s, err := d.buyer.status.ReportSnapshot(context.Background(), d.buyerLog, c)
	require.NoError(t, err)
	r, book, err := d.buyer.status.ReportWithBook(context.Background(), d.buyerLog, c)
	require.NoError(t, err)
	same := func(a, b any) {
		ja, err := json.Marshal(a)
		require.NoError(t, err)
		jb, err := json.Marshal(b)
		require.NoError(t, err)
		assert.JSONEq(t, string(jb), string(ja))
	}
	same(s.Report, r)
	same(s.Book, book)
	require.Contains(t, s.Payments, "paid_as_agreed")
	assert.Equal(t, PaymentPass, s.Payments["paid_as_agreed"].Check)
	assert.Equal(t, CheckPassed, rowByID(t, s.Report, "paid_as_agreed").Check, "the report's check read the same result")
	assert.Empty(t, s.PaymentsNotRun)

	d.buyer.status.OfferPair = nil
	s, err = d.buyer.status.ReportSnapshot(context.Background(), d.buyerLog, c)
	require.NoError(t, err)
	assert.Empty(t, s.Payments)
	assert.Equal(t, map[string]string{"paid_as_agreed": NotRunNoOfferPair}, s.PaymentsNotRun)
}

// A row whose owed is an amount but which is not recomputed is no payment
// row: it is listed as not run, not recomputed.
func TestPaymentsNotRunNamesARowThatIsNotRecomputed(t *testing.T) {
	offer := Record{Body: &ProposeBody{TermsRows: TermsRows{Rows: []Row{
		{ID: "paid", OwedBy: RoleBuyer, Owed: json.RawMessage(`{"amount_minor":6000,"currency":"USD"}`), Recomputed: true},
		{ID: "deposit", OwedBy: RoleBuyer, Owed: json.RawMessage(`{"amount_minor":500,"currency":"USD"}`)},
		{ID: "delivered", OwedBy: RoleSeller, Owed: json.RawMessage(`"the lamp"`)},
	}}}}
	got := paymentsNotRun(StatusBooks{Offer: offer, Payments: map[string]PaymentResult{"paid": {}}})
	assert.Equal(t, map[string]string{"deposit": CheckNotRecomputed}, got)
	got = paymentsNotRun(StatusBooks{Offer: offer, Payments: map[string]PaymentResult{}})
	assert.Equal(t, map[string]string{"deposit": CheckNotRecomputed, "paid": NotRunNoAcceptorKey}, got)
}

// The provider's own skips reach PaymentsNotRun with their reasons: an offer
// pair callback that has no pair is no_offer_pair; an offerer that knows no
// acceptor key yet is no_acceptor_key.
func TestThePaymentsSkipsCarryTheirReasons(t *testing.T) {
	d := newPaymentDeal(t, nil, true)
	ag := *d.buyerAg.Body.(*AgreeBody)
	p := &StatusProvider{OfferPair: func(context.Context, string) ([]byte, error) { return nil, nil }}
	res, err := p.payments(context.Background(), d.offer, ag, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.Equal(t, map[string]string{"paid_as_agreed": NotRunNoOfferPair}, paymentsNotRun(StatusBooks{Offer: d.offer, Payments: res}))

	p.OfferPair = func(context.Context, string) ([]byte, error) { return d.offerPair, nil }
	ag.CounterpartyKey = NullOf[string]()
	res, err = p.payments(context.Background(), d.offer, ag, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, res, "the check ran, and skipped the row")
	assert.Empty(t, res)
	assert.Equal(t, map[string]string{"paid_as_agreed": NotRunNoAcceptorKey}, paymentsNotRun(StatusBooks{Offer: d.offer, Payments: res}))
}
