package deal

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal ruling entries` is the adjudicator's report of whether each party
// entered its ruling (evidencebook#773; Ethan, 2026-11-23: option one). It
// reads each party's copy (`deal report --share ... --bundle`, the disclose
// act a party's book leaves by, Sherlock2 f77127fd) with xdealv1.EntryOf.
// The ruling's time is its witnessed stamp (the case book record's time
// from a witness the agreement names, Shelf.RecordTimeByKey), never its own
// at (Sherlock2, #785). It seals nothing, and never says a party refused.

// entryWords are the words for each state (#773, #801). A copy that does
// not show the entry says why: the ruling has no witnessed time, or the
// copy's checkpoint is not after it.
func entryWords(e xdealv1.Entry, rulingAt string) string {
	switch e.State {
	case xdealv1.EntryEntered:
		return "entered"
	case xdealv1.EntryUnwitnessed:
		return "entered, not witnessed"
	case xdealv1.EntryNotEntered:
		return "ruling not entered by the " + e.Role + " as of its checkpoint at " + e.CheckpointAt
	}
	if rulingAt == "" {
		return "not shown entered (the ruling has no witnessed time)"
	}
	return "not shown entered (no checkpoint after the ruling's witnessed time)"
}

// entryProof is a party's stamp proof file with the witness receipts it
// holds for that checkpoint: hashes only, so not a disclose act.
type entryProof struct {
	StampProof      *evidencebook.StampProof      `json:"stamp_proof"`
	WitnessReceipts []evidencebook.WitnessReceipt `json:"witness_receipts,omitempty"`
}

func dealRulingEntriesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "entries",
		Short: "As a deal's adjudicator, show whether each party entered your ruling (seals nothing)",
		Long: `Show, for each party whose copy you give (--copy, from deal report --share
adjudicator --bundle), whether it entered your ruling in its deal book:
"entered", "entered, not witnessed", "ruling not entered by the <party> as of
its checkpoint at <time>", or "not shown entered". The ruling's witnessed time
is the time a witness the agreement names gave your stamp that covers it. A
party is shown as not having entered a ruling only by a checkpoint after that
time; with no witnessed time, a copy without the entry is "not shown entered". --proof gives a party's stamp proof file with its witness receipts,
for "entered" rather than "entered, not witnessed". Seals nothing.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			str := func(name string) string { v, _ := c.Flags().GetString(name); return strings.TrimSpace(v) }
			dealID, ruling := str("deal"), str("ruling")
			copies, _ := c.Flags().GetStringArray("copy")
			proofs, _ := c.Flags().GetStringArray("proof")
			switch {
			case !cli.DealIDPattern.MatchString(dealID):
				return cli.InputError("--deal must be a deal id: deal- followed by 16 hex characters")
			case len(ruling) != 64 || !cli.IsLowerHex(ruling):
				return cli.InputError("--ruling must be the ruling's digest, as deal ruling printed it (64 lowercase hex)")
			case len(copies) == 0:
				return cli.InputError("give each party's copy with --copy (deal report --share adjudicator --bundle)")
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				return s.entries(ctx, c, dealID, ruling, copies, proofs)
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal (required)")
	cmd.Flags().String("ruling", "", "The ruling's digest, as deal ruling printed it (required)")
	cmd.Flags().StringArray("copy", nil, "A party's copy (deal report --share adjudicator --bundle); one per party")
	cmd.Flags().StringArray("proof", nil, "A party's stamp proof file with its witness receipts (optional)")
	return cmd
}

