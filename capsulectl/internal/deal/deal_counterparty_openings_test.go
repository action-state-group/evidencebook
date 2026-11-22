package deal

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The user's own copy opens who the deal was with and what the buyer wrote
// (evidencebook-deal#56). The page recomputes each against the sealed
// fingerprint or content_commitment; a shared copy carries none of it, nor
// the deal key.

const buyerWords = "Deal, that works for me"

// acceptedSale seals a seller's deal whose buyer accepted the offer, and
// returns its id.
func acceptedSale(t *testing.T) string {
	t.Helper()
	dealFixture(t)
	id := openTyped(t, sellerTyped)
	check := makeOffer(t, id, "190000")
	_, err := acceptOffer(t, id, check)
	require.NoError(t, err)
	return id
}

func ownReport(t *testing.T, id string) map[string]interface{} {
	t.Helper()
	path := filepath.Join(t.TempDir(), "own.json")
	dealRun(t, "report", "--deal", id, "--bundle", path)
	return bundleFile(t, path)
}

func sealedReport(t *testing.T, b map[string]interface{}) map[string]interface{} {
	t.Helper()
	r, _, _ := cli.DealSealedReport(b)
	require.NotNil(t, r)
	return r
}

func TestTheOwnCopyOpensTheCounterpartyAndTheBuyersWords(t *testing.T) {
	id := acceptedSale(t)
	b := ownReport(t, id)
	r := sealedReport(t, b)

	cp, _ := r["counterparty_openings"].(map[string]interface{})
	require.NotNil(t, cp)
	assert.NotEmpty(t, cp["key"])
	var names, domains int
	for _, e := range cp["steps"].([]interface{}) {
		entry := e.(map[string]interface{})
		if entry["name"] != nil {
			names++
			assert.Equal(t, "Example Buyer", entry["name"])
		}
		if entry["domain"] != nil {
			domains++
			assert.Equal(t, "buyer.example", entry["domain"])
		}
	}
	assert.Positive(t, names)
	assert.Positive(t, domains)

	acc := r["acceptance_openings"].([]interface{})
	require.Len(t, acc, 1)
	assert.Equal(t, buyerWords, acc[0].(map[string]interface{})["text"])

	out, err := dealPageOpenings(b)
	require.NoError(t, err)
	require.NotEmpty(t, out["counterparty"])
	for i, kinds := range out["counterparty"].([]interface{}) {
		for kind, ok := range kinds.(map[string]interface{}) {
			assert.Equal(t, true, ok, "%d %s", i, kind)
		}
	}
	require.Len(t, out["acceptances"], 1)
	assert.Equal(t, true, out["acceptances"].([]interface{})[0])
}

// An opening that does not recompute is reported not verified: other words, a
// name or domain that was not sealed, another key or nonce, or a step that
// is not the acceptance. The page then shows none of the value.
func TestAnOpeningThatDoesNotMatchIsNotVerified(t *testing.T) {
	id := acceptedSale(t)
	path := filepath.Join(t.TempDir(), "own.json")
	dealRun(t, "report", "--deal", id, "--bundle", path)

	for name, tamper := range map[string]func(r map[string]interface{}){
		"other name": func(r map[string]interface{}) {
			r["counterparty_openings"].(map[string]interface{})["steps"].([]interface{})[0].(map[string]interface{})["name"] = "Someone Else"
		},
		"other domain": func(r map[string]interface{}) {
			e := r["counterparty_openings"].(map[string]interface{})["steps"].([]interface{})[0].(map[string]interface{})
			if e["domain"] != nil {
				e["domain"] = "else.example"
			} else {
				e["name"] = "Someone Else"
			}
		},
		"other key": func(r map[string]interface{}) {
			r["counterparty_openings"].(map[string]interface{})["key"] = strings.Repeat("0", 64)
		},
		"no key": func(r map[string]interface{}) { delete(r["counterparty_openings"].(map[string]interface{}), "key") },
		"other words": func(r map[string]interface{}) {
			r["acceptance_openings"].([]interface{})[0].(map[string]interface{})["text"] = "Not what they said"
		},
		"other nonce": func(r map[string]interface{}) {
			r["acceptance_openings"].([]interface{})[0].(map[string]interface{})["nonce"] = strings.Repeat("0", 64)
		},
		"empty step": func(r map[string]interface{}) {
			r["counterparty_openings"].(map[string]interface{})["steps"].([]interface{})[0].(map[string]interface{})["step"] = ""
		},
		"no such step": func(r map[string]interface{}) {
			r["acceptance_openings"].([]interface{})[0].(map[string]interface{})["step"] = "nope"
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := bundleFile(t, path)
			r := sealedReport(t, b)
			tamper(r)
			out, err := dealPageOpenings(b)
			require.NoError(t, err, "the page is written; the value is not shown")
			failed := false
			for _, kinds := range out["counterparty"].([]interface{}) {
				for _, ok := range kinds.(map[string]interface{}) {
					failed = failed || ok == false
				}
			}
			for _, ok := range out["acceptances"].([]interface{}) {
				failed = failed || ok == false
			}
			assert.True(t, failed, "something does not verify")
		})
	}
}

