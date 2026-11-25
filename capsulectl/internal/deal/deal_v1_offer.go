package deal

import (
	"context"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal offer` writes the offer (a propose record) to the journal and sends
// it whole: the record is sealed twice in one call, as a journal record and
// as a standalone Capsule whose file `--out` receives (§4.2, §4.3). The
// plugin has no transport; the host sends the file. `deal offer cancel`
// withdraws an offer before the deal starts.

func dealOfferCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "offer",
		Short: "Write an x-deal-v1 offer and the file to send it in (--input FILE --out FILE); `offer cancel` withdraws one",
		Long: `Write an x-deal-v1 offer from --input, a JSON file of the offer's members
(role, to, terms, pack_digest, agree_by, terms_rows, adjudicator, forums,
witnesses, and optionally counter_to, pack_url, ask, profiles, listing_digest),
seal it in your journal, and write the Capsule that carries it whole to --out
(standard output with none): send that file to the other side. The offer's
time is taken from the clock, never from the input. Re-running the same
command after a failure finds the offer it left and writes the file again; it
never seals the same offer twice. Once it has succeeded, running it again
writes a new offer under a new deal id.

An offer whose to is null is to a party that keeps no signed records of its own (a
shop that runs no deal tool, say): it stays with you, there is nothing to
send, and you agree to it yourself (deal agree).

x-deal-v1 is the default. A profile set to the legacy x-deal-v0 needs
'deal set-version x-deal-v1' first, and a deal store in an older layout
needs 'deal set-version x-deal-v1 --migrate' once.`,
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			path, _ := c.Flags().GetString("input")
			raw, err := cli.ReadInput(path)
			if err != nil {
				return err
			}
			var body xdealv1.ProposeBody
			if err = cli.DecodeJSONAs("--input", raw, &body); err != nil {
				return err
			}
			counterTo, _ := c.Flags().GetString("counter-to")
			forums, _ := c.Flags().GetStringArray("forum")
			for _, f := range forums {
				if !slices.Contains(body.Forums, f) {
					body.Forums = append(body.Forums, f)
				}
			}
			if counterTo != "" {
				ref := xdealv1.RefTo(counterTo)
				body.CounterTo = &ref
			}
			out, _ := c.Flags().GetString("out")
			// The request is the input and the flags that change the record.
			request := v1Request([]byte("offer"), raw, []byte(counterTo), []byte(strings.Join(forums, "\x00")))
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				if _, err := s.requireV1(ctx); err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				sent, err := s.store.Offer(ctx, body, request)
				if err != nil {
					return libErr(err)
				}
				if err = s.writeSent(ctx, c, out, sent); err != nil {
					return err
				}
				if out == "" {
					return nil
				}
				res := map[string]any{"deal_id": sent.DealID, "offer": sent.Digest, "file": out}
				if body.To.V == nil {
					res["note"] = "this offer names no key: it is to a party that keeps no signed records of its own, so there is nothing to send. It stays with you; agree to it yourself with `deal agree` once your admission decision and intent are sealed"
				}
				return cli.Output(c, res)
			})
		},
	}
	cmd.Flags().String("input", "", "The offer's members, as a JSON file")
	cmd.Flags().String("counter-to", "", "Make this a counter-offer to the offer with this digest")
	cmd.Flags().StringArray("forum", nil, "A forum both sides accept for a dispute (repeatable); added to the input's forums")
	cmd.Flags().String("out", "", "Where to write the Capsule that carries the offer (default: standard output)")
	cmd.AddCommand(dealOfferCancelCommand(), dealOfferDeclineCommand())
	return cmd
}

func dealOfferCancelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel OFFER_DIGEST",
		Short: "Withdraw an offer you sent, before the deal starts, and write the file to send",
		Long: `Withdraw an offer you wrote (OFFER_DIGEST is the digest 'deal offer' printed):
seal a cancellation in your journal and write the Capsule that carries it whole
to --out (standard output with none). An offer is cancelled once.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			offer := args[0]
			out, _ := c.Flags().GetString("out")
			request := v1Request([]byte("cancellation"), []byte(offer))
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				// The setting governs the offers a profile writes, not a deal it
				// already sent: only the store's layout matters here.
				if _, err := s.requireMigrated(ctx); err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				sent, err := s.store.CancelOffer(ctx, offer, request)
				if err != nil {
					return libErr(err)
				}
				if err = s.writeSent(ctx, c, out, sent); err != nil {
					return err
				}
				if out == "" {
					return nil
				}
				return cli.Output(c, map[string]any{"deal_id": sent.DealID, "cancelled": offer, "file": out})
			})
		},
	}
	cmd.Flags().String("out", "", "Where to write the Capsule that carries the cancellation (default: standard output)")
	return cmd
}
