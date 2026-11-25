// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engineOutput writes what `capsule-engine admission evaluate --admit FILE`
// prints (capsule-engine go/cmd/capsule-engine/main.go, output and encode):
// the decision's verdict, tier, rows, asks, pack and block, and with --admit
// the admit body and admits_deal. edit changes the object before it is
// written; the admit body is under "admit".
func engineOutput(t *testing.T, verdict string, admitsDeal bool, edit func(out map[string]any)) string {
	t.Helper()
	nonce, err := xdealv1.NewNonce()
	require.NoError(t, err)
	pack := strings.Repeat("a", 64)
	out := map[string]any{
		"verdict": verdict, "source": "pack", "tier": "standard", "band": nil,
		"rows": []any{map[string]any{"row": "paid_as_agreed", "result": "met", "effect": "go"}}, "asks": []any{}, "pack": map[string]any{"id": "baseline", "digest": pack},
		"block": "{}", "block_sha256": strings.Repeat("c", 64),
		"admit": map[string]any{
			"outcome": verdict, "tier": "standard", "rows": []string{"paid_as_agreed"}, "asks": []any{},
			"pack_digest": pack, "card_digest": strings.Repeat("b", 64), "answers": []any{}, "nonce": nonce,
		},
		"admits_deal": admitsDeal,
	}
	if edit != nil {
		edit(out)
	}
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	return writeJSON(t, string(raw))
}

// receivedOffer runs the buyer's offer and the seller's receipt of it, and
// leaves the seller acting: the point at which the seller admits the deal.
func receivedOffer(t *testing.T) *handshakeV1 {
	t.Helper()
	h := &handshakeV1{}
	h.seller = newV1Side(t)
	h.buyer = newV1Side(t)
	h.offerFile = filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offerTo(h.seller.party.signer)), "--out", h.offerFile)
	h.dealID, h.offer = res["deal_id"].(string), res["offer"].(string)
	h.seller.act(t)
	got := dealRun(t, "receive", h.offerFile)
	require.Equal(t, "kept, and counted", got["record"], "%v", got)
	return h
}

func TestDealAdmitSealsTheEnginesDecisionAndAgreeCitesIt(t *testing.T) {
	h := receivedOffer(t)
	input := engineOutput(t, "GO", true, nil)
	res := dealRun(t, "admit", "--deal", h.dealID, "--input", input)
	admit := res["admit"].(string)
	require.Len(t, admit, 64)
	assert.Equal(t, h.dealID, res["deal_id"])

	// The journal holds it as an x-deal-v1 admit with the engine's body.
	withSession(t, func(s *dealSession) {
		rec, held, err := s.journalRecord(t.Context(), admit)
		require.NoError(t, err)
		require.True(t, held)
		require.NotNil(t, rec)
		assert.Equal(t, xdealv1.TypeAdmit, rec.Block.RecordType)
		assert.Empty(t, rec.Block.DealID, "an admit carries no deal id (x-deal-v1 PROFILE §3)")
	})

	// Running it again seals nothing and prints the same digest.
	again := dealRun(t, "admit", "--deal", h.dealID, "--input", input)
	assert.Equal(t, admit, again["admit"])

	// With no --admit, the agreement cites the deal's own admit.
	out := filepath.Join(t.TempDir(), "agreement.json")
	dealRun(t, "agree", h.offer, "--out", out)
	read, err := xdealv1.VerifyBook(readPush(t, out).Record)
	require.NoError(t, err)
	ag, ok := agreeOf(read.Record)
	require.True(t, ok)
	require.NotNil(t, ag.Admit)
	assert.Equal(t, admit, ag.Admit.Digest)
}