// A shared copy carries neither the openings nor the deal key (which would let
// a holder test a guessed name against counterparty.ids.name) nor the
// acceptance's nonce, and a shared copy that did carry them refuses the page.
func TestSharedCopiesCarryNoCounterpartyOpenings(t *testing.T) {
	id := acceptedSale(t)
	dir := t.TempDir()
	own := filepath.Join(dir, "own.json")
	dealRun(t, "report", "--deal", id, "--bundle", own)
	ownReport := sealedReport(t, bundleFile(t, own))
	key := ownReport["counterparty_openings"].(map[string]interface{})["key"].(string)
	nonce := ownReport["acceptance_openings"].([]interface{})[0].(map[string]interface{})["nonce"].(string)
	require.NotEmpty(t, key)
	require.NotEmpty(t, nonce)

	for _, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
		html := filepath.Join(dir, audience+".html")
		dealRun(t, "report", "--deal", id, "--share", audience, "--to", "them", "--html", html)
		raw := string(mustRead(t, html))
		assert.NotContains(t, raw, key, audience)
		assert.NotContains(t, raw, nonce, audience)
		r := sealedReport(t, embeddedBundle(t, raw))
		assert.NotContains(t, r, "counterparty_openings")
		assert.NotContains(t, r, "acceptance_openings")

		for field, value := range map[string]interface{}{
			"counterparty_openings": map[string]interface{}{"key": key, "steps": []interface{}{}},
			"acceptance_openings":   []interface{}{},
		} {
			b := embeddedBundle(t, raw)
			sealedReport(t, b)[field] = value
			_, err := dealPageOpenings(b)
			require.Error(t, err, "%s on a %s copy", field, audience)
			assert.NotContains(t, err.Error(), key)
		}
	}
}

// A deal sealed with no payee, name, domain, phone, email or acceptance to open reports, and
// renders, as it did.
func TestADealWithNothingToOpenCarriesNoOpenings(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, `{"type":"purchase","channel":"web",
		"intent":{"verbatim":"buy me an otter sticker under 8 bucks","max_total_minor":800,"allowed":["pay"]},
		"who":{"relay_address":"seller@relay.stickers.example"},
		"terms":{"item":"otter sticker","price_minor":600,"currency":"USD"},
		"recourse":{"rail":"card","refundable":true}}`))["deal_id"].(string)
	b := ownReport(t, id)
	r := sealedReport(t, b)
	assert.NotContains(t, r, "counterparty_openings")
	assert.NotContains(t, r, "acceptance_openings")
	out, err := dealPageOpenings(b)
	require.NoError(t, err)
	assert.NotContains(t, out, "counterparty")
	assert.NotContains(t, out, "acceptances")
}

