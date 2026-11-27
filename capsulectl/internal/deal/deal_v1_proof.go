package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal checkpoint proof` writes, for the checkpoint a party's copy rests
// on, the stamp proof of its x-deal-v1 deal book and the witness receipts
// its shelf holds for that cadence checkpoint: the file `deal ruling
// entries --proof` reads to say "entered" rather than "entered, not
// witnessed" (evidencebook#788). It carries no record content, only
// checkpoints, proofs and witness receipts, so it is not a disclose act
// (Sherlock2, #773). It stamps nothing.

func dealCheckpointProofCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proof",
		Short: "The stamp proof and witness receipts for the checkpoint your copy rests on, for the adjudicator's entry check; read-only: never seals, stamps or contacts the witness (--out writes the file)",
		Long: `Write, for the checkpoint your copy rests on (--copy, the file deal report
--share ... --bundle wrote), the stamp proof of your x-deal-v1 deal book and the
witness receipts your shelf holds for that cadence checkpoint. Send the file to
the adjudicator with your copy: deal ruling entries --proof reads it to say
"entered" rather than "entered, not witnessed", when a witness your agreement
names has a receipt for that checkpoint. With no such receipt yet, the file
changes nothing: the entry stays "entered, not witnessed", and the output says
so; run this again once the witness's receipt has come back, and it replaces
the file. It carries no record content, only checkpoints, proofs and witness
receipts. It stamps nothing: when no stamp covers that checkpoint yet, run
deal tick once one is due, then this again.

On an x-deal-v0 deal, the file is your book's witnessed cadence chain for that
checkpoint: deal verify --bundle COPY --stamp-proof FILE --witness-directory W
reads it and reports the copy's witness time, and deal report --html
--witness-directory W puts that time on your own page. Keep this file to
yourself: it shows your book's position and cadence, which is why no copy you
share carries it. A chain witnessed only for an earlier checkpoint gives no
time for this copy.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			str := func(name string) string { v, _ := c.Flags().GetString(name); return strings.TrimSpace(v) }
			dealID, copyPath, out := str("deal"), str("copy"), str("out")
			switch {
			case !cli.DealIDPattern.MatchString(dealID):
				return cli.InputError("--deal must be a deal id: deal- followed by 16 hex characters")
			case copyPath == "":
				return cli.InputError("--copy is required: your copy, as deal report --share ... --bundle wrote it")
			case out == "":
				return cli.InputError("--out is required: where to write the proof file")
			}
			bundle, err := os.ReadFile(copyPath)
			if err != nil {
				return cli.InputError("cannot read --copy " + copyPath)
			}
			// An x-deal-v0 deal's proof is the holder's cadence chain
			// (deal_v0_witnessed.go).
			if v1, err := v1DealOfFlag(c); err != nil {
				return err
			} else if !v1 {
				return runDeal(c, true, func(ctx context.Context, s *dealSession, _ string, events []sealedEvent) error {
					return s.v0CheckpointProof(ctx, c, dealID, bundle, out, events)
				})
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				return s.checkpointProof(ctx, c, dealID, bundle, out)
			})
		},
	}
	cmd.Flags().String("deal", "", "The deal (required)")
	cmd.Flags().String("copy", "", "Your copy, as deal report --share ... --bundle wrote it (required)")
	cmd.Flags().String("out", "", "Where to write the proof file (required)")
	return cmd
}

func (s *dealSession) checkpointProof(ctx context.Context, c *cobra.Command, dealID string, bundle []byte, out string) error {
	cad, err := s.requireMigrated(ctx)
	if err != nil {
		return err
	}
	if err := s.ensureV1Tables(ctx); err != nil {
		return err
	}
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return err
	}
	if d == nil || d.OwnAgreementID == "" {
		return cli.InputError("deal " + dealID + " is not an x-deal-v1 deal with a book of yours")
	}
	vb, err := evidencebook.VerifyBundle(bundle)
	if err != nil {
		return cli.InputError("the copy does not verify: " + err.Error())
	}
	if vb.LogID != d.OwnLogID {
		return cli.InputError("the copy is not of your deal book of deal " + dealID)
	}
	named, err := copyWitnessKeys(vb)
	if err != nil {
		return err
	}
	dir, err := pinnedWitnessDirectory(s.p)
	if err != nil {
		return err
	}
	best, err := bestStampProof(ctx, cad, d.OwnAgreementID, vb, dir, named)
	if err != nil {
		return err
	}
	if best == nil {
		return cli.InputError("no stamp covers the checkpoint your copy rests on yet: run deal tick once one is due, then this again")
	}
	data, err := json.Marshal(entryProof{StampProof: &best.proof, WitnessReceipts: best.receipts})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := cli.AtomicFile(out, data, isProofFile(out)); err != nil {
		if prev, rerr := os.ReadFile(out); rerr == nil && bytes.Equal(prev, data) {
			// The same file again is a re-run.
		} else if rerr == nil && !isProofFile(out) {
			return cli.InputError("--out " + out + " exists and is not a proof file: choose another --out")
		} else {
			return err
		}
	}
	result := map[string]any{"deal_id": dealID, "file": out, "witness_receipts": len(best.receipts), "named_witness_receipts": best.named}
	// named counts under this holder's pinned witness; the adjudicator
	// verifies under its own directory, so a held receipt that does not
	// verify here may still count there.
	switch {
	case len(best.receipts) == 0:
		result["note"] = "no witness receipt is held for this checkpoint yet: the file changes nothing, and the entry stays \"entered, not witnessed\""
	case best.named == 0:
		result["note"] = "no receipt held for this checkpoint verifies, under your pinned witness, as from a witness your agreement names: unless the adjudicator's witness directory verifies one, the entry stays \"entered, not witnessed\""
	}
	return cli.Output(c, result)
}

// copyWitnessKeys are the key ids of the witnesses the copy's agreement names.
func copyWitnessKeys(vb evidencebook.VerifiedBundle) ([]string, error) {
	var x xdealv1.ReportBundle
	if err := json.Unmarshal(vb.Extensions[xdealv1.Profile], &x); err != nil || vb.Extensions[xdealv1.Profile] == nil {
		return nil, cli.InputError("the copy is not an x-deal-v1 report copy (deal report --share ... --bundle)")
	}
	first, err := xdealv1.VerifyBook(x.Book.Agreement)
	if err != nil {
		return nil, cli.InputError("the copy's agreement does not verify: " + err.Error())
	}
	ag, ok := agreeOf(first.Record)
	if !ok {
		return nil, cli.InputError("the copy's first record is not an agreement")
	}
	keys := make([]string, 0, len(ag.Witnesses))
	for _, w := range ag.Witnesses {
		keys = append(keys, w.KeyID)
	}
	return keys, nil
}

// namedReceipts counts the receipts that verify, under this holder's pinned
// witness directory, as from a witness the agreement names: those the
// adjudicator's entry check counts.
func namedReceipts(statement []byte, receipts []evidencebook.WitnessReceipt, dir witness.Directory, named []string) int {
	n := 0
	for _, rc := range receipts {
		check, keyID, err := evidencebook.VerifyWitnessReceipt(statement, rc, dir)
		if err == nil && check.State == evidencebook.WitnessVerified && slices.Contains(named, keyID) {
			n++
		}
	}
	return n
}

// isProofFile reports whether path holds a proof file this verb wrote, which
// a later run may replace (it holds no record content).
func isProofFile(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var p entryProof
	if json.Unmarshal(raw, &p) == nil && p.StampProof != nil && len(p.StampProof.ShelfCheckpoint) > 0 {
		return true
	}
	var v0 v0ChainProof
	return json.Unmarshal(raw, &v0) == nil && v0.Kind == v0ChainProofKind
}

// stampCandidate is a stamp proof of the deal book with the witness receipts
// the shelf holds for its shelf checkpoint, and how many of them verify, under
// the directory, as from a witness the agreement names.
type stampCandidate struct {
	proof    evidencebook.StampProof
	receipts []evidencebook.WitnessReceipt
	named    int
}

// bestStampProof is the proof `checkpoint proof` writes for the checkpoint
// vb rests on: every finished stamp after that checkpoint covers it while the
// book is unchanged; it is the newest of them that a named witness registered,
// else the newest one with any receipt, else the newest. nil when no stamp
// covers it yet.
func bestStampProof(ctx context.Context, cad *evidencebook.Cadence, agreementID string, vb evidencebook.VerifiedBundle, dir witness.Directory, named []string) (*stampCandidate, error) {
	last, ok, err := cad.Last(ctx)
	if err != nil {
		return nil, err
	}
	// Every finished stamp after the copy's checkpoint covers it while the
	// book is unchanged; the proof is the newest of them that a named witness
	// registered, else the newest one with any receipt, else the newest.
	var best *stampCandidate
	for n := last.N; ok && n >= 1; n-- {
		tick, err := cad.Get(ctx, n)
		if err != nil {
			return nil, err
		}
		if tick.CheckpointSize == 0 || tick.Leaves.Stamp == nil {
			continue // not a finished stamp: an interrupted tick, or one from before the shelf's stamps
		}
		// The adjudicator's audience: the fact's link opening, never its
		// status opening (OpeningsFor).
		p, err := cad.StampProof(ctx, agreementID, n, evidencebook.AudienceAdjudicator)
		if errors.Is(err, evidencebook.ErrNotFound) {
			break // the book has no fact at this stamp, nor at any older one
		}
		if err != nil {
			return nil, err
		}
		v, err := evidencebook.VerifyStampProof(p)
		if err != nil {
			return nil, err
		}
		if v.BookCheckpoint.TreeSize < vb.Anchor.MMRSize {
			break // older stamps cover only older checkpoints of the book
		}
		if v.BookCheckpoint.Root != vb.Anchor.Root || v.BookCheckpoint.TreeSize != vb.Anchor.MMRSize {
			continue
		}
		receipts, err := cad.Receipts(ctx, p.ShelfCheckpoint)
		if err != nil {
			return nil, err
		}
		got := stampCandidate{proof: p, receipts: receipts, named: namedReceipts(p.ShelfCheckpoint, receipts, dir, named)}
		if best == nil || (best.named == 0 && got.named > 0) || (best.named == 0 && len(best.receipts) == 0 && len(got.receipts) > 0) {
			best = &got
		}
		if got.named > 0 {
			break
		}
	}
	return best, nil
}
