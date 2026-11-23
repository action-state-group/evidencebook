package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
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
"entered" rather than "entered, not witnessed". It carries no record content,
only checkpoints, proofs and witness receipts. It stamps nothing: when no stamp
covers that checkpoint yet, run deal tick once one is due, then this again.`,
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
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				return s.checkpointProof(ctx, c, dealID, bundle, out)
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal (required)")
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
	last, ok, err := cad.Last(ctx)
	if err != nil {
		return err
	}
	var proof evidencebook.StampProof
	found := false
	for n := last.N; ok && n >= 1 && !found; n-- {
		p, err := cad.StampProof(ctx, d.OwnAgreementID, n, evidencebook.AudienceCounterparty)
		if errors.Is(err, evidencebook.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		v, err := evidencebook.VerifyStampProof(p)
		if err != nil {
			return err
		}
		if v.BookCheckpoint.Root == vb.Anchor.Root && v.BookCheckpoint.TreeSize == vb.Anchor.MMRSize {
			proof, found = p, true
		}
	}
	if !found {
		return cli.InputError("no stamp covers the checkpoint your copy rests on yet: run deal tick once one is due, then this again")
	}
	receipts, err := cad.Receipts(ctx, proof.ShelfCheckpoint)
	if err != nil {
		return err
	}
	data, err := json.Marshal(entryProof{StampProof: &proof, WitnessReceipts: receipts})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := cli.AtomicFile(out, data, false); err != nil {
		// The same file again is a re-run.
		if prev, rerr := os.ReadFile(out); rerr != nil || !bytes.Equal(prev, data) {
			return err
		}
	}
	return cli.Output(c, map[string]any{"deal_id": dealID, "file": out, "witness_receipts": len(receipts)})
}
