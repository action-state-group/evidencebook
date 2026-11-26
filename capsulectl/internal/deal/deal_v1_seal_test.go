// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// receiptTop is the top of the holder's own `deal report --html` page: the
// top line's case and words, and every seal the page draws.
type receiptTop struct {
	Line   string   `json:"line"`
	Text   string   `json:"text"`
	Seals  []string `json:"seals"`
	Errors []any    `json:"errors"`
}

// reportTop writes the active side's receipt page for dealID and reads its
// top in Chrome.
func reportTop(t *testing.T, chrome *headlessChrome, dealID string) receiptTop {
	t.Helper()
	file := filepath.Join(t.TempDir(), "receipt.html")
	dealRun(t, "report", "--deal", dealID, "--html", file)
	page := chrome.open(t, "file://"+file, presentationViews[1])
	var got receiptTop
	page.eval(t, `(() => {
  const root = document.querySelector('[data-receipt-module="capsuleviewer.receipt.deal/v1"]');
  const top = root.querySelector("[data-top-line]");
  return {
    line: top ? top.getAttribute("data-top-line") : "",
    text: top ? top.textContent : "",
    seals: [...root.querySelectorAll("svg[data-seal]")].map((e) => e.getAttribute("data-seal")),
    errors: window.__renderErrors || [],
  };
})()`, &got)
	assert.Empty(t, got.Errors)
	return got
}

// The receipt's seal draws only in the ruling's two cases (capsule-viewer#382,
// https://github.com/action-state-group/capsule-viewer/issues/382#issuecomment-7190374309):
// "Done" when the deal is settled, every term is met and both sides say the
// same, and the holder's settle record is held (TestPresentationV1ReceiptDesign);
// "Didn't go as agreed" on a final ruling of not met. Every other case draws
// no seal and says where the deal stands in words (evidencebook#993;
// CAPSULECTL_CHROME).
func TestPresentationV1ReceiptSeal(t *testing.T) {
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}

	t.Run("agreed so far", func(t *testing.T) {
		usePluginVerify(t)
		chrome := startHeadlessChrome(t, binary)
		// Both sides said the term was met, and the contest window is still
		// open (paidLegs stops at 2026-11-04 10:20; the window ends 23:59).
		h := paidLegs(t, "6000")
		got := reportTop(t, chrome, h.dealID)
		assert.Equal(t, "all.agreed_so_far", got.Line)
		assert.Equal(t, "Both sides say the same so far; still open to dispute", strings.TrimSpace(got.Text))
		assert.Empty(t, got.Seals)
	})

	t.Run("paid, waiting for the seller", func(t *testing.T) {
		usePluginVerify(t)
		chrome := startHeadlessChrome(t, binary)
		h := agreedBoth(t, paymentOffer)
		raw, err := os.ReadFile(h.offerFile)
		require.NoError(t, err)
		sent, err := xdealv1.VerifySent(raw, nil)
		require.NoError(t, err)
		// The buyer's payer leg and statement only: the seller has said
		// nothing yet.
		clockAt(t, time.Date(2026, 11, 1, 10, 20, 0, 0, time.UTC))
		h.buyer.act(t)
		withSession(t, func(s *dealSession) {
			_ = settleLeg(t, s, h.dealID, &xdealv1.SettleBody{Leg: xdealv1.LegPayer,
				Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: xdealv1.ValueMet}}, Check: refOf(strings.Repeat("1", 64))},
				map[string]any{"version": "0", "leg": "payer_observed", "sealer_role": "payer", "terms_ref": sent.CapsuleID,
					"amount":      map[string]any{"value": "6000", "assetCode": "USD", "assetScale": 2},
					"payment_ref": map[string]any{"type": "acp.order_id", "value": "order-0001"},
					"status":      "settled", "observed_at": "2026-11-01T10:15:00Z"})
		})
		clockAt(t, time.Date(2026, 11, 2, 10, 20, 0, 0, time.UTC))
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		got := reportTop(t, chrome, h.dealID)
		assert.Equal(t, "paid_waiting", got.Line)
		assert.Equal(t, "Payment recorded. Waiting for the seller to say it too", strings.TrimSpace(got.Text))
		assert.Empty(t, got.Seals)
	})

	t.Run("not as agreed", func(t *testing.T) {
		usePluginVerify(t)
		chrome := startHeadlessChrome(t, binary)
		h, adj := adjudicatedDeal(t)
		h.buyer.act(t)
		dispute := filepath.Join(t.TempDir(), "dispute.json")
		dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
		buyerCopy := adjudicatorCopy(t, h.buyer, h.dealID)
		sellerCopy := adjudicatorCopy(t, h.seller, h.dealID)
		adj.act(t)
		ruling := filepath.Join(t.TempDir(), "ruling.json")
		dealRun(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller", "--final",
			"--dispute", dispute, "--copy", buyerCopy, "--copy", sellerCopy, "--out", ruling)
		h.buyer.act(t)
		require.Equal(t, "kept, and counted", dealRun(t, "receive", ruling)["record"])
		clockAt(t, cli.DealClock().Add(72*time.Hour))
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		got := reportTop(t, chrome, h.dealID)
		assert.Equal(t, "ruled_not_met", got.Line)
		assert.Equal(t, []string{"not_as_agreed"}, got.Seals)
	})
}
