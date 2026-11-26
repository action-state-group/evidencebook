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
	decidedBy                               string
	against                                 xdealv1.Null[string]
	final                                   bool
	remedy                                  *xdealv1.RulingRemedy
	copies, answers                         []string
	bindings                                []json.RawMessage
}

func readRulingFlags(c *cobra.Command) (rulingFlags, error) {
	str := func(name string) string { v, _ := c.Flags().GetString(name); return strings.TrimSpace(v) }
	f := rulingFlags{dealID: str("deal"), row: str("row"), value: str("value"), forum: str("forum"), out: str("out"), dispute: str("dispute"),
		decidedBy: str("decided-by")}
	f.final, _ = c.Flags().GetBool("final")
	f.copies, _ = c.Flags().GetStringArray("copy")
	f.answers, _ = c.Flags().GetStringArray("answer")
	bindingPaths, _ := c.Flags().GetStringArray("binding")
	var err error
	if f.bindings, err = readJSONFiles("--binding", bindingPaths); err != nil {
		return f, err
	}
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
	switch {
	case len(f.copies) > 2:
		return f, cli.InputError("give at most two --copy files: each party's `deal report --share adjudicator` copy that opened the case")
	case len(f.answers) > 2:
		return f, cli.InputError("give at most two --answer files: each party's answer to your request (`deal respond`)")
	case len(f.copies)+len(f.answers) == 0:
		return f, cli.InputError("give each party's copy that opened the case (--copy) or its answer to your request (--answer), at least one")
	case c.Flags().Changed("decided-by") && f.decidedBy == "":
		return f, cli.InputError("--decided-by is empty: give the deciding record's id `deal ruling check` printed, or leave the flag out")
	case f.decidedBy != "" && len(f.answers) == 0:
		return f, cli.InputError("--decided-by needs the parties' answers (--answer): the check reads the case from their books")
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

With the parties' answers to your request for the deal (--answer, the file
deal respond wrote), the case is read from up to four books: each side's own
book and the copy of it the other side held. The ruling then names what it
read each side from (sources), a source not shown being "not shown to the
adjudicator", and the output lists each side's book read, with each record's
time and what timed it (own_stamp: the side's own stamps; holder_link: the
other side's held proofs). Where a side's book forked, the copy the other
side held governs and fork_at gives the position. A ruling may go against a
side that did not answer, but only on records that side signed: one against
a side none of whose records was read is refused. A party needs a
capsulectl-deal release that reads sources to take such a ruling: an older one
refuses it, so tell the parties the minimum release with the ruling.

Your rulings of a deal go in one case book, whose place both parties compute,
and a ruling counts only from that log. Your profile must checkpoint with your
record key, since the agreement names you by that key, and a profile's
checkpoint key is set when the profile is made. Make the adjudicator's profile
with one key for both:
  capsulectl --profile NAME deal init --dir DIR --signing-key-file KEY --checkpoint-key-file KEY
KEY is the key file you sign records with, so the key id your agreements name
stays; a new adjudicator makes one first, and gives the parties the public key
it prints:
  capsulectl key generate --output KEY
A profile that has checkpointed with another key cannot switch to it: make a
new one, with another name and --dir, leave the old store as it is, and seal
nothing more with it (one key, one live store). Your stamps go to the
profile's witness as every checkpoint does: hashes only.

The file carries your witness receipts for the stamp, which tell a party when
the ruling was stamped, and any controller bindings you give (--binding).
Run again with the same flags, it writes the same ruling and seals nothing new,
and writes the file again with the receipts your shelf holds by then.
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
	cmd.Flags().StringArray("copy", nil, "A party's adjudicator copy that opened the case (deal report --share adjudicator); at most one per party")
	cmd.Flags().StringArray("answer", nil, "A party's full answer to your request for the deal (deal respond); at most one per party")
	cmd.Flags().StringArray("binding", nil, "A controller-binding record between your key and a party's, to deliver with the ruling (repeatable)")
	cmd.Flags().String("out", "", "Where to write the file to send (default: standard output)")
	cmd.Flags().String("decided-by", "", "Rule only as `deal ruling check` decides: the deciding record's id it printed (with --answer)")
	cmd.AddCommand(dealRulingEntriesCommand())
	cmd.AddCommand(dealRulingCheckCommand())
	return cmd
}

// adjudicatorKeyAdvice is how an adjudicator's profile comes to checkpoint
// with its record key: made so, before its first checkpoint.
const adjudicatorKeyAdvice = "An adjudicator checkpoints with its record key, since the agreement names it by that key, and a profile's checkpoint key is set when the profile is made: " +
	"make a new adjudicator profile, with another name and --dir, using the key file you sign records with for both, so the key id your agreements name stays: `capsulectl --profile NEW deal init --dir NEWDIR --signing-key-file KEY --checkpoint-key-file KEY`. " +
	"Leave this profile's store as it is, and seal nothing more with it (one key, one live store)"

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
	// A profile whose store has stamped with another key cannot stamp with
	// this one (CLL refuses its stored checkpoints as corrupt): refuse before
	// sealing, with the way out (evidencebook#814). The newest finished tick
	// says which key: a stamp that failed on the key leaves an unfinished one
	// last.
	last, ok, err := cad.Last(ctx)
	if err != nil {
		return err
	}
	for n := last.N; ok && n >= 1; n-- {
		tick, err := cad.Get(ctx, n)
		if err != nil {
			return err
		}
		if tick.CheckpointSize == 0 {
			continue
		}
		record, err := checkpoint.ParseRecord(tick.Checkpoint)
		if err != nil {
			return err
		}
		if record.KeyID != me {
			return cli.InputError("this profile's store checkpointed with another key, so it cannot stamp a ruling with your record key; switching its checkpoint key back to the earlier one makes the store readable again. " + adjudicatorKeyAdvice)
		}
		break
	}
	dispute, copies, err := readCaseInputs(f.dispute, f.copies)
	if err != nil {
		return err
	}
	// The case rules are the library's, so every adjudicator applies the
	// same ones (evidencebook#779). With the parties' full answers, the case
	// is gathered from up to four books (evidencebook#861) and the ruling
	// names what it read each side from (sources).
	var (
		cs       xdealv1.Case
		gathered *xdealv1.GatheredCase
	)
	if len(f.answers) == 0 {
		if cs, err = xdealv1.CheckCase(dispute, copies, me); err != nil {
			return cli.InputError("the dispute and the copies are not a case for you: " + err.Error())
		}
	} else {
		g, err := s.gatherAnswered(dispute, copies, f.answers, f.dealID, me)
		if err != nil {
			return err
		}
		cs, gathered = g.Case, &g
	}
	switch {
	case cs.DealID != f.dealID:
		return cli.InputError("the dispute is of deal " + cs.DealID + ", not " + f.dealID)
	case cs.Row != f.row:
		return cli.InputError("the dispute is on row " + cs.Row + ", not " + f.row)
	}
	if f.decidedBy != "" {
		if err := checkDecidedBy(*gathered, f); err != nil {
			return err
		}
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
	case err != nil && gathered != nil && f.against.Present && !f.against.IsNull() && gathered.Side(*f.against.V).Read.IsNull() && len(gathered.Side(*f.against.V).Forked) == 0:
		// Steven's ruling 2 of 2026-11-24 (note §6): a ruling against a side turns only on
		// records that side signed, so a side none of whose records was read
		// cannot be ruled against.
		return cli.InputError("the ruling goes against the " + *f.against.V + ", but no record the " + *f.against.V + " signed was read: neither its own book nor the copy the other side held was shown, so a ruling cannot rest on its records")
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
	// The file carries the earliest stamp that covers the ruling, with the
	// receipts the shelf holds for it, which time the ruling for a party
	// (#819). A later run, once the witness's receipt is back, writes the
	// file again with it, and never moves to a later stamp.
	proof, receipts, err := earliestCaseProof(ctx, cad, sealed, proof)
	if err != nil {
		return err
	}
	data, err := json.Marshal(v1Push{Record: sealed.Pair, StampProof: &proof, WitnessReceipts: receipts, Bindings: f.bindings})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	result := map[string]any{"deal_id": f.dealID, "row": f.row, "ruling": sealed.Digest, "seq": sealed.Seq, "case_log": logID, "final": f.final, "sealed": !sealed.Existing}
	if gathered != nil {
		result["sources"] = cs.Sources
		result["books"] = map[string]any{xdealv1.RoleBuyer: gatheredSideOut(gathered.Buyer), xdealv1.RoleSeller: gatheredSideOut(gathered.Seller)}
	}
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
		return err == nil && v.BookCheckpoint.Covers(sealed.Seq)
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

// earliestCaseProof is the stamp proof the ruling's file carries: of the
// stamps that cover the ruling, the oldest one the shelf holds a witness
// receipt for, else the oldest one, with its receipts. newest is caseProof's,
// which covers the ruling. A stamp before the ruling, or one that is not a
// finished stamp, is passed over; the walk stops at the first stamp that no
// longer covers it, since older ones cover less.
func earliestCaseProof(ctx context.Context, cad *evidencebook.Cadence, sealed xdealv1.SealedRuling, newest evidencebook.StampProof) (evidencebook.StampProof, []evidencebook.WitnessReceipt, error) {
	oldest, oldestReceipted := newest, (*evidencebook.StampProof)(nil)
	var receipted []evidencebook.WitnessReceipt
	last, ok, err := cad.Last(ctx)
	if err != nil {
		return evidencebook.StampProof{}, nil, err
	}
	for n := last.N; ok && n >= 1; n-- {
		tick, err := cad.Get(ctx, n)
		if err != nil {
			return evidencebook.StampProof{}, nil, err
		}
		if tick.CheckpointSize == 0 || tick.Leaves.Stamp == nil {
			continue
		}
		p, err := cad.StampProof(ctx, sealed.BookID, n, evidencebook.AudienceCounterparty)
		if errors.Is(err, evidencebook.ErrNotFound) {
			break
		}
		if err != nil {
			return evidencebook.StampProof{}, nil, err
		}
		v, err := evidencebook.VerifyStampProof(p)
		if err != nil {
			return evidencebook.StampProof{}, nil, err
		}
		if !v.BookCheckpoint.Covers(sealed.Seq) {
			break
		}
		oldest = p
		rs, err := cad.Receipts(ctx, p.ShelfCheckpoint)
		if err != nil {
			return evidencebook.StampProof{}, nil, err
		}
		if len(rs) > 0 {
			q := p
			oldestReceipted, receipted = &q, rs
		}
	}
	if oldestReceipted != nil {
		return *oldestReceipted, receipted, nil
	}
	rs, err := cad.Receipts(ctx, oldest.ShelfCheckpoint)
	return oldest, rs, err
}
