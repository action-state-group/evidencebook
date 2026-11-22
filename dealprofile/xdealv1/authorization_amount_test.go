// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// amountFlow is one authorized act on a check of action, amount and
// currency (nil and "" for none): a payer settle on a pay check, a refund
// (a settle that reverses a payment) of leg on a cancel check.
func amountFlow(t *testing.T, action string, amount *int64, currency, leg string) (*authFlow, Record) {
	t.Helper()
	f := &authFlow{t: t}
	c := f.seal(TypeCheck, true, &CheckBody{CheckBody: dealprofile.CheckBody{Action: action, AmountMinor: amount, Currency: currency}, PaymentRef: "invoice-17", Nonce: label("amount check nonce")})
	f.approve(f.verdict(c, "pass"), "user", true)
	b := inBook(TypeSettle, 2)
	b.At = f.at()
	body := &SettleBody{Leg: leg, Statements: []StatementItem{}, Check: &c}
	if action == "cancel" {
		body.Reverses = &Ref{Digest: label("the reversed payment")}
	}
	return f, record(b, body)
}

func checkLeg(t *testing.T, f *authFlow, act Record, member string) error {
	t.Helper()
	_, err := CheckActs(f.journal, []ActIn{{Record: act, Settlement: json.RawMessage(member)}})
	return err
}

// x-deal-v0 rule 5, kept by §8: the settled amount and currency equal the
// checked ones. CheckActs reads the act's settlement member, which the record
// does not carry.
func TestCheckActsHoldsTheSettledAmountToTheCheckedOne(t *testing.T) {
	amount := int64(4500)
	f, act := amountFlow(t, "pay", &amount, "USD", LegPayer)

	for name, member := range map[string]string{
		"the checked amount":     `{"amount":{"value":"4500","assetCode":"USD","assetScale":2}}`,
		"the same at scale 3":    `{"amount":{"value":"45000","assetCode":"USD","assetScale":3}}`,
		"the same at scale 0":    `{"amount":{"value":"45","assetCode":"USD","assetScale":0}}`,
		"a member with no field": `{"status":"pending"}`,
	} {
		assert.NoError(t, checkLeg(t, f, act, member), name)
	}
	_, err := CheckActs(f.journal, []ActIn{{Record: act}})
	require.NoError(t, err, "no settlement member: read as CheckAuthorizations reads it")

	for name, c := range map[string]struct{ member, want string }{
		"more":              {`{"amount":{"value":"9900","assetCode":"USD","assetScale":2}}`, "settled amount differs from the checked 4500 USD"},
		"more at scale 3":   {`{"amount":{"value":"45001","assetCode":"USD","assetScale":3}}`, "settled amount differs"},
		"another code":      {`{"amount":{"value":"4500","assetCode":"EUR","assetScale":2}}`, "settled currency EUR differs from the checked USD"},
		"not a number":      {`{"amount":{"value":"4.5","assetCode":"USD","assetScale":2}}`, "not a whole number"},
		"not json":          {`{`, "settlement member"},
		"the payee's field": {`{"received":{"value":"4500","assetCode":"USD","assetScale":2}}`, "other leg's amount field for a payer leg"},
		"both fields":       {`{"amount":{"value":"4500","assetCode":"USD","assetScale":2},"received":{"value":"1","assetCode":"USD","assetScale":2}}`, "other leg's amount field for a payer leg"},
	} {
		err := checkLeg(t, f, act, c.member)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), c.want, name)
	}
}

// A refund rests on a cancel check, whose amount_minor is the refund's. The
// payer leg settles its amount; the payee leg (the refund received) settles
// received plus receive_fee, AAC's amount rule.
func TestCheckActsHoldsARefundToItsCancelCheck(t *testing.T) {
	amount := int64(6000)
	f, payer := amountFlow(t, "cancel", &amount, "USD", LegPayer)
	require.NoError(t, checkLeg(t, f, payer, `{"amount":{"value":"6000","assetCode":"USD","assetScale":2}}`))
	err := checkLeg(t, f, payer, `{"amount":{"value":"6500","assetCode":"USD","assetScale":2}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "differs from the checked 6000 USD")

	f, payee := amountFlow(t, "cancel", &amount, "USD", LegPayee)
	for name, member := range map[string]string{
		"received with its fee":    `{"received":{"value":"5970","assetCode":"USD","assetScale":2},"receive_fee":{"value":"30","assetCode":"USD","assetScale":2}}`,
		"fee at another scale":     `{"received":{"value":"5970","assetCode":"USD","assetScale":2},"receive_fee":{"value":"300","assetCode":"USD","assetScale":3}}`,
		"received with a zero fee": `{"received":{"value":"6000","assetCode":"USD","assetScale":2},"receive_fee":{"value":"0","assetCode":"USD","assetScale":2}}`,
	} {
		assert.NoError(t, checkLeg(t, f, payee, member), name)
	}
	for name, c := range map[string]struct{ member, want string }{
		"no fee stated, at checked":   {`{"received":{"value":"6000","assetCode":"USD","assetScale":2}}`, "states no receive_fee"},
		"no fee stated, net of one":   {`{"received":{"value":"5970","assetCode":"USD","assetScale":2}}`, "states no receive_fee"},
		"zero fee, net below checked": {`{"received":{"value":"5970","assetCode":"USD","assetScale":2},"receive_fee":{"value":"0","assetCode":"USD","assetScale":2}}`, "settled amount differs"},
		"both fields":                 {`{"received":{"value":"6000","assetCode":"USD","assetScale":2},"amount":{"value":"6000","assetCode":"USD","assetScale":2}}`, "other leg's amount field for a payee leg"},
		"a sum off by one":            {`{"received":{"value":"5970","assetCode":"USD","assetScale":2},"receive_fee":{"value":"31","assetCode":"USD","assetScale":2}}`, "settled amount differs"},
		"a fee in another currency":   {`{"received":{"value":"5970","assetCode":"USD","assetScale":2},"receive_fee":{"value":"30","assetCode":"EUR","assetScale":2}}`, "settled currency EUR"},
		"the payer's field":           {`{"amount":{"value":"6000","assetCode":"USD","assetScale":2}}`, "other leg's amount field for a payee leg"},
	} {
		err := checkLeg(t, f, payee, c.member)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), c.want, name)
	}
}

// Rule 5 binds each field both sides carry: a check with a currency and no
// amount holds the currency; one with neither holds nothing.
func TestCheckActsHoldsOnlyTheFieldsTheCheckCarries(t *testing.T) {
	f, act := amountFlow(t, "pay", nil, "USD", LegPayer)
	require.NoError(t, checkLeg(t, f, act, `{"amount":{"value":"1","assetCode":"USD","assetScale":2}}`))
	err := checkLeg(t, f, act, `{"amount":{"value":"1","assetCode":"EUR","assetScale":2}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "settled currency EUR differs from the checked USD")

	f, act = amountFlow(t, "pay", nil, "", LegPayer)
	require.NoError(t, checkLeg(t, f, act, `{"amount":{"value":"1","assetCode":"EUR","assetScale":2}}`))
}

// A checked amount in a currency with no known minor units cannot be read,
// so the settled amount is refused rather than compared on its digits.
func TestCheckActsRefusesAnAmountInAnUnknownCurrency(t *testing.T) {
	amount := int64(10)
	f, act := amountFlow(t, "pay", &amount, "XQQ", LegPayer)
	err := checkLeg(t, f, act, `{"amount":{"value":"10","assetCode":"XQQ","assetScale":0}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no known minor units")
}
