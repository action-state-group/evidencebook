package deal

import (
	"context"
	"fmt"
	"slices"

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
				held, err := s.offerByDigest(ctx, offer)
				if err != nil {
					return err
				}
				if held == nil {
					return cli.InputError("this profile holds no offer with the digest " + offer + ": receive it first (`deal receive`)")
				}
				if held.Own {
					return cli.InputError("you wrote this offer: withdraw it with `deal offer cancel`")
				}
				prop, ok := proposeOf(held.Record)
				if !ok {
					return cli.InputError("the record " + offer + " is not an offer")
				}
				if prop.To.V == nil {
					return cli.InputError("this offer names no key (to is null): it is to a party that keeps no signed records, and only its offerer holds it, so there is nothing to decline")
				}
				if *prop.To.V != s.ownKeyID() {
					return cli.InputError("this offer was sent to another key, so you cannot decline it: only the key it was sent to can sign its decline")
				}
				dealID := held.Record.Block.DealID
				d, err := s.v1DealOf(ctx, dealID)
				if err != nil {
					return err
				}
				if d == nil {
					return cli.InputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
				}
				// An agreement of yours and a decline exclude each other: once
				// you agreed, no decline is sealed or written, not even a
				// stopped one resumed. An agreeing run that stopped before its
				// book holds a record sealed nothing: it is no agreement.
				open, err := s.declinableState(ctx, d)
				if err != nil {
					return err
				}
				op, err := s.findSentOp(ctx, request)
				if err != nil {
					return err
				}
				if op != nil && op.state == sentWritten {
					return cli.InputError("this offer is already declined: `deal offer decline` writes one decline per offer")
				}
				if op != nil && op.pair == nil && d.State != open {
					// Nothing of it left the shelf, and the deal moved on (a
					// cancellation received): it is not sealed now.
					inJournal, err := s.journalHolds(ctx, op.recordDigest)
					if err != nil {
						return err
					}
					if !inJournal {
						return cli.InputError("this offer is " + d.State + ": there is nothing to decline")
					}
				}
				if op != nil && op.pair == nil {
					// A decline the journal holds but whose file was never made
					// cannot be rebuilt: it is repeated as a new operation (§4.2).
					inJournal, err := s.journalHolds(ctx, op.recordDigest)
					if err != nil {
						return err
					}
					if inJournal {
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
					// A stopped decline resumes with its own record, whatever
					// --reason says now, and says so when that differs.
					payload, digest = op.payload, op.recordDigest
					if stored, err := xdealv1.Decode(payload); err == nil && reason != "" {
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
				} else {
					if d.State != open {
						return cli.InputError("this offer is " + d.State + ": there is nothing to decline")
					}
					rec := xdealv1.Record{
						Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypeAgree, At: v1Now()},
						Body:  xdealv1.AgreeBody{Kind: xdealv1.KindDecline, Offer: xdealv1.RefTo(offer), Reason: reason},
					}
					if err = xdealv1.CheckSealed(rec, xdealv1.SealContext{OfferExists: true}); err != nil {
						return cli.InputError("the decline does not pass the x-deal-v1 checks: " + err.Error())
					}
					if payload, digest, err = xdealv1.Encode(rec); err != nil {
						return err
					}
				}
				pair, err := s.sendV1(ctx, request, dealID, xdealv1.KindDecline, payload, digest)
				if err != nil {
					return err
				}
				// The deal's state before the file, as cancel does: a run that
				// stops before the file is made leaves the operation unwritten,
				// and its re-run writes the file.
				if _, err = s.db.ExecContext(ctx, `UPDATE deal_versions SET state='declined' WHERE deal_id=? AND state IN ('offer_received','agreeing','declined')`, dealID); err != nil {
					return err
				}
				if err = s.writePair(ctx, c, out, digest, request, pair); err != nil {
					return err
				}
				if out == "" {
					return nil
				}
				return cli.Output(c, map[string]any{"deal_id": dealID, "declined": offer, "file": out})
			})
		},
	}
	cmd.Flags().String("out", "", "Where to write the Capsule that carries the decline (default: standard output)")
	cmd.Flags().String("reason", "", "Why: not_selected (another offer was chosen) or other; optional")
	return cmd
}

// pendingDecline says a decline of the deal was begun: any decline row of
// it, under its request key or one superseded (a run can stop between
// superseding a row and storing its repeat), which an agreement must not
// overtake.
func (s *dealSession) pendingDecline(ctx context.Context, dealID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deal_sent WHERE deal_id=? AND kind=?`, dealID, xdealv1.KindDecline).Scan(&n)
	return n > 0, err
}

// declinableState is the state a deal can be declined from: offer_received,
// or agreeing when that run stopped before the book held a record (it sealed
// nothing). An agreement of this profile, or a book that holds one, refuses.
func (s *dealSession) declinableState(ctx context.Context, d *v1Deal) (string, error) {
	agreed := cli.InputError("you agreed to this offer, so you cannot decline it")
	switch {
	case d.OwnAgreementID != "" || d.State == "agreed":
		return "", agreed
	case d.State != "agreeing":
		return "offer_received", nil
	}
	if d.OwnLogID != "" {
		books, err := s.shelf.Books(ctx)
		if err != nil {
			return "", err
		}
		if slices.Contains(books, d.OwnLogID) {
			return "", agreed
		}
	}
	return "agreeing", nil
}
