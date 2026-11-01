// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adjudicatorPayload(t *testing.T, dealID, referee string) []byte {
	t.Helper()
	p, err := ReportPayload(Report{DealID: dealID, Audience: AudienceAdjudicator, Nonce: strings.Repeat("b", 64), Body: map[string]any{}, Referee: referee})
	require.NoError(t, err)
	return p
}

func TestReportPayloadReferee(t *testing.T) {
	r := Report{DealID: "deal-0123456789abcdef", Audience: AudienceAdjudicator, Nonce: strings.Repeat("a", 64), Body: map[string]any{"n": 1}, Referee: "  Café Disputes\n"}
	payload, err := ReportPayload(r)
	require.NoError(t, err)
	assert.JSONEq(t, `{"audience":"adjudicator","deal_id":"deal-0123456789abcdef","nonce":"`+r.Nonce+`","profile":"x-deal-v0","referee":"Café Disputes","report":{"n":1},"type":"deal_report"}`, string(payload), "sealed NFC and trimmed")

	got, err := ReportReferee(payload)
	require.NoError(t, err)
	assert.Equal(t, "Café Disputes", got)

	t.Run("an adjudicator's copy names its referee", func(t *testing.T) {
		for _, referee := range []string{"", " \t\n"} {
			bad := r
			bad.Referee = referee
			_, err := ReportPayload(bad)
			assert.ErrorContains(t, err, "names the referee")
		}
	})
	t.Run("no other copy names one", func(t *testing.T) {
		for _, audience := range []string{AudienceKeep, AudienceCounterparty} {
			bad := r
			bad.Audience = audience
			_, err := ReportPayload(bad)
			assert.ErrorContains(t, err, "only an adjudicator's report names a referee")
			bad.Referee = ""
			payload, err := ReportPayload(bad)
			require.NoError(t, err)
			assert.NotContains(t, string(payload), "referee")
			got, err := ReportReferee(payload)
			require.NoError(t, err)
			assert.Empty(t, got)
		}
	})
}

func TestSharedReferee(t *testing.T) {
	const deal = "deal-0123456789abcdef"
	buyer := ReportCopy{Payload: adjudicatorPayload(t, deal, "the platform"), Party: "buyer-key"}
	seller := ReportCopy{Payload: adjudicatorPayload(t, deal, " the platform "), Party: "seller-key"}
	got, err := SharedReferee(buyer, seller)
	require.NoError(t, err)
	assert.Equal(t, "the platform", got)

	keep, err := ReportPayload(Report{DealID: deal, Audience: AudienceKeep, Nonce: strings.Repeat("c", 64), Body: map[string]any{}})
	require.NoError(t, err)
	before := []byte(`{"audience":"adjudicator","deal_id":"` + deal + `","nonce":"` + strings.Repeat("d", 64) + `","profile":"x-deal-v0","report":{},"type":"deal_report"}`)

	for name, c := range map[string]struct {
		copies []ReportCopy
		reason string
	}{
		"one copy":                 {[]ReportCopy{buyer}, RefereeOneCopy},
		"no copy":                  {nil, RefereeOneCopy},
		"three copies":             {[]ReportCopy{buyer, seller, seller}, RefereeOneCopy},
		"one party twice":          {[]ReportCopy{buyer, {Payload: seller.Payload, Party: buyer.Party}}, RefereeOneParty},
		"a copy with no party":     {[]ReportCopy{buyer, {Payload: seller.Payload}}, RefereeOneParty},
		"two deals":                {[]ReportCopy{buyer, {Payload: adjudicatorPayload(t, "deal-fedcba9876543210", "the platform"), Party: seller.Party}}, RefereeTwoDeals},
		"different referees":       {[]ReportCopy{buyer, {Payload: adjudicatorPayload(t, deal, "The Platform"), Party: seller.Party}}, RefereeDifferent},
		"a copy for the user":      {[]ReportCopy{buyer, {Payload: keep, Party: seller.Party}}, RefereeNotAdjudicator},
		"a copy sealed before":     {[]ReportCopy{{Payload: before, Party: buyer.Party}, seller}, RefereeNotNamed},
		"a null referee":           {[]ReportCopy{buyer, {Payload: []byte(strings.Replace(string(before), `"profile"`, `"referee":null,"profile"`, 1)), Party: seller.Party}}, RefereeNotNamed},
		"a referee not a string":   {[]ReportCopy{buyer, {Payload: []byte(strings.Replace(string(before), `"profile"`, `"referee":7,"profile"`, 1)), Party: seller.Party}}, RefereeMalformed},
		"a member in another case": {[]ReportCopy{buyer, {Payload: []byte(strings.Replace(string(before), `"profile"`, `"Referee":"the platform","profile"`, 1)), Party: seller.Party}}, RefereeNotNamed},
		"not JSON":                 {[]ReportCopy{buyer, {Payload: []byte(`{`), Party: seller.Party}}, RefereeMalformed},
		"a trailing brace":         {[]ReportCopy{buyer, {Payload: append(append([]byte{}, seller.Payload...), '}'), Party: seller.Party}}, RefereeMalformed},
		"trailing bytes":           {[]ReportCopy{buyer, {Payload: append(append([]byte{}, seller.Payload...), []byte(` {}`)...), Party: seller.Party}}, RefereeMalformed},
		"not a deal report":        {[]ReportCopy{buyer, {Payload: []byte(`{"type":"deal_terms_result","profile":"x-deal-v0","deal_id":"` + deal + `","audience":"adjudicator","referee":"the platform"}`), Party: seller.Party}}, RefereeMalformed},
		"another profile":          {[]ReportCopy{buyer, {Payload: []byte(strings.Replace(string(seller.Payload), "x-deal-v0", "x-deal-v1", 1)), Party: seller.Party}}, RefereeMalformed},
		"no deal id":               {[]ReportCopy{buyer, {Payload: []byte(`{"type":"deal_report","profile":"x-deal-v0","audience":"adjudicator","referee":"the platform"}`), Party: seller.Party}}, RefereeMalformed},
		"invalid UTF-8":            {[]ReportCopy{buyer, {Payload: []byte(strings.Replace(string(seller.Payload), "platform", "platform\xff", 1)), Party: seller.Party}}, RefereeMalformed},
		"a JSON array":             {[]ReportCopy{buyer, {Payload: []byte(`[]`), Party: seller.Party}}, RefereeMalformed},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := SharedReferee(c.copies...)
			var re *RefereeError
			require.True(t, errors.As(err, &re), "%v", err)
			assert.Equal(t, c.reason, re.Reason)
			assert.Empty(t, got)
			if c.reason == RefereeDifferent {
				assert.Equal(t, []string{"the platform", "The Platform"}, re.Named, "both names, in order")
				assert.ErrorContains(t, err, `one side named "the platform", the other "The Platform"`)
			} else {
				assert.Nil(t, re.Named)
			}
		})
	}
}
