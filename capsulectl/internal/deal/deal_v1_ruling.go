package deal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal ruling` is the adjudicator's verb (evidencebook#773): it seals a
// `ruling` on one row of an x-deal-v1 deal in the adjudicator's case book of
// that deal (one per deal and adjudicator, at xdealv1.CaseLogID, evidencebook
// #615), stamps it, and writes the file both parties take with `deal
// receive`, where the receive names the ruling in each party's book. The
// inputs are the dispute the adjudicator was sent and one or both parties'
// adjudicator copies (`deal report --share adjudicator`), each checked before
// anything is sealed. The adjudicator's profile checkpoints with its record
// key, since the agreement names it by that one key and a case book's stamp
// proof binds to it. The verdict is the adjudicator's own decision; this
// verb only records it.

// readCaseCopyFile reads a party's adjudicator copy (`deal report --share
// adjudicator --bundle`): the bundle must verify, and its report and
// x-deal-v1 extension are what xdealv1.CheckCase reads.
func readCaseCopyFile(path string) (xdealv1.CaseCopy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return xdealv1.CaseCopy{}, cli.InputError("cannot read --copy " + path)
	}
	bad := func(why string) (xdealv1.CaseCopy, error) {
		return xdealv1.CaseCopy{}, cli.InputError("--copy " + path + " " + why)
	}
	decoded, err := cli.DecodeBundleJSON(raw)
	if err != nil {
		return bad("is not a bundle: " + err.Error())
	}
	if err := cli.VerifyProducedBundle(decoded, true); err != nil {
		return bad("does not verify: " + err.Error())
	}
	var b struct {
		Extensions map[string]json.RawMessage `json:"extensions"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return bad("is not a bundle: " + err.Error())
	}
	report, ext := b.Extensions[dealprofile.ReportExtension], b.Extensions[xdealv1.Profile]
	if report == nil || ext == nil {
		return bad("is not an x-deal-v1 report copy: write it with `deal report --share adjudicator --bundle`")
	}
	return xdealv1.CaseCopy{Report: report, Extension: ext}, nil
}

// rulingFlags are the verb's flags, read and checked before the store opens.
type rulingFlags struct {
	dealID, row, value, forum, out, dispute string
	against                                 xdealv1.Null[string]
	final                                   bool
	remedy                                  *xdealv1.RulingRemedy
	copies                                  []string
}

func readRulingFlags(c *cobra.Command) (rulingFlags, error) {
	str := func(name string) string { v, _ := c.Flags().GetString(name); return strings.TrimSpace(v) }
	f := rulingFlags{dealID: str("deal"), row: str("row"), value: str("value"), forum: str("forum"), out: str("out"), dispute: str("dispute")}
	f.final, _ = c.Flags().GetBool("final")
	f.copies, _ = c.Flags().GetStringArray("copy")
	if !cli.DealIDPattern.MatchString(f.dealID) {
		return f, cli.InputError("--deal must be a deal id: deal- followed by 16 hex characters")
	}
	if f.row == "" {
		return f, cli.InputError("--row is required: the row the dispute is on")
	}
	if f.value != xdealv1.ValueMet && f.value != xdealv1.ValueNotMet {
		return f, cli.InputError("--value is required: met or not_met")
	}
	switch against := str("against"); against {
	case xdealv1.RoleBuyer, xdealv1.RoleSeller:
		f.against = xdealv1.Some(against)
	case "none":
		f.against = xdealv1.NullOf[string]()
	default:
		return f, cli.InputError("--against is required: buyer, seller or none")
	}
	if f.dispute == "" {
		return f, cli.InputError("--dispute is required: the dispute file you were sent (`deal dispute --out`)")
	}
	if len(f.copies) < 1 || len(f.copies) > 2 {
		return f, cli.InputError("give one or two --copy files: each party's `deal report --share adjudicator` copy")
	}
	kind, due, currency := str("remedy-kind"), str("remedy-due"), str("currency")
	amount, _ := c.Flags().GetInt64("amount-minor")
	amountSet := c.Flags().Changed("amount-minor")
	switch {
	case kind == "" && (due != "" || currency != "" || amountSet):
		return f, cli.InputError("--remedy-due, --amount-minor and --currency go with --remedy-kind")
	case kind != "" && due == "":
		return f, cli.InputError("--remedy-kind needs --remedy-due: when the remedy is due")
	case amountSet != (currency != ""):
		return f, cli.InputError("--amount-minor and --currency go together")
	case kind != "":
		f.remedy = &xdealv1.RulingRemedy{Kind: kind, Due: due, Currency: currency}
		if amountSet {
			f.remedy.AmountMinor = &amount
		}
	}
	return f, nil
}

func dealRulingCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ruling",
		Short: "As a deal's adjudicator, seal your ruling on a disputed row and write the file both parties take",
		Long: `As the adjudicator an x-deal-v1 agreement names, seal your ruling on one row
of a deal, stamp it (a new stamp when none covers it yet), and write the file to
send to both parties (--out, or standard output with none). Each party takes it
with deal receive, which enters it in their deal book.

The ruling is your decision; this command records it. It is checked against
the dispute you were sent (--dispute, the file deal dispute wrote) and one or
both parties' adjudicator copies (--copy, from deal report --share
adjudicator): the dispute must name you and the row, and each copy must verify
and name you as adjudicator. A side you hold no copy of is recorded as not
read.

