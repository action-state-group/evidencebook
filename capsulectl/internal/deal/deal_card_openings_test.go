package deal

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cardOpeningsOf is the card_openings of the report a bundle file seals.
func cardOpeningsOf(t *testing.T, b map[string]interface{}) []interface{} {
	t.Helper()
	r, _, _ := cli.DealSealedReport(b)
	require.NotNil(t, r)
	cards, _ := r["card_openings"].([]interface{})
	return cards
}

func bundleFile(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	b, err := cli.DecodeBundleJSON(mustRead(t, path))
	require.NoError(t, err)
	return b
}

// answeredOnACard seals a deal whose user answered a check on its card: an
// x-deal-v0 deal or a typed one. It returns the deal, the card, the answer's
// step and the check's.
var answeredOnACard = []struct {
	name, field string
	deal        func(t *testing.T) (id, card, answered, check string)
}{
	{"x-deal-v0", "card_commitment", func(t *testing.T) (string, string, string, string) {
		id := openSticker(t, true)
		ck := checkAct(t, id, "pay", 3000)
		a, err := answer(t, id, ck["check_id"].(string), "yes, 30 is fine", shownCard(t, ck["card"].(string)))
		require.NoError(t, err)
		return id, ck["card"].(string), a["capsule_id"].(string), ck["check_id"].(string)
	}},
	{"typed", "rendering_commitment", func(t *testing.T) (string, string, string, string) {
		id := openTypedFlight(t)
		ck := dealRun(t, "check", "--deal", id, "--input", writeJSON(t, merchantCheck))
		a, err := answerCheck(t, id, ck["check_id"].(string), "yes, the new site is fine", ck["card"].(string))
		require.NoError(t, err)
		return id, ck["card"].(string), a["capsule_id"].(string), ck["check_id"].(string)
	}},
}

// The user's own copy opens the card each answer was given on: the answer's
// step, and the card under its check's card nonce, which recomputes to the
// answer's sealed commitment (card_commitment in an x-deal-v0 approval,
// rendering_commitment in a typed one). An answer given on no card has no
// opening, and no shared copy carries any.
func TestTheOwnCopyOpensTheCardsTheUserAnsweredOn(t *testing.T) {
	for _, c := range answeredOnACard {
		t.Run(c.name, func(t *testing.T) {
			dealFixture(t)
			id, card, answered, _ := c.deal(t)
			dir := t.TempDir()
			own := filepath.Join(dir, "own.json")
			dealRun(t, "report", "--deal", id, "--bundle", own)
			b := bundleFile(t, own)
			cards := cardOpeningsOf(t, b)
			require.Len(t, cards, 1)
			opening := cards[0].(map[string]interface{})
			assert.Equal(t, answered, opening["step"])
			assert.Equal(t, card, opening["text"])
			sealed := b["disclosures"].(map[string]interface{})[answered].(map[string]interface{})["agent_input"].(map[string]interface{})
			got, err := cli.CommitText(opening["nonce"].(string), opening["text"].(string))
			require.NoError(t, err)
			assert.Equal(t, sealed["body"].(map[string]interface{})[c.field], got)
			_, err = dealPageOpenings(b)
			require.NoError(t, err, "the page is written")

			for _, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
				shared := filepath.Join(dir, audience+".html")
				dealRun(t, "report", "--deal", id, "--share", audience, "--to", "them", "--html", shared)
				r, _, _ := cli.DealSealedReport(embeddedBundle(t, string(mustRead(t, shared))))
				require.NotNil(t, r)
				assert.NotContains(t, r, "card_openings", "a card can hold the counterparty's raw values: your copy only")
			}
		})
	}

	dealFixture(t)
	id := openSticker(t, true)
	ck := checkAct(t, id, "pay", 3000)
	_, err := answer(t, id, ck["check_id"].(string), "yes", "")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "own.json")
	dealRun(t, "report", "--deal", id, "--bundle", path)
	assert.Empty(t, cardOpeningsOf(t, bundleFile(t, path)), "an answer given on no card opens none")
}

// A card opening the page could show as seen but that is not the answer's
// own sealed card refuses the page: other words, another nonce or none, an
// opening that names the check rather than the answer (the same commitment,
// but not the card as answered on), a repeated step, a field that is not a
// list, or one riding on a shared copy, even empty. The refusal never echoes
// the card, even one put where the step goes.
func TestADealPageWhoseCardsDoNotMatchIsNotWritten(t *testing.T) {
	const otherWords = "Pay $3 to someone else"
	for _, c := range answeredOnACard {
		t.Run(c.name, func(t *testing.T) {
			dealFixture(t)
			id, card, _, check := c.deal(t)
			path := filepath.Join(t.TempDir(), "own.json")
			dealRun(t, "report", "--deal", id, "--bundle", path)
			report := func(b map[string]interface{}) map[string]interface{} {
				r, _, _ := cli.DealSealedReport(b)
				require.NotNil(t, r)
				return r
			}
			first := func(b map[string]interface{}) map[string]interface{} {
				return report(b)["card_openings"].([]interface{})[0].(map[string]interface{})
			}
			for name, tamper := range map[string]func(b map[string]interface{}){
				"other words":  func(b map[string]interface{}) { first(b)["text"] = otherWords },
				"other nonce":  func(b map[string]interface{}) { first(b)["nonce"] = strings.Repeat("0", 64) },
				"no nonce":     func(b map[string]interface{}) { delete(first(b), "nonce") },
				"the check":    func(b map[string]interface{}) { first(b)["step"] = check },
				"no such step": func(b map[string]interface{}) { first(b)["step"] = "nope" },
				"the card as its step": func(b map[string]interface{}) {
					first(b)["step"] = first(b)["text"]
				},
				"repeated": func(b map[string]interface{}) {
					r := report(b)
					r["card_openings"] = append(r["card_openings"].([]interface{}), first(b))
				},
				"not an opening": func(b map[string]interface{}) {
					report(b)["card_openings"] = []interface{}{"x"}
				},
				"not a list": func(b map[string]interface{}) {
					report(b)["card_openings"] = first(b)
				},
				"a shared copy": func(b map[string]interface{}) { report(b)["audience"] = cli.DealAudienceCounterparty },
				"not a list, on a shared copy": func(b map[string]interface{}) {
					r := report(b)
					r["card_openings"] = first(b)
					r["audience"] = cli.DealAudienceAdjudicator
				},
				"empty, on a shared copy": func(b map[string]interface{}) {
					r := report(b)
					r["card_openings"] = []interface{}{}
					r["audience"] = cli.DealAudienceCounterparty
				},
			} {
				t.Run(name, func(t *testing.T) {
					b := bundleFile(t, path)
					tamper(b)
					_, err := dealPageOpenings(b)
					require.Error(t, err)
					assert.Contains(t, err.Error(), "refusing to write the page")
					assert.NotContains(t, err.Error(), card, "the refusal never echoes the card")
					assert.NotContains(t, err.Error(), otherWords)
				})
			}
		})
	}
}
