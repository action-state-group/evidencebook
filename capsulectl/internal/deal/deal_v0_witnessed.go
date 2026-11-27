// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// The witness time of an x-deal-v0 copy's own book, beside the copy and never
// in it (u219, u225: a copy carries no witnessed chain, since the chain shows
// the holder's shelf position and cadence). The holder keeps the chain; `deal
// checkpoint proof` writes it for a copy, and `verify --bundle F --stamp-proof
// P --witness-directory W` reads it and reports, in the copy's x-deal-v0
// entry in any report form, `witnessed: {status, witness, at}`, as
// v1Witnessed does for x-deal-v1 (capsule-viewer#383, CV3b's contract):
//   - verified: the chain covers this copy's own checkpoint (extent all), its
//     structure checks as a carried chain's does (cadenceChainChecked), and a
//     receipt verifies under W with a time; witness is that row's name in W
//     (the reader's own label) and at the receipt's registration time, the
//     earliest when several;
//   - failed: the chain's structure fails (another deal's or copy's chain, a
//     bad path, inclusion or signature), or a receipt fails and none verifies;
//   - withheld: no directory, no receipt, none checkable (an unknown witness,
//     a verified receipt with no time), or a chain that covers only an earlier
//     checkpoint of the deal (extent part: its time is not this copy's).
// Each names the W row of the witness it reports on, as v1Witnessed does
// (witness and independent_of, evidencebook#1073).
// It is never a claim: the copy's verdict and witness claim are unchanged.

// v0ChainProofKind names the proof file `deal checkpoint proof` writes for an
// x-deal-v0 copy.
const v0ChainProofKind = "capsulectl-deal/x-deal-v0-cadence-chain"

// v0ChainProof is that file: the holder's witnessed cadence chain for the
// copy's checkpoint, as the extension block a copy carried before u225.
type v0ChainProof struct {
	Kind  string                 `json:"kind"`
	Chain map[string]interface{} `json:"parent_log_witness"`
}

// cadenceExtensionEntry is a witnessed cadence state as the extension entry
// a bundle carried it in (parent-log-witness/v1, the only name a bundle now
// carries), and its block: the chain's members with the cadence entry as
// "parent". A verifier reads the block back with CadenceChainOf, which
// renames "parent" to "cadence" and computes the extent from the
// checkpoints. A state that is not a witnessed chain (scheduled, pending,
// unverified, not_configured) names no leaf and no receipt, so it is no
// block a verifier could check (Sherlock2, buzz 8c3ec04f). ok is false for
// those states.
func cadenceExtensionEntry(chain map[string]interface{}) (name string, block map[string]interface{}, ok bool) {
	parent, isObject := chain["cadence"].(map[string]interface{})
	if chain["state"] != "witnessed" || !isObject {
		return "", nil, false
	}
	block = make(map[string]interface{}, len(chain))
	for k, v := range chain {
		if k != "cadence" {
			block[k] = v
		}
	}
	block["parent"] = parent
	return DealParentWitnessExtension, block, true
}

// readVerifyProof reads verify's --stamp-proof file: an x-deal-v1 copy's
// stamp proof (deal_v1_witnessed.go) or an x-deal-v0 copy's cadence chain.
func readVerifyProof(path string) (*entryProof, *v0ChainProof, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, cli.InputError("cannot read --" + stampProofFlag + " " + path)
	}
	var v0 v0ChainProof
	if json.Unmarshal(raw, &v0) == nil && v0.Kind == v0ChainProofKind {
		if v0.Chain == nil {
			return nil, nil, cli.InputError("--" + stampProofFlag + " " + path + " names no cadence chain")
		}
		return nil, &v0, nil
	}
	p, err := readStampProof(path)
	return p, nil, err
}

// v0Witnessed is the witnessed member for an x-deal-v0 copy value, given the
// holder's chain for it. dealStatement is the copy's signed checkpoint, nil
// when it did not verify.
func v0Witnessed(value map[string]interface{}, proof *v0ChainProof, dealStatement []byte, dir *witness.Directory, rows witnessRows) map[string]any {
	withheld := func(reason string) map[string]any { return map[string]any{"status": "withheld", "reason": reason} }
	failed := func(reason string) map[string]any { return map[string]any{"status": "failed", "reason": reason} }
	if dir == nil {
		return withheld("no witness directory was given (--witness-directory)")
	}
	if dealStatement == nil {
		return withheld("the copy's checkpoint did not verify")
	}
	// The chain read as a carried chain is: the block put back under its name.
	ext := map[string]interface{}{DealParentWitnessExtension: proof.Chain}
	chain := CadenceChainOf(map[string]interface{}{"extensions": ext})
	_, statement, finding := cadenceChainChecked(chain, dealStatement)
	if finding != "" {
		return failed("the chain is not of this copy's checkpoint: " + finding)
	}
	if chain["extent"] == "part" {
		return withheld("the chain is witnessed for an earlier checkpoint of this deal, not this copy's")
	}
	inner, _ := chain["cadence"].(map[string]interface{})
	signed, err := witness.ParseSignedCheckpoint(statement)
	if err != nil {
		return failed("the chain's cadence checkpoint does not verify")
	}
	// A malformed receipt is no result but fails the whole: it counts as a
	// receipt that does not verify.
	status, _, results := witness.VerifyReceipts(inner["witnesses"], &signed, statement, *dir)
	if len(results) == 0 {
		if status == witness.StatusFail {
			return failed("a witness receipt is malformed")
		}
		return withheld("no witness receipt is held for this checkpoint yet")
	}
	var (
		at            time.Time
		verified, bad bool
		// The receipts whose rows the member names, as in v1Witnessed.
		best, badRow, listed string
	)
	for _, r := range results {
		switch {
		case r.Status == witness.StatusPass && r.RegistrationTime != nil:
			when := time.Unix(*r.RegistrationTime, 0).UTC()
			if !verified || when.Before(at) {
				best, at, verified = r.TSURL, when, true
			}
		case r.Status == witness.StatusPass:
			// A receipt that verifies but carries no time: no time is guessed.
		case r.Status == witness.StatusFail:
			bad, badRow = true, firstListed(badRow, r.TSURL, dir)
		}
		listed = firstListed(listed, r.TSURL, dir)
	}
	switch {
	case verified:
		member := rows.withRow(map[string]any{"status": "verified"}, dir, best)
		member["at"] = at.Format(time.RFC3339)
		return member
	case bad, status == witness.StatusFail:
		return rows.withRow(failed("a witness receipt does not verify under the witness directory"), dir, badRow)
	}
	return rows.withRow(withheld("no receipt verifies under the witness directory"), dir, listed)
}