// A step opened twice verifies only its first entry, so a wrong value put
// before or after the right one is never shown beside it as checked.
func TestARepeatedStepVerifiesOnlyItsFirstOpening(t *testing.T) {
	id := acceptedSale(t)
	b := ownReport(t, id)
	r := sealedReport(t, b)
	steps := r["counterparty_openings"].(map[string]interface{})["steps"].([]interface{})
	first := steps[0].(map[string]interface{})
	wrong := map[string]interface{}{"step": first["step"], "name": "Someone Else"}
	r["counterparty_openings"].(map[string]interface{})["steps"] = append(steps, wrong)
	acc := r["acceptance_openings"].([]interface{})
	again := map[string]interface{}{}
	for k, v := range acc[0].(map[string]interface{}) {
		again[k] = v
	}
	r["acceptance_openings"] = append(acc, again)

	out, err := dealPageOpenings(b)
	require.NoError(t, err)
	checked := out["counterparty"].([]interface{})
	require.Len(t, checked, len(steps)+1)
	assert.Equal(t, false, checked[len(steps)].(map[string]interface{})["name"])
	assert.Equal(t, []interface{}{true, false}, out["acceptances"])
}

// The receipt names the counterparty as the OK card does (the payee, else the
// name, else the site, phone or email, with the site in brackets), field by field from the last entry that names
// it, and shows only what recomputed: a value that did not is never shown, and nor are a
// repeated step's later entries or an acceptance that did not verify.
func TestTheReceiptShowsOnlyWhatRecomputed(t *testing.T) {
	steps := func(entries ...map[string]any) map[string]any {
		list := make([]any, len(entries))
		for i, e := range entries {
			list[i] = e
		}
		return map[string]any{"counterparty_openings": map[string]any{"key": "00", "steps": list}}
	}
	withAcceptances := func(r map[string]any, texts ...string) map[string]any {
		var list []any
		for _, text := range texts {
			list = append(list, map[string]any{"step": "s", "nonce": "n", "text": text})
		}
		r["acceptance_openings"] = list
		return r
	}
	got := runDealViewFunctions(t, map[string]any{
		"close": []any{}, "band": []any{}, "afterYes": []any{}, "settlement": []any{},
		"openings": []map[string]any{
			// payee, else name; the earlier leg is not named.
			{"report": steps(map[string]any{"step": "a", "name": "A", "domain": "a.example"}, map[string]any{"step": "b", "payee": "B Pay", "name": "B", "domain": "b.example"}),
				"checked": map[string]any{"counterparty": []any{map[string]any{"name": true, "domain": true}, map[string]any{"payee": true, "name": true, "domain": true}}}},
			// a value that did not recompute is not shown and says so.
			{"report": steps(map[string]any{"step": "a", "name": "Evil"}, map[string]any{"step": "a", "name": "Real"}),
				"checked": map[string]any{"counterparty": []any{map[string]any{"name": false}, map[string]any{"name": true}}}},
			// no result at all for an opening: not verified.
			{"report": steps(map[string]any{"step": "", "name": "Evil"}), "checked": map[string]any{}},
			// the buyer's words: shown when they recomputed only.
			{"report": withAcceptances(map[string]any{}, "yes", "no"), "checked": map[string]any{"acceptances": []any{true, false}}},
			// the last step's values did not recompute: nobody is named, not the step before.
			{"report": steps(map[string]any{"step": "a", "name": "Alice"}, map[string]any{"step": "b", "name": "Bob"}),
				"checked": map[string]any{"counterparty": []any{map[string]any{"name": true}, map[string]any{"name": false}}}},
			// a payment names only the payee: the opening's site stays.
			{"report": steps(map[string]any{"step": "a", "name": "Air", "domain": "air.example"}, map[string]any{"step": "b", "payee": "Air Pay"}),
				"checked": map[string]any{"counterparty": []any{map[string]any{"name": true, "domain": true}, map[string]any{"payee": true}}}},
			// a deal with nothing to open.
			{"report": map[string]any{}, "checked": map[string]any{}},
			// known only by an address: the phone, else the email, as the card names them.
			{"report": steps(map[string]any{"step": "a", "phone": "+1 415 555 0100"}), "checked": map[string]any{"counterparty": []any{map[string]any{"phone": true}}}},
			{"report": steps(map[string]any{"step": "a", "email": "a@b.example"}), "checked": map[string]any{"counterparty": []any{map[string]any{"email": true}}}},
			// an address that did not recompute is not shown and says so.
			{"report": steps(map[string]any{"step": "a", "email": "evil@b.example"}), "checked": map[string]any{"counterparty": []any{map[string]any{"email": false}}}},
		},
	})
	lines := got["openings"].([]any)
	line := func(i int) map[string]any { return lines[i].(map[string]any) }
	assert.Equal(t, "B Pay (b.example)", line(0)["who"])
	assert.Equal(t, false, line(0)["whoBad"])
	assert.Equal(t, "Real", line(1)["who"])
	assert.Equal(t, true, line(1)["whoBad"])
	assert.Equal(t, "", line(2)["who"])
	assert.Equal(t, true, line(2)["whoBad"])
	assert.Equal(t, []any{map[string]any{"step": "s", "text": "yes"}, map[string]any{}}, line(3)["accepted"])
	assert.Equal(t, "", line(4)["who"])
	assert.Equal(t, true, line(4)["whoBad"])
	assert.Equal(t, "Air Pay (air.example)", line(5)["who"])
	assert.Equal(t, "", line(6)["who"])
	assert.Equal(t, false, line(6)["whoBad"])
	assert.Empty(t, line(6)["accepted"])
	assert.Equal(t, "+1 415 555 0100", line(7)["who"])
	assert.Equal(t, "a@b.example", line(8)["who"])
	assert.Equal(t, "", line(9)["who"])
	assert.Equal(t, true, line(9)["whoBad"])
}

