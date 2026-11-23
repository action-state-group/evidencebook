package deal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// runChallenge is `deal dispute --challenge RULING --grounds FILE`
// (evidencebook#773): it seals a dispute of kind challenge in the holder's
// deal book, naming a ruling of the deal the holder holds and the SHA-256 of
// the grounds file, and writes the file to send. A challenge changes no row
// status (Sherlock2, evidencebook#773: the first final ruling stays
// effective, and the adjudicator may rule again); the record shows that the
// party objected, and when. The same challenge run again writes the same
// record and seals nothing.
func runChallenge(c *cobra.Command, dealID, ruling, grounds, out string, withRowFlags bool) error {
	switch {
	case withRowFlags:
		return cli.InputError("--challenge takes no --row, --forum or --withdraw: it names a ruling, which names its row")
	case ruling == "":
		return cli.InputError("--grounds goes with --challenge RULING: the digest of the ruling you challenge")
	case len(ruling) != 64 || !cli.IsLowerHex(ruling):
		return cli.InputError("--challenge must be the ruling's digest, as deal receive printed it (64 lowercase hex)")
	case grounds == "":
		return cli.InputError("--challenge needs --grounds FILE: your grounds, of which only the SHA-256 is sealed")
	}
	raw, err := os.ReadFile(grounds)
	if err != nil {
		return cli.InputError("cannot read --grounds " + grounds)
	}
	sum := sha256.Sum256(raw)
	commitment := hex.EncodeToString(sum[:])
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
		ag, ok := agreeOf(book[0])
		if !ok || ag.Kind != xdealv1.KindAgreement {
			return cli.InputError("the first record of your deal book is not your agreement")
		}
		if ag.Adjudicator.V == nil {
			return cli.InputError("the agreement of deal " + dealID + " names no adjudicator, so it has no ruling to challenge")
		}
		_, found, err := xdealv1.HeldRuling(ctx, s.shelf, dealID, ag.Adjudicator.V.KeyID, ruling)
		if err != nil {
			return err
		}
		if !found {
			return cli.InputError("ruling " + ruling + " is unavailable: this profile holds no such ruling of deal " + dealID +
				" from its adjudicator; take the adjudicator's file with deal receive first")
		}
		result := map[string]any{"deal_id": dealID, "ruling": ruling, "grounds_commitment": commitment}
		for i, r := range book {
			if b, ok := disputeOf(r); ok && b.Kind == xdealv1.KindChallenge && b.Ruling != nil &&
				b.Ruling.Digest == ruling && b.GroundsCommitment == commitment {
				// The same challenge again is written again, never sealed twice.
				return s.writeSealedDispute(ctx, c, d, i+1, r, out, result)
			}
		}
		ref := xdealv1.RefTo(ruling)
		body := xdealv1.DisputeBody{Kind: xdealv1.KindChallenge, Ruling: &ref, GroundsCommitment: commitment}
		return s.sealDispute(ctx, c, d, body, nil, out, result)
	})
}
