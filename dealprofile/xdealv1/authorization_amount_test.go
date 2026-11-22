// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// x-deal-v0 rule 5, kept by §8: the settled amount and currency equal the
// checked ones. CheckActs reads the act's settlement member, which the record
// does not carry.
func TestCheckActsHoldsTheSettledAmountToTheCheckedOne(t *testing.T) {
	amount := int64(4500)
	f := &authFlow{t: t}
	c := f.seal(TypeCheck, true, &CheckBody{CheckBody: dealprofile.CheckBody{Action: "pay", AmountMinor: &amount, Currency: "USD"}, PaymentRef: "invoice-17", Nonce: label("amount check nonce")})
	f.approve(f.verdict(c, "pass"), "user", true)
	f.pay(c)
	leg := func(value, code string, scale int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"version":"0","leg":"payer_observed","sealer_role":"payer","amount":{"value":%q,"assetCode":%q,"assetScale":%d},"status":"settled"}`, value, code, scale))
	}
	act := f.acts[0]

	_, err := CheckActs(f.journal, []ActIn{{Record: act, Settlement: leg("4500", "USD", 2)}})
	require.NoError(t, err, "the checked amount")
	_, err = CheckActs(f.journal, []ActIn{{Record: act}})
	require.NoError(t, err, "no settlement member: read as CheckAuthorizations reads it")

	for name, c := range map[string]struct {
		member json.RawMessage
		want   string
	}{
		"more":            {leg("9900", "USD", 2), "settled amount 9900 differs from the checked 4500"},
		"another code":    {leg("4500", "EUR", 2), "settled currency EUR differs from the checked USD"},
		"another scale":   {leg("4500", "USD", 3), "scale 3 is not USD's 2"},
		"not json":        {json.RawMessage(`{`), "settlement member"},
		"received, wrong": {json.RawMessage(`{"received":{"value":"1","assetCode":"USD","assetScale":2}}`), "differs from the checked 4500"},
	} {
		_, err := CheckActs(f.journal, []ActIn{{Record: act, Settlement: c.member}})
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), c.want, name)
	}
}

// A check with no amount holds no settled amount (rule 5 applies where both
// sides carry it).
func TestCheckActsWithAnUnpricedCheck(t *testing.T) {
	f := &authFlow{t: t}
	c := f.check("pay")
	f.approve(f.verdict(c, "pass"), "user", true)
	f.pay(c)
	_, err := CheckActs(f.journal, []ActIn{{Record: f.acts[0], Settlement: json.RawMessage(`{"amount":{"value":"1","assetCode":"USD","assetScale":2}}`)}})
	require.NoError(t, err)
}
