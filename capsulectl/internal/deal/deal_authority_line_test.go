package deal

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lineOpen = `{"type":"purchase","channel":"web",
	"intent":{"verbatim":"buy me an otter sticker for at most 5 dollars","max_total_minor":500,"allowed":["pay"]%s},
	"who":{"name":"Sticker Marketplace","domain":"stickers.example"},
	"terms":{"item":"otter sticker","price_minor":454,"currency":"USD"},
	"recourse":{"rail":"card","refundable":true}}`

const lineOpenNoSeller = `{"type":"purchase","channel":"web",
	"intent":{"verbatim":"buy me an otter sticker for at most 5 dollars","max_total_minor":500,"allowed":["pay"]%s},
	"terms":{"item":"otter sticker","price_minor":454,"currency":"USD"},
	"recourse":{"rail":"card","refundable":true}}`

func lineInput(t *testing.T, tmpl, extra string) string {
	t.Helper()
	return writeJSON(t, fmt.Sprintf(tmpl, extra))
}

func TestTheAuthorityLineIsShownBeforeAnythingIsSealed(t *testing.T) {
	dealFixture(t)
	out := dealRun(t, "open", "--records", "typed", "--authority-line", "--input", lineInput(t, lineOpen, ""))
	assert.Equal(t, "I'll record that you authorize: pay up to $5 for otter sticker. "+
		"You chose Sticker Marketplace; the rules check the amount, not the seller. Is that right? (yes/no)", out["authority_line"])
	assert.NotContains(t, out, "deal_id", "showing the line seals nothing")

	out = dealRun(t, "open", "--records", "typed", "--authority-line", "--input", lineInput(t, lineOpenNoSeller, ""))
	assert.Equal(t, "I'll record that you authorize: pay up to $5 for otter sticker. "+
		"You haven't picked a seller, so this doesn't limit who gets paid. Is that right? (yes/no)", out["authority_line"])
}

func TestTheAuthorityLineStatesNoMaximumWhenNoneWasGiven(t *testing.T) {
	dealFixture(t)
	in := writeJSON(t, `{"type":"purchase","intent":{"verbatim":"buy the otter sticker","allowed":["pay"]},
		"terms":{"item":"otter sticker","currency":"USD"},"recourse":{"rail":"card","refundable":true}}`)
	out := dealRun(t, "open", "--records", "typed", "--authority-line", "--input", in)
	assert.Equal(t, "I'll record that you authorize: pay, with no amount limit, for otter sticker. "+
		"You haven't picked a seller, so this doesn't limit who gets paid. Is that right? (yes/no)", out["authority_line"])
}

func TestABuyersPayDealOpensOnlyOnTheUsersYes(t *testing.T) {
	dealFixture(t)
	keepOpenUnconfirmed = true
	t.Cleanup(func() { keepOpenUnconfirmed = false })
	out, err := invoke(t, "", "--profile", "deal", "deal", "open", "--records", "typed", "--input", lineInput(t, lineOpen, ""))
	require.Error(t, err)
	assert.Contains(t, out+err.Error(), "intent.confirmed")

	// Not a buyer's typed pay deal: a yes has nothing to answer.
	for name, input := range map[string]string{
		"no pay":         `{"type":"purchase","intent":{"verbatim":"show me options","allowed":[],"confirmed":"yes"},"terms":{"item":"x","currency":"USD"},"recourse":{"rail":"card","refundable":true}}`,
		"x-deal-v0 open": fmt.Sprintf(lineOpen, `,"confirmed":"yes"`),
	} {
		args := []string{"--profile", "deal", "deal", "open", "--input", writeJSON(t, input)}
		if name == "no pay" {
			args = append(args, "--records", "typed")
		}
		out, err := invoke(t, "", args...)
		require.Error(t, err, name)
		assert.Contains(t, out+err.Error(), "intent.confirmed", name)
	}
}

