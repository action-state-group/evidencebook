package book

import (
	"encoding/hex"
	"errors"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// bundleDiffResult is what `capsulectl book bundle-diff` prints: what the
// lower disclosure level leaves out of the higher one.
type bundleDiffResult struct {
	LogID           string   `json:"log_id"`
	AnchorKeyID     string   `json:"anchor_key_id"`
	DroppedRecords  []string `json:"dropped_records"`
	DroppedPayloads []string `json:"dropped_payloads"`
}

// bundleDiffCommand is `capsulectl book bundle-diff`: one history at two
// disclosure levels (evidencebook DiffBundles). It reads two held Evidence
// Bundles of the same log and lists the records and payloads the lower one
// drops. It reads files only: no profile, no book, nothing sealed.
func bundleDiffCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "bundle-diff", Short: "List what a lower disclosure level drops from a higher one, from two held bundles of the same log (seals nothing)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		higherPath, _ := c.Flags().GetString("higher")
		lowerPath, _ := c.Flags().GetString("lower")
		key, _ := c.Flags().GetString("checkpoint-key")
		if higherPath == "" || lowerPath == "" {
			return cli.InputError("--higher and --lower are required: the two bundle files to compare")
		}
		higher, err := cli.ReadBundleInput(higherPath)
		if err != nil {
			return err
		}
		lower, err := cli.ReadBundleInput(lowerPath)
		if err != nil {
			return err
		}
		// Verify first, so a bad bundle is named by its flag.
		verified, err := evidencebook.VerifyBundle(higher)
		if err != nil {
			return errors.Join(cli.InputError("the --higher bundle does not verify: "+err.Error()), err)
		}
		verifiedLower, err := evidencebook.VerifyBundle(lower)
		if err != nil {
			return errors.Join(cli.InputError("the --lower bundle does not verify: "+err.Error()), err)
		}
		// DiffBundles proves both come from one log under an authenticated
		// checkpoint; only the caller knows whose key that is, and that both
		// bundles carry the same one.
		diff, err := evidencebook.DiffBundles(higher, lower)
		if err != nil {
			return errors.Join(cli.InputError("the bundles cannot be compared: "+err.Error()), err)
		}
		if verifiedLower.AnchorKeyID != verified.AnchorKeyID {
			return cli.InputError("the two bundles' checkpoints are signed by different keys")
		}
		if c.Flags().Changed("checkpoint-key") {
			pinned, e := cli.ParseKeys([]string{key})
			if e != nil || key == "" {
				return cli.InputError("--checkpoint-key is a 32-byte Ed25519 public key in hex")
			}
			if verified.AnchorKeyID != hex.EncodeToString(pinned[0]) {
				return cli.InputError("the bundles' checkpoint is not signed by --checkpoint-key")
			}
		}
		if !diff.Subset() {
			return cli.InputError("--lower is not a lower level of --higher: it discloses what --higher does not (" + joinFirst(diff.Extra, 5) + ")")
		}
		return cli.Output(c, bundleDiffResult{LogID: verified.LogID, AnchorKeyID: verified.AnchorKeyID, DroppedRecords: nonNil(diff.DroppedRecords), DroppedPayloads: nonNil(diff.DroppedPayloads)})
	}}
	cmd.Flags().String("higher", "", "The bundle that discloses more (required)")
	cmd.Flags().String("lower", "", "The bundle that discloses less, of the same log (required)")
	cmd.Flags().String("checkpoint-key", "", "Require the bundles' checkpoint to be signed by this 32-byte Ed25519 public key (hex)")
	return cmd
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func joinFirst(s []string, n int) string {
	more := ""
	if len(s) > n {
		s, more = s[:n], ", ..."
	}
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out + more
}