// The counterparty is who the deal ends with, as the OK card names them: a
// change of site and payee after the opening replaces those two, the name the
// opening gave stays, and every value is opened at a step whose sealed
// fingerprint it recomputes to.
func TestTheCounterpartyOpenedIsTheOneTheDealEndsWith(t *testing.T) {
	dealFixture(t)
	id := openTypedFlight(t)
	b := ownReport(t, id)
	got := map[string]string{}
	for _, e := range sealedReport(t, b)["counterparty_openings"].(map[string]interface{})["steps"].([]interface{}) {
		for kind, v := range e.(map[string]interface{}) {
			if kind != "step" {
				got[kind] = v.(string)
			}
		}
	}
	assert.Equal(t, map[string]string{"name": "Example Air", "domain": "travel-super-discount.example", "payee": "Travel Super Discount"}, got)
	out, err := dealPageOpenings(b)
	require.NoError(t, err)
	for _, kinds := range out["counterparty"].([]interface{}) {
		for kind, ok := range kinds.(map[string]interface{}) {
			assert.Equal(t, true, ok, kind)
		}
	}
}

// A payment to a payee the check names (not the opening's) is named on the
// receipt as the OK card named it: the payee beside the opening's site.
func TestThePayeeTheUserApprovedIsNamedOnTheReceipt(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, fawnOpen))["deal_id"].(string)
	pay := dealRun(t, "check", "--deal", id, "--input", writeJSON(t, `{"action":"pay","amount_minor":190,"authorized_max_minor":190,"who":{"payee":"sticker-payouts-2"},"terms":{"item":"Fawn Sticker","price_minor":175},"recourse":{"rail":"card","refundable":true}}`))
	_, err := answer(t, id, pay["check_id"].(string), "yes, the new payee is fine", shownCard(t, pay["card"].(string)))
	require.NoError(t, err)
	dealRun(t, "note", "--deal", id, "--kind", "act", "--input", writeJSON(t, `{"action":"pay","amount_minor":190,"payee":"sticker-payouts-2","rail":"card"}`))

	b := ownReport(t, id)
	got := map[string]string{}
	for _, e := range sealedReport(t, b)["counterparty_openings"].(map[string]interface{})["steps"].([]interface{}) {
		for kind, v := range e.(map[string]interface{}) {
			if kind != "step" {
				got[kind] = v.(string)
			}
		}
	}
	assert.Equal(t, "sticker-payouts-2", got["payee"])
	assert.NotEmpty(t, got["name"])
	out, err := dealPageOpenings(b)
	require.NoError(t, err)
	for _, kinds := range out["counterparty"].([]interface{}) {
		for kind, ok := range kinds.(map[string]interface{}) {
			assert.Equal(t, true, ok, kind)
		}
	}
}

