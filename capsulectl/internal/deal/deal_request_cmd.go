package deal

import (
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// requestCommand is `capsulectl deal request` on a deal profile: ask the
// other party for its copy of a deal both hold, record what came back, or
// record that nothing came back (deal_evidence.go). The flags and their
// checks are the core's (cli.EvidenceRequestCommand), which
// `capsulectl book request` shares.
func requestCommand() *cobra.Command {
	return cli.EvidenceRequestCommand(func(c *cobra.Command, p cli.Profile, in cli.EvidenceRequestInput) error {
		responder := evidencebook.ResponderKeys{Signer: in.ResponderSigner, Checkpoint: in.ResponderCheckpoint}
		// Witness receipts a deal copy carries are checked against its anchor
		// under this directory, the requester's own; without one, a copy
		// carrying any receipt is recorded as failed.
		if in.WitnessDirectory != "" {
			raw, e := cli.ReadInput(in.WitnessDirectory)
			if e != nil {
				return e
			}
			if responder.Witnesses, e = evidencebook.ParseWitnessDirectory(raw); e != nil {
				return cli.Hint(cli.ErrInput, "--witness-directory "+in.WitnessDirectory+": "+e.Error())
			}
		}
		return dealRequest(c, p, in.Request, in.Response, in.For, in.WindowEnd, responder)
	})
}

// respondCommand is `capsulectl deal respond`: answer the other party's
// request for a deal's copy with the counterparty copy, and record it.
func respondCommand() *cobra.Command {
	cmd := cli.EvidenceRespondCommand(func(c *cobra.Command, p cli.Profile, in cli.EvidenceRespondInput) error {
		if in.PolicyPath != "" {
			return cli.InputError("--policy is the evidence book's share policy; a deal profile serves the counterparty copy, so it does not apply")
		}
		return dealRespond(c, p, in.Request, in.Requester, in.Output)
	})
	cmd.Long += `

An x-deal-v1 deal's adjudicator asks for the deal with derivation
deal_adjudicator_copy/1 (subject: the deal id as correlation). Its answer is
the whole: your deal book, the copy of the other side's book you hold, and
your book's stamps, in one file to send to the adjudicator, recorded in your
journal. There is no part-answer, and the private floor and the agent's claims
are never in it. --requester is the adjudicator's key id the agreement names;
a request from anyone else is refused.`
	return cmd
}
