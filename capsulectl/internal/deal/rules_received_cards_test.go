package deal

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rulesBlocks collects every "rules" member under v, in document order.
func rulesBlocks(v any) []any {
	var out []any
	switch x := v.(type) {
	case map[string]any:
		if r, ok := x["rules"]; ok {
			out = append(out, r)
		}
		for _, k := range slices.Sorted(maps.Keys(x)) {
			out = append(out, rulesBlocks(x[k])...)
		}
	case []any:
		for _, e := range x {
			out = append(out, rulesBlocks(e)...)
		}
	}
	return out
}

func exportAsAny(t *testing.T, dealID string) ([]any, string) {
	t.Helper()
	records, _ := exportRecords(t, dealID)
	all := make([]any, len(records))
	for i, r := range records {
		all[i] = r
	}
	raw, err := json.Marshal(records)
	require.NoError(t, err)
	return all, string(raw)
}

const cardMarker = "CARD-MARKER-9f3a"

func cardsFile(t *testing.T) string {
	return writeJSON(t, `[{"schema":"history_card/1","counterparty":"`+cardMarker+`","n":9007199254740993},{"schema":"selection/1"}]`)
}

// Cards handed to the rules checker at a check change nothing that is sealed
// (evidencebook-deal#166): the sealed rules block comes from a run given no
// cards, so a checker that refuses cards, changes its verdict over them, or
// puts them in a finding or on stderr cannot reach it. The checker's line
// about them is shown in the check's output only.
func TestReceivedCardsSealNothingWhateverTheCheckerDoes(t *testing.T) {
	for name, tc := range map[string]struct {
		body       string
		wantNotice string
		wantNone   string // the reason there is no line
	}{
		"a checker that complies": {
			body: `if grep -q received_cards "$INPUT"; then
  printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"example-rules/1.0.0","definition_digest":"` + exampleRulesDigest + `","verdict":"allow","findings":[],"notice":{"line":"read 2 cards"}}'
else ` + checkerPrints("allow", "") + `; fi`,
			wantNotice: "read 2 cards",
		},
		"a checker that refuses cards and names one on stderr": {
			body: `if grep -q received_cards "$INPUT"; then echo "refused ` + cardMarker + `" >&2; exit 3; fi
` + checkerPrints("allow", ""),
			wantNone: "did not read the cards",
		},
		"a checker that denies over cards and echoes one in a finding": {
			body: `if grep -q received_cards "$INPUT"; then
  printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"example-rules/1.0.0","definition_digest":"` + exampleRulesDigest + `","verdict":"deny","findings":[{"id":"card","check":"` + cardMarker + `","verdict":"fail","reason":"` + cardMarker + `"}]}'
else ` + checkerPrints("allow", "") + `; fi`,
			wantNone: "printed no line about the cards",
		},
		"a checker that prints a notice with no cards too": {
			body:       `printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"example-rules/1.0.0","definition_digest":"` + exampleRulesDigest + `","verdict":"allow","findings":[],"notice":{"line":"always"}}'`,
			wantNotice: "always",
		},
	} {
		t.Run(name, func(t *testing.T) {
			id := stickerDeal(t, "card", false)
			// The checker runs from a private copy, so it reads its own stdin
			// where the stub wrote it, beside the original.
			root := t.TempDir()
			t.Setenv("CAPSULECTL_PLUGIN_ROOTS", root)
			body := strings.ReplaceAll(tc.body, "$INPUT", root+"/checker.input")
			pinChecker(t, map[string]any{"command": []string{stubChecker(t, "checker", body)}, "definition_digest": exampleRulesDigest})
			input := writeJSON(t, payCheck("card", 454, 480))

			without := dealRun(t, "check", "--deal", id, "--input", input)
			with := dealRun(t, "check", "--deal", id, "--input", input, "--received", cardsFile(t))

			assert.NotContains(t, without, "rules_notice", "no cards, no line, whatever the checker prints")
			assert.NotContains(t, without, "rules_notice_unavailable")
			assert.Equal(t, without["verdict"], with["verdict"])
			assert.Equal(t, without["rules"], with["rules"], "the rules block the check reports is the same with and without cards")
			if tc.wantNotice != "" {
				assert.Equal(t, tc.wantNotice, with["rules_notice"])
			} else {
				assert.NotContains(t, with, "rules_notice")
				assert.Contains(t, with["rules_notice_unavailable"], tc.wantNone)
				rawWith, err := json.Marshal(with)
				require.NoError(t, err)
				assert.NotContains(t, string(rawWith), cardMarker, "no card text reaches the output")
			}

			records, raw := exportAsAny(t, id)
			blocks := rulesBlocks(records)
			require.Len(t, blocks, 2, "one sealed rules block per check")
			assert.Equal(t, blocks[0], blocks[1], "the sealed rules blocks are identical")
			for _, leaked := range []string{cardMarker, "received_cards", "notice", "9007199254740993"} {
				assert.False(t, strings.Contains(raw, leaked), "the sealed export holds %q", leaked)
			}
		})
	}
}

