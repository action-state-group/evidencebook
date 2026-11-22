package deal

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seller's verdict on its agent's claim is its own step, sealed right after
// the claim (evidencebook#607, evidencebook-deal#244).

const offerCheckInput = `{"action":"offer","amount_minor":190000,"terms":{"item":"example bicycle","quantity":1,"price_minor":190000}}`

// visible is what a shared copy's report says of a deal, minus its step list's
// withheld steps: the same words, step by step, wherever the seller's agent
// was or was not authorized.
func visible(t *testing.T, b map[string]any) map[string]any {
	t.Helper()
	ext := dealReportOf(b)
	var steps [][2]any
	for _, s := range sharedSteps(t, b) {
		if s["withheld"] != true {
			steps = append(steps, [2]any{s["kind"], s["line"]})
		}
	}
	texts := func(key string) []any {
		var out []any
		list, _ := ext[key].([]any)
		for _, x := range list {
			out = append(out, x.(map[string]any)["text"])
		}
		return out
	}
	var reps []any
	list, _ := ext["representations"].([]any)
	for _, x := range list {
		r := x.(map[string]any)
		reps = append(reps, [2]any{r["class"], r["text"]})
	}
	return map[string]any{
		"steps": steps, "anomalies": texts("anomalies"), "did": texts("did"), "told": texts("told"),
		"representations": reps, "withheld": ext["withheld"],
	}
}

// Two sales that differ only in whether the agent's ordered claim was
// authorized, then the same offer check: a counterparty's copy of one reads
// the same as the other's, says "not shared" and never "none", carries no
// verdict, no authority and no finding, and still verifies.
func TestTheCounterpartyCopyDoesNotSayWhetherAClaimWasAuthorized(t *testing.T) {
	copies := map[string]map[string]any{}
	raws := map[string]string{}
	for name, authorized := range map[string]bool{"authorized": true, "unchecked": false} {
		dealFixture(t)
		id := sellerRepOpen(t, `["condition"]`)
		check := ""
		if authorized {
			ck := represent(t, id, repFile("represent-condition.json"))
			require.Equal(t, "pass", ck["verdict"])
			check = ck["check_id"].(string)
		}
		claimWith(t, id, check, repFile("claim-condition.json"))
		dealRun(t, "check", "--deal", id, "--input", writeJSON(t, offerCheckInput))

		// The finding the gate raises is on the user's own log of the deal
		// whose claim nothing authorized, so its absence below is not vacuous.
		own, _ := exportRecords(t, id)
		ownJSON, _ := json.Marshal(own)
		assert.Equal(t, !authorized, strings.Contains(string(ownJSON), "required_representation_missing"), name)

		b, raw := sharedCopy(t, id, cli.DealAudienceCounterparty, "the buyer")
		copies[name], raws[name] = b, raw
		for _, leak := range []string{"required_representation_missing", `"unchecked"`, "authority_basis", "no_check", "evaluation_ref"} {
			assert.NotContains(t, raw, leak, "%s: %s", name, leak)
		}
		var markers []string
		for _, a := range dealReportOf(b)["anomalies"].([]any) {
			if item := a.(map[string]any); item["kind"] == "unapproved_representation_not_shared" {
				markers = append(markers, item["text"].(string))
			}
		}
		assert.Equal(t, []string{authorityNotShared}, markers, name)
		assert.Contains(t, dealReportOf(b)["withheld"], "how your agent was authorized for what it said and did (the checks, approvals and verdicts)")

		path := filepath.Join(t.TempDir(), name+".json")
		out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "counterparty", "--to", "the buyer", "--out", path)
		require.NoError(t, err, out)
		result, err := verifyWithDirectory(t, path, "")
		require.NoError(t, err)
		assert.Equal(t, "VALID", result["verdict"], name)

		// The agent's claim itself stays for the buyer: its words, checkable
		// against the sealed commitment.
		reps, _ := dealReportOf(b)["representations"].([]any)
		require.Len(t, reps, 1, name)
		assert.Equal(t, "The frame has one small scratch by the seat", reps[0].(map[string]any)["text"])
	}
	one, _ := json.Marshal(visible(t, copies["authorized"]))
	two, _ := json.Marshal(visible(t, copies["unchecked"]))
	// Steps, anomalies, did, told: the same, but for nonces and ids in the openings.
	assert.JSONEq(t, string(one), string(two), "the two copies read the same")
}