Your rulings of a deal go in one case book, whose place both parties compute,
and a ruling counts only from that log. Your profile must checkpoint with your
record key, since the agreement names you by that key: make it with one key for
both, before it checkpoints anything,
  capsulectl --profile NAME deal init --dir DIR --signing-key-file KEY --checkpoint-key-file KEY
A profile that has already checkpointed with another key cannot switch to it.

Run again with the same flags, it writes the same ruling and seals nothing new.
A second final ruling on a row that differs from the first is refused.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			f, err := readRulingFlags(c)
			if err != nil {
				return err
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				return s.rule(ctx, c, f)
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal (required)")
	cmd.Flags().String("row", "", "The disputed row, as the offer names it (required)")
	cmd.Flags().String("value", "", "Your finding on the row: met or not_met (required)")
	cmd.Flags().String("against", "", "Who the ruling goes against: buyer, seller or none (required)")
	cmd.Flags().String("forum", "", "The forum you rule in, one of the agreement's (default: the dispute's)")
	cmd.Flags().Bool("final", false, "A final ruling (the first final ruling on a row is the one that applies)")
	cmd.Flags().String("remedy-kind", "", "The remedy you order, such as refund")
	cmd.Flags().String("remedy-due", "", "When the remedy is due (RFC 3339)")
	cmd.Flags().Int64("amount-minor", 0, "The remedy's amount in minor units (with --currency)")
	cmd.Flags().String("currency", "", "The remedy's currency (with --amount-minor)")
	cmd.Flags().String("dispute", "", "The dispute file you were sent (required)")
	cmd.Flags().StringArray("copy", nil, "A party's adjudicator copy (deal report --share adjudicator); give one or two")
	cmd.Flags().String("out", "", "Where to write the file to send (default: standard output)")
	cmd.AddCommand(dealRulingEntriesCommand())
	return cmd
}

// adjudicatorKeyAdvice is how an adjudicator's profile comes to checkpoint
// with its record key: made so, before its first checkpoint.
const adjudicatorKeyAdvice = "An adjudicator checkpoints with its record key, since the agreement names it by that key: make its profile with one key for both, before it checkpoints anything: " +
	"`capsulectl --profile NAME deal init --dir DIR --signing-key-file KEY --checkpoint-key-file KEY` (KEY: the key file you sign records with)"