// v0ChainProofOf is the proof file's value for an x-deal-v0 copy b: the
// holder's witnessed chain for its checkpoint, or ok false when the holder
// holds none yet.
func (s *dealSession) v0ChainProofOf(ctx context.Context, dealID string, b map[string]interface{}) (*v0ChainProof, bool, error) {
	cp, _ := b["checkpoint"].(map[string]interface{})
	encoded, _ := cp["cose"].(string)
	statement, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || encoded == "" {
		return nil, false, cli.InputError("the copy carries no signed checkpoint")
	}
	state, err := s.dealWitnessState(ctx, dealID, statement)
	if err != nil {
		return nil, false, err
	}
	_, block, ok := cadenceExtensionEntry(state)
	if !ok {
		return nil, false, nil
	}
	// One JSON round trip, so the block holds what a reader decodes.
	raw, err := json.Marshal(block)
	if err != nil {
		return nil, false, err
	}
	var decoded map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return nil, false, err
	}
	return &v0ChainProof{Kind: v0ChainProofKind, Chain: decoded}, true, nil
}

// v0CheckpointProof is `deal checkpoint proof` for an x-deal-v0 copy: the
// holder's witnessed cadence chain for the checkpoint the copy rests on,
// written to out. It refuses a copy that is not of this deal, and says when
// no witnessed chain is held yet.
func (s *dealSession) v0CheckpointProof(ctx context.Context, c *cobra.Command, dealID string, bundle []byte, out string, events []sealedEvent) error {
	b, err := s.dealBundleFrom(bundle, dealID, events, func(why string) error {
		return cli.InputError("--copy is not a copy of deal " + dealID + ": " + why)
	})
	if err != nil {
		return err
	}
	proof, ok, err := s.v0ChainProofOf(ctx, dealID, b)
	if err != nil {
		return err
	}
	if !ok {
		return cli.InputError("no witnessed cadence chain covers the checkpoint your copy rests on yet: run deal tick once the witness has answered, then this again")
	}
	data, err := json.Marshal(proof)
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
	result := map[string]any{"deal_id": dealID, "file": out, "extent": proof.Chain["extent"]}
	if CadenceChainOf(map[string]interface{}{"extensions": map[string]interface{}{DealParentWitnessExtension: proof.Chain}})["extent"] == "part" {
		result["note"] = "the chain is witnessed for an earlier checkpoint of this deal: deal verify reads it as withheld for this copy until a later chain covers its checkpoint"
	}
	return cli.Output(c, result)
}

// v0PageWitness is what an x-deal-v0 page's verify run gets for the holder's
// witness time: the chain and the directory to check it under.
type v0PageWitness struct {
	proof     *v0ChainProof
	directory string
}

// v0PageWitnessOf is `deal report --html --witness-directory W`'s witness for
// the page of copy b: the holder's chain for its checkpoint and W's path, or
// nil when no witnessed chain is held yet (the page then draws no time). W
// must read as a witness directory.
func (s *dealSession) v0PageWitnessOf(ctx context.Context, dealID string, b map[string]interface{}, dirPath string) (*v0PageWitness, error) {
	// Only the holder's own copy carries the time, whatever audience the
	// command names: a shared copy given with --from-bundle is refused, so
	// its page's verify report never holds the witness (u219).
	if audience, _ := dealPageReport(b)["audience"].(string); audience != cli.DealAudienceKeep {
		return nil, cli.InputError("--witness-directory needs --html on your own copy: it puts your book's witness time on your own page, and a shared copy carries none")
	}
	raw, err := os.ReadFile(dirPath)
	if err != nil {
		return nil, cli.InputError("cannot read --witness-directory " + dirPath)
	}
	if _, err := witness.ParseDirectory(raw); err != nil {
		return nil, cli.InputError("--witness-directory " + dirPath + " is not a witness directory: " + err.Error())
	}
	proof, ok, err := s.v0ChainProofOf(ctx, dealID, b)
	if err != nil || !ok {
		return nil, err
	}
	abs, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, err
	}
	return &v0PageWitness{proof: proof, directory: abs}, nil
}