// One authorized and one unchecked claim in a deal: the counterparty copy and
// the default adjudicator copy both withhold the verdicts and the represent
// trail, both still verify, and neither says "none".
func TestOneAuthorizedAndOneUncheckedClaimLookTheSameInBothCopies(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	ck := represent(t, id, repFile("represent-condition.json"))
	claimWith(t, id, ck["check_id"].(string), repFile("claim-condition.json"))
	claimWith(t, id, "", repFile("claim-warranty.json"))

	for _, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
		b, raw := sharedCopy(t, id, audience, "the referee")
		for _, leak := range []string{`"unchecked"`, "authority_basis", "evaluation_ref", `"rule":"no_check"`} {
			assert.NotContains(t, raw, leak, "%s: %s", audience, leak)
		}
		withheld := 0
		for _, s := range sharedSteps(t, b) {
			if s["kind"] == dealSharePrivateKind {
				withheld++
			}
		}
		// The two claim_authority steps, and the represent snapshot and check.
		assert.GreaterOrEqual(t, withheld, 4, audience)
		path := filepath.Join(t.TempDir(), audience+".json")
		out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", audience, "--to", "the referee", "--out", path)
		require.NoError(t, err, out)
		result, err := verifyWithDirectory(t, path, "")
		require.NoError(t, err)
		assert.Equal(t, "VALID", result["verdict"], audience)
		var markers int
		for _, a := range dealReportOf(b)["anomalies"].([]any) {
			if item := a.(map[string]any); item["kind"] == "unapproved_representation_not_shared" {
				markers++
			}
		}
		assert.Equal(t, 1, markers, audience)
	}
}

// The user's own copy carries everything, and a step that stopped between the
// claim and its verdict is finished by the next command that writes, from the
// verdict the claim was noted with.
func TestAClaimWithoutItsVerdictStepIsFinishedBeforeAnythingElseIsSealed(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	require.NoError(t, s.useDeal(t.Context(), id, false))
	events, err := s.load(t.Context(), id)
	require.NoError(t, err)
	// The crash: the claim is sealed, its claim_authority is not.
	_, err = s.seal(t.Context(), id, events, dealEvent{Kind: "claim", ClaimShape: dealClaimShapeVersion, Claim: &dealClaim{
		Text: "The frame has no scratches at all", SourceKind: "agent", Class: "condition", Unchecked: true, Rule: "no_check",
	}})
	require.NoError(t, err)
	require.NoError(t, s.close())
	assert.Empty(t, recordsOfType(t, id, "claim_authority"))

	dealRun(t, "note", "--deal", id, "--kind", "message", "--input", writeJSON(t, `{"from": "counterparty", "text": "Is it still available?"}`))
	records, _ := exportRecords(t, id)
	var kinds []string
	for _, r := range records {
		if b, ok := r["x-deal-v0"].(map[string]any); ok {
			kinds = append(kinds, b["record_type"].(string))
		}
	}
	require.Contains(t, kinds, "claim_authority")
	for i, k := range kinds {
		if k == "claim_authority" {
			assert.Equal(t, "claim", kinds[i-1], "right after its claim, before the message")
		}
	}
	verdict := recordsOfType(t, id, "claim_authority")[0]["body"].(map[string]any)
	assert.Equal(t, true, verdict["unchecked"])
	assert.Equal(t, "no_check", verdict["rule"], "the verdict the claim was noted with")
	checkerPasses(t, id)
}

// Every disclosure is withheld from a rule 12 counterparty copy, by kind, and
// so is its row in "told": the row of an unapproved one would carry the step's
// id and "without your approval".
func TestTheCounterpartyCopyWithholdsEveryTellingAndItsToldRow(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	ck := represent(t, id, repFile("represent-condition.json"))
	claimWith(t, id, ck["check_id"].(string), repFile("claim-condition.json"))
	check := dealRun(t, "check", "--deal", id, "--input", writeJSON(t, `{"action":"share_contact","disclosing_to":"counterparty","description":"give the buyer my number","disclosing":["phone"]}`))
	require.Equal(t, "pause", check["verdict"])
	dealRun(t, "note", "--deal", id, "--kind", "approval", "--check", check["check_id"].(string), "--choice", "proceed", "--said", "yes", "--shown-card", shownCardFile(t, check["card"].(string)))
	body := writeJSON(t, `{"channel":"marketplace","fields":[{"class":"phone","value":"555-0100"}]}`)
	approved := dealRun(t, "note", "--deal", id, "--kind", "disclosure", "--input", body)
	unapproved := dealRun(t, "note", "--deal", id, "--kind", "disclosure", "--input", body)
	require.Empty(t, unapproved["authorized_by"])

	b, raw := sharedCopy(t, id, cli.DealAudienceCounterparty, "the buyer")
	assert.NotContains(t, raw, "without your approval")
	// The approval that covered the other telling answers a check, which
	// the copy withholds, so no telling shows whether it was approved.
	assert.Empty(t, dealReportOf(b)["told"])
	// Every telling is withheld, the approved one as well, so the buyer, who
	// knows from its own side that it was told twice, cannot tell which was
	// not approved.
	withheld := map[string]bool{}
	for _, st := range sharedSteps(t, b) {
		withheld[st["capsule_id"].(string)] = st["withheld"] == true
	}
	assert.True(t, withheld[approved["capsule_id"].(string)], "the approved telling")
	assert.True(t, withheld[unapproved["capsule_id"].(string)], "the unapproved telling")
}