// An act before a switch to another counterparty went to the earlier one: the
// receipt names the one the deal switched to, not the earlier payee beside
// the new site.
func TestAPayeeBeforeASwitchIsNotNamedBesideTheNewCounterparty(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, fawnOpen))["deal_id"].(string)
	pay := dealRun(t, "check", "--deal", id, "--input", writeJSON(t, `{"action":"pay","amount_minor":190,"authorized_max_minor":190,"who":{"payee":"sticker-payouts-2"},"terms":{"item":"Fawn Sticker","price_minor":175},"recourse":{"rail":"card","refundable":true}}`))
	_, err := answer(t, id, pay["check_id"].(string), "yes, the new payee is fine", shownCard(t, pay["card"].(string)))
	require.NoError(t, err)
	dealRun(t, "note", "--deal", id, "--kind", "act", "--input", writeJSON(t, `{"action":"pay","amount_minor":190,"payee":"sticker-payouts-2","rail":"card"}`))
	dealRun(t, "note", "--deal", id, "--kind", "intent", "--input", writeJSON(t, `{"verbatim":"lets change to ebay","who":{"name":"eBay","domain":"ebay.com"}}`))

	got := map[string]string{}
	r := sealedReport(t, ownReport(t, id))
	if cp, ok := r["counterparty_openings"].(map[string]interface{}); ok {
		for _, e := range cp["steps"].([]interface{}) {
			for kind, v := range e.(map[string]interface{}) {
				if kind != "step" {
					got[kind] = v.(string)
				}
			}
		}
	}
	assert.Equal(t, map[string]string{"name": "eBay", "domain": "ebay.com"}, got)
}

// A counterparty known only by phone or email is opened as the OK card names
// them (the phone, else the email), and only when the card has no payee, name
// or site to name them by; the other address stays unopened (evidencebook-deal#162).
func TestACounterpartyKnownOnlyByAddressIsOpenedAsTheCardNamesThem(t *testing.T) {
	for name, tc := range map[string]struct {
		who  string
		want map[string]string
	}{
		"phone only":      {`{"phone":"+1 415 555 0100"}`, map[string]string{"phone": "+1 415 555 0100"}},
		"email only":      {`{"email":"seller@buyer.example"}`, map[string]string{"email": "seller@buyer.example"}},
		"phone and email": {`{"phone":"+1 415 555 0100","email":"seller@buyer.example"}`, map[string]string{"phone": "+1 415 555 0100"}},
		"a name wins":     {`{"name":"Fawn Seller","email":"seller@buyer.example"}`, map[string]string{"name": "Fawn Seller"}},
		"a site wins":     {`{"domain":"buyer.example","phone":"+1 415 555 0100"}`, map[string]string{"domain": "buyer.example"}},
	} {
		t.Run(name, func(t *testing.T) {
			dealFixture(t)
			open := strings.Replace(fawnOpen, `"who":{"name":"sticker_shop_example","domain":"marketplace.example"}`, `"who":`+tc.who, 1)
			require.NotEqual(t, fawnOpen, open)
			id := dealRun(t, "open", "--input", writeJSON(t, open))["deal_id"].(string)
			b := ownReport(t, id)
			got := map[string]string{}
			if cp, ok := sealedReport(t, b)["counterparty_openings"].(map[string]interface{}); ok {
				for _, e := range cp["steps"].([]interface{}) {
					for kind, v := range e.(map[string]interface{}) {
						if kind != "step" {
							got[kind] = v.(string)
						}
					}
				}
			}
			assert.Equal(t, tc.want, got)
			out, err := dealPageOpenings(b)
			require.NoError(t, err)
			require.NotEmpty(t, out["counterparty"])
			for _, kinds := range out["counterparty"].([]interface{}) {
				for kind, ok := range kinds.(map[string]interface{}) {
					assert.Equal(t, true, ok, kind)
				}
			}
		})
	}
}

