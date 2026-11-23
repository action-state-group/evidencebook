// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pausedV1Check runs a check that pauses and returns its check id and card.
func pausedV1Check(t *testing.T, dealID, input string) (string, string) {
	t.Helper()
	out := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, input))
	require.Equal(t, "pause", out["verdict"], "%v", out)
	return out["check_id"].(string), shownCard(t, out["card"].(string))
}

// An answer deal note marked not applied (a later check came first) does not
// authorize a payment or an act; an answer to the newer check does
// (evidencebook#790).
func TestAStaleAnswerAuthorizesNothingOnV1(t *testing.T) {
	ref := strings.Repeat("ab", 32)
	pay := `{"action":"pay","amount_minor":6000,"authorized_max_minor":6000,"terms":{"item":"lamp","price_minor":6000,"currency":"USD"},"payment_ref":"` + ref + `"}`
	commit := `{"action":"commit","amount_minor":6000,"terms":{"item":"lamp","price_minor":6000,"currency":"USD"}}`
	h := struct{ dealID string }{noLayerAgreed(t)}
	first, card := pausedV1Check(t, h.dealID, pay)
	pausedV1Check(t, h.dealID, commit)
	res, err := answer(t, h.dealID, first, "yes", card)
	require.NoError(t, err)
	assert.Equal(t, false, res["proceed"], "the answer came after a later check")
	sent := []string{"settle", "--deal", h.dealID, "--sent", "--payment-ref-type", "ln.payment_hash", "--check"}
	assert.Contains(t, v1Err(t, append(sent, first)...), "was not applied")

	// The same for an act on a commit check.
	act, actCard := pausedV1Check(t, h.dealID, commit)
	again, againCard := pausedV1Check(t, h.dealID, pay)
	_, err = answer(t, h.dealID, act, "yes", actCard)
	require.NoError(t, err)
	perform := func(check string) []string {
		return []string{"perform", "--deal", h.dealID, "--check", check, "--act", "deliver", "--observed", "self", "--out", filepath.Join(t.TempDir(), "p.json")}
	}
	assert.Contains(t, v1Err(t, perform(act)...), "was not applied")

	// An answer to the newest check is applied, and the payment goes ahead.
	res, err = answer(t, h.dealID, again, "yes", againCard)
	require.NoError(t, err)
	require.Equal(t, true, res["proceed"], "%v", res)
	dealRun(t, append(sent, again)...)
}
