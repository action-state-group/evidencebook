package deal

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal ruling check` reports whether a case's row is decided by the records
// of the side the dispute is raised against (xdealv1.DecideCase): its own
// latest statement on the row, in a record it signed, says not_met. It seals
// nothing; the adjudicator still rules with `deal ruling`, which takes
// --decided-by to rule only on what the check decides.

// readCaseInputs reads the dispute file (its record) and the case-opening
// copies, as `deal ruling` and `deal ruling check` both take them.
func readCaseInputs(dispute string, copies []string) ([]byte, []xdealv1.CaseCopy, error) {
	raw, err := os.ReadFile(dispute)
	if err != nil {
		return nil, nil, cli.InputError("cannot read --dispute " + dispute)
	}
	var push v1Push
	if err := json.Unmarshal(raw, &push); err != nil || push.Record == nil {
		return nil, nil, cli.InputError("--dispute " + dispute + " is not a file deal dispute wrote")
	}
	var cc []xdealv1.CaseCopy
	for _, path := range copies {
		c, err := readCaseCopyFile(path)
		if err != nil {
			return nil, nil, err
		}
		cc = append(cc, c)
	}
	return push.Record, cc, nil
}

// gatherAnswered reads a dispute's record, the case-opening copies and the
// parties' full answers as one case for the adjudicator me
// (xdealv1.GatherCase), under the profile's pinned witness directory.
func (s *dealSession) gatherAnswered(dispute []byte, cc []xdealv1.CaseCopy, answers []string, dealID, me string) (xdealv1.GatheredCase, error) {
	var as []xdealv1.CaseAnswer
	for _, path := range answers {
		a, err := readAnswerFile(path, dealID)
		if err != nil {
			return xdealv1.GatheredCase{}, err
		}
		as = append(as, a)
	}
	dir, err := pinnedWitnessDirectory(s.p)
	if err != nil {
		return xdealv1.GatheredCase{}, err
	}
	g, err := xdealv1.GatherCase(dispute, cc, as, me, dir)
	if err != nil {
		return xdealv1.GatheredCase{}, cli.InputError("the dispute, the copies and the answers are not a case for you: " + err.Error())
	}
	return g, nil
}

// sealedRulingOf is a sealed ruling file (the file deal ruling wrote) read
// as a ruling of this case: it verifies, holds a ruling, is signed by this
// adjudicator (me) in the case's book, and is on the case's deal, row and
// keys. Anything else is refused, so a check never reads as far as some other
// record says.
func sealedRulingOf(path string, g xdealv1.GatheredCase, me string) (xdealv1.RulingBody, error) {
	bad := func(why string) (xdealv1.RulingBody, error) {
		return xdealv1.RulingBody{}, cli.InputError("--ruling " + path + " " + why)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return xdealv1.RulingBody{}, cli.InputError("cannot read --ruling " + path)
	}
	var push v1Push
	if err := json.Unmarshal(raw, &push); err != nil || push.Record == nil {
		return bad("is not a file deal ruling wrote")
	}
	read, err := xdealv1.VerifyBook(push.Record)
	if err != nil {
		return bad("does not verify: " + err.Error())
	}
	body, ok := read.Record.Body.(*xdealv1.RulingBody)
	switch {
	case !ok:
		return bad("holds a " + read.Record.Block.RecordType + " record, not a ruling")
	case read.KeyID != me:
		return bad("is not signed by your key, the adjudicator's")
	case read.LogID != g.LogID:
		return bad("is not in this deal's case book")
	case read.Record.Block.DealID != g.DealID || body.Row != g.Row || body.Keys != g.Keys:
		return bad("is not a ruling on this case's deal and row")
	}
	return *body, nil
}

// sourcesAgree is whether the case read now has the same sources for side as
// the sealed ruling did: each source present or not shown alike, and a fork
// the same. A check of the ruling read as far as it read, but from other
// sources (an answer added since, say), could decide what the ruling could not.
func sourcesAgree(g xdealv1.GatheredCase, body xdealv1.RulingBody, side string) bool {
	if g.Sources == nil || body.Sources == nil {
		return g.Sources == nil && body.Sources == nil
	}
	shown := func(n xdealv1.Null[string]) bool { return n.Present && !n.IsNull() }
	if side == xdealv1.RoleBuyer {
		now, then := g.Sources.Buyer, body.Sources.Buyer
		return shown(now.Own) == shown(then.Own) && shown(now.HeldBySeller) == shown(then.HeldBySeller) && now.ForkAt == then.ForkAt
	}
	now, then := g.Sources.Seller, body.Sources.Seller
	return shown(now.Own) == shown(then.Own) && shown(now.HeldByBuyer) == shown(then.HeldByBuyer) && now.ForkAt == then.ForkAt
}

func dealRulingCheckCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "As a deal's adjudicator, report whether the owing side's own records decide a disputed row (seals nothing)",
		Long: `Report whether a case's row is decided without a person: the side the
