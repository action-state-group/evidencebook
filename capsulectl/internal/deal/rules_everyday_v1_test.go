// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capsule-engine's Everyday Rules with a person's own limits (`guard check
// --limits`), pinned as a buyer profile's rules checker and run by `deal
// check` on x-deal-v1 deals (evidencebook#890). The engine is the version
// go.mod names as a tool (capsule-engine go/v0.8.0, the version the ARD
// demo's RUNBOOK builds), built once for the package's tests.
//
// The limits file is the person's own configuration, read at each check:
// the pin hashes the engine alone, and the ruleset's definition digest is
// the pack's, with or without --limits. What shows the person's limits
// were applied is each caps finding's limit, which these tests read.

var (
	everydayEngineOnce sync.Once
	everydayEnginePath string
	everydayEngineErr  error
)

// everydayEngine is a copy of capsule-engine in a new trusted plugin root,
// ready to pin.
func everydayEngine(t *testing.T) string {
	t.Helper()
	everydayEngineOnce.Do(func() {
		dir, err := os.MkdirTemp("", "capsule-engine-")
		if err != nil {
			everydayEngineErr = err
			return
		}
		everydayEnginePath = filepath.Join(dir, "capsule-engine")
		out, err := exec.Command("go", "build", "-o", everydayEnginePath, "github.com/action-state-group/capsule-engine/go/cmd/capsule-engine").CombinedOutput()
		if err != nil {
			everydayEngineErr = fmt.Errorf("go build capsule-engine: %v: %s", err, out)
		}
	})
	require.NoError(t, everydayEngineErr)
	raw, err := os.ReadFile(everydayEnginePath)
	require.NoError(t, err)
	root := t.TempDir()
	t.Setenv("CAPSULECTL_PLUGIN_ROOTS", root)
	engine := filepath.Join(root, "capsule-engine")
	require.NoError(t, os.WriteFile(engine, raw, 0o700))
	return engine
}

// everydayLimits is a person's limits on purchases as capsule-engine's
// policy-profile/v0: the most for one order and the most over a rolling 7
// days, in minor units.
func everydayLimits(perOrder, weekly int) string {
	return fmt.Sprintf(`{"format":"policy-profile/v0","packs":[{"pack":"asg/everyday","parameters":{"caps":{"caps_minor":{"money.purchase":%d},"per_action_minor":{"money.purchase":%d}}}}]}`, weekly, perOrder)
}

// pinEverydayLimits pins the engine's Everyday Rules as the profile's rules
// checker, reading the limits file at limitsPath (written with
// everydayLimits unless the test leaves it out).
func pinEverydayLimitsAt(t *testing.T, limitsPath string) {
	t.Helper()
	pinChecker(t, map[string]any{"command": []string{everydayEngine(t), "guard", "check", "--limits", limitsPath}})
}

func pinEverydayLimits(t *testing.T, perOrder, weekly int) {
	t.Helper()
	pinEverydayLimitsAt(t, writeJSON(t, everydayLimits(perOrder, weekly)))
}

// everydayIntent caps the buyer's own total well above every amount below,
// so only the person's limits decide.
const everydayIntent = `{"verbatim":"buy the lamp, at most 200 dollars","max_total_minor":20000}`

// everydayOffer is v1OfferInput with the price in its terms too, as the ARD
// demo's offers state it (evidencebook-skills demo/ard/party.py): the price
// was agreed, so a payment of it changes no material term.
func everydayOffer(key string) string {
	return strings.Replace(offerTo(key), `"terms": {"item": "lamp", "currency": "USD"}`, `"terms": {"item": "lamp", "price_minor": 6000, "currency": "USD"}`, 1)
}

