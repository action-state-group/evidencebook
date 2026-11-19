package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every rule authorizeClaim can seal an unchecked claim under has words for
// the seller's Receipt (capsule-cli#333). The set is read from the function's
// own source, so a rule added or renamed there (capsule-engine#171) fails
// here until the table says it in words.
func TestEveryUncheckedRepresentationRuleHasWords(t *testing.T) {
	src, err := os.ReadFile("deal_representations.go")
	require.NoError(t, err)
	body := string(src)
	start := strings.Index(body, "func authorizeClaim(")
	require.NotEqual(t, -1, start)
	end := strings.Index(body[start:], "\n}\n")
	require.NotEqual(t, -1, end)
	rules := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)return "", .*, "(\w+)"$`).FindAllStringSubmatch(body[start:start+end], -1) {
		rules[m[1]] = true
	}
	require.NotEmpty(t, rules)
	for rule := range rules {
		assert.NotEmpty(t, representationRuleWords[rule], "rule %q has no words", rule)
	}
	for rule := range representationRuleWords {
		assert.True(t, rules[rule], "the table names %q, which authorizeClaim never returns", rule)
	}
}

// The seller's own Receipt lists each claim the agent made with no authority,
// in the attention section (the report's agent-side anomalies), and its page
// draws it; an authorized claim is not listed; no shared copy carries it.
func TestTheSellersReceiptShowsAClaimMadeWithoutAuthority(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "The frame has no scratches at all", "source_kind": "agent", "class": "condition"}`))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "I will cover the gears for 90 days", "source_kind": "agent", "class": "warranty"}`))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "I paid 1500 for it new", "source_kind": "user"}`))

	page := filepath.Join(t.TempDir(), "own.html")
	report := dealRun(t, "report", "--deal", id, "--html", page)
	var said []string
	for _, line := range reportTexts(t, report, "anomalies") {
		if strings.HasPrefix(line, "agent/unapproved_representation: ") {
			said = append(said, line)
		}
	}
	assert.Equal(t, []string{
		"agent/unapproved_representation: Your agent said this without your authority: “The frame has no scratches at all” (you weren't asked about it)",
		"agent/unapproved_representation: Your agent said this without your authority: “I will cover the gears for 90 days” (you weren't asked about it)",
	}, said, "each unchecked agent claim, none of the user's own")

	raw, err := os.ReadFile(page)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Your agent said this without your authority")

	for _, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
		b, _ := sharedCopy(t, id, audience, "the buyer")
		for _, a := range dealReportOf(b)["anomalies"].([]any) {
			item := a.(map[string]any)
			assert.NotEqual(t, "unapproved_representation", item["kind"], audience)
			assert.NotContains(t, item["text"], "without your authority", audience)
		}
	}
}

// A seller may show one adjudicator copy every claim their agent made without
// authority (evidencebook-deal#207): off by default, where the copy says "not
// shared" (never "none"); on, it carries all of them and says the seller chose
// to; a counterparty's copy never carries them, and the choice is refused there.
func TestTheSellerMayShowAnAdjudicatorTheClaimsMadeWithoutAuthority(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "The frame has no scratches at all", "source_kind": "agent", "class": "condition"}`))
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text": "I will cover the gears for 90 days", "source_kind": "agent", "class": "warranty"}`))

	anomalies := func(b map[string]any) []map[string]any {
		var out []map[string]any
		for _, a := range dealReportOf(b)["anomalies"].([]any) {
			out = append(out, a.(map[string]any))
		}
		return out
	}

	// Default: no claim, and the copy says they are not shared.
	b, _ := sharedCopy(t, id, cli.DealAudienceAdjudicator, "the referee")
	var texts []string
	for _, a := range anomalies(b) {
		assert.NotEqual(t, "unapproved_representation", a["kind"])
		texts = append(texts, a["text"].(string))
	}
	assert.Contains(t, texts, unauthorizedClaimsNotShared)
	for _, text := range texts {
		assert.NotContains(t, text, "without your authority")
	}

	// Opted in: every claim, marked as the seller's choice.
	path := filepath.Join(t.TempDir(), "opted.json")
	out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--include-unauthorized-claims", "--out", path)
	require.NoError(t, err, out)
	var opted map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, path), &opted))
	var said, marks []string
	for _, a := range anomalies(opted) {
		switch a["kind"] {
		case "unapproved_representation":
			said = append(said, a["text"].(string))
		case "unapproved_representation_shared":
			marks = append(marks, a["text"].(string))
		case "unapproved_representation_not_shared":
			t.Fatal("an opted-in copy also says not shared")
		}
	}
	assert.Equal(t, []string{
		"Your agent said this without your authority: “The frame has no scratches at all” (you weren't asked about it)",
		"Your agent said this without your authority: “I will cover the gears for 90 days” (you weren't asked about it)",
	}, said, "all of them, none hand-picked")
	for _, a := range anomalies(opted) {
		if a["kind"] == "unapproved_representation_shared" {
			assert.Equal(t, "agent", a["side"], "the page draws an anomaly only under its side")
		}
	}
	for _, a := range anomalies(b) {
		if a["kind"] == "unapproved_representation_not_shared" {
			assert.Equal(t, "agent", a["side"], "the page draws an anomaly only under its side")
		}
	}
	assert.Equal(t, []string{"Shared by the seller's choice: every claim their agent made without their authority (2)"}, marks)

	// The page of an opted-in copy draws them; the default page does not.
	page := filepath.Join(t.TempDir(), "adj.html")
	dealRun(t, "report", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--include-unauthorized-claims", "--html", page)
	assert.Contains(t, string(mustRead(t, page)), "Shared by the seller's choice")
	own := filepath.Join(t.TempDir(), "adj-default.html")
	dealRun(t, "report", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--html", own)
	assert.Contains(t, string(mustRead(t, own)), "Not shared: whether the seller's agent made any claim")
	assert.NotContains(t, string(mustRead(t, own)), "without your authority")

	// Never for a counterparty, and never silently dropped: it is refused.
	_, err = invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "counterparty", "--to", "the buyer", "--include-unauthorized-claims", "--out", filepath.Join(t.TempDir(), "c.json"))
	require.Error(t, err)
	_, err = invoke(t, "", "--profile", "deal", "report", "--deal", id, "--include-unauthorized-claims")
	require.Error(t, err)
}

// The choice is the seller's: on a deal where the user buys, it is refused.
func TestTheClaimsChoiceIsRefusedOnABuyersDeal(t *testing.T) {
	dealFixture(t)
	id := retailDeal(t)
	_, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--include-unauthorized-claims", "--out", filepath.Join(t.TempDir(), "x.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seller's choice")
}

// Opted in with nothing to show, the copy says so in words ("none" is honest
// there, since the seller did share).
func TestAnOptedInCopyWithNoClaimsSaysTheAgentMadeNone(t *testing.T) {
	dealFixture(t)
	id := sellerRepOpen(t, `["condition"]`)
	path := filepath.Join(t.TempDir(), "opted.json")
	out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", "adjudicator", "--to", "the referee", "--include-unauthorized-claims", "--out", path)
	require.NoError(t, err, out)
	var b map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, path), &b))
	var texts []string
	for _, a := range dealReportOf(b)["anomalies"].([]any) {
		item := a.(map[string]any)
		if item["kind"] == "unapproved_representation_shared" {
			texts = append(texts, item["text"].(string))
		}
	}
	assert.Equal(t, []string{unauthorizedClaimsSharedNone}, texts)
}