// rule checks the inputs, seals the ruling once, stamps it and writes the push.
func (s *dealSession) rule(ctx context.Context, c *cobra.Command, f rulingFlags) error {
	cad, err := s.requireMigrated(ctx)
	if err != nil {
		return err
	}
	me := hex.EncodeToString(s.key.Public().(ed25519.PublicKey))
	ckey, err := cli.PrivateKey(s.p.Checkpoint.Signing)
	if err != nil {
		return err
	}
	if !bytes.Equal(ckey.Public().(ed25519.PublicKey), s.key.Public().(ed25519.PublicKey)) {
		return cli.InputError(adjudicatorKeyAdvice)
	}
	// A profile whose store already checkpointed with another key cannot stamp
	// with this one (CLL refuses its stored checkpoints as corrupt): refuse
	// before sealing, with the way out (evidencebook#814).
	if last, ok, err := cad.Last(ctx); err != nil {
		return err
	} else if ok && last.CheckpointSize > 0 {
		record, err := checkpoint.ParseRecord(last.Checkpoint)
		if err != nil {
			return err
		}
		if record.KeyID != me {
			return cli.InputError("this profile's store checkpointed with another key, so it cannot stamp a ruling with your record key. " + adjudicatorKeyAdvice)
		}
	}
	raw, err := os.ReadFile(f.dispute)
	if err != nil {
		return cli.InputError("cannot read --dispute " + f.dispute)
	}
	var push v1Push
	if err := json.Unmarshal(raw, &push); err != nil || push.Record == nil {
		return cli.InputError("--dispute " + f.dispute + " is not a file deal dispute wrote")
	}
	var copies []xdealv1.CaseCopy
	for _, path := range f.copies {
		cc, err := readCaseCopyFile(path)
		if err != nil {
			return err
		}
		copies = append(copies, cc)
	}
	// The case rules are the library's, so every adjudicator applies the
	// same ones (evidencebook#779).
	cs, err := xdealv1.CheckCase(push.Record, copies, me)
	if err != nil {
		return cli.InputError("the dispute and the copies are not a case for you: " + err.Error())
	}
	switch {
	case cs.DealID != f.dealID:
		return cli.InputError("the dispute is of deal " + cs.DealID + ", not " + f.dealID)
	case cs.Row != f.row:
		return cli.InputError("the dispute is on row " + cs.Row + ", not " + f.row)
	}
	body := cs.RulingOf(f.value, f.against, f.remedy, f.final, f.forum)
	// A file already at --out is an earlier run's only if it holds this same
	// ruling of this case book: an old command run again must not seal a
	// ruling only to fail on its file.
	if f.out != "" {
		if err := sameRulingFile(f.out, cs.LogID, body); err != nil {
			return err
		}
	}
	sealed, err := xdealv1.SealRuling(ctx, s.shelf, cs, body, cli.DealClock())
	switch {
	case errors.Is(err, xdealv1.ErrRulingConflict):
		return cli.InputError("row " + f.row + " of deal " + f.dealID + " already has your final ruling, which differs from this one: the first final ruling is the one that applies")
	case errors.Is(err, evidencebook.ErrBusy):
		return fmt.Errorf("%w: %v; run the command again", cli.ErrConflict, err)
	case err != nil:
		var se *xdealv1.StageError
		if errors.As(err, &se) {
			return cli.InputError("the ruling does not pass the x-deal-v1 checks: " + err.Error())
		}
		return err
	}
	logID := cs.LogID
	proof, err := s.caseProof(ctx, cad, sealed)
	if err != nil {
		return err
	}
	data, err := json.Marshal(v1Push{Record: sealed.Pair, StampProof: &proof})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	result := map[string]any{"deal_id": f.dealID, "row": f.row, "ruling": sealed.Digest, "seq": sealed.Seq, "case_log": logID, "final": f.final, "sealed": !sealed.Existing}
	if f.out == "" {
		_, err = c.OutOrStdout().Write(data)
		return err
	}
	if err := cli.AtomicFile(f.out, data, false); err != nil {
		// A file already there is an earlier run's only if it holds this
		// ruling; it is written again with the proof that covers it, so the
		// file the output describes is the file on disk.
		prev, rerr := os.ReadFile(f.out)
		var was v1Push
		if rerr != nil || json.Unmarshal(prev, &was) != nil || !bytes.Equal(was.Record, sealed.Pair) {
			return err
		}
		if !bytes.Equal(prev, data) {
			if err := cli.AtomicFile(f.out, data, true); err != nil {
				return err
			}
		}
	}
	result["file"] = f.out
	return cli.Output(c, result)
}

// caseProof is a stamp proof covering the ruling: the newest one when it
// does, else after a new stamp.
func (s *dealSession) caseProof(ctx context.Context, cad *evidencebook.Cadence, sealed xdealv1.SealedRuling) (evidencebook.StampProof, error) {
	covers := func(p evidencebook.StampProof) bool {
		v, err := evidencebook.VerifyStampProof(p)
		return err == nil && v.BookCheckpoint.TreeSize >= sealed.Seq
	}
	proof, err := cad.StampProof(ctx, sealed.BookID, 0, evidencebook.AudienceCounterparty)
	if err == nil && covers(proof) {
		return proof, nil
	}
	if _, err := cad.Stamp(ctx, cli.DealClock()); err != nil {
		return evidencebook.StampProof{}, err
	}
	proof, err = cad.StampProof(ctx, sealed.BookID, 0, evidencebook.AudienceCounterparty)
	if err != nil {
		return evidencebook.StampProof{}, err
	}
	if !covers(proof) {
		return evidencebook.StampProof{}, fmt.Errorf("%w: the case book's newest stamp does not cover the ruling", cli.ErrConflict)
	}
	return proof, nil
}

// sameRulingFile refuses an existing file at out unless it is a push of a
// ruling in the case book logID with body: the earlier run of the same
// command. A missing file is fine.
func sameRulingFile(out, logID string, body xdealv1.RulingBody) error {
	prev, err := os.ReadFile(out)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	refuse := cli.InputError(out + " exists and holds another record: --out names a new file, or the file an earlier run of this same ruling wrote")
	var was v1Push
	if json.Unmarshal(prev, &was) != nil || was.Record == nil {
		return refuse
	}
	read, err := xdealv1.VerifyBook(was.Record)
	if err != nil || read.LogID != logID {
		return refuse
	}
	rb, ok := read.Record.Body.(*xdealv1.RulingBody)
	if !ok {
		return refuse
	}
	got, err1 := json.Marshal(rb)
	want, err2 := json.Marshal(body)
	if err1 != nil || err2 != nil || !bytes.Equal(got, want) {
		return refuse
	}
	return nil
}