// The baseline's own agent claims are no step of their own, and no producer
// seals one now (an old book may hold them): the default adjudicator copy
// opens none of them, and --include-agent-claims opens them. This runs the
// predicate the share extension uses, on a historical baseline.
func TestTheDefaultAdjudicatorCopyOpensNoBaselineAgentClaim(t *testing.T) {
	agent := dealClaim{Text: "Barely used", SourceKind: "agent"}
	own := dealClaim{Text: "From the listing", SourceKind: "platform"}
	events := []sealedEvent{{CapsuleID: "open", Event: dealEvent{Kind: "open", ClaimCommit: "c", Open: &dealOpen{Claims: []dealClaim{agent, own}},
		Nonces: map[string]string{"claim_text_0": "n0", "claim_text_1": "n1"}}}}
	clean := func(dealClaim) bool { return true }
	textsOf := func(opened []interface{}) []string {
		var out []string
		for _, o := range opened {
			out = append(out, o.(map[string]interface{})["text"].(map[string]interface{})["text"].(string))
		}
		return out
	}
	assert.Equal(t, []string{"From the listing"}, textsOf(claimOpenings(events, map[string]bool{}, adjudicatorOpens(true, clean))))
	assert.Equal(t, []string{"Barely used", "From the listing"}, textsOf(claimOpenings(events, map[string]bool{}, adjudicatorOpens(false, clean))))
}

// A claim left without its verdict is also finished by `deal tick`, before it
// seals a deferred email after it.
func TestATickFinishesAPendingClaimBeforeSealingADeferredEmail(t *testing.T) {
	dealFixture(t)
	setDealClock(t, "2026-10-03T21:00:00Z")
	records := map[string]string{merchantSelector + "._domainkey.shop.example": merchantKeyTXT(t), dmarcName: merchantDMARC(t)}
	stubDNS(t, records)
	id := sellerRepOpen(t, `["condition"]`)
	blockDNS(t)
	dealRun(t, "note", "--deal", id, "--kind", "evidence", "--email", filepath.Join(merchantFixture, "confirmation.eml"))

	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	require.NoError(t, s.useDeal(t.Context(), id, false))
	events, err := s.load(t.Context(), id)
	require.NoError(t, err)
	_, err = s.seal(t.Context(), id, events, dealEvent{Kind: "claim", ClaimShape: dealClaimShapeVersion, Claim: &dealClaim{
		Text: "The frame has no scratches at all", SourceKind: "agent", Class: "condition", Unchecked: true, Rule: "no_check",
	}})
	require.NoError(t, err)
	require.NoError(t, s.close())
	require.Empty(t, recordsOfType(t, id, "claim_authority"))

	stubDNS(t, records)
	setDealClock(t, "2026-10-03T21:10:00Z")
	out := dealRun(t, "tick")["merchant_emails"].(map[string]any)
	require.Len(t, out["sealed"], 1)
	records2, _ := exportRecords(t, id)
	var kinds []string
	for _, r := range records2 {
		if b, ok := r["x-deal-v0"].(map[string]any); ok {
			kinds = append(kinds, b["record_type"].(string))
		}
	}
	i := slices.Index(kinds, "claim")
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, "claim_authority", kinds[i+1], "the verdict follows its claim, before the email")
}

// A merchant email's row says what was approved at a check and on what basis;
// on a rule 12 counterparty copy that is read from withheld records, so the
// row carries neither.
func TestARule12CounterpartyCopyMerchantRowSaysNothingApproved(t *testing.T) {
	dealFixture(t)
	setDealClock(t, "2026-10-03T21:00:00Z")
	stubDNS(t, map[string]string{merchantSelector + "._domainkey.shop.example": merchantKeyTXT(t), dmarcName: merchantDMARC(t)})
	id := sellerRepOpen(t, `["condition"]`)
	dealRun(t, "note", "--deal", id, "--kind", "evidence", "--email", filepath.Join(merchantFixture, "confirmation.eml"))

	require.Contains(t, receiptText(t, id, false), "approved_basis", "the owner's own copy says it, so the check below is not vacuous")

	b, _ := sharedCopy(t, id, cli.DealAudienceCounterparty, "the buyer")
	rows, _ := dealReportOf(b)["merchant"].([]any)
	require.NotEmpty(t, rows)
	for _, r := range rows {
		assert.NotContains(t, r, "approved")
		assert.NotContains(t, r, "approved_basis")
	}
}
