package deal

import (
	"encoding/json"
	"math/rand"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The limit gate masks a random id by the copy's JSON structure, not by a
// pattern (issue #62, docs/design/limit-gate-structural-id-mask.md): only the
// string value of a "cose" member found in a real JSON region. Each row is
// the design note's vector table: the limit in minor units (USD, buyer), the
// copy, and whether the counterparty's copy is refused.
func TestTheGateMasksOnlyACoseValueInRealJSON(t *testing.T) {
	const id = "Zq3-Tr9xYb_Lm4NpWk7VcHd2Fg5Js8Aa"
	page := func(markup, bundle string) string {
		return "<!doctype html><html><title>" + markup + "</title><script>window.__BUNDLE__ = " + bundle + ";</script></html>"
	}
	rows := []struct {
		limit   int64
		copy    string
		refused bool
		why     string
	}{
		{800, `{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"}`, false, ""},
		{800, `{"cose":"8krSiljccsfhGc4lpkxi8w"}`, false, ""},
		{800, `{"cose":"8krSiljccsfhGcAlpkxiAw"}`, false, ""},
		{800, `{"cose":"8kR2HyiCRXnmH-sExilKR7VFhmKmkFs-A8P1Z2q_z_R"}`, false, ""},
		{800, `{"cose":"8gbp-Qlhh6xzsywqlv3kuyf8b1xtzkQ"}`, false, ""},
		{800, `{"cose":"8usd_K3j2h1xxxxxxxxxxxxxxxxxx"}`, false, ""},
		{800, `{"cose":"8eur-Ab3xxxxxxxxxxxxxxxxxxxxxxx"}`, false, ""},
		{800, `{"cose":"8chf-Xz9xxxxxxxxxxxxxxxxxxxxxxxx"}`, false, ""},
		{800, page("Deal report", `{"checkpoint":{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"}}`), false, ""},
		{800, `{"checkpoint":{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"},"b":"x"}`, false, ""},
		{800, `{"a":{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"},"b":"x"}`, true, "a cose outside a checkpoint is not masked"},
		{800, `{"cose" : "8krSiljccsfh-Gc4lpkxi8m4j8wl"}`, false, "whitespace before the colon: no producer writes it"},
		{800, `{"k":"8A3Cad8E9f0a1b2c3d4"}`, false, "mixed-case hex is blanked first"},
		{800, `{"cose":"8A3Cad8E9f0a1b2c3d4"}`, false, ""},

		{800, "my limit is $8.00", true, ""},
		{800, "8kr", true, ""},
		{800, "limit 8usd-ish", true, ""},
		{800, "my-limit-is-$8", true, ""},
		{800, "limit 8.00 " + id, true, ""},
		{800, id + " limit 8.00", true, ""},
		{800, "Lowest-I-Will-Take-Is-Exactly-8-Dollars-Thanks-ok limit is 8 usd", true, ""},
		{800, "Max_Budget_Limit_FY2026Q4: 8", true, ""},
		{800, "My-Personal-Spending-Limit-2026 is 8", true, ""},
		{800, "My-Absolute-Spending-Limit-Is-8.50 and 8.00", true, ""},
		{800, "8 usd_Settlement_Ref_A1b2c3d4", true, ""},
		{800, "8USD-Per-Month", true, ""},
		{800, `{"note":"8krSiljccsfh-Gc4lpkxi8m4j8wl and my limit is 8"}`, true, ""},
		{800, `{"note":"my limit is 8 USD"}`, true, ""},
		{800, `{"note":"8USD-Per-Month-Plan-2026-Ok limit"}`, true, ""},
		{800, `{"8krSiljccsfh-Gc4lpkxi8m4j8wl":"limit 8 usd"}`, true, "an id as a key"},
		{800, `My limit is "8USD-Per-Month-Plan-2026".`, true, ""},
		{800, `{"note":"My limit is \"8USD-Per-Month-Plan-2026\"."}`, true, ""},
		{800, `["8USD-Per-Month-Plan-2026"]`, true, ""},
		{800, `{"max_total_minor":800}`, true, ""},
		{800, `{"x_max_total_minor_` + id + `":1}`, true, ""},
		{850, "$8.50USD-Per-Month-Plan-2026", true, "an amount is not cut at a decimal point"},
		{80000, "USD700-USD800", true, ""},
		{80000, "800USD-900USD", true, ""},
		{80000, "800usd-ish", true, ""},
		{800000, "$8k-per-month", true, ""},
		{800000, `{"pay":"8k-4Qwxyz"}`, true, ""},
		{800000, "USD7k-USD8k", true, ""},
		{800000, "USD8k-per-month", true, ""},
		{800000, "my limit is 8k-per-month", true, ""},
		{80000, "my limit is 800USD- final", true, ""},
		{800, "$8kr", true, ""},
		{800, "8 kr", true, ""},
		{80000, "Max_Budget_Limit_FY2026Q4: 800", true, ""},
		{80000, "800 USD_Settlement_Ref_A1b2c3", true, ""},
		{800, `{"note":"My \"limit\": \"8USD-Per-Month-Plan-2026\"." , }`, true, "invalid JSON is read whole"},
		{800, `{"note":"8USD-Per-Month-aaaaaaaaaaaaaaaa"}`, true, "the hex tail is blanked first, so the value is no id"},
		{800, `{"cose":"8USD-Per-Month-aaaaaaaaaaaaaaaa"}`, true, "the same, in a cose value"},

		// #62: prose that imitates JSON, and a whole field value shaped like an id.
		{800, `My "limit": "8USD-Per-Month-Plan-2026".`, true, "the #62 repro"},
		{800, page(`My &#34;limit&#34;: &#34;8USD-Per-Month-Plan-2026&#34;.`, `{"a":1}`), true, ""},
		{800, page(`My "limit": "8USD-Per-Month-Plan-2026".`, `{"a":1}`), true, ""},
		{800, `{"note":"My \\u0022limit\\u0022: \\u00228USD-Per-Month-Plan-2026\\u0022."}`, true, ""},
		{800, `{"note":"My &#34;limit&#34;: &#34;8USD-Per-Month-Plan-2026&#34;."}`, true, ""},
		{800, `{"note":"My &quot;limit&quot;: &quot;8USD-Per-Month-Plan-2026&quot;."}`, true, ""},
		{800, `{"note":"My %22limit%22: %228USD-Per-Month-Plan-2026%22."}`, true, ""},
		{800, page("Deal report", `{"note":"My &#34;limit&#34;: &#34;8USD-Per-Month-Plan-2026&#34;."}`), true, ""},
		{800, page("Deal report", `{"note":"My %22limit%22: %228USD-Per-Month-Plan-2026%22."}`), true, ""},
		{800, `{"note":"8USD-Per-Month-Plan-2026"}`, true, "a whole field value that is not cose"},
		{800, page("Deal report", `{"note":"8USD-Per-Month-Plan-2026"}`), true, ""},
		{800, `{"text":"8USD-Per-Month-Plan-ok"}`, true, "a seller's claim"},
		{800, `{"\"cose":"8USD-Per-Month-Plan-2026"}`, true, "a key that is not cose"},
		{800, `{"prefix\"cose":"8USD-Per-Month-Plan-ok"}`, true, "a key that is not cose"},
		{800, `{"cose":{"note":"8USD-Per-Month-Plan-2026"}}`, true, "a cose object is not masked"},
		{800, `{"cose":["8USD-Per-Month-Plan-2026"]}`, true, ""},
		{800, `{"terms":{"conditions":{"cose":"8USD-Per-Month-Plan-ok"}}}`, true, "terms.conditions takes any lowercase name: a cose key outside a checkpoint is text"},
		{800, `{"checkpoint":{"x":{"cose":"8USD-Per-Month-Plan-ok"}}}`, true, "the checkpoint is the cose object's parent, not an ancestor"},

		// Declared residuals: false positives, asserted so a change is deliberate.
		{800, `{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"} trailing prose`, true, "not valid JSON: read as money"},
		{800, `{"cose":"\u0038krSiljccsfh-Gc4lpkxi8m4j8wl"}`, true, "an escaped id is read as money"},
	}
	for _, r := range rows {
		err := dealCeilingGate([]byte(r.copy), ceilingEvents(r.limit), cli.DealAudienceCounterparty)
		if r.refused {
			assert.Error(t, err, "%s (%s)", r.copy, r.why)
		} else {
			assert.NoError(t, err, "%s (%s)", r.copy, r.why)
		}
	}
	floor := int64(170000)
	fe := []sealedEvent{{Event: dealEvent{Kind: "open", Open: &dealOpen{
		Intent: dealIntent{PartyRole: dealRoleSeller, MinTotalMinor: &floor}, Terms: dealTerms{Currency: "USD"}}}}}
	assert.Error(t, dealCeilingGate([]byte(`{"x_min_total_minor_`+id+`":1}`), fe, cli.DealAudienceCounterparty), "a long key does not hide the floor's name")
}

// A seller's agent claim that is shaped like an id, sealed before the user
// sets a floor of 8.00, is opened in the counterparty copy as a whole text
// value; the copy must be refused, as the control claim's is.
func TestASellersIDShapedClaimDoesNotHideTheFloor(t *testing.T) {
	for claim, refused := range map[string]bool{"8USD-Per-Month-Plan-ok": true, "lowest is 8 USD": true, "see you at nine": false} {
		dealFixture(t)
		id := openTyped(t, `{"type": "purchase", "demo": true, "channel": "web",
 "intent": {"verbatim": "Sell my example bicycle", "party_role": "seller", "asked": {"item": "example bicycle", "quantity": 1}, "allowed": ["commit"]},
 "who": {"name": "Example Buyer", "domain": "buyer.example"},
 "terms": {"item": "example bicycle", "quantity": 1, "price_minor": 190000, "currency": "USD"},
 "recourse": {"rail": "card", "refundable": false}}`)
		noteClaim(t, id, `{"text":"`+claim+`","source_kind":"agent","source":"listing_message","class":"condition"}`)
		dealRun(t, "note", "--deal", id, "--kind", "intent", "--input", writeJSON(t, `{"verbatim": "Not under 8", "min_total_minor": 800, "allowed": ["commit"]}`))
		out, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", cli.DealAudienceCounterparty, "--to", "x", "--out", filepath.Join(t.TempDir(), "shared.json"))
		if refused {
			assert.ErrorContains(t, err, "the lowest price you will take", "%q: %s", claim, out)
		} else {
			assert.NoError(t, err, "%q: %s", claim, out)
		}
	}
}

// Real copies, with checkpoint.cose replaced by a random 400-character
// base64url value starting with the limit's digit (half "8kr"): none is
// refused. A fixed seed, so a failure names its token.
func TestARandomCoseInARealCopyIsNeverTheLimit(t *testing.T) {
	dealFixture(t)
	stubDNS(t, map[string]string{merchantSelector + "._domainkey.shop.example": merchantKeyTXT(t), dmarcName: merchantDMARC(t)})
	id := openShortMerchantDeal(t)
	_, bundle := sharedCopy(t, id, cli.DealAudienceCounterparty, "x")
	dir := t.TempDir()
	dealRun(t, "report", "--deal", id, "--share", cli.DealAudienceCounterparty, "--to", "x", "--html", filepath.Join(dir, "page.html"))
	pageCopy := string(mustRead(t, filepath.Join(dir, "page.html")))

	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	cose := regexp.MustCompile(`"cose":"[0-9A-Za-z_-]+"`)
	require.True(t, cose.MatchString(bundle), "the bundle carries a checkpoint")
	require.True(t, cose.MatchString(pageCopy), "the page's bundle carries one")
	events := ceilingEvents(800)
	for name, c := range map[string]struct {
		raw    string
		rounds int
	}{"bundle": {bundle, 2000}, "page": {pageCopy, 200}} {
		r := rand.New(rand.NewSource(62))
		for range c.rounds {
			tok := make([]byte, 400)
			for i := range tok {
				tok[i] = alphabet[r.Intn(len(alphabet))]
			}
			tok[0] = '8'
			if r.Intn(2) == 0 {
				tok[1], tok[2] = 'k', 'r'
			}
			in := cose.ReplaceAllString(c.raw, `"cose":"`+string(tok)+`"`)
			require.NoError(t, dealCeilingGate([]byte(in), events, cli.DealAudienceCounterparty), "%s %s", name, tok)
		}
	}
}

// No copy kind carries an id-shaped string under a key other than "cose": a
// base64url field the mask does not know brings back #26's random refusals
// for it. A hit is a signal to investigate; a field joins the mask only when
// its producer shows it holds machine-encoded bytes (see maskCoseIDs).
// Covered: the counterparty and adjudicator bundles and the counterparty
// page of a short merchant deal. Not built here: the history copy, the terms
// Result bundle, the evidence-request copy and a cadence witness's chain
// (whose checkpoint is a "cose" under "checkpoint" as well).
func TestNoOtherFieldHoldsAnID(t *testing.T) {
	dealFixture(t)
	stubDNS(t, map[string]string{merchantSelector + "._domainkey.shop.example": merchantKeyTXT(t), dmarcName: merchantDMARC(t)})
	id := openShortMerchantDeal(t)
	var walk func(path string, v any, found *[]string)
	walk = func(path string, v any, found *[]string) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				walk(k, e, found)
			}
		case []any:
			for _, e := range x {
				walk(path, e, found)
			}
		case string:
			if path != "cose" && isWholeID(nil, x) {
				*found = append(*found, path+"="+x)
			}
		}
	}
	for _, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
		b, _ := sharedCopy(t, id, audience, "x")
		var found []string
		walk("", b, &found)
		assert.Empty(t, found, audience)
	}
	// The counterparty page: every data script's JSON value.
	pagePath := filepath.Join(t.TempDir(), "page.html")
	dealRun(t, "report", "--deal", id, "--share", cli.DealAudienceCounterparty, "--to", "x", "--html", pagePath)
	var regions int
	dealGateText(mustRead(t, pagePath), func(raw []byte) []byte {
		var v any
		require.NoError(t, json.Unmarshal(raw, &v))
		var found []string
		walk("", v, &found)
		assert.Empty(t, found, "page")
		regions++
		return raw
	})
	assert.NotZero(t, regions, "the page has data scripts")
}
