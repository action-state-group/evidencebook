// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"fmt"

	dealapi "github.com/action-state-group/evidencebook/deal"
)

// cliRefusal is how this command line words a coded refusal of package deal:
// the library names its own calls (Store.Admit), the CLI its commands
// (`deal admit`). args is the number of Args the code carries.
type cliRefusal struct {
	args int
	text func(a []string) string
}

// cliRefusals words each coded refusal the way capsulectl-deal always has
// (testdata/deal-open-golden.json, TestTheCodedRefusalsKeepTheirCLIText). A
// refusal is worded from Reason, the library's own text, when its code is
// not here (a code a later library adds) or its Args do not fit.
var cliRefusals = map[dealapi.Code]cliRefusal{
	dealapi.CodeCancelAgain: {0, func([]string) string {
		return "this offer is already cancelled: `deal offer cancel` writes one cancellation per offer"
	}},
	dealapi.CodeDeclineUnknownOffer: {1, func(a []string) string {
		return "this profile holds no offer with the digest " + a[0] + ": receive it first (`deal receive`)"
	}},
	dealapi.CodeDeclineOwnOffer: {0, func([]string) string {
		return "you wrote this offer: withdraw it with `deal offer cancel`"
	}},
	dealapi.CodeDeclineAgain: {0, func([]string) string {
		return "this offer is already declined: `deal offer decline` writes one decline per offer"
	}},
	dealapi.CodeAdmitNotAdmitted: {1, func(a []string) string {
		return fmt.Sprintf("capsule-engine says this decision (%s) does not admit the deal: an agreement cites only one that does. If it asks for answers, answer them and run `capsule-engine admission evaluate --admit FILE` again with them; then run `deal admit` with the new output. A LIMIT whose tier does not hold the deal admits a smaller deal only", a[0])
	}},
	dealapi.CodeAdmitUnknownDeal: {1, func(a []string) string {
		return "deal " + a[0] + " is not an x-deal-v1 deal this profile holds: make or receive its offer first (`deal offer`, `deal receive`)"
	}},
	dealapi.CodeAdmitAgain: {2, func(a []string) string {
		return fmt.Sprintf("deal %s already has an admission decision, %s, and `deal agree` cites that one: a deal has one, so to decide again, start a new deal", a[0], a[1])
	}},
	dealapi.CodeAgreeUnknownOffer: {1, func(a []string) string {
		return "this profile holds no offer with the digest " + a[0] + ": `deal receive FILE` takes one"
	}},
	dealapi.CodeAgreeDeclineUnfinished: {0, func([]string) string {
		return "you began to decline this offer and it did not finish: run `deal offer decline` again to finish it. A declined offer has no deal to agree to."
	}},
	dealapi.CodeAgreeBeforeReceive: {0, func([]string) string {
		return "the acceptor's agreement has not been received: `deal receive FILE` takes it, and you agree after"
	}},
	dealapi.CodeAgreeNoAdmit: {1, func(a []string) string {
		return "deal " + a[0] + " has no admission decision: seal capsule-engine's with `deal admit --deal " + a[0] + " --input FILE`, which an agreement cites"
	}},
	dealapi.CodeAgreeAdmitNotOwn: {3, func(a []string) string {
		return " " + a[0] + " is not this deal's admission decision: `deal admit` sealed " + a[1] + " for deal " + a[2] + ", and the agreement cites that one"
	}},
	dealapi.CodeAgreeAdmitOtherDeal: {3, func(a []string) string {
		return " " + a[0] + " is the admission decision `deal admit` sealed for deal " + a[1] + ", not for deal " + a[2]
	}},
	dealapi.CodeAgreeFirst: {1, func(a []string) string {
		return "deal " + a[0] + " has no deal book yet: it opens at your agreement (`deal agree`)"
	}},
	dealapi.CodeRecordRetry: {0, func([]string) string {
		return "the deal book kept changing while the record was sealed; run the command again"
	}},
	dealapi.CodeNoAgreement: {1, func(a []string) string {
		return "deal " + a[0] + " has no agreement of yours yet: `deal agree` opens your deal book"
	}},
	dealapi.CodeDisputeOpen: {2, func(a []string) string {
		return "you already have an open dispute on row " + a[0] + " in forum " + a[1] + ": take it back with --withdraw first"
	}},
	dealapi.CodeChallengeUnknownRuling: {2, func(a []string) string {
		return "ruling " + a[0] + " is unavailable: this profile holds no such ruling of deal " + a[1] +
			" from its adjudicator; take the adjudicator's file with deal receive first"
	}},
	dealapi.CodeSettleWhichRow: {0, func([]string) string {
		return "the offer has more than one payment row: name it with --row"
	}},
	dealapi.CodeSettlePayer: {1, func(a []string) string {
		if a[0] == "refund" {
			return "a refund you make is a payer leg: it rests on your cancel check (--check), sent with --sent first"
		}
		return "you owe this row: a payment you make rests on your pay check (--check), sent with --sent first"
	}},
	dealapi.CodeSettleRefunded: {1, func(a []string) string {
		return "leg " + a[0] + " is already refunded: a later observation of that refund names it with --supersedes, and a failed refund leaves the leg free to refund again"
	}},
	dealapi.CodeSettleNegative: {0, func([]string) string {
		return "--received, --receive-fee and --routing-fee are amounts in minor units and are never negative"
	}},
	dealapi.CodeRecordNotRebuilt: {1, func(a []string) string {
		return fmt.Sprintf("the record %s was sealed but its file was not made, and the library cannot rebuild it: for an offer, cancel it with `deal offer cancel`", a[0])
	}},
}

// cliReason is a refusal's text in this command line's terms: the CLI's
// wording of a coded refusal, else the library's Reason.
func cliReason(code dealapi.Code, args []string, reason string) string {
	if r, ok := cliRefusals[code]; ok && len(args) == r.args {
		return r.text(args)
	}
	return reason
}