// everydayPayCheck is the buyer's pay check of amount on the deal.
func everydayPayCheck(t *testing.T, dealID string, amount int, paymentRef string) map[string]any {
	t.Helper()
	in := fmt.Sprintf(`{"action":"pay","amount_minor":%d,"authorized_max_minor":%d,"terms":{"item":"lamp","price_minor":%d,"currency":"USD"},"payment_ref":%q}`, amount, amount, amount, paymentRef)
	return dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, in))
}

// everydayBuyer is a v1 deal both sides agreed to at 6000, with the buyer's
// intent; the buyer is acting.
func everydayBuyer(t *testing.T) *handshakeV1 {
	t.Helper()
	h := agreedBoth(t, everydayOffer)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, everydayIntent), "--opening", writeJSON(t, v1Opening))
	return h
}

// againWith is a second deal between h's two profiles, agreed at 6000, with
// the buyer's intent; the buyer is acting.
func againWith(t *testing.T, h *handshakeV1) *handshakeV1 {
	t.Helper()
	g := &handshakeV1{buyer: h.buyer, seller: h.seller}
	h.buyer.act(t)
	g.offerFile = filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, everydayOffer(h.seller.party.signer)), "--out", g.offerFile)
	g.dealID, g.offer = res["deal_id"].(string), res["offer"].(string)
	h.seller.act(t)
	dealRun(t, "receive", g.offerFile)
	g.sellerAgree = filepath.Join(t.TempDir(), "agreement.json")
	dealRun(t, "agree", g.offer, "--admit", sealAdmit(t, "GO"), "--out", g.sellerAgree)
	h.buyer.act(t)
	dealRun(t, "receive", g.sellerAgree)
	g.buyerAgr = filepath.Join(t.TempDir(), "buyer-agreement.json")
	dealRun(t, "agree", g.offer, "--admit", sealAdmit(t, "GO"), "--out", g.buyerAgr)
	h.seller.act(t)
	dealRun(t, "receive", g.buyerAgr)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", g.dealID, "--kind", "intent", "--input", writeJSON(t, everydayIntent), "--opening", writeJSON(t, v1Opening))
	return g
}

// rulesFinding is the evaluated rules' finding with id, from a check's output.
func rulesFinding(t *testing.T, check map[string]any, id string) map[string]any {
	t.Helper()
	rules, ok := check["rules"].(map[string]any)
	require.True(t, ok, "%v", check)
	require.Equal(t, "evaluated", rules["status"], "%v", rules)
	for _, f := range rules["findings"].([]any) {
		if m := f.(map[string]any); m["id"] == id {
			return m
		}
	}
	require.Failf(t, "no finding", "%s in %v", id, rules["findings"])
	return nil
}

// assertCaps checks one caps finding: its verdict, and the person's limit
// and the value it read.
func assertCaps(t *testing.T, check map[string]any, period, verdict string, limit, value int) {
	t.Helper()
	f := rulesFinding(t, check, "r05-spending-limits#"+period)
	assert.Equal(t, verdict, f["verdict"], period)
	assert.EqualValues(t, limit, f["limit"], "%s: the person's limit, not the pack's", period)
	assert.EqualValues(t, value, f["value"], period)
}

// payOn has the buyer, acting, answer check yes if it paused (on the card it
// returned) and count the payment as sent.
func payOn(t *testing.T, dealID string, check map[string]any) {
	t.Helper()
	if check["verdict"] == "pause" {
		_, err := answer(t, dealID, check["check_id"].(string), "Buy anyway", shownCard(t, check["card"].(string)))
		require.NoError(t, err)
	}
	dealRun(t, "settle", "--deal", dealID, "--check", check["check_id"].(string), "--sent", "--payment-ref-type", "ln.payment_hash")
}

