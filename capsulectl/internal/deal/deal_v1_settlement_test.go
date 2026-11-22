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
	out := dealRun(t, "settlement", "--deal", dealID)
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
	assert.Equal(t, "passed", row["check"], "the report row's own word")
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
	assert.Equal(t, "failed", row["check"], "the report row's own word")
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
