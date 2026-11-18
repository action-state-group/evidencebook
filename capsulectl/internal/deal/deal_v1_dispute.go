package deal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
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
// payer settle is. A challenge of a ruling is not here: it needs the ruling
// the adjudicator sends back.

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

// openDisputeOn finds the holder's own dispute on a row that no later
// withdrawal of the same row has taken back: its place in the book (seq) and
// body. ok is false when none is open. last is the latest dispute-type
// record on the row (a dispute or a withdrawal), seq 0 when there is none.
func openDisputeOn(book []xdealv1.Record, row string) (seq int, open bool, last *xdealv1.DisputeBody, lastSeq int) {
	for i, r := range book {
		b, ok := disputeOf(r)
		if !ok || b.Row != row {
			continue
		}
		last, lastSeq = b, i+1
		switch b.Kind {
		case xdealv1.KindDispute:
			seq, open = i+1, true
		case xdealv1.KindWithdrawal:
			seq, open = 0, false
		}
	}
	return seq, open, last, lastSeq
}

// statementsOnRow are the record digests of the holder's own statements on a
// row that exist now: what a dispute cites (design §3.3).
func statementsOnRow(book []xdealv1.Record, row string) ([]xdealv1.Ref, error) {
	refs := []xdealv1.Ref{}
	for _, r := range book {
		var sb xdealv1.StatementBody
		switch b := r.Body.(type) {
		case *xdealv1.StatementBody:
			sb = *b
		case xdealv1.StatementBody:
			sb = b
		default:
			continue
		}
		if !slices.ContainsFunc(sb.Statements, func(it xdealv1.StatementItem) bool { return it.Row == row }) {
			continue
		}
		d, err := xdealv1.Digest(r)
		if err != nil {
			return nil, err
		}
		refs = append(refs, xdealv1.RefTo(d))
	}
	return refs, nil
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
		Short: "Dispute a row of an x-deal-v1 deal, or take the dispute back, and write the file to send",
		Long: `Dispute a row of an x-deal-v1 deal: seal a dispute record in your deal book
and write the file to send to --out (standard output with none). The other
side counts it with deal receive, and the same record is what a row's status
and the adjudicator read.

The dispute names the adjudicator and the forum your agreement accepts:
--forum picks one of the agreement's forums and may be left out when it names
only one. An agreement with no adjudicator names no forum, so it allows no
dispute. The dispute cites your own statements on the row that exist now.

--withdraw takes back your open dispute on the row, in the same file form.
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
			if !cli.DealIDPattern.MatchString(dealID) {
				return cli.InputError("--deal must be a deal id as printed by `deal offer`: deal- followed by 16 hex characters")
			}
			row = strings.TrimSpace(row)
			if row == "" {
				return cli.InputError("--row is required: the id of the row, as the offer names it")
			}
			if withdraw && forum != "" {
				return cli.InputError("--forum goes with a dispute, not with --withdraw")
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				if _, err := s.requireMigrated(ctx); err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				d, err := s.v1DealOf(ctx, dealID)
				if err != nil {
					return err
				}
				if d == nil {
					return cli.InputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
				}
				if d.OwnLogID != "" {
					if err := s.repairIfStopped(ctx, d); err != nil {
						return err
					}
				}
				if d.OwnAgreementID == "" {
					return cli.InputError("deal " + dealID + " has no agreement of yours yet: `deal agree` opens your deal book")
				}
				book, err := s.ownBookV1(ctx, d)
				if err != nil {
					return err
				}
				agreement := book[0]
				ag, ok := agreeOf(agreement)
				if !ok || ag.Kind != xdealv1.KindAgreement {
					return cli.InputError("the first record of your deal book is not your agreement")
				}
				if ag.Adjudicator.IsNull() || !ag.Adjudicator.Present {
					return cli.InputError("the agreement of deal " + dealID + " names no adjudicator and no forum, so no dispute is possible")
				}
				offer, err := s.offerByDigest(ctx, d.OfferDigest)
				if err != nil {
					return err
				}
				if offer == nil {
					return cli.InputError("this profile no longer holds the offer of deal " + dealID)
				}
				prop, _ := proposeOf(offer.Record)
				if !slices.ContainsFunc(prop.TermsRows.Rows, func(r xdealv1.Row) bool { return r.ID == row }) {
					return cli.InputError("deal " + dealID + " has no row " + row + ": the rows are the ones its offer names")
				}
				seq, open, last, lastSeq := openDisputeOn(book, row)
				result := map[string]any{"deal_id": dealID, "row": row}
				if withdraw {
					if !open {
						// The withdrawal already sealed is written again, never sealed twice.
						if last != nil && last.Kind == xdealv1.KindWithdrawal {
							return s.writeSealedDispute(ctx, c, d, lastSeq, book[lastSeq-1], out, result)
						}
						return cli.InputError("you have no open dispute on row " + row + " of deal " + dealID + " to take back")
					}
					return s.sealDispute(ctx, c, d, xdealv1.DisputeBody{Kind: xdealv1.KindWithdrawal, Row: row}, nil, out, result)
				}
				if open {
					// The same dispute again is written again, never sealed twice.
					prev, _ := disputeOf(book[seq-1])
					if forum != "" && forum != prev.Forum {
						return cli.InputError("you already have an open dispute on row " + row + " in forum " + prev.Forum + ": take it back with --withdraw first")
					}
					return s.writeSealedDispute(ctx, c, d, seq, book[seq-1], out, result)
				}
				if forum == "" {
					if len(ag.Forums) != 1 {
						return cli.InputError("--forum is required: the agreement accepts " + strings.Join(ag.Forums, ", "))
					}
					forum = ag.Forums[0]
				}
				stmts, err := statementsOnRow(book, row)
				if err != nil {
					return err
				}
				body := xdealv1.DisputeBody{
					Kind: xdealv1.KindDispute, Row: row, Statements: stmts,
					Adjudicator: ag.Adjudicator.V.KeyID, Forum: forum,
				}
				// The finished record is held to the holder's own agreement before
				// anything is stored: the adjudicator and forum it names are the
				// agreement's, and it is bound to that agreement.
				validate := func(rec xdealv1.Record) error {
					if err := xdealv1.CheckDispute(rec, agreement); err != nil {
						return cli.InputError("the dispute does not pass the x-deal-v1 checks: " + err.Error())
					}
					return nil
				}
				return s.sealDispute(ctx, c, d, body, validate, out, result)
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal (required)")
	cmd.Flags().String("row", "", "The row to dispute, as the offer names it (required)")
	cmd.Flags().String("forum", "", "The forum, one of the agreement's forums (default: the only one)")
	cmd.Flags().Bool("withdraw", false, "Take back your open dispute on the row")
	cmd.Flags().String("out", "", "Where to write the file to send (default: standard output)")
	return cmd
}

// sealDispute seals a dispute record in the holder's book and writes it.
func (s *dealSession) sealDispute(ctx context.Context, c *cobra.Command, d *v1Deal, body xdealv1.DisputeBody, validate func(xdealv1.Record) error, out string, result map[string]any) error {
	// A file already at --out is never this new record's: an old command run
	// again must not seal a record only to fail on its file.
	if out != "" {
		// Lstat, so a dangling link at the path counts as there too.
		if _, err := os.Lstat(out); err == nil {
			return cli.InputError(out + " exists: --out names a new file, and this seals a new record")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	sealed, err := s.sealBookV1(ctx, d.DealID, bookRecordIn{Body: body, Validate: validate})
	if err != nil {
		return err
	}
	result["kind"], result["dispute"], result["seq"] = body.Kind, sealed.Digest, sealed.Seq
	if body.Forum != "" {
		result["forum"] = body.Forum
	}
	return writePush(c, sealed.Pair, out, result)
}

// writeSealedDispute writes again a dispute record the book already holds.
func (s *dealSession) writeSealedDispute(ctx context.Context, c *cobra.Command, d *v1Deal, seq int, rec xdealv1.Record, out string, result map[string]any) error {
	pair, err := s.bookPairAt(ctx, d, seq)
	if err != nil {
		return err
	}
	digest, err := xdealv1.Digest(rec)
	if err != nil {
		return err
	}
	b, _ := disputeOf(rec)
	result["kind"], result["dispute"], result["seq"] = b.Kind, digest, seq
	if b.Forum != "" {
		result["forum"] = b.Forum
	}
	return writePush(c, pair, out, result)
}
