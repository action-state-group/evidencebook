// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/disclosure"
	"github.com/action-state-group/agent-action-capsule/go/envelope"
	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
)

// cardAnchorVia names the receipts cardAnchorWitnessClaim reports.
const cardAnchorVia = "card_anchor"

// cardAnchorWitnessClaim checks a History card bundle's
// x-standard-history-anchor-witness/v0 block (shAnchorWitness): the one cll
// witness receipt it carries covers the checkpoint the card is as of, and
// that checkpoint is the one the card's signed payload names. present is
// false when the bundle carries no such block. In order:
//
//  0. the bundle is no deal copy: it carries no deal section and no cadence
//     chain under any name (u219: no witness evidence in a deal copy, and a
//     cadence chain would put two witness claims on one bundle);
//  1. the bundle's own checkpoint verified (statement non-nil);
//  2. the root record is the plugin's History card Capsule, signed, with its
//     disclosed payload matching its seal, and the payload is a card of a
//     derivation the plugin writes;
//  3. the payload's anchor is the block's as_of.anchor;
//  4. as_of.checkpoint hashes to that anchor, verifies, and is signed by the
//     key that signed the bundle's own checkpoint (the profile's checkpoint
//     key signs both the shelf and the card's log), so a card cannot borrow
//     another holder's witnessed checkpoint;
//  5. exactly one receipt, which AAC's verifier checks against that
//     checkpoint under the directory (its leaf is derived from the
//     checkpoint, not the anchor).
//
// No step passes on a failure: each is a named finding.
func cardAnchorWitnessClaim(value map[string]interface{}, result aacbundle.VerificationResult, signatures map[string]string, statement []byte, directory *witness.Directory) (aacbundle.ClaimResult, []map[string]any, bool) {
	exts, _ := value["extensions"].(map[string]interface{})
	block, present := exts[shAnchorWitnessExtension]
	if !present {
		return aacbundle.ClaimResult{}, nil, false
	}
	fail := func(finding string) (aacbundle.ClaimResult, []map[string]any, bool) {
		return aacbundle.ClaimResult{Status: "fail", Findings: []string{finding}}, []map[string]any{}, true
	}
	_, v0 := exts[DealProfile]
	_, v1 := exts[DealProfileV1]
	if v0 || v1 || CadenceChainOf(value) != nil {
		return fail("card_anchor_witness_in_deal_copy")
	}
	if statement == nil {
		return aacbundle.ClaimResult{Status: "withheld", Findings: []string{"checkpoint_unverified"}}, []map[string]any{}, true
	}
	own, err := checkpoint.ParseRecord(statement)
	if err != nil {
		return fail("card_checkpoint_malformed")
	}

	rootID, _ := value["root"].(string)
	matched := false
	for _, d := range result.Disclosures {
		if d.CapsuleID == rootID && d.Member == "agent_input" && d.Status == disclosure.Match {
			matched = true
		}
	}
	isCard := false
	records, _ := value["records"].([]interface{})
	for _, raw := range records {
		r, _ := raw.(map[string]interface{})
		if r["capsule_id"] == rootID && r["action_id"] == shActionID {
			isCard = true
		}
	}
	if !matched || !isCard || signatures[rootID] != envelope.Authored {
		return fail("card_unverified")
	}
	disclosures, _ := value["disclosures"].(map[string]interface{})
	d, _ := disclosures[rootID].(map[string]interface{})
	card, _ := d["agent_input"].(map[string]interface{})
	if derivation, _ := card["derivation"].(string); !slices.Contains(shCardDerivations, derivation) {
		return fail("card_unverified")
	}

	sh, _ := exts[shExtension].(map[string]interface{})
	asOf, _ := sh["as_of"].(map[string]interface{})
	anchor, _ := card["anchor"].(string)
	if anchor == "" || asOf["anchor"] != anchor {
		return fail("card_anchor_mismatch")
	}
	text, _ := asOf["checkpoint"].(string)
	anchored, err := hex.DecodeString(text)
	sum := sha256.Sum256(anchored)
	if err != nil || hex.EncodeToString(sum[:]) != anchor {
		return fail("card_anchor_checkpoint_mismatch")
	}
	if _, err = cli.SignedCheckpoint(anchored); err != nil {
		return fail("card_anchor_checkpoint_signature_invalid")
	}
	record, err := checkpoint.ParseRecord(anchored)
	if err != nil {
		return fail("card_anchor_checkpoint_malformed")
	}
	if record.KeyID != own.KeyID {
		return fail("card_anchor_checkpoint_signer_differs")
	}

	b, _ := block.(map[string]interface{})
	entries, isList := b["witnesses"].([]interface{})
	if len(b) != 1 || !isList || len(entries) != 1 {
		return fail("card_anchor_witness_malformed")
	}
	claim, receipts := receiptsClaim(entries, anchored, directory, cardAnchorVia)
	return claim, receipts, true
}
