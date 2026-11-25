package deal

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
)

// The witness time of an x-deal-v1 copy's own book, beside the copy and never
// in it (Sherlock2, 2026-11-25: no witness evidence in any copy, since a
// stamp proof shows the holder's shelf checkpoint and log position, which #46
// hides). `verify --bundle F --stamp-proof P --witness-directory W` reads the
// proof `deal checkpoint proof` writes for that copy and reports, in its
// x-deal-v1 entry, `witnessed: {status, witness, at}`:
//   - verified: a receipt verifies, under W, over the shelf checkpoint the
//     stamp proof binds this copy's checkpoint into; witness is that row's
//     name in W (the reader's own label, not something the copy proves) and
//     at the receipt's own registration time, the earliest when several;
//   - failed: the proof is not of this copy's checkpoint, or a receipt does
//     not verify and none does;
//   - withheld: no directory, no receipt held, or no receipt that could be
//     checked under the directory.
// It is never a claim: a witness result leaves the copy's verdict as it was.

// stampProofFlag is verify's flag for the proof file.
const stampProofFlag = "stamp-proof"

// readStampProof reads a proof file as `deal checkpoint proof` writes it.
func readStampProof(path string) (*entryProof, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, cli.InputError("cannot read --" + stampProofFlag + " " + path)
	}
	var p entryProof
	if err := json.Unmarshal(raw, &p); err != nil || p.StampProof == nil {
		return nil, cli.InputError("--" + stampProofFlag + " " + path + " is not a proof file (deal checkpoint proof --out)")
	}
	return &p, nil
}

// v1Witnessed is the witnessed member for the copy value, given its proof.
func v1Witnessed(value map[string]interface{}, proof *entryProof, dir *witness.Directory) map[string]any {
	withheld := func(reason string) map[string]any { return map[string]any{"status": "withheld", "reason": reason} }
	failed := func(reason string) map[string]any { return map[string]any{"status": "failed", "reason": reason} }
	if dir == nil {
		return withheld("no witness directory was given (--witness-directory)")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return failed("the copy could not be read")
	}
	vb, err := evidencebook.VerifyBundle(raw)
	if err != nil {
		return failed("the copy does not verify")
	}
	v, err := evidencebook.VerifyStampProof(*proof.StampProof)
	if err != nil {
		return failed("the stamp proof does not verify: " + err.Error())
	}
	if reason := stampProofOfCopy(vb, v); reason != "" {
		return failed(reason)
	}
	if len(proof.WitnessReceipts) == 0 {
		return withheld("no witness receipt is held for this checkpoint yet")
	}
	var (
		name     string
		at       time.Time
		verified bool
		bad      bool
	)
	for _, rc := range proof.WitnessReceipts {
		check, _, when, ok, err := evidencebook.VerifyWitnessReceiptTime(proof.StampProof.ShelfCheckpoint, rc, *dir)
		switch {
		case err != nil:
			return failed("the stamp proof's shelf checkpoint does not verify")
		case check.State == evidencebook.WitnessVerified && ok:
			if !verified || when.Before(at) {
				row, _ := dir.RowFor(rc.TSURL)
				name, at, verified = row.Name, when, true
			}
		case check.State == evidencebook.WitnessVerified:
			// A receipt that verifies but carries no time: no time is guessed.
		case check.State == evidencebook.WitnessInvalid:
			bad = true
		}
	}
	switch {
	case verified:
		return map[string]any{"status": "verified", "witness": name, "at": at.Format(time.RFC3339)}
	case bad:
		return failed("a witness receipt does not verify under the witness directory")
	}
	return withheld("no receipt verifies under the witness directory")
}

// v1PageWitness is what `deal report --html --witness-directory W` gives the
// page's verify run: the stamp proof `checkpoint proof` would write for this
// copy and W's path. Without W, or with no stamp covering the copy's
// checkpoint yet, nil: the page draws no witness time.
func (s *dealSession) v1PageWitness(ctx context.Context, c *cobra.Command, d *v1Deal, bundle []byte) (*entryProof, string, error) {
	dirPath, _ := c.Flags().GetString("witness-directory")
	if dirPath == "" {
		return nil, "", nil
	}
	raw, err := os.ReadFile(dirPath)
	if err != nil {
		return nil, "", cli.InputError("cannot read --witness-directory " + dirPath)
	}
	dir, err := witness.ParseDirectory(raw)
	if err != nil {
		return nil, "", cli.InputError("--witness-directory " + dirPath + " is not a witness directory: " + err.Error())
	}
	cad, err := s.requireMigrated(ctx)
	if err != nil {
		return nil, "", err
	}
	vb, err := evidencebook.VerifyBundle(bundle)
	if err != nil {
		return nil, "", err
	}
	named, err := copyWitnessKeys(vb)
	if err != nil {
		return nil, "", err
	}
	best, err := bestStampProof(ctx, cad, d.OwnAgreementID, vb, dir, named)
	if err != nil || best == nil {
		return nil, dirPath, err
	}
	return &entryProof{StampProof: &best.proof, WitnessReceipts: best.receipts}, dirPath, nil
}

// stampProofOfCopy says why a verified stamp proof is not of the copy's own
// signed checkpoint, or "" when it is: the copy's anchor is signed, by the
// key that signed the proof's checkpoints (VerifyStampProof holds both to one
// key), for the same log, root and size. A proof is outside input, so a root
// and size alone are not enough: another key's checkpoint over the same
// records would pass (Codex, #935).
func stampProofOfCopy(vb evidencebook.VerifiedBundle, v evidencebook.VerifiedStampProof) string {
	switch {
	case !vb.AnchorAuthenticated:
		return "the copy's checkpoint is not signed, so no stamp proof is of it"
	case v.KeyID != vb.AnchorKeyID:
		return "the stamp proof is signed by another key than the copy's checkpoint"
	case v.BookCheckpoint.LogID != vb.LogID:
		return "the stamp proof is of another log than the copy's"
	case v.BookCheckpoint.Root != vb.Anchor.Root || v.BookCheckpoint.TreeSize != vb.Anchor.MMRSize:
		return "the stamp proof is not of this copy's checkpoint"
	}
	return ""
}
