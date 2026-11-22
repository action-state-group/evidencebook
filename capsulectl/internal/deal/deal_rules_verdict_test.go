package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The party's own overall rules verdict (evidencebook-deal#191, Steven's ruling Buzz 8b6aaecf):
// a sealed rules_verdict record, carried by a counterparty copy only when the holder's share
// policy says so, naming the pack by its digest alone, never by a name.

const publishedPackName = "example/travel-deal/1.2.0"

// closedUnderPack is a deal whose one check ran the stub pack with the given ruleset id and pin
// kind, closed with nothing done.
func closedUnderPack(t *testing.T, rulesetID, kind string) string {
	t.Helper()
	dealFixture(t)
	out := `printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"` + rulesetID + `","definition_digest":"` + packDigest +
		`","verdict":"allow","tier":"recomputed","findings":[{"id":"price_as_offered","check":"price equals the offer","verdict":"pass"}]}'`
	pin := map[string]any{"command": []string{stubChecker(t, "pack", out)}, "definition_digest": packDigest}
	if kind != "" {
		pin["kind"] = kind
	}
	pinChecker(t, pin)
	id := openTyped(t, sellerTyped)
	dealRun(t, "check", "--deal", id, "--input", writeJSON(t,
		`{"action":"offer","amount_minor":190000,"terms":{"item":"example bicycle","quantity":1,"price_minor":190000}}`))
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"not_selected"}`))
	return id
}

func sealVerdict(t *testing.T, id string, more ...string) map[string]any {
	t.Helper()
	return dealRun(t, append([]string{"rules-verdict", "seal", "--deal", id, "--word", "out_of_scope"}, more...)...)
}

// bundleText is a copy's whole bundle as JSON text: its disclosed records and its report.
func bundleText(t *testing.T, id string, share ...string) string {
	t.Helper()
	page := filepath.Join(t.TempDir(), "copy.html")
	args := append([]string{"report", "--deal", id, "--html", page}, share...)
	dealRun(t, args...)
	raw, err := os.ReadFile(page)
	require.NoError(t, err)
	text, err := json.Marshal(embeddedBundle(t, string(raw)))
	require.NoError(t, err)
	return string(text)
}

// counterpartyCut is the counterparty copy's whole bundle, as `deal report --share
// counterparty` writes it.
func counterpartyCut(t *testing.T, id string) string {
	t.Helper()
	return bundleText(t, id, "--share", "counterparty", "--to", "the other side")
}

func verdictRecords(t *testing.T, id string) []map[string]any {
	t.Helper()
	return recordsOfType(t, id, "rules_verdict")
}

// Acceptance 3: the verdict is a record in the party's own book, bound to the deal id and the
// pack digest, sealed after the final close and confirming it.
func TestARulesVerdictIsARecordBoundToTheDealAndThePackDigest(t *testing.T) {
	id := closedUnderPack(t, packID, "")
	out := sealVerdict(t, id)
	assert.Equal(t, "out_of_scope", out["word"])
	assert.Equal(t, packDigest, out["pack_digest"])
	records := verdictRecords(t, id)
	require.Len(t, records, 1)
	block := records[0][dealprofile.Profile].(map[string]any)
	assert.Equal(t, id, block["deal_id"])
	assert.Equal(t, "rules_verdict", block["record_type"])
	assert.Equal(t, map[string]any{"word": "out_of_scope", "pack_digest": packDigest}, bodyOf(records[0]), "a private pack's body names the digest alone")
	events, recs := recordsOf(t, id)
	closeAt := finalClose(events)
	refs := block["refs"].([]any)
	require.Len(t, refs, 1)
	assert.Equal(t, "confirms", refs[0].(map[string]any)["rel"])
	assert.Equal(t, events[closeAt].Digest, refs[0].(map[string]any)["digest"], "it confirms the final close")
	assert.Equal(t, "rules_verdict", recs[len(recs)-1][dealprofile.Profile].(map[string]any)["record_type"])
}