// An email opened under another value, or a phone not sealed, is not verified.
func TestAnAddressOpeningThatDoesNotMatchIsNotVerified(t *testing.T) {
	dealFixture(t)
	open := strings.Replace(fawnOpen, `"who":{"name":"sticker_shop_example","domain":"marketplace.example"}`, `"who":{"email":"seller@buyer.example"}`, 1)
	id := dealRun(t, "open", "--input", writeJSON(t, open))["deal_id"].(string)
	b := ownReport(t, id)
	r := sealedReport(t, b)
	e := r["counterparty_openings"].(map[string]interface{})["steps"].([]interface{})[0].(map[string]interface{})
	e["email"] = "other@buyer.example"
	e["phone"] = "+1 415 555 0100"
	out, err := dealPageOpenings(b)
	require.NoError(t, err)
	kinds := out["counterparty"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, false, kinds["email"])
	assert.Equal(t, false, kinds["phone"])
}

// The address is chosen as recipientName chooses it: a blank field is skipped.
func TestCardAddressKindSkipsBlankFields(t *testing.T) {
	assert.Equal(t, "email", cardAddressKind(dealWho{Phone: " ", Email: "a@b.example"}))
	assert.Equal(t, "phone", cardAddressKind(dealWho{Phone: "+1 415 555 0100", Email: "a@b.example"}))
	assert.Equal(t, "", cardAddressKind(dealWho{Domain: " ", Phone: " "}))
	assert.Equal(t, "email", cardAddressKind(dealWho{Domain: "  ", Email: "a@b.example"}))
	assert.Equal(t, "", cardAddressKind(dealWho{Name: "N", Email: "a@b.example"}))
}

// The card takes no phone or email from a check: a check naming another phone
// than the one the deal opened with leaves the opened email the card names, and
// the check's phone is not opened.
func TestACheckDoesNotChangeTheAddressTheCardNames(t *testing.T) {
	dealFixture(t)
	open := strings.Replace(fawnOpen, `"who":{"name":"sticker_shop_example","domain":"marketplace.example"}`, `"who":{"email":"seller@buyer.example"}`, 1)
	id := dealRun(t, "open", "--input", writeJSON(t, open))["deal_id"].(string)
	pay := dealRun(t, "check", "--deal", id, "--input", writeJSON(t, `{"action":"pay","amount_minor":190,"authorized_max_minor":190,"who":{"phone":"+1 415 555 0199"},"terms":{"item":"Fawn Sticker","price_minor":175},"recourse":{"rail":"card","refundable":true}}`))
	require.NotEmpty(t, pay["check_id"])
	dealRun(t, "note", "--deal", id, "--kind", "act", "--input", writeJSON(t, `{"action":"pay","amount_minor":190,"rail":"card"}`))

	got := map[string]string{}
	for _, e := range sealedReport(t, ownReport(t, id))["counterparty_openings"].(map[string]interface{})["steps"].([]interface{}) {
		for kind, v := range e.(map[string]interface{}) {
			if kind != "step" {
				got[kind] = v.(string)
			}
		}
	}
	assert.Equal(t, map[string]string{"email": "seller@buyer.example"}, got)
}
