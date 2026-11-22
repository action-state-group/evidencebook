// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A check on a v1 deal runs the v0 check over the local list and seals the
// journal check (the v0 snapshot's record) and its verdict.
func TestAV1CheckSealsTheJournalCheckAndVerdict(t *testing.T) {
	h := openedV1(t)
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"commit","terms":{"item":"lamp","price_minor":6000,"currency":"USD"}}`))
	require.NotEmpty(t, out["verdict"], "%v", out)
	types, withDeal := journalTypes(t, h.dealID)
	require.GreaterOrEqual(t, len(types), 3, "%v", types)
	assert.Equal(t, []string{"intent", "check", "verdict"}, types[:3])
	assert.Equal(t, []bool{false, true, true}, withDeal[:3])
}

// buyerOpenedV1 is a v1 deal opened by its buyer, the offerer, whose intent
// caps the total at 5000.
func buyerOpenedV1(t *testing.T) *handshakeV1 {
	t.Helper()
	h := receivedOffer(t)
	h.buyer.act(t)
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening))
	return h
}

const (
	v1BuyerIntent = `{"verbatim":"buy the lamp, at most 50 dollars","max_total_minor":5000}`
	v0BuyerOpen   = `{"type":"purchase","intent":{"verbatim":"buy the lamp, at most 50 dollars","max_total_minor":5000,"party_role":"buyer"},"who":{"name":"Kettle Shop","domain":"kettle.example"},"terms":{"item":"lamp","currency":"USD"},"recourse":{"rail":"card","refundable":true}}`
	payInput      = `{"action":"pay","amount_minor":4500,"authorized_max_minor":4500,"terms":{"item":"lamp","price_minor":4500,"currency":"USD"}%s}`
)

// A pay check on a v1 deal records payment_ref, the reference fixed before
// paying, in its journal check; without it the check is refused. A v0 deal
// takes no payment_ref.
func TestAV1PayCheckRecordsItsPaymentRef(t *testing.T) {
	h := buyerOpenedV1(t)
	assert.Contains(t, v1Err(t, "check", "--deal", h.dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, ""))), "records payment_ref")
	ref := strings.Repeat("ab", 32)
	dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+ref+`"`)))
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		var found bool
		for _, r := range journal {
			if b, ok := r.Body.(*xdealv1.CheckBody); ok {
				assert.Equal(t, ref, b.PaymentRef)
				found = true
			}
		}
		assert.True(t, found, "the journal holds the check")
	})

	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, v0BuyerOpen))["deal_id"].(string)
	assert.Contains(t, v1Err(t, "check", "--deal", id, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+ref+`"`))), "payment_ref goes on an x-deal-v1 deal")
}

// The same input on a v0 deal and on an equivalent v1 deal gives the same
// verdict and card, and hands the pinned rules checker the same record body.
func TestOneCheckInputOnAV0AndAV1DealGivesTheSameCard(t *testing.T) {
	ref := strings.Repeat("ab", 32)
	run := func(t *testing.T, dealID, extra string) (map[string]any, map[string]any) {
		checker := stubChecker(t, "rules-pass", checkerPrints("pass", passFinding))
		pinChecker(t, map[string]any{"command": []string{checker}})
		out := dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, extra)))
		var input map[string]any
		require.NoError(t, json.Unmarshal(mustRead(t, checker+".input"), &input))
		return out, input
	}
	h := buyerOpenedV1(t)
	v1out, v1in := run(t, h.dealID, `,"payment_ref":"`+ref+`"`)

	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, v0BuyerOpen))["deal_id"].(string)
	v0out, v0in := run(t, id, "")
	assert.Equal(t, v0out["verdict"], v1out["verdict"])
	assert.Equal(t, v0out["card"], v1out["card"])
	body := func(in map[string]any) any {
		rec, _ := in["record"].(map[string]any)
		ai, _ := rec["agent_input"].(map[string]any)
		return ai["body"]
	}
	require.NotNil(t, body(v0in), "the v0 checker input: %v", v0in)
	assert.Equal(t, body(v0in), body(v1in), "the rules read the same body")
}

// payAct is a payer leg of the deal that cites check.
func payAct(dealID, check string) xdealv1.Record {
	ref := xdealv1.RefTo(check)
	return xdealv1.Record{
		Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypeSettle, At: "2099-01-01T00:00:00Z", Seq: 2},
		Body:  &xdealv1.SettleBody{Leg: xdealv1.LegPayer, Statements: []xdealv1.StatementItem{}, Check: &ref},
	}
}

// checkDigest is the digest of the deal's newest journal check.
func checkDigest(t *testing.T, journal []xdealv1.Record) string {
	t.Helper()
	var d string
	for _, r := range journal {
		if r.Block.RecordType == xdealv1.TypeCheck {
			var err error
			_, d, err = xdealv1.Encode(r)
			require.NoError(t, err)
		}
	}
	require.NotEmpty(t, d)
	return d
}