func TestTheTaskAuthorityRecordsWhatTheUserConfirmed(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--records", "typed", "--input",
		lineInput(t, lineOpen, `,"confirmed":"yes, that is right"`))["deal_id"].(string)
	records := chainRecords(t, id)
	var ta map[string]any
	for _, r := range records {
		if typeOf(r) == dealprofile.TypeTaskAuthority {
			ta = bodyOf(r)
		}
	}
	require.NotNil(t, ta)
	assert.Equal(t, []any{"pay"}, ta["allowed"])
	assert.EqualValues(t, 500, ta["max_total_minor"])
	asked := ta["asked"].(map[string]any)
	assert.Equal(t, "otter sticker", asked["item"])
	seller := asked["seller"].(map[string]any)
	assert.Equal(t, dealprofile.TypedFPAlg, seller["fp_alg"])
	raw, err := json.Marshal(ta)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "Sticker Marketplace", "the seller is a keyed fingerprint, never the name")
	assert.Regexp(t, `^[0-9a-f]{64}$`, asked["said_commitment"])
	assert.Regexp(t, `^[0-9a-f]{64}$`, asked["rendered_commitment"])
	assert.NotEqual(t, ta["verbatim_commitment"], asked["said_commitment"], "the yes is committed under its own nonce")
}

func TestTheTaskAuthorityLeavesTheSellerUnconstrainedWhenNoneWasNamed(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--records", "typed", "--input",
		lineInput(t, lineOpenNoSeller, `,"confirmed":"yes"`))["deal_id"].(string)
	for _, r := range chainRecords(t, id) {
		if typeOf(r) == dealprofile.TypeTaskAuthority {
			assert.Equal(t, unconstrainedSeller, bodyOf(r)["asked"].(map[string]any)["seller"])
			return
		}
	}
	t.Fatal("no task authority sealed")
}

func TestTheLineTheUserWasShownIsTheLineOfTheTermsSealed(t *testing.T) {
	dealFixture(t)
	keepOpenUnconfirmed = true
	t.Cleanup(func() { keepOpenUnconfirmed = false })
	shown := dealRun(t, "open", "--records", "typed", "--authority-line", "--input", lineInput(t, lineOpen, ""))["authority_line"].(string)
	// The user confirmed $5; an open at another limit is not what they answered.
	other := fmt.Sprintf(lineOpen, fmt.Sprintf(`,"confirmed":"yes","rendered":%q`, shown))
	other = strings.Replace(other, `"max_total_minor":500`, `"max_total_minor":50000`, 1)
	out, err := invokeRaw(t, "", "--profile", "deal", "deal", "open", "--records", "typed", "--input", writeJSON(t, other))
	require.Error(t, err)
	assert.Contains(t, out+err.Error(), "intent.rendered")
	// The same line opens.
	dealRun(t, "open", "--records", "typed", "--input", writeJSON(t, fmt.Sprintf(lineOpen, fmt.Sprintf(`,"confirmed":"yes","rendered":%q`, shown))))
}

func TestTheLineSaysWhenNoItemIsNamedYet(t *testing.T) {
	dealFixture(t)
	in := writeJSON(t, `{"type":"purchase","intent":{"verbatim":"buy something","max_total_minor":500,"allowed":["pay"]},
		"terms":{"currency":"USD"},"recourse":{"rail":"card","refundable":true}}`)
	out := dealRun(t, "open", "--records", "typed", "--authority-line", "--input", in)
	assert.Contains(t, out["authority_line"], "pay up to $5 for an item you have not named yet.")
}

func TestASaleTakesNoYesOfItsOwn(t *testing.T) {
	dealFixture(t)
	in := writeJSON(t, `{"type":"purchase","intent":{"verbatim":"sell my sticker","allowed":["offer","commit"],"confirmed":"yes"},
		"terms":{"item":"otter sticker","price_minor":454,"currency":"USD"},"recourse":{"rail":"card","refundable":true}}`)
	out, err := invokeRaw(t, "", "--profile", "deal", "deal", "sale", "new", "--input", in)
	require.Error(t, err)
	assert.Contains(t, out+err.Error(), "intent.confirmed")
}