// A verdict is for a finished deal, one per deal, one of four words, under a pack the deal's own
// checks ran under; this repository never reruns or derives it.
func TestARulesVerdictRefusesWhatTheRulingDoesNotAllow(t *testing.T) {
	id := closedUnderPack(t, packID, "")
	for name, args := range map[string][]string{
		"a word of another kind": {"rules-verdict", "seal", "--deal", id, "--word", "pass"},
		"no word":                {"rules-verdict", "seal", "--deal", id},
		"another pack's digest":  {"rules-verdict", "seal", "--deal", id, "--word", "met", "--pack-digest", strings.Repeat("0", 64)},
	} {
		out, err := invoke(t, "", append([]string{"--profile", "deal", "deal"}, args...)...)
		assert.Error(t, err, name+": "+out)
	}
	sealVerdict(t, id)
	_, err := invoke(t, "", "--profile", "deal", "deal", "rules-verdict", "seal", "--deal", id, "--word", "met")
	require.Error(t, err, "a deal takes one verdict")
	assert.Contains(t, err.Error(), "already")
	assert.Len(t, verdictRecords(t, id), 1)
}

func TestARulesVerdictIsSealedOnlyAfterTheFinalClose(t *testing.T) {
	dealFixture(t)
	pinChecker(t, map[string]any{"command": []string{stubChecker(t, "pack", `printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"`+packID+`","definition_digest":"`+packDigest+`","verdict":"allow","tier":"recomputed","findings":[{"id":"x","check":"x","verdict":"pass"}]}'`)}, "definition_digest": packDigest})
	id := openTyped(t, sellerTyped)
	dealRun(t, "check", "--deal", id, "--input", writeJSON(t, `{"action":"offer","amount_minor":190000,"terms":{"item":"example bicycle","quantity":1,"price_minor":190000}}`))
	_, err := invoke(t, "", "--profile", "deal", "deal", "rules-verdict", "seal", "--deal", id, "--word", "met")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "close")
}

// Acceptance 1: with the policy on, the counterparty cut carries the sealed record; with it off,
// or a person's default, the cut does not, and says nothing of what the record is.
func TestACounterpartyCutCarriesTheVerdictOnlyWhenTheHoldersPolicySaysSo(t *testing.T) {
	type holder struct {
		kind, flag string
		carried    bool
	}
	for name, h := range map[string]holder{
		"a node's default":           {"node", "", true},
		"a node that opts out":       {"node", "--no-share", false},
		"a person's default":         {"person", "", false},
		"a person that opts in":      {"person", "--share", true},
		"a holder with no kind":      {"", "", false},
		"a holder with no kind, off": {"", "--no-share", false},
	} {
		id := closedUnderPack(t, packID, "")
		if h.kind != "" {
			dealRun(t, "standard-history", "declare", "--holder-kind", h.kind)
			if h.flag != "" {
				dealRun(t, "rules-verdict", "policy", h.flag)
			}
		} else if h.flag != "" {
			_, err := invoke(t, "", "--profile", "deal", "deal", "rules-verdict", "policy", h.flag)
			require.Error(t, err, name)
			continue
		}
		sealVerdict(t, id)
		cut := counterpartyCut(t, id)
		assert.Equal(t, h.carried, strings.Contains(cut, "out_of_scope"), name)
		assert.Equal(t, h.carried, strings.Contains(cut, "(their rules)"), name)
		if !h.carried {
			assert.NotContains(t, cut, "rules_verdict", name+": a withheld verdict does not say what it is")
			assert.NotContains(t, cut, "their rules", name)
			assert.NotContains(t, cut, packDigest, name+": nor the pack's digest")
		}
		// The holder's own copy always carries it.
		assert.Contains(t, bundleText(t, id), "out_of_scope", name)
	}
}

func TestAnAdjudicatorCopyNeverCarriesTheVerdict(t *testing.T) {
	id := closedUnderPack(t, packID, "")
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	sealVerdict(t, id)
	text := bundleText(t, id, "--share", "adjudicator", "--to", "a referee")
	assert.NotContains(t, text, "out_of_scope")
	assert.NotContains(t, text, "their rules")
}

func TestThePolicyIsShownAndSurvivesADeclareAgain(t *testing.T) {
	dealFixture(t)
	_, err := invoke(t, "", "--profile", "deal", "deal", "rules-verdict", "policy", "--share")
	require.Error(t, err, "no holder kind is declared")
	assert.Equal(t, false, dealRun(t, "rules-verdict", "policy")["shares"])
	dealRun(t, "standard-history", "declare", "--holder-kind", "person")
	assert.Equal(t, false, dealRun(t, "rules-verdict", "policy")["shares"], "a person does not")
	assert.Equal(t, true, dealRun(t, "rules-verdict", "policy", "--share")["shares"])
	dealRun(t, "standard-history", "declare", "--holder-kind", "person")
	assert.Equal(t, true, dealRun(t, "rules-verdict", "policy")["shares"], "declaring again keeps the choice")
	_, err = invoke(t, "", "--profile", "deal", "deal", "rules-verdict", "policy", "--share", "--no-share")
	require.Error(t, err)
}

