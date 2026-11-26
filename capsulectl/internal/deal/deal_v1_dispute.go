package deal

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal dispute` is the one act that contests a row of an x-deal-v1 deal
// (evidencebook-deal#95, Steven 2026-11-17: disputes are sealed as one act
// with the contest verb). It seals a `dispute` record in the holder's deal
// book, of kind dispute or, with --withdraw, withdrawal, and writes the file
// that travels to the other side, where `deal receive` counts it. The record
// is the only place a dispute lives, so a row's status, a contested close and
// a card's disputes raised and received all read the same record (xdealv1
// DisputeBody; the card's count is the reader's, evidencebook#274). A dispute
// is never gated by the first-act rule (u610, u611): only a first perform or
// payer settle is. --challenge seals a dispute of kind challenge naming a
// ruling the holder holds (runChallenge).

// disputeOf is a dispute record of the holder's own book.
func disputeOf(r xdealv1.Record) (*xdealv1.DisputeBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.DisputeBody:
		return b, true
	case xdealv1.DisputeBody:
		return &b, true
	}
	return nil, false
}

// bookPairAt is the pair of the record at a place of the holder's own book,
// as it travels.
func (s *dealSession) bookPairAt(ctx context.Context, d *v1Deal, seq int) ([]byte, error) {
	book, err := s.shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return nil, err
	}
	rows, err := book.Query(ctx, evidencebook.Filter{FromSeq: uint64(seq), ToSeq: uint64(seq)})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || len(rows[0].Header.PayloadCommitments) != 1 {
		return nil, cli.InputError("the deal book has no record at that place")
	}
	sealed, err := book.Sealed(ctx, rows[0].RecordID)
	if err != nil {
		return nil, err
	}
	payload, err := book.ResolvePayload(ctx, rows[0].RecordID, rows[0].Header.PayloadCommitments[0])
	if err != nil {
		return nil, err
	}
	return xdealv1.BookPair(sealed, payload)
}

// writePush writes a book record's pair as the file to send, and prints what
// was sealed. A file already there is the earlier run's only if it holds the
// same record.
func writePush(c *cobra.Command, pair []byte, out string, result map[string]any) error {
	data, err := json.Marshal(v1Push{Record: pair})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if out == "" {
		_, err = c.OutOrStdout().Write(data)
		return err
	}
	if err := cli.AtomicFile(out, data, false); err != nil {
		prev, rerr := os.ReadFile(out)
		var was v1Push
		if rerr != nil || json.Unmarshal(prev, &was) != nil || string(was.Record) != string(pair) {
			return err
		}
	}
	result["file"] = out
	return cli.Output(c, result)
}

func dealDisputeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispute",
		Short: "Dispute a row of an x-deal-v1 deal, take the dispute back, or challenge a ruling, and write the file to send",
		Long: `Dispute a row of an x-deal-v1 deal: seal a dispute record in your deal book
and write the file to send to --out (standard output with none). The other
side counts it with deal receive, and the same record is what a row's status
and the adjudicator read.

The dispute names the adjudicator and the forum your agreement accepts:
--forum picks one of the agreement's forums and may be left out when it names
only one. An agreement with no adjudicator names no forum, so it allows no
dispute. The dispute cites your own statements on the row that exist now.

--withdraw takes back your open dispute on the row, in the same file form.

--challenge RULING --grounds FILE challenges a ruling of the deal you hold
(deal receive took the adjudicator's file): it seals a dispute of kind
challenge naming the ruling and the SHA-256 of your grounds. The grounds stay
with you; only their SHA-256 is sealed, so send the file to the adjudicator
yourself. A challenge changes no row's status: the first final ruling still
applies and is never replaced; the adjudicator may issue a later ruling that is
not final. It takes no --row, --forum or --withdraw.

Run again with the same --out, a command rewrites the same file and seals
nothing new. An --out file that already exists is refused for a new dispute
or withdrawal. Needs your agreement (deal agree) and an x-deal-v1 deal.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dealID, _ := c.Flags().GetString("deal")
			row, _ := c.Flags().GetString("row")
			forum, _ := c.Flags().GetString("forum")
			out, _ := c.Flags().GetString("out")
			withdraw, _ := c.Flags().GetBool("withdraw")
			challenge, _ := c.Flags().GetString("challenge")
			grounds, _ := c.Flags().GetString("grounds")
			if !cli.DealIDPattern.MatchString(dealID) {
				return cli.InputError("--deal must be a deal id as printed by `deal offer`: deal- followed by 16 hex characters")
			}
			if challenge != "" || grounds != "" {
				return runChallenge(c, dealID, strings.TrimSpace(challenge), grounds, out, row != "" || forum != "" || withdraw)
			}
			row = strings.TrimSpace(row)
			if row == "" {
				return cli.InputError("--row is required: the id of the row, as the offer names it")
			}
			if withdraw && forum != "" {
				return cli.InputError("--forum goes with a dispute, not with --withdraw")
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				st, err := s.v1Store(ctx)
				if err != nil {
					return err
				}
				before := func(xdealv1.Record, *xdealv1.ActAuthorization) error { return refuseExistingOut(out) }
				var res dealapi.Acted
				if withdraw {
					res, err = st.WithdrawDispute(ctx, dealID, dealapi.WithdrawIn{Row: row, Before: before})
				} else {
					res, err = st.Dispute(ctx, dealID, dealapi.DisputeIn{Row: row, Forum: forum, Before: before})
				}
				if err != nil {
					return libErr(err)
				}
				return writeDispute(c, res, out, map[string]any{"deal_id": dealID, "row": row})
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal (required)")
	cmd.Flags().String("row", "", "The row to dispute, as the offer names it (required)")
	cmd.Flags().String("forum", "", "The forum, one of the agreement's forums (default: the only one)")
	cmd.Flags().Bool("withdraw", false, "Take back your open dispute on the row")
	cmd.Flags().String("challenge", "", "Challenge a ruling you hold, by the digest deal receive printed (with --grounds)")
	cmd.Flags().String("grounds", "", "With --challenge: a file of your grounds; it stays with you, and only its SHA-256 is sealed")
	cmd.Flags().String("out", "", "Where to write the file to send (default: standard output)")
	return cmd
}

// writeDispute writes a dispute record package deal sealed, or found sealed,
// as the file to send, and prints what it is.
func writeDispute(c *cobra.Command, res dealapi.Acted, out string, result map[string]any) error {
	b, _ := disputeOf(res.Record)
	result["kind"], result["dispute"], result["seq"] = b.Kind, res.Digest, res.Seq
	if b.Forum != "" {
		result["forum"] = b.Forum
	}
	return writePush(c, res.Pair, out, result)
}
