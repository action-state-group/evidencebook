package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keepStepLines is the step lines of the user's own copy of a deal.
func keepStepLines(t *testing.T, id string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "own.json")
	dealRun(t, "report", "--deal", id, "--bundle", path)
	return bundleStepLines(t, path)
}

// bundleStepLines is the step lines of a deal bundle file.
func bundleStepLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var b map[string]any
	require.NoError(t, json.Unmarshal(raw, &b))
	var lines []string
	for _, s := range dealReportOf(b)["steps"].([]any) {
		lines = append(lines, s.(map[string]any)["line"].(string))
	}
	return lines
}

// A claim noted with whose it is and no note of where it was read names whose
// it is wherever a line says where it came from, never "()" or "(from )"
// (evidencebook#680).
func TestAClaimWithNoSourceNoteNamesWhoseItIs(t *testing.T) {
	for kind, words := range sourceKindWords {
		assert.Equal(t, words, claimSourceWords(dealClaim{Text: "x", SourceKind: kind}), kind)
	}
	assert.Equal(t, "buyer refund request", claimSourceWords(dealClaim{Text: "x", Source: "buyer_refund_request", SourceKind: "external"}),
		"a source note reads with spaces")
	assert.Equal(t, "source not stated", claimSourceWords(dealClaim{Text: "x"}))

	// A buyer's deal: the agent's claim and the user's have no source note.
	dealFixture(t)
	id := openJetSki(t)
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "The listing shows two jet skis", "source_kind": "agent"}`))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "I rented from them last year", "source_kind": "user"}`))
	report := dealRun(t, "report", "--deal", id)
	anomalies := reportTexts(t, report, "anomalies")
	assert.Contains(t, anomalies, "counterparty/unverified_claim: Unverified: The listing shows two jet skis (from your agent)")
	for _, line := range anomalies {
		assert.NotContains(t, line, "I rented from them last year", "the user's own claim is not the other party's (#1036)")
	}
	steps := keepStepLines(t, id)
	for _, line := range append(anomalies, steps...) {
		assert.NotContains(t, line, "()")
		assert.NotContains(t, line, "(from )")
	}
	assert.Contains(t, steps, "Claim recorded (your agent): The listing shows two jet skis")
	assert.Contains(t, steps, "Claim recorded (you): I rented from them last year")
}

// Where the user sells, the agent's own claims are the seller's
// representations: they are not listed as the other party's unverified claims,
// on the user's own Receipt or in an adjudicator's copy the seller opened
// them to; nor is the user's own claim on any deal. A claim read from the
// buyer keeps its entry (evidencebook#680, #1036).
func TestASellersAgentClaimsAreNotTheCounterpartysUnverifiedClaims(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "The frame has one small scratch by the seat", "source_kind": "agent", "class": "condition"}`))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "I kept it indoors all winter", "source_kind": "user"}`))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "The buyer says it will be collected Friday", "source_kind": "external", "source": "buyer_message"}`))

	unverified := func(texts []string) []string {
		var out []string
		for _, line := range texts {
			if strings.Contains(line, "unverified_claim") {
				out = append(out, line)
			}
		}
		return out
	}
	report := dealRun(t, "report", "--deal", id)
	assert.Equal(t, []string{
		"counterparty/unverified_claim: Unverified: The buyer says it will be collected Friday (from buyer message)",
	}, unverified(reportTexts(t, report, "anomalies")))
	steps := keepStepLines(t, id)
	assert.Contains(t, steps, "Claim recorded (your agent): The frame has one small scratch by the seat")
	assert.Contains(t, steps, "Claim recorded (you): I kept it indoors all winter")

	// The adjudicator's copy the seller opened the agent's claims to says
	// the agent's claim on its line, and still not as an unverified one.
	opted := filepath.Join(t.TempDir(), "opted.json")
	_, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--include-agent-claims", "--out", opted)
	assert.NoError(t, err)
	lines := bundleStepLines(t, opted)
	var b map[string]any
	readJSONFile(t, opted, &b)
	var items []string
	for _, a := range dealReportOf(b)["anomalies"].([]any) {
		if item := a.(map[string]any); item["kind"] == "unverified_claim" {
			items = append(items, item["text"].(string))
		}
	}
	assert.Contains(t, lines, "Claim recorded (your agent): The frame has one small scratch by the seat")
	assert.Equal(t, []string{
		"Unverified: The buyer says it will be collected Friday (from buyer message)",
	}, items)

	// The default adjudicator's copy withholds the agent's claim as before.
	b, _ = sharedCopy(t, id, cli.DealAudienceAdjudicator, "the referee")
	for _, s := range sharedSteps(t, b) {
		assert.NotContains(t, s["line"], "one small scratch")
	}
}
