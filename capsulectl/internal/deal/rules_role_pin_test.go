package deal

import (
	"encoding/json"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rules checker is held to the acting role's pin alone (capsule-cli#323):
// swapping one role's pin by a profile update changes only that role's check.
func TestARolePinChangesOnlyThatRolesCheck(t *testing.T) {
	buyer := stickerDeal(t, "card", false)
	seller := openTyped(t, sellerTyped)
	checker := stubChecker(t, "rules", checkerPrints("allow", passFinding))
	sellerCheck := `{"action":"offer","amount_minor":190000,"terms":{"item":"example bicycle","quantity":1,"price_minor":190000}}`
	pin := func(sellerDigest string) {
		pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": exampleRulesDigest,
			"roles": map[string]any{"buyer": map[string]any{"pack": "asg/everyday/buyer", "definition_digest": exampleRulesDigest},
				"seller": map[string]any{"pack": "asg/everyday/seller", "definition_digest": sellerDigest}}})
	}
	changed := "the ruleset changed since it was pinned"

	// The seller's pin names another ruleset than the checker reports: only
	// the seller's check stops.
	pin(secondRulesDigest)
	assert.NotContains(t, differenceTexts(dealRun(t, "check", "--deal", buyer, "--input", writeJSON(t, payCheck("card", 454, 454)))), changed, "the buyer's pin is untouched")
	got := dealRun(t, "check", "--deal", seller, "--input", writeJSON(t, sellerCheck))
	assert.Equal(t, "pause", got["verdict"])
	assert.Contains(t, differenceTexts(got), changed)

	// Swapping the seller's pin back, and nothing else, lets its check run.
	pin(exampleRulesDigest)
	assert.NotContains(t, differenceTexts(dealRun(t, "check", "--deal", seller, "--input", writeJSON(t, sellerCheck))), changed)
}

// A role with no entry, and a role entry that names a pack alone, are held to
// the flat digests, as a profile without roles always was.
func TestARoleWithoutADigestIsHeldToTheFlatPin(t *testing.T) {
	id := stickerDeal(t, "card", false)
	checker := stubChecker(t, "rules", checkerPrints("allow", passFinding))
	check := func() map[string]any {
		return dealRun(t, "check", "--deal", id, "--input", writeJSON(t, payCheck("card", 454, 454)))
	}
	pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": secondRulesDigest})
	assert.Contains(t, differenceTexts(check()), "the ruleset changed since it was pinned")
	pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": secondRulesDigest,
		"roles": map[string]any{"buyer": map[string]any{"pack": "asg/everyday/buyer"}}})
	assert.Contains(t, differenceTexts(check()), "the ruleset changed since it was pinned", "naming a pack never loosens the digest check")
	pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": secondRulesDigest,
		"roles": map[string]any{"buyer": map[string]any{"pack": "asg/everyday/buyer", "definition_digest": exampleRulesDigest}}})
	assert.NotContains(t, differenceTexts(check()), "the ruleset changed since it was pinned")
}

// The checker is sent the acting role's pin alone as rules_pin, only on a
// profile that pins roles (evidencebook#230); the input fits the schema.
func TestTheCheckerIsSentTheActingRolesPin(t *testing.T) {
	dealFixture(t)
	buyer := stickerDeal(t, "card", false)
	seller := openTyped(t, sellerTyped)
	checker := stubChecker(t, "rules", checkerPrints("allow", passFinding))
	sellerCheck := `{"action":"offer","amount_minor":190000,"terms":{"item":"example bicycle","quantity":1,"price_minor":190000}}`
	givenPin := func() map[string]any {
		raw := rawCheckerInput(t, checker)
		inputFitsSchema(t, raw)
		var input map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &input))
		pin, _ := input["rules_pin"].(map[string]any)
		return pin
	}

	// No roles: what was sent before, with no rules_pin.
	pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": exampleRulesDigest})
	dealRun(t, "check", "--deal", buyer, "--input", writeJSON(t, payCheck("card", 454, 454)))
	assert.Nil(t, givenPin())

	pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": exampleRulesDigest,
		"roles": map[string]any{
			"buyer":  map[string]any{"pack": "asg/everyday/buyer", "definition_digest": exampleRulesDigest},
			"seller": map[string]any{"pack": "asg/everyday/seller", "definition_digests": []string{exampleRulesDigest, secondRulesDigest}}}})
	dealRun(t, "check", "--deal", buyer, "--input", writeJSON(t, payCheck("card", 454, 454)))
	assert.Equal(t, map[string]any{"pack": "asg/everyday/buyer", "definition_digests": []any{exampleRulesDigest}}, givenPin(), "the buyer's pin alone")
	dealRun(t, "check", "--deal", seller, "--input", writeJSON(t, sellerCheck))
	assert.Equal(t, map[string]any{"pack": "asg/everyday/seller", "definition_digests": []any{exampleRulesDigest, secondRulesDigest}}, givenPin(), "the seller's pin alone")

	// A pack alone keeps the flat digests; a role with no entry sends the
	// flat pin with no pack.
	pinChecker(t, map[string]any{"command": []string{checker}, "definition_digest": exampleRulesDigest,
		"roles": map[string]any{"buyer": map[string]any{"pack": "asg/everyday/buyer"}}})
	dealRun(t, "check", "--deal", buyer, "--input", writeJSON(t, payCheck("card", 454, 454)))
	assert.Equal(t, map[string]any{"pack": "asg/everyday/buyer", "definition_digests": []any{exampleRulesDigest}}, givenPin())
	dealRun(t, "check", "--deal", seller, "--input", writeJSON(t, sellerCheck))
	assert.Equal(t, map[string]any{"definition_digests": []any{exampleRulesDigest}}, givenPin())
}

// A pin that names nothing is absent, never empty.
func TestARolePinThatNamesNothingIsNotSent(t *testing.T) {
	assert.Nil(t, rulesPin(cli.ProfileRulesChecker{Roles: map[string]cli.ProfileRulesRole{"buyer": {}}}, "seller"))
	assert.Nil(t, rulesPin(cli.ProfileRulesChecker{}, "buyer"))
	assert.Equal(t, map[string]interface{}{"definition_digests": []string{"a"}},
		rulesPin(cli.ProfileRulesChecker{DefinitionDigest: "a", Roles: map[string]cli.ProfileRulesRole{"buyer": {Pack: ""}}}, "seller"))
}