func (s *dealSession) entries(ctx context.Context, c *cobra.Command, dealID, ruling string, copies, proofs []string) error {
	if _, err := s.requireMigrated(ctx); err != nil {
		return err
	}
	me := hex.EncodeToString(s.key.Public().(ed25519.PublicKey))
	dir, err := pinnedWitnessDirectory(s.p)
	if err != nil {
		return err
	}
	var held []entryProof
	for _, path := range proofs {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cli.InputError("cannot read --proof " + path)
		}
		var p entryProof
		if err := json.Unmarshal(raw, &p); err != nil || p.StampProof == nil {
			return cli.InputError("--proof " + path + " is not a stamp proof file")
		}
		held = append(held, p)
	}
	var out []map[string]any
	for _, path := range copies {
		bundle, err := os.ReadFile(path)
		if err != nil {
			return cli.InputError("cannot read --copy " + path)
		}
		rulingAt, sentAt, err := s.rulingTimes(ctx, bundle, dealID, ruling, me)
		if err != nil {
			return err
		}
		in := xdealv1.EntryInput{Bundle: bundle, Ruling: ruling, RulingAt: rulingAt, AdjudicatorKey: me, Witnesses: dir}
		// A proof counts for the copy whose checkpoint it carries; EntryOf
		// checks the binding, so trying each is safe.
		var e xdealv1.Entry
		for i := range held {
			in.StampProof, in.Receipts = held[i].StampProof, held[i].WitnessReceipts
			if e, err = xdealv1.EntryOf(in); err == nil && e.State == xdealv1.EntryEntered {
				break
			}
		}
		if e.State != xdealv1.EntryEntered {
			in.StampProof, in.Receipts = nil, nil
			if e, err = xdealv1.EntryOf(in); err != nil {
				return cli.InputError("--copy " + path + " cannot be read for the entry check: " + err.Error())
			}
		}
		row := map[string]any{"party": e.Role, "state": e.State, "words": entryWords(e, rulingAt), "checkpoint_at": e.CheckpointAt,
			"sent": "sent " + sentAt + " (adjudicator's statement)"}
		if rulingAt != "" {
			row["ruling_witnessed_at"] = rulingAt
		}
		out = append(out, row)
	}
	return cli.Output(c, map[string]any{"deal_id": dealID, "ruling": ruling, "entries": out})
}

// rulingTimes are the ruling's witnessed time (RFC 3339 UTC, "" when no
// witness the copy's agreement names timed it) and its own at, the
// adjudicator's statement of when it was sent. The ruling is found in this
// adjudicator's case book of the copy's deal.
func (s *dealSession) rulingTimes(ctx context.Context, bundle []byte, dealID, ruling, me string) (string, string, error) {
	vb, err := evidencebook.VerifyBundle(bundle)
	if err != nil {
		return "", "", cli.InputError("the copy is not a bundle that verifies: " + err.Error())
	}
	ext := vb.Extensions[xdealv1.Profile]
	var x xdealv1.ReportBundle
	if err := json.Unmarshal(ext, &x); err != nil || ext == nil {
		return "", "", cli.InputError("the copy is not an x-deal-v1 report copy (deal report --share adjudicator --bundle)")
	}
	first, err := xdealv1.VerifyBook(x.Book.Agreement)
	if err != nil {
		return "", "", cli.InputError("the copy's agreement does not verify: " + err.Error())
	}
	ag, ok := agreeOf(first.Record)
	if !ok || first.Record.Block.DealID != dealID {
		return "", "", cli.InputError("the copy is not of deal " + dealID)
	}
	logID := xdealv1.CaseLogID(ag.Offer.Digest, me)
	held, err := s.shelf.Books(ctx)
	if err != nil {
		return "", "", err
	}
	if !slices.Contains(held, logID) {
		return "", "", cli.InputError("you hold no case book of deal " + dealID + ": run deal ruling first")
	}
	book, err := s.shelf.Open(ctx, logID)
	if err != nil {
		return "", "", err
	}
	if book.Size() == 0 {
		return "", "", cli.InputError("you hold no case book of deal " + dealID + ": run deal ruling first")
	}
	rows, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: book.Size()})
	if err != nil {
		return "", "", err
	}
	keys := make([]string, 0, len(ag.Witnesses))
	for _, w := range ag.Witnesses {
		keys = append(keys, w.KeyID)
	}
	for i, r := range rows {
		if len(r.Header.PayloadCommitments) != 1 || r.Header.PayloadCommitments[0] != ruling {
			continue
		}
		payload, err := book.ResolvePayload(ctx, r.RecordID, r.Header.PayloadCommitments[0])
		if err != nil {
			return "", "", err
		}
		rec, err := xdealv1.Decode(payload)
		if err != nil {
			return "", "", err
		}
		at, witnessed, err := s.shelf.RecordTimeByKey(ctx, rows[0].RecordID, uint64(i+1), keys)
		if err != nil {
			return "", "", err
		}
		if !witnessed {
			return "", rec.Block.At, nil
		}
		return at.UTC().Format(time.RFC3339), rec.Block.At, nil
	}
	return "", "", cli.InputError("your case book of deal " + dealID + " holds no ruling " + ruling)
}
