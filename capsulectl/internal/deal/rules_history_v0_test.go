package deal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// everydayV0Paid is a legacy v0 sticker deal (its steps in the SQLite index,
// so a test can break a link) paid with an act that cites its check: checked
// under an allowing stub, or, with approved, under an escalating one and
// approved on the card shown. Everyday Rules are then pinned with limits well
// above the price. It returns the deal and the act.
func everydayV0Paid(t *testing.T, approved bool) (dealID, actID string) {
	t.Helper()
	legacyDeals(t)
	dealID = stickerDeal(t, "card", false)
	verdict, finding := "allow", passFinding
	if approved {
		verdict, finding = "escalate", `{"id":"new-merchant","check":"caps/1","verdict":"fail","reason":"first time paying this seller"}`
	}
	pinChecker(t, map[string]any{"command": []string{stubChecker(t, "rules", checkerPrints(verdict, finding))}})
	check := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, stickerPay))
	if approved {
		require.Equal(t, "pause", check["verdict"])
		_, err := answer(t, dealID, check["check_id"].(string), "Pay", shownCard(t, check["card"].(string)))
		require.NoError(t, err)
	} else {
		require.Equal(t, "pass", check["verdict"])
	}
	act := payNow(t, dealID, 454)
	require.Equal(t, false, act["unchecked"])
	pinEverydayLimits(t, 7000, 10000)
	return dealID, act["capsule_id"].(string)
}

// againV0 is the Everyday check of a payment to the same merchant on a
// second deal of the profile.
func againV0(t *testing.T) map[string]any {
	t.Helper()
	second := dealRun(t, "open", "--input", writeJSON(t, `{"type":"purchase","channel":"web",
		"intent":{"verbatim":"another otter sticker for at most 5 dollars","max_total_minor":500,"allowed":["pay"]},
		"who":{"name":"Sticker Marketplace","domain":"stickers.example"},
		"terms":{"item":"otter sticker","price_minor":454,"currency":"USD"},
		"recourse":{"rail":"card","refundable":true}}`))["deal_id"].(string)
	return dealRun(t, "check", "--deal", second, "--input", writeJSON(t, stickerPay))
}

// v0History is the rules history, read on the deal.
func v0History(t *testing.T, dealID string) (history []interface{}, scope *dealRulesHistory) {
	t.Helper()
	withSession(t, func(s *dealSession) {
		require.NoError(t, s.useDeal(t.Context(), dealID, false))
		var err error
		history, scope, err = s.rulesHistory(t.Context())
		require.NoError(t, err)
	})
	return history, scope
}

// breakV0Link rewrites one field of the deal's step of kind in the SQLite
// index.
func breakV0Link(t *testing.T, dealID, kind, path, value string) {
	t.Helper()
	withSession(t, func(s *dealSession) {
		res, err := s.db.ExecContext(t.Context(), `UPDATE deal_steps SET local=json_set(local, ?, ?) WHERE deal_id=? AND kind=?`, path, value, dealID, kind)
		require.NoError(t, err)
		n, err := res.RowsAffected()
		require.NoError(t, err)
		require.EqualValues(t, 1, n, "one %s step", kind)
	})
}

// An intact x-deal-v0 history act carries who it was with, the history is
// complete, and a payment to the same merchant on another deal reads it: r02
// and r06 pass.
func TestAV0HistoryActWithItsCheckIsComplete(t *testing.T) {
	for _, approved := range []bool{false, true} {
		dealID, actID := everydayV0Paid(t, approved)
		history, scope := v0History(t, dealID)
		require.Len(t, history, 1)
		assert.True(t, scope.Complete)
		record := history[0].(map[string]interface{})
		assert.Equal(t, actID, record["capsule_id"])
		assert.NotNil(t, record["counterparty_profile"])
		assert.NotNil(t, record["counterparty"])
	}

	out := againV0(t)
	assert.Equal(t, true, out["rules"].(map[string]any)["history"].(map[string]any)["complete"])
	for _, id := range []string{"r02-ordinary-purchase", "r06-new-merchant"} {
		assert.Equal(t, "pass", rulesFinding(t, out, id)["verdict"], "%s: an earlier payment to this merchant", id)
	}
}

// An x-deal-v0 history act whose chain to its check is broken at any link is
// kept without who it was with, and the history says it is not complete: the
// merchant is never read as new without saying so (evidencebook#955), as for
// v1 (TestAV1HistoryActWithABrokenCheckLinkIsIncomplete).
func TestAV0HistoryActWithABrokenCheckLinkIsIncomplete(t *testing.T) {
	missing := "no-such-capsule"
	for _, tc := range []struct {
		name              string
		approved          bool
		kind, path, value string
	}{
		{"its authorization is not found", false, "act", "$.act.authorized_by", missing},
		{"its approval's check is not found", true, "approval", "$.approval.check", missing},
		{"its check names no snapshot", false, "check", "$.check.snapshot", ""},
		{"its sealed check is not found", false, "check", "$.check.snapshot", missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dealID, actID := everydayV0Paid(t, tc.approved)
			breakV0Link(t, dealID, tc.kind, tc.path, tc.value)
			history, scope := v0History(t, dealID)
			require.Len(t, history, 1, "the act is kept")
			assert.False(t, scope.Complete, "a broken link is said, not read as a new merchant")
			record := history[0].(map[string]interface{})
			assert.Equal(t, actID, record["capsule_id"])
			assert.Nil(t, record["counterparty"])

			out := againV0(t)
			assert.Equal(t, false, out["rules"].(map[string]any)["history"].(map[string]any)["complete"])
			for _, id := range []string{"r02-ordinary-purchase", "r06-new-merchant"} {
				assert.NotEqual(t, "pass", rulesFinding(t, out, id)["verdict"], id)
			}
		})
	}
}

// An unchecked x-deal-v0 act cites no authorization: it is kept without a
// counterparty and leaves the history complete.
func TestAnUncheckedV0HistoryActLeavesTheHistoryComplete(t *testing.T) {
	dealID := stickerDeal(t, "card", false)
	act := payNow(t, dealID, 100)
	require.Equal(t, true, act["unchecked"])
	history, scope := v0History(t, dealID)
	require.Len(t, history, 1)
	assert.True(t, scope.Complete)
	assert.NotContains(t, history[0].(map[string]interface{}), "counterparty")
}
