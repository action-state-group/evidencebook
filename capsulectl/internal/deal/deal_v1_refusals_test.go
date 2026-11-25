// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"errors"
	"fmt"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/stretchr/testify/assert"
)

// Each coded refusal of package deal reads, through libErr, exactly as
// capsulectl-deal printed it before the library named its own calls (the
// texts of deal/*.go at evidencebook 1372f2ea), with the same input-error
// or conflict class and exit code. The golden file pins only some of them;
// this pins every one, and catches a reordering of a code's Args.
func TestTheCodedRefusalsKeepTheirCLIText(t *testing.T) {
	const a, b, c = "aaaa", "bbbb", "cccc"
	for _, tc := range []struct {
		code     dealapi.Code
		field    string
		args     []string
		conflict bool
		want     string
	}{
		{dealapi.CodeCancelAgain, "", nil, false, "this offer is already cancelled: `deal offer cancel` writes one cancellation per offer"},
		{dealapi.CodeDeclineUnknownOffer, "", []string{a}, false, "this profile holds no offer with the digest aaaa: receive it first (`deal receive`)"},
		{dealapi.CodeDeclineOwnOffer, "", nil, false, "you wrote this offer: withdraw it with `deal offer cancel`"},
		{dealapi.CodeDeclineAgain, "", nil, false, "this offer is already declined: `deal offer decline` writes one decline per offer"},
		{dealapi.CodeAdmitNotAdmitted, "", []string{"ASK"}, false, "capsule-engine says this decision (ASK) does not admit the deal: an agreement cites only one that does. If it asks for answers, answer them and run `capsule-engine admission evaluate --admit FILE` again with them; then run `deal admit` with the new output. A LIMIT whose tier does not hold the deal admits a smaller deal only"},
		{dealapi.CodeAdmitUnknownDeal, "", []string{a}, false, "deal aaaa is not an x-deal-v1 deal this profile holds: make or receive its offer first (`deal offer`, `deal receive`)"},
		{dealapi.CodeAdmitAgain, "", []string{a, b}, true, "deal aaaa already has an admission decision, bbbb, and `deal agree` cites that one: a deal has one, so to decide again, start a new deal"},
		{dealapi.CodeAgreeUnknownOffer, "", []string{a}, false, "this profile holds no offer with the digest aaaa: `deal receive FILE` takes one"},
		{dealapi.CodeAgreeDeclineUnfinished, "", nil, false, "you began to decline this offer and it did not finish: run `deal offer decline` again to finish it. A declined offer has no deal to agree to."},
		{dealapi.CodeAgreeBeforeReceive, "", nil, false, "the acceptor's agreement has not been received: `deal receive FILE` takes it, and you agree after"},
		{dealapi.CodeAgreeNoAdmit, "", []string{a}, false, "deal aaaa has no admission decision: seal capsule-engine's with `deal admit --deal aaaa --input FILE`, which an agreement cites"},
		{dealapi.CodeAgreeAdmitNotOwn, "admit", []string{a, b, c}, false, "--admit aaaa is not this deal's admission decision: `deal admit` sealed bbbb for deal cccc, and the agreement cites that one"},
		{dealapi.CodeAgreeAdmitOtherDeal, "admit", []string{a, b, c}, false, "--admit aaaa is the admission decision `deal admit` sealed for deal bbbb, not for deal cccc"},
		{dealapi.CodeAgreeFirst, "", []string{a}, false, "deal aaaa has no deal book yet: it opens at your agreement (`deal agree`)"},
		{dealapi.CodeRecordRetry, "", nil, true, "the deal book kept changing while the record was sealed; run the command again"},
		{dealapi.CodeRecordNotRebuilt, "", []string{a}, true, "the record aaaa was sealed but its file was not made, and the library cannot rebuild it: for an offer, cancel it with `deal offer cancel`"},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			var in error = &dealapi.InputError{Field: tc.field, Reason: "library text", Code: tc.code, Args: tc.args}
			want := cli.InputError(tc.want)
			if tc.conflict {
				in = &dealapi.ConflictError{Reason: "library text", Code: tc.code, Args: tc.args}
				want = fmt.Errorf("%w: %s", cli.ErrConflict, tc.want)
			}
			got := libErr(in)
			assert.Equal(t, want.Error(), got.Error())
			assert.Equal(t, cli.SafeError(want), cli.SafeError(got))
			assert.Equal(t, cli.ExitCode(want), cli.ExitCode(got))
			assert.Equal(t, tc.conflict, errors.Is(got, cli.ErrConflict))
			assert.NotContains(t, got.Error(), string(tc.code), "a Code is never printed")
		})
	}
}

// A code the CLI does not word, or one whose Args do not fit, reads as the
// library's own text rather than failing.
func TestAnUnwordedRefusalReadsAsTheLibrarysText(t *testing.T) {
	assert.Equal(t, cli.InputError("--admit is new").Error(), libErr(&dealapi.InputError{Field: "admit", Reason: " is new", Code: "a-later-code"}).Error())
	assert.Equal(t, cli.InputError("library text").Error(), libErr(&dealapi.InputError{Reason: "library text", Code: dealapi.CodeAgreeNoAdmit}).Error())
	assert.Equal(t, fmt.Errorf("%w: %s", cli.ErrConflict, "library text").Error(), libErr(&dealapi.ConflictError{Reason: "library text", Code: dealapi.CodeAdmitAgain, Args: []string{"x"}}).Error())
}