func TestDealAdmitFollowsTheEnginesAdmitsDeal(t *testing.T) {
	h := receivedOffer(t)
	// The engine's admits_deal decides, never the outcome word: a LIMIT the
	// engine says holds the deal is sealed, and a GO it says does not is
	// refused (a body and flag that disagree are not the engine's, but the
	// plugin still follows the flag).
	for _, c := range []struct {
		verdict string
		admits  bool
	}{{"ASK", false}, {"LIMIT", false}, {"GO", false}} {
		msg := v1Err(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, c.verdict, c.admits, nil))
		assert.Contains(t, msg, "does not admit the deal", c.verdict)
		assert.Contains(t, msg, "capsule-engine admission evaluate --admit FILE", "the refusal says where to go next")
	}
	res := dealRun(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "LIMIT", true, nil))
	assert.Len(t, res["admit"], 64)
}

func TestDealAdmitRefusesWhatIsNotTheEnginesAdmit(t *testing.T) {
	h := receivedOffer(t)
	for name, c := range map[string]struct {
		edit func(map[string]any)
		want string
	}{
		"no tier (a NO)":        {func(o map[string]any) { o["admit"] = nil }, "has no tier"},
		"run without --admit":   {func(o map[string]any) { delete(o, "admit"); delete(o, "admits_deal") }, "has no admit body"},
		"a member added":        {func(o map[string]any) { o["admit"].(map[string]any)["deal_id"] = "deal-0123456789abcdef" }, "not an x-deal-v1 admit body"},
		"a member missing":      {func(o map[string]any) { delete(o["admit"].(map[string]any), "nonce") }, "x-deal-v1"},
		"another verdict":       {func(o map[string]any) { o["verdict"] = "LIMIT" }, "does not match the decision"},
		"another pack":          {func(o map[string]any) { o["pack"] = map[string]any{"id": "x", "digest": strings.Repeat("d", 64)} }, "does not match the decision"},
		"a malformed card hash": {func(o map[string]any) { o["admit"].(map[string]any)["card_digest"] = "nope" }, "x-deal-v1"},
	} {
		msg := v1Err(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "GO", true, c.edit))
		assert.Contains(t, msg, c.want, name)
	}
	assert.Contains(t, v1Err(t, "admit", "--deal", h.dealID, "--input", writeJSON(t, `[1]`)), "not the JSON output of", "not an object")
	assert.Contains(t, v1Err(t, "admit", "--deal", h.dealID), "--input is required")
	assert.Contains(t, v1Err(t, "admit", "--deal", "deal-0000000000000000", "--input", engineOutput(t, "GO", true, nil)), "not an x-deal-v1 deal this profile holds")
	assert.Contains(t, v1Err(t, "admit", "--deal", "nope", "--input", engineOutput(t, "GO", true, nil)), "--deal must be a deal id")
}

func TestADealHasOneAdmissionDecision(t *testing.T) {
	h := receivedOffer(t)
	first := dealRun(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "GO", true, nil))["admit"].(string)
	// Another decision (a fresh nonce, so another body) for the same deal.
	msg := v1Err(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "GO", true, nil))
	assert.Contains(t, msg, "already has an admission decision, "+first)
	assert.Contains(t, msg, "start a new deal")

	// --admit may name the deal's own admit, but no other.
	other := sealAdmit(t, "GO")
	assert.Contains(t, v1Err(t, "agree", h.offer, "--admit", other), "is not this deal's admission decision")
	dealRun(t, "agree", h.offer, "--admit", first, "--out", filepath.Join(t.TempDir(), "a.json"))
}

func TestAnAdmitSealedForOneDealIsNotCitedOnAnother(t *testing.T) {
	h := receivedOffer(t)
	// The seller makes an offer of its own: a second v1 deal on this profile.
	own := dealRun(t, "offer", "--input", writeJSON(t, offerTo(h.buyer.party.signer)), "--out", filepath.Join(t.TempDir(), "own.json"))
	otherDeal := own["deal_id"].(string)
	admit := dealRun(t, "admit", "--deal", otherDeal, "--input", engineOutput(t, "GO", true, nil))["admit"].(string)
	msg := v1Err(t, "agree", h.offer, "--admit", admit)
	assert.Contains(t, msg, "sealed for deal "+otherDeal)
}

