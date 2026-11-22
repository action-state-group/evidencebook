package deal

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The adjudicator's copy of a sale carries none of the seller's agent's claims
// unless the seller chose to show them (Steven's ruling 2026-11-21,
// evidencebook-deal#231): not the sealed unchecked and rule fields, not the
// words, wherever the copy says them. A claim that is not the agent's stays.
func TestTheAdjudicatorCopyHidesTheAgentsClaimsUnlessTheSellerChooses(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "The frame has no scratches at all", "source_kind": "agent", "class": "condition"}`))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "I will cover the gears for 90 days", "source_kind": "agent", "class": "warranty"}`))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "The buyer said it ships by Friday", "source_kind": "external", "source": "the_buyer_page"}`))

	agentWords := []string{"no scratches at all", "cover the gears"}

	// Default: the records are withheld, and no section says the words.
	b, raw := sharedCopy(t, id, cli.DealAudienceAdjudicator, "the referee")
	for _, w := range agentWords {
		assert.NotContains(t, raw, w)
	}
	assert.NotContains(t, raw, `"unchecked"`, "the sealed unchecked field")
	assert.NotContains(t, raw, `"rule":"`, "the sealed rule field")
	report := dealReportOf(b)
	for _, s := range sharedSteps(t, b) {
		if s["kind"] == "claim" {
			if s["withheld"] == true {
				assert.Equal(t, "Claim recorded (withheld)", s["line"])
			}
		}
	}
	openings, _ := json.Marshal(report["claim_openings"])
	assert.Contains(t, string(openings), "ships by Friday", "a claim that is not the agent's is still opened")
	for _, w := range agentWords {
		assert.NotContains(t, string(openings), w)
	}
	assert.Nil(t, report["representations"])
	var unverified []string
	for _, a := range report["anomalies"].([]any) {
		if item := a.(map[string]any); item["kind"] == "unverified_claim" {
			unverified = append(unverified, item["text"].(string))
		}
	}
	for _, u := range unverified {
		for _, w := range agentWords {
			assert.NotContains(t, u, w)
		}
	}
	assert.Contains(t, unverified, "Unverified: The buyer said it ships by Friday (from the buyer page)", "a claim that is not the agent's keeps its anomaly")

	// The copy's own list of what it leaves out, and the one sealed in the
	// disclosure record, name the agent's claims wherever the marker does.
	const claimsEntry = "the claims your agent made (their words and whether you authorized them)"
	assert.Contains(t, report["withheld"], claimsEntry)
	records, _ := sharedRecords(t, id)
	require.NotEmpty(t, records)
	assert.Contains(t, records[len(records)-1]["suppressed_receipt_fields"], claimsEntry)

	// Every agent claim record is withheld, authorized or not, and the copy still verifies.
	withheldClaims := 0
	for _, s := range sharedSteps(t, b) {
		if s["kind"] == "claim" && s["withheld"] == true {
			withheldClaims++
		}
	}
	assert.Equal(t, 2, withheldClaims)
	path := filepath.Join(t.TempDir(), "default.json")
	out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--out", path)
	require.NoError(t, err, out)
	result, err := verifyWithDirectory(t, path, "")
	require.NoError(t, err)
	assert.Equal(t, "VALID", result["verdict"])

	// Opted in: the words, the openings and the representations are there.
	opted := filepath.Join(t.TempDir(), "opted.json")
	out, err = invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--include-agent-claims", "--out", opted)
	require.NoError(t, err, out)
	optedRaw := string(mustRead(t, opted))
	for _, w := range agentWords {
		assert.Contains(t, optedRaw, w)
	}
	assert.Contains(t, optedRaw, `"unchecked"`)
	result, err = verifyWithDirectory(t, opted, "")
	require.NoError(t, err)
	assert.Equal(t, "VALID", result["verdict"])
	var ob map[string]any
	require.NoError(t, json.Unmarshal([]byte(optedRaw), &ob))
	assert.NotNil(t, dealReportOf(ob)["representations"])
	assert.NotContains(t, dealReportOf(ob)["withheld"], claimsEntry, "an opted-in copy does not say they are withheld")
	records, _ = sharedRecords(t, id)
	assert.NotContains(t, records[len(records)-1]["suppressed_receipt_fields"], claimsEntry)

	// The default page says nothing of the words either.
	page := filepath.Join(t.TempDir(), "adj.html")
	dealRun(t, "report", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--html", page)
	for _, w := range agentWords {
		assert.NotContains(t, string(mustRead(t, page)), w)
	}
}