// The checker is handed each card with its values and numbers as given, ones a float cannot hold included.
func TestReceivedCardsReachTheCheckerAsReceived(t *testing.T) {
	id := stickerDeal(t, "card", false)
	checker := stubChecker(t, "checker", checkerPrints("allow", ""))
	pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": exampleRulesDigest})
	dealRun(t, "check", "--deal", id, "--input", writeJSON(t, payCheck("card", 454, 480)), "--received", cardsFile(t))
	raw, err := os.ReadFile(checker + ".input")
	require.NoError(t, err)
	got := string(raw)
	assert.Contains(t, got, `"n":9007199254740993`)
	assert.Contains(t, got, cardMarker)
}

// --received takes a non-empty array of at most 64 card objects and 1 MiB.
func TestReceivedCardsAreRefusedUnlessAnArrayOfCards(t *testing.T) {
	id := stickerDeal(t, "card", false)
	pinStub(t, "1000")
	cards := func(n int) string { return `[` + strings.TrimSuffix(strings.Repeat(`{},`, n), ",") + `]` }
	input := writeJSON(t, payCheck("card", 454, 480))
	for name, tc := range map[string]struct{ body, reason string }{
		"an empty array": {`[]`, "non-empty"},
		"an object":      {`{"schema":"history_card/1"}`, ""},
		"a string card":  {`["card"]`, "not a card"},
		"a null card":    {`[null]`, "not a card"},
		"65 cards":       {cards(65), "at most 64"},
		"over 1 MiB":     {`[{"x":"` + strings.Repeat("a", 1<<20) + `"}]`, "1 MiB"},
	} {
		err := dealRunErr(t, "check", "--deal", id, "--input", input, "--received", writeJSON(t, tc.body))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.reason, name)
	}
	dealRun(t, "check", "--deal", id, "--input", input, "--received", writeJSON(t, cards(64)))
	err := dealRunErr(t, "check", "--deal", id, "--input", input, "--received", "")
	require.Error(t, err, "an empty path is not 'no cards'")
}

// Cards go to a pinned rules checker or nowhere: a profile with none refuses
// the flag, and so does a sale's post check, which runs no checker.
func TestReceivedCardsNeedARulesChecker(t *testing.T) {
	id := stickerDeal(t, "card", false)
	err := dealRunErr(t, "check", "--deal", id, "--input", writeJSON(t, payCheck("card", 454, 480)), "--received", cardsFile(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned rules checker")

	sale := dealRun(t, "sale", "new", "--input", writeJSON(t, `{"type": "purchase", "demo": true,
 "intent": {"verbatim": "Sell my example bicycle; pickup only", "asked": {"item": "example bicycle", "quantity": 1}, "allowed": ["offer", "commit"]},
 "terms": {"item": "example bicycle", "quantity": 1, "price_minor": 190000, "currency": "USD", "handover": "in_person"},
 "recourse": {"rail": "cash", "refundable": false}}`))["sale_id"].(string)
	pinStub(t, "1000")
	err = dealRunErr(t, "check", "--deal", sale, "--input", writeJSON(t, `{"action":"post","posting":`+postingJSON(listingText, "example", "condition")+`}`), "--received", cardsFile(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "post check runs no rules checker")
}

// When the check's own run is not answered, the cards get no second run: the
// checker is not run twice over a failure, and the output says why.
func TestReceivedCardsGetNoSecondRunWhenTheCheckWasNotEvaluated(t *testing.T) {
	id := stickerDeal(t, "card", false)
	root := t.TempDir()
	t.Setenv("CAPSULECTL_PLUGIN_ROOTS", root)
	checker := stubChecker(t, "checker", "echo refused >&2; exit 3")
	pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": exampleRulesDigest})
	check := dealRun(t, "check", "--deal", id, "--input", writeJSON(t, payCheck("card", 454, 480)), "--received", cardsFile(t))
	assert.Contains(t, check["rules_notice_unavailable"], "the check itself was not evaluated")
	assert.NotContains(t, check, "rules_notice")
	raw, err := os.ReadFile(checker + ".input")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "received_cards", "the checker was run once, without cards")
}
