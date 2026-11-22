package deal

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openCeilingDeal opens a purchase whose opening record holds nothing else
// a shared copy withholds (an HMAC'd domain, a price and a currency), with a
// spending limit of $50.00 above the $48.00 price, or none.
func openCeilingDeal(t *testing.T, limit bool) string {
	t.Helper()
	intent := `{"verbatim": "buy it", "max_total_minor": 5000}`
	if !limit {
		intent = `{"verbatim": "buy it"}`
	}
	dealID := dealRun(t, "open", "--input", writeJSON(t, `{
		"type": "purchase", "channel": "web",
		"intent": `+intent+`,
		"who": {"domain": "shop.example"},
		"terms": {"price_minor": 4800, "currency": "USD"},
		"recourse": {"rail": "card", "refundable": true}
	}`))["deal_id"].(string)
	dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, `{"action":"pay","amount_minor":4800,"authorized_max_minor":4800}`))
	return dealID
}

// The counterparty never learns the user's spending limit; the adjudicator
// may (it is how "over your limit" is judged).
func TestDealSpendingLimitIsPrivateFromTheCounterparty(t *testing.T) {
	dealFixture(t)
	// Without a limit, the counterparty's copy discloses the opening record.
	twin := openCeilingDeal(t, false)
	b, _ := sharedCopy(t, twin, cli.DealAudienceCounterparty, "x")
	ext := dealReportOf(b)
	require.Equal(t, false, ext["steps"].([]any)[0].(map[string]any)["withheld"])

	dealID := openCeilingDeal(t, true)
	b, raw := sharedCopy(t, dealID, cli.DealAudienceCounterparty, "x")
	assert.NotContains(t, raw, "max_total_minor")
	assert.NotContains(t, raw, "$50.00")
	assert.NotContains(t, raw, "50.00")
	ext = dealReportOf(b)
	assert.Equal(t, true, ext["steps"].([]any)[0].(map[string]any)["withheld"], "the opening record carries the limit: withheld")
	assert.Contains(t, ext["withheld"], "your spending limit")

	_, raw = sharedCopy(t, dealID, cli.DealAudienceAdjudicator, "x")
	assert.Contains(t, raw, `"max_total_minor":5000`, "the adjudicator's copy carries the limit")

	// The counterparty's page too.
	html := filepath.Join(t.TempDir(), "c.html")
	dealRun(t, "report", "--deal", dealID, "--html", html, "--share", "counterparty", "--to", "the shop")
	page, err := os.ReadFile(html)
	require.NoError(t, err)
	assert.NotContains(t, string(page), "max_total_minor")
	assert.NotContains(t, string(page), "$50.00")
}

// The gate on its own, as if withholding had missed the limit: a counterparty
// copy naming it, as the field or as money, is refused; the adjudicator's is
// not; and the limit is not refused where it equals the price the seller
// asked.
func TestDealCeilingGate(t *testing.T) {
	events := []sealedEvent{{Event: dealEvent{Kind: "open", Open: &dealOpen{
		Intent: dealIntent{MaxTotalMinor: ptrInt64(5000)},
		Terms:  dealTerms{PriceMinor: ptrInt64(4800), Currency: "USD"},
	}}}}
	for _, page := range []string{`{"intent":{"max_total_minor":5000}}`, "approved $50.00 (your limit)", "approved 50.00 USD"} {
		assert.Error(t, dealCeilingGate([]byte(page), events, cli.DealAudienceCounterparty), page)
		assert.NoError(t, dealCeilingGate([]byte(page), events, cli.DealAudienceAdjudicator), page)
	}
	for _, page := range []string{"pay $48.00 by card", "charged $150.00", "$500.00", "at 05:00:00 on 2026-10-03"} {
		assert.NoError(t, dealCeilingGate([]byte(page), events, cli.DealAudienceCounterparty), page)
	}
	events[0].Event.Open.Terms.PriceMinor = ptrInt64(5000)
	assert.NoError(t, dealCeilingGate([]byte("pay $50.00 by card"), events, cli.DealAudienceCounterparty), "the limit equals the asked price: the seller knows that amount")
	assert.True(t, strings.HasPrefix(dealBasisYourLimit, "your"))
}

func ptrInt64(v int64) *int64 { return &v }

// openShortMerchantDeal is the shortest merchant deal: open, a passing check
// (snapshot, check, approval) and the merchant's own email: five steps.
func openShortMerchantDeal(t *testing.T) string {
	t.Helper()
	dealID := dealRun(t, "open", "--input", writeJSON(t, `{"type":"purchase","channel":"web",
		"intent":{"verbatim":"Buy the sticker pack, no more than $50 all in","max_total_minor":5000,"allowed":["pay"]},
		"who":{"name":"Shop Example","domain":"shop.example"},
		"terms":{"item":"sticker pack","quantity":1,"price_minor":4800,"currency":"USD"},
		"recourse":{"rail":"card","refundable":true}}`))["deal_id"].(string)
	dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, `{"action":"pay","amount_minor":4800,"authorized_max_minor":4800}`))
	dealRun(t, "note", "--deal", dealID, "--kind", "evidence", "--email", filepath.Join(merchantFixture, "confirmation.eml"))
	return dealID
}

