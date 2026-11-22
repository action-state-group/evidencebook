package deal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paidLegs seals the buyer's payer leg and the seller's payee leg on a
// payment offer's deal (received is the payee's amount), the seller
// receiving the buyer's, and a due tick checkpoints the seller's book.
// The seller acts last.
func paidLegs(t *testing.T, received string) *handshakeV1 {
	t.Helper()
	h := agreedBoth(t, paymentOffer)
	raw, err := os.ReadFile(h.offerFile)
	require.NoError(t, err)
	sent, err := xdealv1.VerifySent(raw, nil)
	require.NoError(t, err)
	usd := func(v string) map[string]any { return map[string]any{"value": v, "assetCode": "USD", "assetScale": 2} }
	ref := map[string]any{"type": "acp.order_id", "value": "order-0001"}
	clockAt(t, time.Date(2026, 11, 1, 10, 20, 0, 0, time.UTC))
	h.buyer.act(t)
	var payer string
	withSession(t, func(s *dealSession) {
		payer = settleLeg(t, s, h.dealID, &xdealv1.SettleBody{Leg: xdealv1.LegPayer,
			Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: xdealv1.ValueMet}}, Check: refOf(strings.Repeat("1", 64))},
			map[string]any{"version": "0", "leg": "payer_observed", "sealer_role": "payer", "terms_ref": sent.CapsuleID,
				"amount": usd("6000"), "payment_ref": ref, "status": "settled", "observed_at": "2026-11-01T10:15:00Z"})
	})
	h.seller.act(t)
	require.Equal(t, "kept, and counted", dealRun(t, "receive", payer)["record"])
	withSession(t, func(s *dealSession) {
		_ = settleLeg(t, s, h.dealID, &xdealv1.SettleBody{Leg: xdealv1.LegPayee,
			Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: xdealv1.ValueMet}}},
			map[string]any{"version": "0", "leg": "payee_observed", "sealer_role": "payee", "terms_ref": sent.CapsuleID,
				"received": usd(received), "receive_fee": usd("0"), "payment_ref": ref, "status": "settled", "observed_at": "2026-11-01T10:18:00Z"})
	})
	clockAt(t, time.Date(2026, 11, 4, 10, 20, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	return h
}

// settlementRow is deal settlement's one row of a v1 deal.
func settlementRow(t *testing.T, dealID string) map[string]any {
	t.Helper()
	before := v1StoreFiles(t)
	out := dealRun(t, "settlement", "--deal", dealID)
	assert.Equal(t, before, v1StoreFiles(t), "deal settlement writes nothing")
	assert.Equal(t, "listed", out["rows_state"])
	rows := out["rows"].([]any)
	require.Len(t, rows, 1, "%v", out)
	assert.Contains(t, out["settled_note"], "No act on this deal carries the person's own signature yet")
	return rows[0].(map[string]any)
}

// Agreed legs with the terms' amount pass, as the library's check reads
// them; the report row's check says the same.
func TestAV1SettlementPasses(t *testing.T) {
	h := paidLegs(t, "6000")
	row := settlementRow(t, h.dealID)
	assert.Equal(t, "paid_as_agreed", row["id"])
	pay := row["payment"].(map[string]any)
	assert.Equal(t, "pass", pay["check"], "%v", row)
	assert.Equal(t, "passed", row["report_check"], "the report row's own word")
	assert.NotEmpty(t, pay["checked_over"])
	assert.NotContains(t, row, "not_run")
}

// Legs that disagree fail with the library's reason, printed as it is.
func TestAV1SettlementShowsAMismatch(t *testing.T) {
	h := paidLegs(t, "5000")
	row := settlementRow(t, h.dealID)
	pay := row["payment"].(map[string]any)
	assert.Equal(t, "fail", pay["check"], "%v", row)
	assert.Equal(t, xdealv1.ReasonMismatch, pay["reason"])
	assert.Equal(t, "failed", row["report_check"], "the report row's own word")
}

// A row whose owed is an amount but is not recomputed is no payment row: its
// check is not run, and it says why.
func TestAV1SettlementSaysWhyACheckIsNotRun(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	row := settlementRow(t, h.dealID)
	assert.Nil(t, row["payment"])
	assert.Equal(t, xdealv1.CheckNotRecomputed, row["not_run"])
}

// v0's leg flags are refused, and a deal with no book names deal agree.
func TestAV1SettlementRefusals(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	assert.Contains(t, v1Err(t, "settlement", "--deal", h.dealID), "`deal agree`")
	for _, flag := range []string{"--leg", "--object", "--payer-key", "--payee-key"} {
		assert.Contains(t, v1Err(t, "settlement", "--deal", h.dealID, flag, filepath.Join(t.TempDir(), "x")), "reads the other side's legs", flag)
	}
}

// Of an offer's rows, only those whose owed is an amount are listed, in
// the offer's order: a recomputed one with its check, a non-recomputed one
// not run; a row whose owed is no amount is not listed.
func TestAV1SettlementListsOnlyAmountRows(t *testing.T) {
	mixed := func(key string) string {
		return strings.Replace(paymentOffer(key), `"window_end": {"at": "2026-11-04T23:59:59Z"}}`, `"window_end": {"at": "2026-11-04T23:59:59Z"}},
      {"id": "deposit", "owed_by": "buyer", "owed": {"amount_minor": 1000, "currency": "USD"},
       "due": {"at": "2026-11-01T23:59:59Z"}, "window_end": {"at": "2026-11-04T23:59:59Z"}},
      {"id": "delivered", "owed_by": "seller", "owed": {},
       "due": {"at": "2026-11-01T23:59:59Z"}, "window_end": {"at": "2026-11-04T23:59:59Z"}}`, 1)
	}
	h := agreedBoth(t, mixed)
	h.buyer.act(t)
	out := dealRun(t, "settlement", "--deal", h.dealID)
	rows := out["rows"].([]any)
	require.Len(t, rows, 2, "%v", out)
	first, second := rows[0].(map[string]any), rows[1].(map[string]any)
	assert.Equal(t, "paid_as_agreed", first["id"])
	assert.NotNil(t, first["payment"], "a payment row's check ran: %v", first)
	assert.Equal(t, "deposit", second["id"])
	assert.Nil(t, second["payment"])
	assert.Equal(t, xdealv1.CheckNotRecomputed, second["not_run"])
}

// Before the deal starts the report lists no rows, and the output says so.
func TestAV1SettlementBeforeTheDealStarts(t *testing.T) {
	h := newHandshakeV1With(t, paymentOffer) // the seller has agreed; the buyer's agreement has not arrived
	out := dealRun(t, "settlement", "--deal", h.dealID)
	assert.Equal(t, "not_started", out["rows_state"])
	assert.Empty(t, out["rows"])
	assert.Contains(t, out["rows_note"], "has not started")
}

// An offer kept without its signed copy cannot be checked, the output says
// what to do, and receiving the offer again keeps it.
func TestAV1SettlementWithoutTheOffersPair(t *testing.T) {
	h := agreedBoth(t, paymentOffer)
	h.seller.act(t)
	_, err := openWritableDB(t).Exec(`UPDATE deal_v1_offers SET pair=NULL WHERE deal_id=?`, h.dealID)
	require.NoError(t, err)
	row := settlementRow(t, h.dealID)
	assert.Equal(t, xdealv1.NotRunNoOfferPair, row["not_run"])
	assert.Contains(t, row["not_run_note"], "receive the offer file again")
	dealRun(t, "receive", h.offerFile)
	row = settlementRow(t, h.dealID)
	assert.NotContains(t, row, "not_run", "the note's remedy works: %v", row)
	assert.NotNil(t, row["payment"])
}
