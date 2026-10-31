package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-cli/settlement"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/spf13/cobra"
)

// baseUSDC is the asset a terms leg names when --asset is not given: Base
// USDC, the asset x402 requirements on Base carry.
const (
	baseUSDC      = "eip155:8453/erc20:0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"
	baseUSDCScale = 6
)

var (
	atomicAmountPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	// assetPattern is a CAIP-19 asset id: chain, then namespace:reference.
	assetPattern         = regexp.MustCompile(`^[-a-z0-9]{3,8}:[-_a-zA-Z0-9]{1,32}/[-a-z0-9]{3,8}:[-.%a-zA-Z0-9]{1,128}$`)
	contentDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// dealTermsLegCommand is `deal terms-leg`: the buyer's side of a sale. It
// writes the terms leg (draft-mih-agent-settlement-records-00) as a bare,
// signed Capsule file, the file `deal note --kind payment_received|delivered
// --terms` answers. It reads and writes no deal store: the leg is the buyer's
// own statement of what the buyer agrees to pay and expects to receive.
func dealTermsLegCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "terms-leg", Short: "Write the buyer's signed terms leg: the price and what the buyer expects, as the file a seller's payment_received and delivered notes answer", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		flag := func(name string) string { v, _ := c.Flags().GetString(name); return strings.TrimSpace(v) }
		out, amount, digest := flag("out"), flag("amount"), flag("content-digest")
		if out == "" || amount == "" || digest == "" {
			return cli.InputError("terms-leg needs --amount, --content-digest and --out")
		}
		if !atomicAmountPattern.MatchString(amount) {
			return cli.InputError("--amount is the price in the asset's atomic units, digits only (170000000 is 170 USDC)")
		}
		if !contentDigestPattern.MatchString(digest) {
			return cli.InputError("--content-digest is the SHA-256 of what you expect to receive, 64 lowercase hex characters")
		}
		scale := baseUSDCScale
		if c.Flags().Changed("asset-scale") {
			scale, _ = c.Flags().GetInt("asset-scale")
		}
		asset := flag("asset")
		switch {
		case asset == "":
			if c.Flags().Changed("asset-scale") && scale != baseUSDCScale {
				return cli.InputError("--asset-scale goes with --asset: the default asset is Base USDC, six decimals")
			}
			asset = baseUSDC
		case !c.Flags().Changed("asset-scale"):
			return cli.InputError("--asset needs --asset-scale, the asset's decimals")
		}
		if !assetPattern.MatchString(asset) {
			return cli.InputError("--asset is a CAIP-19 asset id, such as " + baseUSDC)
		}
		if scale < 0 {
			return cli.InputError("--asset-scale is the asset's decimals, zero or more")
		}

		key, err := termsLegKey(c)
		if err != nil {
			return err
		}
		capsule, capsuleID, err := sealTermsLeg(key, amount, asset, scale, digest)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return cli.InputError("--out " + out + " exists: terms-leg does not overwrite a file")
			}
			return cli.InputError("cannot write --out " + out + ": " + errors.Unwrap(err).Error())
		}
		if _, err := f.Write(capsule); err != nil {
			return errors.Join(err, f.Close(), os.Remove(out))
		}
		if err := f.Close(); err != nil {
			return errors.Join(err, os.Remove(out))
		}
		// The verifier's own check, the one deal note --terms makes: a leg it
		// would refuse is not left behind.
		if _, err := termsLegID(out); err != nil {
			return errors.Join(err, os.Remove(out))
		}
		ephemeral, _ := c.Flags().GetBool("ephemeral")
		return cli.Output(c, map[string]any{"terms_leg": out, "capsule_id": capsuleID,
			"key_id": hex.EncodeToString(key.Public().(ed25519.PublicKey)), "ephemeral": ephemeral})
	}}
	f := cmd.Flags()
	f.String("amount", "", "the price in the asset's atomic units, digits only (170000000 is 170 USDC)")
	f.String("asset", "", "the asset as a CAIP-19 id, such as "+baseUSDC+" (default: Base USDC)")
	f.Int("asset-scale", 0, "the asset's decimals: required with --asset, taken as 6 for the default Base USDC (no table of other known assets is read)")
	f.String("content-digest", "", "the SHA-256 of what you expect to receive, 64 lowercase hex characters")
	f.String("out", "", "New file for the terms leg; an existing file is refused")
	f.Bool("ephemeral", false, "sign with a key made for this run and thrown away, instead of the profile's key; the leg then names no lasting signer, so use it for tests and demonstrations, not real sales")
	return cmd
}

// termsLegKey is the key that signs the leg: the selected profile's, or with
// --ephemeral a fresh one that is never stored.
func termsLegKey(c *cobra.Command) (ed25519.PrivateKey, error) {
	if ephemeral, _ := c.Flags().GetBool("ephemeral"); ephemeral {
		if name, _ := c.Flags().GetString("profile"); name != "" {
			return nil, cli.InputError("--ephemeral signs with no profile: drop --profile")
		}
		_, key, err := ed25519.GenerateKey(rand.Reader)
		return key, err
	}
	p, err := cli.Selected(c)
	if err != nil {
		return nil, err
	}
	return cli.PrivateKey(p.Signing)
}

// sealTermsLeg builds the terms leg and signs it with key, as capsule-cli's
// settlement tests build theirs, and returns the bare Capsule file.
func sealTermsLeg(key ed25519.PrivateKey, amount, asset string, scale int, digest string) ([]byte, string, error) {
	member, err := json.Marshal(map[string]any{
		"version": "0", "leg": settlement.LegTerms, "sealer_role": settlement.RolePayer,
		"amount":      map[string]any{"value": amount, "assetCode": asset, "assetScale": scale},
		"deliverable": map[string]any{"content_digest": digest},
	})
	if err != nil {
		return nil, "", err
	}
	built, err := emit.Build(emit.Input{ActionID: "buyer/terms", ActionType: emit.ActionTypeFYI, Operator: "buyer", Developer: "buyer-agent",
		Timestamp: cli.DealClock().UTC().Truncate(time.Second), Extensions: []emit.Extension{{Name: "settlement", Value: member}}})
	if err != nil {
		return nil, "", err
	}
	identity, err := emit.NewEd25519SigningIdentity(key)
	if err != nil {
		return nil, "", err
	}
	envelope, err := emit.Sign(built, identity)
	if err != nil {
		return nil, "", err
	}
	capsule := map[string]any{}
	for k, v := range built.Value {
		capsule[k] = v
	}
	capsule["signature"] = hex.EncodeToString(envelope)
	capsule["key_id"] = hex.EncodeToString(key.Public().(ed25519.PublicKey))
	data, err := json.Marshal(capsule)
	return data, built.CapsuleID, err
}