// A shared copy carries the merchant's email as its digests and the
// verification result, never the message: no header, no body, no name. The
// raw .eml stays in the local deal store (`deal verify-email` re-checks it
// there); not even the user's own bundle carries it.
func TestDealSharedMerchantEmailIsDigestAndVerdict(t *testing.T) {
	inBothReportModes(t, dealSharedMerchantEmailIsDigestAndVerdict)
}

func dealSharedMerchantEmailIsDigestAndVerdict(t *testing.T, inLog bool) {
	dealFixture(t)
	stubDNS(t, map[string]string{merchantSelector + "._domainkey.shop.example": merchantKeyTXT(t), dmarcName: merchantDMARC(t)})
	dealID := openShortMerchantDeal(t)
	for i, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
		b, raw := sharedCopy(t, dealID, audience, "x")
		if inLog {
			require.Len(t, b["records"], 6+i+1, "the 6 steps (the check's counterparty_profile among them, withheld) and every copy's sealed report so far")
		} else {
			require.Len(t, b["records"], 6, "the 6 steps (the check's counterparty_profile among them, withheld); no copy's report is on the log")
		}
		assert.Contains(t, raw, `"message_digest":"`)
		assert.Contains(t, raw, `"key_records_digest":"`)
		assert.Contains(t, raw, `"dkim":"pass"`)
		for _, v := range []string{"DKIM-Signature", "Received:", "Subject:", "From:", "Content-Type", "Sam Customer", "sam.customer@mail.example", "orders@shop.example"} {
			assert.NotContains(t, raw, v, audience)
		}
	}

	own, err := invoke(t, "", "--profile", "deal", "bundle", "--deal", dealID)
	require.NoError(t, err)
	assert.NotContains(t, own, "DKIM-Signature", "the raw email is in no bundle")
}

// The merchant's charged total is a structured amount, shared even when it
// equals the user's limit: a buyer whose receipt shows a charge at the limit,
// above what the agent paid, can still hand the seller a copy carrying it
// (capsule-cli#278).
func TestAChargeAtTheLimitIsShared(t *testing.T) {
	dealFixture(t)
	stubDNS(t, map[string]string{merchantSelector + "._domainkey.shop.example": merchantKeyTXT(t), dmarcName: merchantDMARC(t)})
	dealID := dealRun(t, "open", "--input", writeJSON(t, `{"type":"purchase","channel":"web",
		"intent":{"verbatim":"Buy the sticker pack","max_total_minor":6400,"allowed":["pay"]},
		"who":{"name":"Shop Example","domain":"shop.example"},
		"terms":{"item":"sticker pack","quantity":1,"price_minor":4800,"currency":"USD"},
		"recourse":{"rail":"card","refundable":true}}`))["deal_id"].(string)
	dealRun(t, "check", "--deal", dealID, "--input", writeJSON(t, `{"action":"pay","amount_minor":4800,"authorized_max_minor":4800}`))
	dealRun(t, "note", "--deal", dealID, "--kind", "evidence", "--email", filepath.Join(merchantFixture, "confirmation.eml"))
	_, raw := sharedCopy(t, dealID, cli.DealAudienceCounterparty, "x")
	assert.Contains(t, raw, `"charged":"$64.00"`, "the merchant's total at the limit travels")
	assert.NotContains(t, raw, "max_total_minor")
}

// A merchant's charge in another currency is not an amount the copy shows in
// the deal's: its minor units equal to the limit exempt nothing.
func TestAChargeInAnotherCurrencyExemptsNothing(t *testing.T) {
	limit, total := int64(6400), int64(6400)
	open := sealedEvent{Event: dealEvent{Kind: "open", Open: &dealOpen{Intent: dealIntent{MaxTotalMinor: &limit}, Terms: dealTerms{Currency: "USD"}}}}
	charge := func(currency string) sealedEvent {
		return sealedEvent{Event: dealEvent{Kind: "evidence", Evidence: &dealEvidence{Email: &merchantEmail{Parsed: merchantEmailParsed{TotalMinor: &total, Currency: currency}}}}}
	}
	assert.True(t, dealShownAmounts([]sealedEvent{open, charge("USD")})[6400])
	assert.True(t, dealShownAmounts([]sealedEvent{open, charge("")})[6400], "an email that names no currency is read in the deal's")
	assert.False(t, dealShownAmounts([]sealedEvent{open, charge("JPY")})[6400])
	assert.ErrorContains(t, dealCeilingGate([]byte("my max is $64.00"), []sealedEvent{open, charge("JPY")}, cli.DealAudienceCounterparty), "your spending limit")
}

