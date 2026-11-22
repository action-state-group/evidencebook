// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two pay checks that each fit the rules' 7-day cap of 5000, made before
// either payment leaves: the first send is accepted, and the second, which the first
// now puts over the cap, is refused at --sent and seals nothing (#718,
// Sherlock2's condition 1). Once the first fails, the second fits again.
func TestASecondSendOverTheCapIsRefused(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	pinStub(t, "10000", "5000")
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	check := func(ref string) string {
		in := fmt.Sprintf(`{"action":"pay","amount_minor":3000,"authorized_max_minor":3000,"terms":{"item":"lamp","price_minor":3000,"currency":"USD"},"payment_ref":%q}`, ref)
		out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, in))
		require.Equal(t, "pass", out["verdict"], "each fits the cap alone: %v", out)
		var d string
		withSession(t, func(s *dealSession) {
			journal, err := s.v1DealJournal(t.Context(), h.dealID)
			require.NoError(t, err)
			d = checkDigest(t, journal)
		})
		return d
	}
	a, b := check(strings.Repeat("a1", 32)), check(strings.Repeat("b2", 32))
	dealRun(t, "settle", "--deal", h.dealID, "--check", a, "--sent", "--payment-ref-type", "ln.payment_hash")
	msg := v1Err(t, "settle", "--deal", h.dealID, "--check", b, "--sent", "--payment-ref-type", "ln.payment_hash")
	assert.Contains(t, msg, "no longer holds")
	assert.Contains(t, msg, "Nothing was sent")
	assert.Nil(t, sendRow(t, h.dealID, b))
	assert.Len(t, v1ActEvents(t, h.dealID), 1)

	dealRun(t, "settle", "--deal", h.dealID, "--check", a, "--status", "failed", "--out", filepath.Join(t.TempDir(), "a.json"))
	dealRun(t, "settle", "--deal", h.dealID, "--check", b, "--sent", "--payment-ref-type", "ln.payment_hash")
}
