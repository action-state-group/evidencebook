package deal

import (
	"context"
	"database/sql"
	"errors"
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

Needs a profile on x-deal-v1 (deal set-version x-deal-v1 --migrate).`,
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
				var (
					dealID  string
					payload []byte
					digest  string
				)
				if op, err := s.findSentOp(ctx, request); err != nil {
					return err
				} else if op != nil && op.state != sentWritten {
					// A run that stopped: its record is fixed, whatever the clock says now.
					dealID, payload, digest = op.dealID, op.payload, op.recordDigest
				} else {
					if dealID, err = newV1DealID(); err != nil {
						return err
					}
					now := v1Now()
					body.StatedAt = now
					rec := xdealv1.Record{
						Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypePropose, At: now},
						Body:  body,
					}
					if err = xdealv1.CheckSealed(rec, xdealv1.SealContext{}); err != nil {
						return cli.InputError("the offer does not pass the x-deal-v1 checks: " + err.Error())
					}
					if payload, digest, err = xdealv1.Encode(rec); err != nil {
						return err
					}
				}
				if _, err := s.db.ExecContext(ctx, `INSERT INTO deal_versions (deal_id, version, own_log_id, counterparty_log_id, state) VALUES (?, ?, '', '', 'offered') ON CONFLICT(deal_id) DO NOTHING`, dealID, dealVersionV1); err != nil {
					return err
				}
				if _, err := s.db.ExecContext(ctx, `INSERT INTO deal_v1 (deal_id, side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest) VALUES (?, ?, ?, ?, '', '', '', '') ON CONFLICT(deal_id) DO NOTHING`,
					dealID, xdealv1.SideOfferer, body.Role, digest); err != nil {
					return err
				}
				pair, err := s.sendV1(ctx, request, dealID, xdealv1.TypePropose, payload, digest)
				if err != nil {
					return err
				}
				if err = s.writePair(ctx, c, out, digest, request, pair); err != nil {
					return err
				}
				if out == "" {
					return nil
				}
				return cli.Output(c, map[string]any{"deal_id": dealID, "offer": digest, "file": out})
			})
		},
	}
	cmd.Flags().String("input", "", "The offer's members, as a JSON file")
	cmd.Flags().String("counter-to", "", "Make this a counter-offer to the offer with this digest")
	cmd.Flags().StringArray("forum", nil, "A forum both sides accept for a dispute (repeatable); added to the input's forums")
	cmd.Flags().String("out", "", "Where to write the Capsule that carries the offer (default: standard output)")
	cmd.AddCommand(dealOfferCancelCommand())
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
				var dealID string
				err := s.db.QueryRowContext(ctx, `SELECT deal_id FROM deal_sent WHERE record_digest=? AND kind=?`, offer, xdealv1.TypePropose).Scan(&dealID)
				if errors.Is(err, sql.ErrNoRows) {
					return cli.InputError("this profile wrote no offer with the digest " + offer)
				}
				if err != nil {
					return err
				}
				op, err := s.findSentOp(ctx, request)
				if err != nil {
					return err
				}
				if op != nil && op.state == sentWritten {
					return cli.InputError("this offer is already cancelled: `deal offer cancel` writes one cancellation per offer")
				}
				if op != nil && op.pair == nil {
					// A cancellation the journal holds but whose file was never made
					// cannot be rebuilt: it is repeated as a new operation (§4.2).
					held, err := s.journalHolds(ctx, op.recordDigest)
					if err != nil {
						return err
					}
					if held {
						if _, err = s.db.ExecContext(ctx, `UPDATE deal_sent SET request_digest=request_digest||':superseded:'||id WHERE record_digest=? AND request_digest=?`, op.recordDigest, request); err != nil {
							return err
						}
						op = nil
					}
				}
				var (
					payload []byte
					digest  string
				)
				if op != nil {
					payload, digest = op.payload, op.recordDigest
				} else {
					rec := xdealv1.Record{
						Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypeAgree, At: v1Now()},
						Body:  xdealv1.AgreeBody{Kind: xdealv1.KindCancellation, Offer: xdealv1.RefTo(offer)},
					}
					if err = xdealv1.CheckSealed(rec, xdealv1.SealContext{OfferExists: true}); err != nil {
						return cli.InputError("the cancellation does not pass the x-deal-v1 checks: " + err.Error())
					}
					if payload, digest, err = xdealv1.Encode(rec); err != nil {
						return err
					}
				}
				pair, err := s.sendV1(ctx, request, dealID, xdealv1.KindCancellation, payload, digest)
				if err != nil {
					return err
				}
				// The deal's state first: a run that stops before the file is made
				// leaves the operation unwritten, and its re-run writes the file.
				if _, err = s.db.ExecContext(ctx, `UPDATE deal_versions SET state='cancelled' WHERE deal_id=?`, dealID); err != nil {
					return err
				}
				if err = s.writePair(ctx, c, out, digest, request, pair); err != nil {
					return err
				}
				if out == "" {
					return nil
				}
				return cli.Output(c, map[string]any{"deal_id": dealID, "cancelled": offer, "file": out})
			})
		},
	}
	cmd.Flags().String("out", "", "Where to write the Capsule that carries the cancellation (default: standard output)")
	return cmd
}