func TestEverydayLimitsOnAV1Deal(t *testing.T) {
	t.Run("within the limits", func(t *testing.T) {
		h := everydayBuyer(t)
		pinEverydayLimits(t, 7000, 10000)
		out := everydayPayCheck(t, h.dealID, 6000, v1LnHash)
		rules := out["rules"].(map[string]any)
		assert.Equal(t, "asg/everyday/0.3.8", rules["ruleset_id"])
		assertCaps(t, out, "per_purchase", "pass", 7000, 6000)
		assertCaps(t, out, "7d", "pass", 10000, 6000)
		assert.NotContains(t, differenceTexts(out), "r05-spending-limits", "a purchase within the limits raises none")
		assert.NotContains(t, differenceTexts(out), "r09-material-terms-changed", "the agreed price changes no material term")
	})

	t.Run("over the per-order limit", func(t *testing.T) {
		h := everydayBuyer(t)
		pinEverydayLimits(t, 5000, 10000)
		out := everydayPayCheck(t, h.dealID, 6000, v1LnHash)
		// A failing caps finding on a person's own limit holds the order for
		// their OK (escalate), never a refusal (ops u325).
		require.Equal(t, "pause", out["verdict"], "%v", out)
		assert.Equal(t, "escalate", out["rules"].(map[string]any)["verdict"])
		assertCaps(t, out, "per_purchase", "fail", 5000, 6000)
		assertCaps(t, out, "7d", "pass", 10000, 6000)
		// Every failing rule is its own line, in the rule's own words: the
		// limit, and a first payment to this merchant (one ask, every reason).
		diffs := differenceTexts(out)
		assert.Contains(t, diffs, "Your rules ask for approval: r05-spending-limits#per_purchase: the authorised maximum is over the per-purchase limit you set (limit 5000; value 6000)")
		assert.Contains(t, diffs, "Your rules ask for approval: r06-new-merchant: no earlier payment to this merchant from this profile")
		assert.Contains(t, out["card"], "over your $50 limit")

		// The person's yes to the card they were shown is sealed on the
		// deal's journal, and the payment then goes.
		payOn(t, h.dealID, out)
		types, _ := journalTypes(t, h.dealID)
		assert.Contains(t, types, "approval")
	})

	t.Run("over the weekly limit with an earlier payment", func(t *testing.T) {
		h := everydayBuyer(t)
		pinEverydayLimits(t, 7000, 10000)
		payOn(t, h.dealID, everydayPayCheck(t, h.dealID, 6000, v1LnHash))

		g := againWith(t, h)
		out := everydayPayCheck(t, g.dealID, 6000, strings.Repeat("cd", 32))
		require.Equal(t, "pause", out["verdict"], "%v", out)
		assert.EqualValues(t, 1, out["rules"].(map[string]any)["history"].(map[string]any)["acts"], "the earlier payment is in the history")
		assertCaps(t, out, "per_purchase", "pass", 7000, 6000)
		assertCaps(t, out, "7d", "fail", 10000, 12000)
		diffs := differenceTexts(out)
		assert.Contains(t, diffs, "r05-spending-limits#7d: the 7-day total with this purchase is over the 7-day limit you set (limit 10000; value 12000)")
		assert.NotContains(t, diffs, "r05-spending-limits#per_purchase")
		assert.NotContains(t, diffs, "r06-new-merchant", "this merchant was paid before")
		for _, id := range []string{"r02-ordinary-purchase", "r06-new-merchant"} {
			assert.Equal(t, "pass", rulesFinding(t, out, id)["verdict"], "%s: an earlier payment to this merchant", id)
		}

	})

	t.Run("a limits file that is gone", func(t *testing.T) {
		h := everydayBuyer(t)
		pinEverydayLimitsAt(t, filepath.Join(t.TempDir(), "missing.json"))
		out := everydayPayCheck(t, h.dealID, 6000, v1LnHash)
		require.Equal(t, "pause", out["verdict"], "%v", out)
		rules := out["rules"].(map[string]any)
		assert.Equal(t, "not_evaluated", rules["status"])
		assert.Contains(t, differenceTexts(out), "Your rules were not checked: the rules checker refused the record: capsule-engine: the limits file cannot be read")
	})
}

