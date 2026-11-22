package deal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
)

// A limit of the user's rules that failed (evidencebook-deal#203): the card
// says it as a limit, the proceed button reads "anyway", and "within your
// limit" is never said.
func limitCard(t *testing.T, f dealRulesFinding, withDealCap bool) (cli.Ask, dealCheckResult) {
	t.Helper()
	w := cli.BuiltinWording()
	cap := int64(3500)
	amount := int64(3200)
	state := dealState{who: dealWho{Name: "Harbor Cases", Domain: "harborcases.example"}, intent: dealIntent{Verbatim: "buy it"},
		terms: dealTerms{Currency: "USD"}}
	if withDealCap {
		state.intent.MaxTotalMinor = &cap
	}
	snap := dealSnapshot{Action: "pay", AmountMinor: &amount}
	r := dealCheckResult{Verdict: "pause", Differences: diffs("rules_escalate", "rules_escalate"),
		Recipient: &dealRecipient{Name: "Harbor Cases", FirstTime: true},
		Options:   []dealOption{{ID: "hold"}, {ID: "proceed"}},
		Rules:     &dealRules{Status: "evaluated", Verdict: "escalate", Findings: []dealRulesFinding{f}}}
	labelOptions(w, &r, snap, state)
	return checkCard(w, state, snap, r, nil), r
}

func TestAFailedPerPurchaseLimitIsNamedAndTheButtonSaysAnyway(t *testing.T) {
	f := dealRulesFinding{ID: "r05", Check: "per_purchase", Verdict: "fail", Limit: json.RawMessage("2500"), Value: json.RawMessage("3200")}
	for _, withCap := range []bool{true, false} {
		card, _ := limitCard(t, f, withCap)
		assert.Contains(t, card.Facts, "over your $25 per-purchase limit")
		assert.NotContains(t, card.Facts, "within")
		assert.Equal(t, []string{"it's over your $25 per-purchase limit", "first time dealing with this seller"}, card.Why)
		assert.Equal(t, "Pay $32 anyway", card.Options[1].Label)
		assert.Equal(t, "Hold", card.Options[0].Label)
	}
}

func TestAFailedLimitWithNoCheckNameFallsBackToTheLimit(t *testing.T) {
	f := dealRulesFinding{ID: "x", Verdict: "fail", Limit: json.RawMessage("2500"), Value: json.RawMessage("3200")}
	card, _ := limitCard(t, f, true)
	assert.Contains(t, card.Facts, "over your $25 limit")
	// Free text is never said as a kind.
	f.Check = "Per Purchase! <b>"
	card, _ = limitCard(t, f, true)
	assert.Contains(t, card.Facts, "over your $25 limit")
}

func TestAFailedFindingWithoutALimitStaysAnOrdinaryAsk(t *testing.T) {
	for name, f := range map[string]dealRulesFinding{
		"no limit or value":              {ID: "r06", Verdict: "fail"},
		"no value":                       {ID: "r05", Check: "per_purchase", Verdict: "fail", Limit: json.RawMessage("2500")},
		"not a number":                   {ID: "r05", Verdict: "fail", Limit: json.RawMessage(`"2500"`), Value: json.RawMessage("3200")},
		"a floor: value under the limit": {ID: "r09", Check: "account_age", Verdict: "fail", Limit: json.RawMessage("30"), Value: json.RawMessage("5")},
		"value equal to the limit":       {ID: "r05", Check: "per_purchase", Verdict: "fail", Limit: json.RawMessage("2500"), Value: json.RawMessage("2500")},
	} {
		t.Run(name, func(t *testing.T) {
			card, _ := limitCard(t, f, true)
			assert.Contains(t, card.Facts, "within your $35 limit")
			assert.Equal(t, "Pay $32", card.Options[1].Label)
			assert.Equal(t, []string{"your rules ask for your OK on this", "first time dealing with this seller"}, card.Why)
		})
	}
}

func TestAPassedLimitIsNotBroken(t *testing.T) {
	f := dealRulesFinding{ID: "r05", Check: "per_purchase", Verdict: "pass", Limit: json.RawMessage("5000"), Value: json.RawMessage("3200")}
	card, _ := limitCard(t, f, true)
	assert.Contains(t, card.Facts, "within your $35 limit")
	assert.Equal(t, "Pay $32", card.Options[1].Label)
	assert.False(t, strings.Contains(strings.Join(card.Why, " "), "per-purchase"))
}

// A failed finding on rules that allowed is not an ask: nothing breaks.
func TestAFailedLimitUnderAnAllowingVerdictIsNotBroken(t *testing.T) {
	f := dealRulesFinding{ID: "r05", Check: "per_purchase", Verdict: "fail", Limit: json.RawMessage("2500"), Value: json.RawMessage("3200")}
	_, ok := brokenLimit(&dealRules{Status: "evaluated", Verdict: "allow", Findings: []dealRulesFinding{f}}, func(int64) string { return "$25" })
	assert.False(t, ok)
	_, ok = brokenLimit(&dealRules{Status: "evaluated", Verdict: "deny", Findings: []dealRulesFinding{f}}, func(int64) string { return "$25" })
	assert.True(t, ok)
}