dispute is raised against (the side that did not sign it) has itself stated,
in a record it signed, that the row was not met, and has not stated otherwise
since. The output is either "decisive" (against, value not_met, and the
deciding record: its digest, record id, seq, record time and what timed it)
or "needs_person" with the reason. Free text, such as a delivery record's
observed, decides nothing, nor does the other side's word. A fork in that
side's book, no statement on the row, a later statement other than not_met,
or none of its records read each need a person.

The case is read as deal ruling reads it with --answer: the dispute you were
sent, the case-opening copies (--copy) and the parties' answers (--answer).
With --ruling, a ruling you sealed, the book of the side the dispute is
against is read only up to the last record the ruling read, so the check gives the answer the ruling was
sealed on, whatever the side stated since. Seals nothing.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			str := func(name string) string { v, _ := c.Flags().GetString(name); return strings.TrimSpace(v) }
			dealID, row, dispute, ruling := str("deal"), str("row"), str("dispute"), str("ruling")
			copies, _ := c.Flags().GetStringArray("copy")
			answers, _ := c.Flags().GetStringArray("answer")
			switch {
			case !cli.DealIDPattern.MatchString(dealID):
				return cli.InputError("--deal must be a deal id: deal- followed by 16 hex characters")
			case row == "":
				return cli.InputError("--row is required: the row the dispute is on")
			case dispute == "":
				return cli.InputError("--dispute is required: the dispute file you were sent (`deal dispute --out`)")
			case len(answers) == 0 || len(answers) > 2:
				return cli.InputError("give one or two --answer files: each party's answer to your request (`deal respond`)")
			case len(copies) > 2:
				return cli.InputError("give at most two --copy files: each party's `deal report --share adjudicator` copy that opened the case")
			case c.Flags().Changed("ruling") && ruling == "":
				return cli.InputError("--ruling is empty: give the ruling file you sealed, or leave the flag out")
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				if _, err := s.requireMigrated(ctx); err != nil {
					return err
				}
				me := hex.EncodeToString(s.key.Public().(ed25519.PublicKey))
				rec, cc, err := readCaseInputs(dispute, copies)
				if err != nil {
					return err
				}
				g, err := s.gatherAnswered(rec, cc, answers, dealID, me)
				if err != nil {
					return err
				}
				switch {
				case g.DealID != dealID:
					return cli.InputError("the dispute is of deal " + g.DealID + ", not " + dealID)
				case g.Row != row:
					return cli.InputError("the dispute is on row " + g.Row + ", not " + row)
				}
				var through xdealv1.Null[string]
				if ruling != "" {
					body, err := sealedRulingOf(ruling, g, me)
					if err != nil {
						return err
					}
					side := xdealv1.DecideCase(g, xdealv1.Null[string]{}).Against
					if !sourcesAgree(g, body, side) {
						return cli.InputError("--ruling " + ruling + " read the " + side + "'s book from other sources than these answers give: give the answers the ruling was made on")
					}
					if side == xdealv1.RoleBuyer {
						through = body.Read.Buyer
					} else {
						through = body.Read.Seller
					}
					if !through.Present {
						through = xdealv1.NullOf[string]()
					}
				}
				return cli.Output(c, xdealv1.DecideCase(g, through))
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal (required)")
	cmd.Flags().String("row", "", "The disputed row, as the offer names it (required)")
	cmd.Flags().String("dispute", "", "The dispute file you were sent (required)")
	cmd.Flags().StringArray("copy", nil, "A party's adjudicator copy that opened the case (deal report --share adjudicator); at most one per party")
	cmd.Flags().StringArray("answer", nil, "A party's full answer to your request for the deal (deal respond); at least one, one per party")
	cmd.Flags().String("ruling", "", "A ruling you sealed on this row (the file deal ruling wrote): read the side the dispute is against only up to what it read, from the same sources")
	return cmd
}

// checkDecidedBy refuses a ruling given --decided-by unless the check decides
// the gathered case's row on exactly that record, with this value and against
// that side: a ruling made without a person rules only on what the check
// decides. The ruling record does not carry the record id (Sherlock2, buzz
// 319f98a4: no wire change); checking again with --ruling gives it back.
func checkDecidedBy(g xdealv1.GatheredCase, f rulingFlags) error {
	d := xdealv1.DecideCase(g, xdealv1.Null[string]{})
	switch {
	case d.Decisive == nil:
		return cli.InputError("--decided-by " + f.decidedBy + ": the case needs a person: " + d.NeedsPerson)
	case d.Decisive.RecordID != f.decidedBy:
		return cli.InputError("--decided-by " + f.decidedBy + ": the check decides on record " + d.Decisive.RecordID + ", not this one")
	case f.value != d.Decisive.Value:
		return cli.InputError("--decided-by: the check finds " + d.Decisive.Value + ", not " + f.value)
	case !f.against.Present || f.against.IsNull() || *f.against.V != d.Decisive.Against:
		return cli.InputError("--decided-by: the check goes against the " + d.Decisive.Against + ", so --against must be " + d.Decisive.Against)
	}
	return nil
}