func ceilingEvents(limit int64) []sealedEvent {
	return []sealedEvent{{Event: dealEvent{Kind: "open", Open: &dealOpen{
		Intent: dealIntent{PartyRole: dealRoleBuyer, MaxTotalMinor: &limit},
		Terms:  dealTerms{Currency: "USD"}}}}}
}

// A random id in base64url (the COSE checkpoint, a witness) is not prose:
// with a limit of 8.00, "8krsil…" read as 8 kr and "8gbp-…" as 8 GBP, and a
// counterparty copy was refused at random (issue #26). Each token below is
// a fixed one of that shape.
func TestARandomIdIsNotTheLimit(t *testing.T) {
	events := ceilingEvents(800)
	for _, tok := range []string{
		"8krSiljccsfh-Gc4lpkxi8m4j8wl", "8krSiljccsfhGc4lpkxi8w", "8krSiljccsfhGcAlpkxiAw", "8kR2HyiCRXnmH-sExilKR7VFhmKmkFs-A8P1Z2q_z_R", "8gbp-Qlhh6xzsywqlv3kuyf8b1xtzkQ",
		"8usd_K3j2h1xxxxxxxxxxxxxxxxxx", "8eur-Ab3xxxxxxxxxxxxxxxxxxxxxxx", "8chf-Xz9xxxxxxxxxxxxxxxxxxxxxxxx",
	} {
		assert.NoError(t, dealCeilingGate([]byte(`{"cose":"`+tok+`"}`), events, cli.DealAudienceCounterparty), tok)
	}
}

// The same ids, drawn at random: none states the limit. A fixed seed, so a
// failure names its token; each starts with the limit's digit, the shape
// that was refused.
func TestRandomBase64urlIdsNeverStateTheLimit(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	events := ceilingEvents(800)
	r := rand.New(rand.NewSource(26))
	for range 20000 {
		tok := make([]byte, 43)
		for i := range tok {
			tok[i] = alphabet[r.Intn(len(alphabet))]
		}
		tok[0] = '8'
		require.NoError(t, dealCeilingGate([]byte(`{"cose":"`+string(tok)+`"}`), events, cli.DealAudienceCounterparty), string(tok))
	}
}

// What the gate caught stays caught: the limit written as money, in or
// beside a long run, and the bound's name, even inside a long key.
func TestTheLimitInOrBesideALongRunIsStillRefused(t *testing.T) {
	events := ceilingEvents(800)
	id := "Zq3-Tr9xYb_Lm4NpWk7VcHd2Fg5Js8Aa"
	for _, page := range []string{
		"my limit is $8.00", "8kr", "limit 8usd-ish", "my-limit-is-$8", "limit 8.00 " + id, id + " limit 8.00",
		"Lowest-I-Will-Take-Is-Exactly-8-Dollars-Thanks-ok limit is 8 usd",
		// Context beside a long run stays: its words, its number, its code.
		"Max_Budget_Limit_FY2026Q4: 8", "My-Personal-Spending-Limit-2026 is 8", "My-Absolute-Spending-Limit-Is-8.50 and 8.00",
		"8 usd_Settlement_Ref_A1b2c3d4", "8USD-Per-Month",
		`{"note":"8krSiljccsfh-Gc4lpkxi8m4j8wl and my limit is 8"}`, `{"note":"my limit is 8 USD"}`, `{"note":"8USD-Per-Month-Plan-2026-Ok limit"}`,
		`{"8krSiljccsfh-Gc4lpkxi8m4j8wl":"limit 8 usd"}`,
		// A quoted slug in prose, or in an array, is not an id value.
		`My limit is "8USD-Per-Month-Plan-2026".`, `{"note":"My limit is \"8USD-Per-Month-Plan-2026\"."}`, `["8USD-Per-Month-Plan-2026"]`, `{"max_total_minor":800}`, `{"x_max_total_minor_` + id + `":1}`,
	} {
		assert.Error(t, dealCeilingGate([]byte(page), events, cli.DealAudienceCounterparty), page)
	}
	assert.Error(t, dealCeilingGate([]byte("$8.50USD-Per-Month-Plan-2026"), ceilingEvents(850), cli.DealAudienceCounterparty), "an amount is not cut at a decimal point")
	floor := int64(170000)
	fe := []sealedEvent{{Event: dealEvent{Kind: "open", Open: &dealOpen{
		Intent: dealIntent{PartyRole: dealRoleSeller, MinTotalMinor: &floor}, Terms: dealTerms{Currency: "USD"}}}}}
	assert.Error(t, dealCeilingGate([]byte(`{"x_min_total_minor_`+id+`":1}`), fe, cli.DealAudienceCounterparty), "a long key does not hide the floor's name")
}