// The library's authorization accepts a payer leg on a passing v1 check with
// its automatic standing_intent approval, over exactly the deal's journal
// records (v1DealJournal), even with another deal's intent in the same
// journal; it refuses a reused approval and a check of another deal.
func TestCheckAuthorizationsAcceptsAV1DealsJournal(t *testing.T) {
	h := buyerOpenedV1(t)
	ref := strings.Repeat("ab", 32)
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+ref+`"`)))
	require.Equal(t, "pass", out["verdict"], "%v", out)
	// Another deal of the same buyer, opened with its own intent.
	other := dealRun(t, "offer", "--input", writeJSON(t, offerTo(h.seller.party.signer)), "--out", filepath.Join(t.TempDir(), "o2.json"))["deal_id"].(string)
	dealRun(t, "note", "--deal", other, "--kind", "intent", "--input", writeJSON(t, `{"verbatim":"another errand","max_total_minor":900}`), "--opening", writeJSON(t, v1Opening))

	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		check := checkDigest(t, journal)
		auth, err := xdealv1.CheckAuthorizations(journal, []xdealv1.Record{payAct(h.dealID, check)})
		require.NoError(t, err, "the deal's own journal authorizes the act")
		require.Len(t, auth, 1)
		_, err = xdealv1.CheckAuthorizations(journal, []xdealv1.Record{payAct(h.dealID, check), payAct(h.dealID, check)})
		assert.Error(t, err, "one approval authorizes one act")
		_, err = xdealv1.CheckAuthorizations(journal, []xdealv1.Record{payAct(other, check)})
		assert.Error(t, err, "the check serves another deal")
	})
}

// On a v1 deal the user's answer to a paused check carries the card it was
// given on.
func TestAV1ApprovalNeedsItsShownCard(t *testing.T) {
	h := buyerOpenedV1(t)
	ref := strings.Repeat("ab", 32)
	over := `{"action":"pay","amount_minor":6000,"authorized_max_minor":6000,"terms":{"item":"lamp","price_minor":6000,"currency":"USD"},"payment_ref":"` + ref + `"}`
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, over))
	require.Equal(t, "pause", out["verdict"], "%v", out)
	check := out["check_id"].(string)
	_, err := answer(t, h.dealID, check, "yes", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pass that card with --shown-card")
	_, err = answer(t, h.dealID, check, "yes", shownCard(t, out["card"].(string)))
	require.NoError(t, err)
	types, _ := journalTypes(t, h.dealID)
	assert.Equal(t, "approval", types[len(types)-1])
	// The library's authorization reads the approval's card commitment and
	// accepts a payer leg on it.
	withSession(t, func(s *dealSession) {
		journal, err := s.v1DealJournal(t.Context(), h.dealID)
		require.NoError(t, err)
		_, err = xdealv1.CheckAuthorizations(journal, []xdealv1.Record{payAct(h.dealID, checkDigest(t, journal))})
		require.NoError(t, err)
	})
}

// An agent_card answer (no --said) also needs its card on a v1 deal.
func TestAV1AgentCardApprovalNeedsItsShownCard(t *testing.T) {
	h := buyerOpenedV1(t)
	over := `{"action":"pay","amount_minor":6000,"authorized_max_minor":6000,"terms":{"item":"lamp","price_minor":6000,"currency":"USD"},"payment_ref":"` + strings.Repeat("ab", 32) + `"}`
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, over))
	require.Equal(t, "pause", out["verdict"], "%v", out)
	_, err := answer(t, h.dealID, out["check_id"].(string), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--shown-card")
}

