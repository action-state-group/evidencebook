// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"fmt"
	"os"
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
	assert.Contains(t, msg, "no longer passes")
	assert.Contains(t, msg, "Nothing was sent")
	assert.Nil(t, sendRow(t, h.dealID, b))
	assert.Len(t, v1ActEvents(t, h.dealID), 1)

	dealRun(t, "settle", "--deal", h.dealID, "--check", a, "--status", "failed", "--out", filepath.Join(t.TempDir(), "a.json"))
	dealRun(t, "settle", "--deal", h.dealID, "--check", b, "--sent", "--payment-ref-type", "ln.payment_hash")
}

// sequencedChecker is a rules checker that answers first, on the check, and
// then, on every later run (the re-run at --sent), as later.
func sequencedChecker(t *testing.T, first, later string) {
	t.Helper()
	// The checker runs from a hashed copy, so its count lives at a fixed path.
	count := filepath.Join(t.TempDir(), "count")
	body := `n=$(cat "` + count + `" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "` + count + `"
if [ "$n" -eq 1 ]; then ` + first + `; else ` + later + `; fi`
	pinChecker(t, map[string]any{"command": []string{stubChecker(t, "rules-seq", body)}})
}

// buyerPaused is a deal whose buyer's pay check paused on the rules and was
// approved with its card; it returns the check's digest.
func buyerPaused(t *testing.T, first, later string) (*handshakeV1, string) {
	t.Helper()
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	sequencedChecker(t, first, later)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	in := fmt.Sprintf(payInput, `,"payment_ref":"`+strings.Repeat("ab", 32)+`"`)
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, in))
	require.Equal(t, "pause", out["verdict"], "%v", out)
	_, err := answer(t, h.dealID, out["check_id"].(string), "yes", shownCard(t, out["card"].(string)))
	require.NoError(t, err)
	var d string
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		d = checkDigest(t, journal)
	})
	return h, d
}

const newMerchant = `{"id":"new-merchant","check":"caps/1","verdict":"fail","reason":"first time paying this seller"}`

// A check the user approved on an escalation is refused at --sent when the
// rules now deny, when they now fail a rule the user never saw, or when they
// cannot answer; the same escalation sends.
func TestAnApprovedEscalationIsAskedAgainAtTheSend(t *testing.T) {
	esc := checkerPrints("escalate", newMerchant)
	weekly := `{"id":"weekly","check":"caps/1","verdict":"fail","reason":"over the 7-day limit","limit":5000,"value":7500}`
	for name, c := range map[string]struct {
		later, want string
	}{
		"now denies":         {checkerPrints("deny", denyFinding), "denied by your rules"},
		"a new failing rule": {checkerPrints("escalate", newMerchant+","+weekly), "over the 7-day limit"},
		"cannot answer":      {"exit 3", "could not answer"},
		"the same id fails":  {checkerPrints("escalate", strings.Replace(newMerchant, `"id":"new-merchant"`, `"id":"new_merchant"`, 1)), "new_merchant"},
	} {
		t.Run(name, func(t *testing.T) {
			h, check := buyerPaused(t, esc, c.later)
			msg := v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
			assert.Contains(t, msg, c.want)
			assert.Contains(t, msg, "Nothing was sent")
			assert.Nil(t, sendRow(t, h.dealID, check))
		})
	}
	t.Run("the same escalation", func(t *testing.T) {
		h, check := buyerPaused(t, esc, esc)
		input := filepath.Join(os.Getenv("CAPSULECTL_PLUGIN_ROOTS"), "rules-seq.input")
		first, err := os.ReadFile(input)
		require.NoError(t, err)
		require.Contains(t, string(first), `"counterparty_profile"`, "the check's payee companion")
		dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
		again, err := os.ReadFile(input)
		require.NoError(t, err)
		assert.Contains(t, string(again), `"counterparty_profile"`, "the re-run reads the same payee companion")
	})
}

// A check whose rule could not be evaluated, approved as it was, is refused
// at --sent when that rule now fails; a check approved with the rules
// unchecked sends while the checker is still down.
func TestARuleThatWasNotEvaluatedAndNowFailsIsRefused(t *testing.T) {
	notEval := `{"id":"weekly","check":"caps/1","verdict":"not_evaluable","reason":"the record carries no week of history"}`
	fails := `{"id":"weekly","check":"caps/1","verdict":"fail","reason":"over the 7-day limit","limit":5000,"value":7500}`
	h, check := buyerPaused(t, checkerPrints("not_evaluable", notEval), checkerPrints("escalate", fails))
	msg := v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	assert.Contains(t, msg, "over the 7-day limit")

	h, check = buyerPaused(t, "exit 3", "exit 3")
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")

	// Approved with the checker down, which then answers allow with a rule
	// it declares but does not measure: that rule is no failure.
	declared := `{"id":"monthly","check":"caps/1","verdict":"not_evaluable","reason":"` + rulesDeclaredNotMeasured + `"}`
	h, check = buyerPaused(t, "exit 3", checkerPrints("allow", declared))
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")

	// Approved on an escalation that also carried that declared rule; the
	// same rule id now cannot be evaluated for real: that was never
	// approved.
	realNotEval := `{"id":"monthly","check":"caps/1","verdict":"not_evaluable","reason":"the record carries no month of history"}`
	h, check = buyerPaused(t, checkerPrints("escalate", newMerchant+","+declared), checkerPrints("escalate", newMerchant+","+realNotEval))
	assert.Contains(t, v1Err(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash"), "no month of history")
}