func TestAgreeWithNoAdmissionDecisionNamesDealAdmit(t *testing.T) {
	h := receivedOffer(t)
	msg := v1Err(t, "agree", h.offer)
	assert.Contains(t, msg, "has no admission decision")
	assert.Contains(t, msg, "deal admit --deal "+h.dealID)
}

func TestDealAdmitNeedsTheNewLayout(t *testing.T) {
	dealFixture(t)
	assert.Contains(t, v1Err(t, "admit", "--deal", "deal-0123456789abcdef", "--input", engineOutput(t, "GO", true, nil)), "older layout")
}

// TestDealAdmitReadsTheRealEnginesOutput runs `deal admit` on capsule-engine's
// own output (testdata/capsule-engine-admit, README there), so the input
// format is held to the producer, not to this package's picture of it.
func TestDealAdmitReadsTheRealEnginesOutput(t *testing.T) {
	h := receivedOffer(t)
	dir := filepath.Join("testdata", "capsule-engine-admit")
	assert.Contains(t, v1Err(t, "admit", "--deal", h.dealID, "--input", filepath.Join(dir, "ask.json")), "does not admit the deal")
	assert.Contains(t, v1Err(t, "admit", "--deal", h.dealID, "--input", filepath.Join(dir, "no.json")), "has no tier")
	res := dealRun(t, "admit", "--deal", h.dealID, "--input", filepath.Join(dir, "go.json"))
	assert.Len(t, res["admit"], 64)
	dealRun(t, "agree", h.offer, "--out", filepath.Join(t.TempDir(), "a.json"))
}

// TestDealAdmitFinishesARunThatStopped stores the binding as a run that
// stopped before sealing would have left it, and checks that the next run
// seals that stored record (same digest) instead of refusing or sealing a
// second one.
func TestDealAdmitFinishesARunThatStopped(t *testing.T) {
	h := receivedOffer(t)
	input := engineOutput(t, "GO", true, nil)
	payload, err := admitPayloadOf(mustRead(t, input))
	require.NoError(t, err)
	_, digest, err := xdealv1.Encode(payload)
	require.NoError(t, err)
	withSession(t, func(s *dealSession) {
		require.NoError(t, s.ensureV1Tables(t.Context()))
		_, err := s.db.ExecContext(t.Context(), `INSERT INTO deal_v1_admits (deal_id, admit_digest, payload) VALUES (?, ?, ?)`, h.dealID, digest, payload)
		require.NoError(t, err)
		held, err := s.journalHolds(t.Context(), digest)
		require.NoError(t, err)
		require.False(t, held)
	})
	res := dealRun(t, "admit", "--deal", h.dealID, "--input", input)
	assert.Equal(t, digest, res["admit"])
	withSession(t, func(s *dealSession) {
		held, err := s.journalHolds(t.Context(), digest)
		require.NoError(t, err)
		assert.True(t, held)
	})
	dealRun(t, "agree", h.offer, "--out", filepath.Join(t.TempDir(), "a.json"))
}

func TestOneEngineOutputIsSealedForOneDealOnly(t *testing.T) {
	h := receivedOffer(t)
	own := dealRun(t, "offer", "--input", writeJSON(t, offerTo(h.buyer.party.signer)), "--out", filepath.Join(t.TempDir(), "own.json"))
	input := engineOutput(t, "GO", true, nil)
	first := dealRun(t, "admit", "--deal", h.dealID, "--input", input)["admit"].(string)
	// The same output for another deal would put a second record with the
	// same nonce in the journal (PROFILE §5), which CheckAuthorizations refuses.
	msg := v1Err(t, "admit", "--deal", own["deal_id"].(string), "--input", input)
	assert.Contains(t, msg, "already sealed, as admit "+first)
}

