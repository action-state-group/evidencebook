// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/stretchr/testify/assert"
)

// The coded refusals of the deal-book calls (evidencebook#899 phase 2)
// read, through libErr, exactly as capsulectl-deal printed them before the
// verbs moved onto package deal (the texts of deal_v1_perform.go,
// deal_v1_dispute.go, deal_v1_challenge.go and deal_v1_settle.go at
// evidencebook 0f54c465).
func TestTheDealBookRefusalsKeepTheirCLIText(t *testing.T) {
	const a, b = "aaaa", "bbbb"
	for _, tc := range []struct {
		code dealapi.Code
		args []string
		want string
	}{
		{dealapi.CodeNoAgreement, []string{a}, "deal aaaa has no agreement of yours yet: `deal agree` opens your deal book"},
		{dealapi.CodeDisputeOpen, []string{a, b}, "you already have an open dispute on row aaaa in forum bbbb: take it back with --withdraw first"},
		{dealapi.CodeChallengeUnknownRuling, []string{a, b}, "ruling aaaa is unavailable: this profile holds no such ruling of deal bbbb from its adjudicator; take the adjudicator's file with deal receive first"},
		{dealapi.CodeSettleWhichRow, nil, "the offer has more than one payment row: name it with --row"},
		{dealapi.CodeSettlePayer, []string{"payment"}, "you owe this row: a payment you make rests on your pay check (--check), sent with --sent first"},
		{dealapi.CodeSettlePayer, []string{"refund"}, "a refund you make is a payer leg: it rests on your cancel check (--check), sent with --sent first"},
		{dealapi.CodeSettleRefunded, []string{a}, "leg aaaa is already refunded: a later observation of that refund names it with --supersedes, and a failed refund leaves the leg free to refund again"},
		{dealapi.CodeSettleNegative, nil, "--received, --receive-fee and --routing-fee are amounts in minor units and are never negative"},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			got := libErr(&dealapi.InputError{Reason: "library text", Code: tc.code, Args: tc.args})
			want := cli.InputError(tc.want)
			assert.Equal(t, want.Error(), got.Error())
			assert.Equal(t, cli.ExitCode(want), cli.ExitCode(got))
		})
	}
	// An argument refusal names its flag.
	for field, want := range map[string]string{"statement": "--statement x", "mark": "--mark x", "forum": "--forum x", "reverses": "--reverses x", "supersedes": "--supersedes x"} {
		assert.Equal(t, cli.InputError(want).Error(), libErr(&dealapi.InputError{Field: field, Reason: " x"}).Error())
	}
}