// Acceptance 2 (coordinator decision 2026-11-13): the pack is named by its digest alone, always.
// A name shaped like a published pack's could be a private pack's, so no name of any pack
// reaches the cut or the record, whatever the pin's kind; the digest does.
func TestThePackIsNamedByItsDigestAloneAndNeverByAName(t *testing.T) {
	type pack struct{ rulesetID, kind string }
	for name, p := range map[string]pack{
		"a published-looking deal pack":     {publishedPackName, "deal"},
		"a private pack named like one":     {"acme/my-budget-rules/1.0.0", "conduct"},
		"a published-looking pack, no kind": {publishedPackName, ""},
		"a deal pack with a private name":   {"my-private-pack", "deal"},
	} {
		id := closedUnderPack(t, p.rulesetID, p.kind)
		dealRun(t, "standard-history", "declare", "--holder-kind", "node")
		sealVerdict(t, id)
		cut := counterpartyCut(t, id)
		assert.Contains(t, cut, packDigest, name+": the digest is always shown")
		assert.NotContains(t, cut, p.rulesetID, name+": no pack name in the cut")
		body := bodyOf(verdictRecords(t, id)[0])
		assert.NotContains(t, body, "pack_id", name)
		assert.Equal(t, map[string]any{"word": "out_of_scope", "pack_digest": packDigest}, body, name)
	}
}

// `deal respond` answers with the same counterparty copy, so the same policy decides.
func TestDealRespondCarriesTheVerdictUnderTheSamePolicy(t *testing.T) {
	for name, c := range map[string]struct {
		kind, flag string
		carried    bool
	}{
		"a node's default":      {"node", "", true},
		"a node that opts out":  {"node", "--no-share", false},
		"a person's default":    {"person", "", false},
		"a person that opts in": {"person", "--share", true},
	} {
		dealFixture(t)
		id := dealRun(t, "open", "--input", writeJSON(t, crossPartyBuyerOpen))["deal_id"].(string)
		dealRun(t, "close", "--deal", id, "--input", writeJSON(t, notSelected))
		asker := thisDealParty(t)
		dealFixture(t)
		pinChecker(t, map[string]any{"kind": "deal", "definition_digest": packDigest, "command": []string{stubChecker(t, "pack",
			`printf '%s' '{"schema":"external-check-result/v0","ruleset_id":"`+publishedPackName+`","definition_digest":"`+packDigest+`","verdict":"allow","tier":"recomputed","findings":[{"id":"x","check":"x","verdict":"pass"}]}'`)}})
		dealRun(t, "open", "--records", "typed", "--deal-id", id, "--input", writeJSON(t, sellerTyped))
		dealRun(t, "check", "--deal", id, "--input", writeJSON(t, `{"action":"offer","amount_minor":190000,"terms":{"item":"example bicycle","quantity":1,"price_minor":190000}}`))
		dealRun(t, "close", "--deal", id, "--input", writeJSON(t, notSelected))
		dealRun(t, "standard-history", "declare", "--holder-kind", c.kind)
		if c.flag != "" {
			dealRun(t, "rules-verdict", "policy", c.flag)
		}
		sealVerdict(t, id)
		responder := thisDealParty(t)
		ask := askFor(t, asker, responder, id, map[string]any{"min_freshness": map[string]any{"size": 1}})
		require.Equal(t, "artifact", ask.outcome, name)
		body := string(mustRead(t, ask.responsePath))
		assert.Equal(t, c.carried, strings.Contains(body, "out_of_scope"), name)
		assert.NotContains(t, body, publishedPackName, name+": the pack's name never rides with it")
	}
}

// Steven's condition 4: the verdict is never carried onto a History card, own copy or shared.
func TestTheVerdictIsNeverCarriedOntoAHistoryCard(t *testing.T) {
	id := closedUnderPack(t, publishedPackName, "deal")
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	sealVerdict(t, id)
	clockAt(t, time.Date(2026, 9, 27, 18, 30, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	dir := t.TempDir()
	for name, args := range map[string][]string{"own.json": nil, "shared.json": {"--shared"}} {
		out := filepath.Join(dir, name)
		dealRun(t, append([]string{"standard-history", "card", "--out", out}, args...)...)
		card := string(mustRead(t, out))
		for _, leak := range []string{"out_of_scope", "rules_verdict", publishedPackName, packDigest} {
			assert.NotContains(t, card, leak, name)
		}
	}
}