// A buyer checks the payment on her own offer before sending it, so the
// person answers a held order before the seller hears of the deal
// (evidencebook-skills#289). On no, she cancels the unsent offer: the seller's
// book holds nothing of it. On yes, the deal is sent and agreed, and the
// payment checked before the agreement settles (evidencebook#890 item 3).
func TestEverydayLimitsCheckedBeforeTheOfferIsSent(t *testing.T) {
	offered := func(t *testing.T) (seller, buyer v1Side, id, offer, offerFile string, check map[string]any) {
		t.Helper()
		seller = newV1Side(t)
		buyer = newV1Side(t)
		pinEverydayLimits(t, 5000, 10000)
		offerFile = filepath.Join(t.TempDir(), "offer.json")
		res := dealRun(t, "offer", "--input", writeJSON(t, everydayOffer(seller.party.signer)), "--out", offerFile)
		id, offer = res["deal_id"].(string), res["offer"].(string)
		dealRun(t, "note", "--deal", id, "--kind", "intent", "--input", writeJSON(t, everydayIntent), "--opening", writeJSON(t, v1Opening))
		check = everydayPayCheck(t, id, 6000, v1LnHash)
		require.Equal(t, "pause", check["verdict"], "%v", check)
		assertCaps(t, check, "per_purchase", "fail", 5000, 6000)
		assert.NotContains(t, differenceTexts(check), "r09-material-terms-changed", "the offered price changes no material term")
		return seller, buyer, id, offer, offerFile, check
	}

	t.Run("no: the offer is cancelled unsent", func(t *testing.T) {
		seller, _, id, offer, _, check := offered(t)
		_, err := invoke(t, "", "--profile", "deal", "deal", "note", "--deal", id, "--kind", "approval", "--check", check["check_id"].(string),
			"--choice", "hold", "--said", "Don't buy", "--shown-card", shownCard(t, check["card"].(string)))
		require.NoError(t, err)
		dealRun(t, "offer", "cancel", offer, "--out", filepath.Join(t.TempDir(), "cancel.json"))
		assert.Equal(t, "cancelled", v1DealState(t, id))
		types, _ := journalTypes(t, id)
		assert.Equal(t, "approval", types[len(types)-1], "the answer is on the buyer's book")
		assert.NotEmpty(t, v1Err(t, "settle", "--deal", id, "--check", check["check_id"].(string), "--sent", "--payment-ref-type", "ln.payment_hash"), "a held order is not paid")

		seller.act(t)
		listed, err := invoke(t, "", "--profile", "deal", "deal", "list", "--json")
		require.NoError(t, err, listed)
		assert.NotContains(t, listed, id, "the seller's book holds nothing of an offer never sent")
	})

	t.Run("yes: sent, agreed and paid", func(t *testing.T) {
		seller, buyer, id, offer, offerFile, check := offered(t)
		_, err := answer(t, id, check["check_id"].(string), "Buy anyway", shownCard(t, check["card"].(string)))
		require.NoError(t, err)
		seller.act(t)
		dealRun(t, "receive", offerFile)
		sellerAgree := filepath.Join(t.TempDir(), "agreement.json")
		dealRun(t, "agree", offer, "--admit", sealAdmit(t, "GO"), "--out", sellerAgree)
		buyer.act(t)
		dealRun(t, "receive", sellerAgree)
		dealRun(t, "agree", offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "buyer-agreement.json"))
		dealRun(t, "settle", "--deal", id, "--check", check["check_id"].(string), "--sent", "--payment-ref-type", "ln.payment_hash")
		withSession(t, func(s *dealSession) {
			history, _, err := s.rulesHistory(t.Context())
			require.NoError(t, err)
			raw, err := json.Marshal(history)
			require.NoError(t, err)
			assert.Contains(t, string(raw), `"amount_minor":6000`, "the payment counts toward the weekly limit")
		})
	})
}

