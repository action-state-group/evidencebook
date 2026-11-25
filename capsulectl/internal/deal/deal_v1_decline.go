package deal

import (
	"context"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// declineRequest is the request key of a decline of offer: one decline per
// offer, whatever its reason, so a re-run never seals a second one.
func declineRequest(offer string) string { return v1Request([]byte("decline"), []byte(offer)) }

// dealOfferDeclineCommand is `deal offer decline` (evidencebook#626 B3;
// write-path note §5, Sherlock2 2a004e65): an agree record of kind decline,
// sealed in the journal and sent whole to the offer's signer. A decline is
// signed by the key the offer was sent to (PROFILE §3.3), so only an offer
// addressed to this profile's key can be declined.
func dealOfferDeclineCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decline OFFER_DIGEST",
		Short: "Decline an offer sent to you, before you agree to it, and write the file to send back",
		Long: `Decline an x-deal-v1 offer that was sent to your key (OFFER_DIGEST is the
digest 'deal receive' printed): seal a decline in your journal and write the
file to send back to the offer's sender to --out (standard output with none). --reason says why: not_selected (another offer was chosen) or
other; it is optional. An offer is declined once, and not after you agreed to it.

To decline a held evidence request, use 'deal decline'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			offer := args[0]
			out, _ := c.Flags().GetString("out")
			reason, _ := c.Flags().GetString("reason")
			if reason != "" && reason != xdealv1.ReasonNotSel && reason != xdealv1.ReasonOther {
				return cli.InputError("--reason must be not_selected or other")
			}
			request := declineRequest(offer)
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				if _, err := s.requireMigrated(ctx); err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				sent, err := s.store.DeclineOffer(ctx, offer, reason, request)
				if err != nil {
					return libErr(err)
				}
				// A stopped decline resumes with its own record, whatever
				// --reason says now, and says so when that differs.
				if sent.Resumed && reason != "" {
					if stored, err := xdealv1.Decode(sent.Payload); err == nil {
						if ag, ok := agreeOf(stored); ok && ag.Reason != reason {
							was := "reason " + ag.Reason
							if ag.Reason == "" {
								was = "no reason"
							}
							if _, err := fmt.Fprintln(c.ErrOrStderr(), "this decline was already sealed with "+was+"; it is written again unchanged"); err != nil {
								return err
							}
						}
					}
				}
				if err = s.writeSent(ctx, c, out, sent); err != nil {
					return err
				}
				if out == "" {
					return nil
				}
				return cli.Output(c, map[string]any{"deal_id": sent.DealID, "declined": offer, "file": out})
			})
		},
	}
	cmd.Flags().String("out", "", "Where to write the Capsule that carries the decline (default: standard output)")
	cmd.Flags().String("reason", "", "Why: not_selected (another offer was chosen) or other; optional")
	return cmd
}