// An authorized claim is withheld like an unchecked one, and so is the represent
// trail before it (the snapshot, the check, the words they quote), so the copy
// does not say which claims had a check (Sherlock2's review of #245).
func TestTheAdjudicatorCopyHidesAnAuthorizedClaimAndItsTrail(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	ck := represent(t, id, repFile("represent-condition.json"))
	require.Equal(t, "pass", ck["verdict"])
	claimWith(t, id, ck["check_id"].(string), repFile("claim-condition.json"))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "I will cover the gears for 90 days", "source_kind": "agent", "class": "warranty"}`))
	words := []string{"one small scratch", "cover the gears"}

	b, raw := sharedCopy(t, id, cli.DealAudienceAdjudicator, "the referee")
	for _, w := range words {
		assert.NotContains(t, raw, w)
	}
	for _, s := range sharedSteps(t, b) {
		assert.NotContains(t, []any{"snapshot", "check"}, s["kind"], "the represent trail is not listed as such")
	}
	path := filepath.Join(t.TempDir(), "d.json")
	out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--out", path)
	require.NoError(t, err, out)
	result, err := verifyWithDirectory(t, path, "")
	require.NoError(t, err)
	assert.Equal(t, "VALID", result["verdict"])
	page := filepath.Join(t.TempDir(), "p.html")
	dealRun(t, "report", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--html", page)
	for _, w := range words {
		assert.NotContains(t, string(mustRead(t, page)), w)
	}

	// Opted in, under the new name: both claims and the trail.
	opted := filepath.Join(t.TempDir(), "o.json")
	out, err = invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--include-agent-claims", "--out", opted)
	require.NoError(t, err, out)
	for _, w := range words {
		assert.Contains(t, string(mustRead(t, opted)), w)
	}
}

// Two claims can say the same words from different sources: the one that is
// not the agent's keeps its "Unverified" line and the agent's does not, in
// either order.
func TestTheUnverifiedLineFollowsTheClaimNotItsWords(t *testing.T) {
	for _, order := range [][2]string{{"agent", "external"}, {"external", "agent"}} {
		dealFixture(t)
		id := sellerRepOpen(t, `["condition"]`)
		for _, kind := range order {
			body := `{"text": "The listing says it ships Friday", "source_kind": "` + kind + `"`
			if kind == "agent" {
				body += `, "class": "condition"}`
			} else {
				body += `, "source": "listing_page"}`
			}
			dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t, body))
		}
		b, _ := sharedCopy(t, id, cli.DealAudienceAdjudicator, "the referee")
		var lines []string
		for _, a := range dealReportOf(b)["anomalies"].([]any) {
			if item := a.(map[string]any); item["kind"] == "unverified_claim" {
				lines = append(lines, item["text"].(string))
			}
		}
		assert.Equal(t, []string{"Unverified: The listing says it ships Friday (from listing page)"}, lines, "%v", order)
	}
}

// The words of a claim the agent says as the seller's own are in the check
// before it: that check is withheld too, whether or not the seller said yes.
func TestAUserAttributedClaimsCheckStaysOutOfTheDefaultCopy(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["authenticity"]`)
	ck := represent(t, id, writeJSON(t, attributedCheck(attributedText)))
	claimWith(t, id, ck["check_id"].(string), writeJSON(t, attributedClaim(attributedText)))
	b, raw := sharedCopy(t, id, cli.DealAudienceAdjudicator, "the referee")
	assert.NotContains(t, raw, "says it's authentic")
	for _, a := range dealReportOf(b)["anomalies"].([]any) {
		assert.NotContains(t, a.(map[string]any)["text"], "authentic")
	}
	opted := filepath.Join(t.TempDir(), "o.json")
	out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--include-agent-claims", "--out", opted)
	require.NoError(t, err, out)
	assert.Contains(t, string(mustRead(t, opted)), "says it's authentic")
}
