package deal

import (
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
