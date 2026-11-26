package deal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"

	"github.com/action-state-group/capsule-cli/cli"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// runChallenge is `deal dispute --challenge RULING --grounds FILE`
// (evidencebook#773): it seals a dispute of kind challenge in the holder's
// deal book, naming a ruling of the deal the holder holds and the SHA-256 of
// the grounds file, and writes the file to send. A challenge changes no row
// status (evidencebook#773: the first final ruling stays effective and is
// never replaced; the adjudicator may issue a later ruling that is not final);
// the record shows that the
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
		st, err := s.v1Store(ctx)
		if err != nil {
			return err
		}
		res, err := st.Challenge(ctx, dealID, dealapi.ChallengeIn{Ruling: ruling, GroundsCommitment: commitment,
			Before: func(xdealv1.Record, *xdealv1.ActAuthorization) error { return refuseExistingOut(out) }})
		if err != nil {
			return libErr(err)
		}
		return writeDispute(c, res, out, map[string]any{"deal_id": dealID, "ruling": ruling, "grounds_commitment": commitment})
	})
}
