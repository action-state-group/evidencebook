// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"

	"github.com/action-state-group/capsule-cli/cli"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/spf13/cobra"
)

const dealAdmitLong = `Seal your admission decision for an x-deal-v1 deal in your journal, so that
` + "`deal agree`" + ` can cite it. --input is the whole output of
` + "`capsule-engine admission evaluate --admit FILE`" + ` for this deal's counterparty;
the decision is capsule-engine's, and this command evaluates nothing.

It refuses a decision capsule-engine says does not admit the deal
("admits_deal": false: an ASK still waiting for answers, a NO, or a LIMIT
whose tier does not hold the deal), an output without an admit body, and an
admit body that is not exactly the one capsule-engine prints. It cannot tell
which counterparty or amount the decision was made for: pass the output of the
run on this deal's own counterparty and amount.

Each deal has one admission decision, and ` + "`deal agree`" + ` cites it. A second
decision for the same deal is refused, and so is a deal already agreed; to
decide again, start a new deal. Running it again with the same --input prints
the same digest and seals nothing. One capsule-engine output is sealed for one
deal only: its admit's nonce is drawn for one record.

The admit record stays in your journal; only its digest leaves you, in your
agreement. Needs a profile on x-deal-v1 (deal set-version x-deal-v1 --migrate)
and the deal's offer (one you made, or one ` + "`deal receive`" + ` took).`

func dealAdmitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admit",
		Short: "Seal capsule-engine's admission decision for an x-deal-v1 deal, for `deal agree` to cite (--deal ID --input FILE)",
		Long:  dealAdmitLong,
		Args:  cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dealID, _ := c.Flags().GetString("deal")
			if !cli.DealIDPattern.MatchString(dealID) {
				return cli.InputError("--deal must be a deal id as printed by `deal offer` or `deal receive`: deal- followed by 16 hex characters")
			}
			path, _ := c.Flags().GetString("input")
			if path == "" {
				return cli.InputError("--input is required: the output of `capsule-engine admission evaluate --admit FILE` for this deal")
			}
			raw, err := cli.ReadInput(path)
			if err != nil {
				return err
			}
			// The decision is read before the store is opened, at the deal
			// clock's time: a refusal of the input comes first.
			admission, err := dealapi.ParseAdmission(raw, cli.DealClock())
			if err != nil {
				return libErr(err)
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				if _, err := s.requireMigrated(ctx); err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				digest, err := s.store.Admit(ctx, dealID, admission)
				if err != nil {
					return libErr(err)
				}
				return cli.Output(c, map[string]any{"deal_id": dealID, "admit": digest})
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal this decision is for")
	cmd.Flags().String("input", "", "The output of `capsule-engine admission evaluate --admit FILE`, as a JSON file")
	return cmd
}