// A confirm_limits approval answers an intent, carries no card and seals on a
// v1 deal; the confirmed limits are then in force.
func TestAV1ConfirmLimitsSeals(t *testing.T) {
	h := buyerOpenedV1(t)
	res := note(t, h.dealID, "intent", `{"verbatim":"up to 70 dollars is fine","max_total_minor":7000}`)
	require.NotNil(t, res["proposed"], "%v", res)
	confirmed := dealRun(t, "note", "--deal", h.dealID, "--kind", "approval", "--check", res["capsule_id"].(string), "--choice", "confirm_limits", "--said", "yes, up to 70")
	require.NotEmpty(t, confirmed["capsule_id"])
	types, withDeal := journalTypes(t, h.dealID)
	assert.Equal(t, "approval", types[len(types)-1])
	assert.False(t, withDeal[len(withDeal)-1], "a confirm_limits approval spans deals")
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"pay","amount_minor":6500,"authorized_max_minor":6500,"terms":{"item":"lamp","price_minor":6500,"currency":"USD"},"payment_ref":"`+strings.Repeat("cd", 32)+`"}`))
	assert.Equal(t, "pass", out["verdict"], "the confirmed limit is in force: %v", out)
}

// A v1 check refuses an offer check and a payment_ref on a check that is not
// a pay check.
func TestAV1CheckRefusesAnOfferAndAStrayPaymentRef(t *testing.T) {
	h := buyerOpenedV1(t)
	assert.Contains(t, v1Err(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"offer","terms":{"item":"lamp","price_minor":4500,"currency":"USD"}}`)), "sealed with `deal offer`")
	assert.Contains(t, v1Err(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"share_contact","disclosing_to":"counterparty","disclosing":["email"],"payment_ref":"`+strings.Repeat("ab", 32)+`"}`)), "payment_ref goes on a pay check only")
	types, _ := journalTypes(t, h.dealID)
	assert.Equal(t, []string{"intent"}, types, "nothing was sealed")
}

// A seller's check after its agent's classified claim hands the pinned
// checker the claim, rebuilt from the local step as the check is.
func TestAV1SellerCheckReadsItsAgentsClaims(t *testing.T) {
	h := openedV1(t)
	note(t, h.dealID, "claim", `{"text":"the lamp is new","source_kind":"agent","class":"condition"}`)
	checker := stubChecker(t, "rules-pass", checkerPrints("pass", passFinding))
	pinChecker(t, map[string]any{"command": []string{checker}})
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"commit","terms":{"item":"lamp","price_minor":6000,"currency":"USD"}}`))
	require.NotEmpty(t, out["verdict"])
	var input map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, checker+".input"), &input))
	record, _ := input["record"].(map[string]any)
	claims, _ := record["deal_claims"].([]any)
	assert.Len(t, claims, 1, "the agent's claim reaches the checker")
}

// A disclosure on a v1 deal counts in the cross-deal memory as a v0 one does:
// a later deal with the same recipient is told only what is new.
func TestAV1DisclosureCountsInTheCrossDealMemory(t *testing.T) {
	h := buyerOpenedV1(t)
	share := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"share_contact","disclosing_to":"counterparty","disclosing":["email"]}`))
	if share["verdict"] == "pause" {
		_, err := answer(t, h.dealID, share["check_id"].(string), "yes", shownCard(t, share["card"].(string)))
		require.NoError(t, err)
	}
	dealRun(t, "note", "--deal", h.dealID, "--kind", "disclosure", "--input", writeJSON(t, `{"channel":"web","fields":[{"class":"email","value":"buyer@example.com"}]}`))
	second := dealRun(t, "open", "--input", writeJSON(t, v0BuyerOpen))["deal_id"].(string)
	again := dealRun(t, "check", "--deal", second, "--input", writeJSON(t, `{"action":"share_contact","disclosing_to":"counterparty","disclosing":["email","address"]}`))
	require.Equal(t, "pause", again["verdict"], "%v", again)
	assert.Contains(t, again["card"], "first time they get your address", "the email was told in the v1 deal")
}

// A journal record of the deal sealed outside these verbs (here a message
// appended with no local event) would reach the authorization but not the
// card: the check refuses.
func TestAV1CheckRefusesAJournalRecordWithNoLocalEvent(t *testing.T) {
	h := buyerOpenedV1(t)
	withSession(t, func(s *dealSession) {
		s.dkey = dealprofile.DealKey(s.secret, h.dealID)
		events, err := s.v1Events(t.Context(), h.dealID)
		require.NoError(t, err)
		p, err := s.prepareV1(h.dealID, events, dealEvent{Kind: "message", Message: &dealMessage{From: "counterparty", Channel: "web", Text: "outside"}}, false)
		require.NoError(t, err)
		require.NoError(t, s.appendV1Journal(t.Context(), p.payload))
	})
	assert.Contains(t, v1Err(t, "check", "--deal", h.dealID, "--input", writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+strings.Repeat("ab", 32)+`"`))), "sealed outside these verbs")
}

// A check's companion (the payee's fingerprint) stays a local step on a v1
// deal: an x-deal-v1 counterparty_profile record has no member to carry it.
func TestAV1CheckCompanionStaysLocal(t *testing.T) {
	h := buyerOpenedV1(t)
	over := `{"action":"pay","amount_minor":6000,"authorized_max_minor":6000,"terms":{"item":"lamp","price_minor":6000,"currency":"USD"},"payment_ref":"` + strings.Repeat("ab", 32) + `"}`
	dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, over))
	withSession(t, func(s *dealSession) {
		events, err := s.v1Events(t.Context(), h.dealID)
		require.NoError(t, err)
		var companions int
		for _, se := range events {
			if se.Event.Kind != "counterparty_profile" {
				continue
			}
			companions++
			held, err := s.journalHolds(t.Context(), se.Digest)
			require.NoError(t, err)
			assert.False(t, held, "the companion is not in the journal")
			require.NotNil(t, se.Event.CounterpartyProfile)
			assert.NotEmpty(t, se.Event.CounterpartyProfile.Payee, "the local step keeps the fingerprint the checker reads")
		}
		assert.Equal(t, 1, companions)
	})
}