func TestDealAdmitRefusesADealAlreadyAgreed(t *testing.T) {
	h := receivedOffer(t)
	// Agreed through --admit with an admit sealed some other way: a later
	// `deal admit` must not bind a decision the agreement does not cite.
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "a.json"))
	assert.Contains(t, v1Err(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "GO", true, nil)), "is already agreed")
}

func TestDealAdmitRefusesATierOrRowsTheDecisionDidNotHave(t *testing.T) {
	h := receivedOffer(t)
	for name, edit := range map[string]func(map[string]any){
		"another tier":     func(o map[string]any) { o["admit"].(map[string]any)["tier"] = "premium" },
		"another row":      func(o map[string]any) { o["admit"].(map[string]any)["rows"] = []string{"other_row"} },
		"no outer tier":    func(o map[string]any) { o["tier"] = nil },
		"other outer rows": func(o map[string]any) { o["rows"] = []any{map[string]any{"row": "x"}} },
	} {
		assert.Contains(t, v1Err(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "GO", true, edit)), "does not match the decision", name)
	}
}

// TestANewDecisionReplacesOneNeverSealed is a run that stopped after storing
// its binding, followed by a fresh capsule-engine run (a new nonce): nothing
// left the holder, so the new decision replaces the stored one.
func TestANewDecisionReplacesOneNeverSealed(t *testing.T) {
	h := receivedOffer(t)
	payload, err := admitPayloadOf(mustRead(t, engineOutput(t, "GO", true, nil)))
	require.NoError(t, err)
	_, stale, err := xdealv1.Encode(payload)
	require.NoError(t, err)
	withSession(t, func(s *dealSession) {
		require.NoError(t, s.ensureV1Tables(t.Context()))
		_, err := s.db.ExecContext(t.Context(), `INSERT INTO deal_v1_admits (deal_id, admit_digest, payload) VALUES (?, ?, ?)`, h.dealID, stale, payload)
		require.NoError(t, err)
	})
	res := dealRun(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "GO", true, nil))
	assert.NotEqual(t, stale, res["admit"])
	dealRun(t, "agree", h.offer, "--out", filepath.Join(t.TempDir(), "a.json"))
}

// TestDealAdmitFindsAnAgreementAStoppedRunAppended forgets what `deal agree`
// recorded after appending, as a stopped run leaves it: `deal admit` must
// still see the agreement and refuse, since it cites another admit.
func TestDealAdmitFindsAnAgreementAStoppedRunAppended(t *testing.T) {
	h := newHandshakeV1(t)
	_, err := openWritableDB(t).Exec(`UPDATE deal_v1 SET own_agreement_id='', own_agreement_digest='' WHERE deal_id=?`, h.dealID)
	require.NoError(t, err)
	assert.Contains(t, v1Err(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "GO", true, nil)), "is already agreed")
}

// TestDealAdmitRefusesWhenTheJournalCannotBeRead loses an earlier admit's
// original: `deal admit` must refuse and bind nothing, rather than miss that
// admit's nonce and risk sealing it twice. Opening the journal checks its
// integrity and fails first today; admitWithNonce also returns any read
// error but an unavailable (withheld or deleted) payload, in case a read
// fails after the open.
func TestDealAdmitRefusesWhenTheJournalCannotBeRead(t *testing.T) {
	h := receivedOffer(t)
	other := sealAdmit(t, "GO")
	res, err := openWritableDB(t).Exec(`DELETE FROM capsule_store_artifacts WHERE content_sha256=?`, other)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the earlier admit's original is gone")
	err = dealRunErr(t, "admit", "--deal", h.dealID, "--input", engineOutput(t, "GO", true, nil))
	require.Error(t, err)
	withSession(t, func(s *dealSession) {
		own, err := s.dealAdmit(t.Context(), h.dealID)
		require.NoError(t, err)
		assert.Empty(t, own, "nothing was bound")
	})
}