// An earlier x-deal-v1 act in the rules history is built under its own
// deal's key: the counterparty of the check it rests on carries the per-deal
// fingerprints that deal sealed, whichever deal is checked now,
// and the payee keyed per profile beside them. The earlier act here rests on
// a passing check's verdict alone (no approval).
func TestAV1HistoryActKeepsItsOwnDealsFingerprints(t *testing.T) {
	h := agreedBoth(t, everydayOffer)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, everydayIntent), "--opening", writeJSON(t, v1Opening))
	checker := stubChecker(t, "rules-pass", checkerPrints("allow", passFinding))
	pinChecker(t, map[string]any{"command": []string{checker}})
	input := func() map[string]any {
		var in map[string]any
		require.NoError(t, json.Unmarshal(mustRead(t, checker+".input"), &in))
		return in
	}
	header := func(record map[string]any) any {
		return record["agent_input"].(map[string]any)["x-deal-v0"].(map[string]any)["counterparty"]
	}
	first := everydayPayCheck(t, h.dealID, 6000, v1LnHash)
	require.Equal(t, "pass", first["verdict"], "%v", first)
	sealedA := input()["record"].(map[string]any)
	dealRun(t, "settle", "--deal", h.dealID, "--check", first["check_id"].(string), "--sent", "--payment-ref-type", "ln.payment_hash")

	g := againWith(t, h)
	everydayPayCheck(t, g.dealID, 6000, strings.Repeat("cd", 32))
	in := input()
	history := in["history"].([]any)
	require.Len(t, history, 1)
	act := history[0].(map[string]any)
	assert.Equal(t, header(sealedA), act["counterparty"], "the check's counterparty, as deal A sealed it")
	assert.NotEqual(t, header(in["record"].(map[string]any)), act["counterparty"], "deal B's per-deal fingerprints differ")
	assert.Equal(t, sealedA["counterparty_profile"], act["counterparty_profile"], "the payee keyed per profile")
	assert.NotNil(t, act["counterparty_profile"])
}

// An earlier x-deal-v1 act whose link to its check is broken (its
// authorization names a record the deal does not hold) is never read as a
// payment to a new merchant without saying so: the history is not complete,
// and the rule that keys on earlier payments does not pass.
func TestAV1HistoryActWithABrokenCheckLinkIsIncomplete(t *testing.T) {
	h := everydayBuyer(t)
	pinEverydayLimits(t, 7000, 10000)
	payOn(t, h.dealID, everydayPayCheck(t, h.dealID, 6000, v1LnHash))
	withSession(t, func(s *dealSession) {
		var n int
		var local string
		require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT n, local FROM deal_v1_events WHERE deal_id=? AND kind='act'`, h.dealID).Scan(&n, &local))
		var ev map[string]any
		require.NoError(t, json.Unmarshal([]byte(local), &ev))
		act := ev["act"].(map[string]any)
		require.NotEmpty(t, act["authorized_by"])
		act["authorized_by"] = strings.Repeat("ee", 32) // no record of the deal
		raw, err := json.Marshal(ev)
		require.NoError(t, err)
		_, err = s.db.ExecContext(t.Context(), `UPDATE deal_v1_events SET local=? WHERE deal_id=? AND n=?`, string(raw), h.dealID, n)
		require.NoError(t, err)
		history, scope, err := s.rulesHistory(t.Context())
		require.NoError(t, err)
		require.Len(t, history, 1)
		assert.False(t, scope.Complete, "a broken link is said, not read as a new merchant")
		assert.Nil(t, history[0].(map[string]interface{})["counterparty_profile"])
	})

	g := againWith(t, h)
	out := everydayPayCheck(t, g.dealID, 6000, strings.Repeat("cd", 32))
	assert.Equal(t, false, out["rules"].(map[string]any)["history"].(map[string]any)["complete"])
	assert.NotEqual(t, "pass", rulesFinding(t, out, "r02-ordinary-purchase")["verdict"])
}
