// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/disclosure"
	"github.com/action-state-group/agent-action-capsule/go/envelope"
	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cll-witnessed seller (the hosted witness's binding) and its card files.
type cllCard struct {
	p        cli.Profile
	endpoint string
	public   ed25519.PublicKey
	key      ed25519.PrivateKey
}

// witnessV0Seller is an x-deal-v0 seller whose newest tick a cll witness
// witnessed (evidencebook#891).
func witnessV0Seller(t *testing.T) cllCard {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	retailDeal(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(3 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	return cllCard{p: p, endpoint: endpoint, public: public, key: key}
}

// card writes the card under derivation, own or shared.
func (c cllCard) card(t *testing.T, derivation string, shared bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "card.json")
	args := []string{"standard-history", "card", "--derivation", derivation, "--out", path}
	if shared {
		args = append(args, "--shared")
	}
	dealRun(t, args...)
	return path
}

func (c cllCard) directory(t *testing.T) string {
	t.Helper()
	return writeDirectory(t, rawKeyRow(c.endpoint, c.public))
}

func readerWitnesses(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	w, ok := result["reader_witnesses"].(map[string]any)
	require.True(t, ok, "reader_witnesses: %v", result)
	return w
}

// The issue's bar: a buyer verifying a cll-witnessed seller's card, own copy
// or shared, under a directory naming the witness, gets the anchor's receipt
// checked and passing. The core's own witnesses row stays about the card's
// own log, which no witness sees; the verdict is unchanged.
func TestACllWitnessedCardsAnchorReceiptVerifies(t *testing.T) {
	c := witnessV0Seller(t)
	for name, shared := range map[string]bool{"own": false, "shared": true} {
		path := c.card(t, "standard_history/1", shared)
		b := readBundle(t, path)
		asOf := shBlock(t, b)["as_of"].(map[string]any)
		assertCheckpointIsTheAnchor(t, asOf, name)
		assert.NotContains(t, asOf, "receipts", name+": a cll receipt never goes in as_of.receipts")
		block := b["extensions"].(map[string]any)[shAnchorWitnessExtension].(map[string]any)
		require.Len(t, block, 1, name)
		require.Len(t, block["witnesses"].([]any), 1, name)

		result, err := verifyWithDirectory(t, path, c.directory(t))
		require.NoError(t, err, name)
		assert.Equal(t, "VALID", result["verdict"], name)
		w := readerWitnesses(t, result)
		assert.Equal(t, "pass", w["status"], name)
		receipts := w["receipts"].([]any)
		require.Len(t, receipts, 1, name)
		assert.Equal(t, cardAnchorVia, receipts[0].(map[string]any)["via"], name)
		assert.Equal(t, "withheld", result["witnesses"].(map[string]any)["status"], name+": the card's own log")

		// No directory row for the witness: not checked, never pass.
		result, err = verifyWithDirectory(t, path, "")
		require.NoError(t, err, name)
		assert.Equal(t, "withheld", readerWitnesses(t, result)["status"], name)
	}
}

// /3 and /4 cards of x-deal-v1 deals carry and pass it too.
func TestACllWitnessedV1CardsAnchorReceiptVerifies(t *testing.T) {
	for _, derivation := range []string{"standard_history/3", "standard_history/4"} {
		t.Run(derivation, func(t *testing.T) {
			h := bothAgreed(t)
			h.seller.act(t)
			public, key, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			endpoint, _ := countingWitness(t)
			p, err := cli.LoadProfile("deal")
			require.NoError(t, err)
			p.Checkpoint.Endpoint, p.Checkpoint.PublicKey = endpoint, hex.EncodeToString(public)
			require.NoError(t, cli.SaveProfile(p, true))
			clockAt(t, time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC))
			dealRun(t, "standard-history", "declare", "--holder-kind", "node")
			clockAt(t, time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC))
			require.Equal(t, "ticked", dealRun(t, "tick")["state"])
			deliver(t, p, cadenceSize(t, p), key)
			c := cllCard{p: p, endpoint: endpoint, public: public, key: key}

			result, err := verifyWithDirectory(t, c.card(t, derivation, true), c.directory(t))
			require.NoError(t, err)
			assert.Equal(t, "pass", readerWitnesses(t, result)["status"])
		})
	}
}

// A tick no witness saw, or a held receipt that does not check under the
// profile's pinned witness, writes no block; the card is still made.
func TestACardCarriesNoAnchorWitnessItCannotCheck(t *testing.T) {
	t.Run("not witnessed", func(t *testing.T) {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		endpoint, _ := countingWitness(t)
		cadenceFixture(t, endpoint, public, "1h", "0s", 0)
		now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
		retailDeal(t)
		dealRun(t, "standard-history", "declare", "--holder-kind", "node")
		*now = now.Add(3 * time.Hour)
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		path := (cllCard{}).card(t, "standard_history/1", false)
		assert.NotContains(t, readBundle(t, path)["extensions"], shAnchorWitnessExtension)
		result, err := verifyBundleOutput(t, path)
		require.NoError(t, err)
		assert.NotContains(t, result, "reader_witnesses")
	})
	t.Run("pinned witness key changed", func(t *testing.T) {
		c := witnessV0Seller(t)
		other, _, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		c.p.Checkpoint.PublicKey = hex.EncodeToString(other)
		require.NoError(t, cli.SaveProfile(c.p, true))
		path := filepath.Join(t.TempDir(), "card.json")
		out, err := invoke(t, "", "--profile", "deal", "deal", "standard-history", "card", "--derivation", "standard_history/1", "--out", path)
		require.NoError(t, err)
		assert.Contains(t, out, "witness receipt left out")
		assert.NotContains(t, readBundle(t, path)["extensions"], shAnchorWitnessExtension)
	})
}

// No deal copy carries the block (u219): a deal report, own or shared, of a
// cll-witnessed profile has none.
func TestNoDealCopyCarriesACardAnchorWitness(t *testing.T) {
	witnessV0Seller(t)
	dealID := retailDeal(t)
	for _, args := range [][]string{{"--bundle"}, {"--share", "counterparty", "--to", "the shop", "--html"}, {"--share", "adjudicator", "--to", "the referee", "--html"}} {
		out := filepath.Join(t.TempDir(), "report")
		dealRun(t, append(append([]string{"report", "--deal", dealID}, args...), out)...)
		raw, err := os.ReadFile(out)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), shAnchorWitnessExtension)
	}
}

// Every way an edited card fails to tie the receipt to its signed anchor is a
// named failure, never a pass.
func TestAnEditedCardAnchorWitnessFails(t *testing.T) {
	c := witnessV0Seller(t)
	path := c.card(t, "standard_history/1", true)
	directory := c.directory(t)
	other := witnessV0Seller(t) // another holder, its own keys and witnessed checkpoint
	otherCard := readBundle(t, other.card(t, "standard_history/1", true))
	otherDirectory := writeDirectory(t, rawKeyRow(c.endpoint, c.public), rawKeyRow(other.endpoint, other.public))

	exts := func(b map[string]any) map[string]any { return b["extensions"].(map[string]any) }
	asOf := func(b map[string]any) map[string]any { return shBlock(t, b)["as_of"].(map[string]any) }
	entries := func(b map[string]any) []any {
		return exts(b)[shAnchorWitnessExtension].(map[string]any)["witnesses"].([]any)
	}
	cases := map[string]struct {
		edit      func(b map[string]any)
		directory string
		finding   string
	}{
		"two receipts": {edit: func(b map[string]any) {
			w := entries(b)
			exts(b)[shAnchorWitnessExtension] = map[string]any{"witnesses": []any{w[0], w[0]}}
		}, finding: "card_anchor_witness_malformed"},
		"another member": {edit: func(b map[string]any) {
			exts(b)[shAnchorWitnessExtension].(map[string]any)["anchor"] = asOf(b)["anchor"]
		}, finding: "card_anchor_witness_malformed"},
		"as_of names another anchor": {edit: func(b map[string]any) {
			asOf(b)["anchor"] = asOf(otherCard)["anchor"]
		}, finding: "card_anchor_mismatch"},
		"a checkpoint that is not the anchor": {edit: func(b map[string]any) {
			asOf(b)["checkpoint"] = asOf(otherCard)["checkpoint"]
		}, finding: "card_anchor_checkpoint_mismatch"},
		"a substituted payload": {edit: func(b map[string]any) {
			root := b["root"].(string)
			b["disclosures"].(map[string]any)[root].(map[string]any)["agent_input"].(map[string]any)["anchor"] = asOf(otherCard)["anchor"]
		}, finding: "card_unverified"},
		"no disclosed payload": {edit: func(b map[string]any) {
			delete(b["disclosures"].(map[string]any), b["root"].(string))
		}, finding: "card_unverified"},
		"a receipt over another checkpoint": {edit: func(b map[string]any) {
			exts(b)[shAnchorWitnessExtension] = exts(otherCard)[shAnchorWitnessExtension]
		}, directory: otherDirectory},
		"a receipt whose signature is broken": {edit: func(b map[string]any) {
			e := entries(b)[0].(map[string]any)
			raw, err := base64.StdEncoding.DecodeString(e["receipt_b64"].(string))
			require.NoError(t, err)
			raw[len(raw)-1] ^= 0xff
			e["receipt_b64"] = base64.StdEncoding.EncodeToString(raw)
		}},
		"a tampered receipt": {edit: func(b map[string]any) {
			e := entries(b)[0].(map[string]any)
			e["entry_hash"] = hex.EncodeToString(make([]byte, sha256.Size))
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := readBundle(t, path)
			tc.edit(b)
			edited := writeBundle(t, b)
			d := directory
			if tc.directory != "" {
				d = tc.directory
			}
			result, _ := verifyWithDirectory(t, edited, d)
			w := readerWitnesses(t, result)
			assert.Equal(t, "fail", w["status"], "%v", w)
			if tc.finding != "" {
				assert.Contains(t, w["findings"], tc.finding)
			}
			assert.NotEqual(t, "VALID", result["verdict"])
		})
	}
}

// The block beside a deal section or a cadence chain, under any name the
// reader takes one by, fails: no deal copy carries witness evidence (u219).
func TestACardAnchorWitnessBesideDealEvidenceFails(t *testing.T) {
	c := witnessV0Seller(t)
	path := c.card(t, "standard_history/1", false)
	for _, name := range []string{DealProfile, DealProfileV1, DealParentWitnessExtension, DealCadenceExtension, "cadence-witness/v0", "x-deal-cadence-v0"} {
		t.Run(name, func(t *testing.T) {
			b := readBundle(t, path)
			b["extensions"].(map[string]any)[name] = map[string]any{"state": "withheld"}
			result, _ := verifyWithDirectory(t, writeBundle(t, b), c.directory(t))
			w := readerWitnesses(t, result)
			assert.Equal(t, "fail", w["status"])
			assert.Contains(t, w["findings"], "card_anchor_witness_in_deal_copy")
		})
	}
}

// A card whose payload, block and receipt all name another holder's witnessed
// checkpoint, with everything else verifying, fails on the signer: the
// checkpoint is not signed by the key that signed the card's own log. A file
// edit cannot reach this (the payload's seal fails first), so the claim is
// called with the core's results as they would be for a re-sealed card.
func TestACardBorrowingAnotherHoldersCheckpointFails(t *testing.T) {
	c := witnessV0Seller(t)
	b := readBundle(t, c.card(t, "standard_history/1", true))
	other := witnessV0Seller(t)
	o := readBundle(t, other.card(t, "standard_history/1", true))
	exts := b["extensions"].(map[string]any)
	asOf := shBlock(t, b)["as_of"].(map[string]any)
	theirs := shBlock(t, o)["as_of"].(map[string]any)
	anchor := theirs["anchor"].(string)
	asOf["anchor"], asOf["checkpoint"] = anchor, theirs["checkpoint"]
	exts[shAnchorWitnessExtension] = o["extensions"].(map[string]any)[shAnchorWitnessExtension]
	root := b["root"].(string)
	b["disclosures"].(map[string]any)[root].(map[string]any)["agent_input"].(map[string]any)["anchor"] = anchor

	statement := bundleStatement(t, b)
	result := aacbundle.VerificationResult{Disclosures: []aacbundle.DisclosureResult{{CapsuleID: root, Member: "agent_input", Status: disclosure.Match}}}
	directory := parseDirectory(t, writeDirectory(t, rawKeyRow(c.endpoint, c.public), rawKeyRow(other.endpoint, other.public)))
	claim, _, present := cardAnchorWitnessClaim(b, result, map[string]string{root: envelope.Authored}, statement, &directory)
	require.True(t, present)
	assert.Equal(t, "fail", claim.Status)
	assert.Equal(t, []string{"card_anchor_checkpoint_signer_differs"}, claim.Findings)

	// The same call on the untouched card passes, so it is the signer that failed.
	b = readBundle(t, c.card(t, "standard_history/1", true))
	root = b["root"].(string)
	result.Disclosures[0].CapsuleID = root
	claim, _, _ = cardAnchorWitnessClaim(b, result, map[string]string{root: envelope.Authored}, bundleStatement(t, b), &directory)
	assert.Equal(t, "pass", claim.Status, "%v", claim.Findings)
}

// bundleStatement is a bundle's own checkpoint statement.
func bundleStatement(t *testing.T, b map[string]any) []byte {
	t.Helper()
	cp := b["checkpoint"].(map[string]any)
	statement, err := base64.RawURLEncoding.DecodeString(cp["cose"].(string))
	require.NoError(t, err)
	return statement
}

func parseDirectory(t *testing.T, path string) witness.Directory {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	d, err := witness.ParseDirectory(raw)
	require.NoError(t, err)
	return d
}

// A held receipt whose bytes are damaged does not check under the pinned
// witness, so the writer leaves it out; the same tick's intact receipt goes in.
func TestTheWriterLeavesOutADamagedAnchorReceipt(t *testing.T) {
	c := witnessV0Seller(t)
	directory, err := pinnedWitnessDirectory(c.p)
	require.NoError(t, err)
	withSession(t, func(s *dealSession) {
		h, err := s.standardHistory(t.Context(), 0)
		require.NoError(t, err)
		require.NotNil(t, h.Tick.Receipt)
		block, leftOut := shAnchorWitness(h, directory)
		assert.NotNil(t, block, "the intact receipt")
		assert.False(t, leftOut)
		intact := *h.Tick.Receipt
		damaged := intact
		damaged.Receipt = append([]byte(nil), damaged.Receipt...)
		damaged.Receipt[len(damaged.Receipt)-1] ^= 0xff
		h.Tick.Receipt = &damaged
		block, leftOut = shAnchorWitness(h, directory)
		assert.Nil(t, block)
		assert.True(t, leftOut, "a held receipt that does not verify is reported as left out")
		// A directory with no usable row for the witness checks nothing.
		h.Tick.Receipt = &intact
		block, leftOut = shAnchorWitness(h, witness.Directory{})
		assert.Nil(t, block)
		assert.True(t, leftOut)
	})
}

// The claim withholds when the bundle's own checkpoint did not verify, and
// fails when the root card is not signed by its key.
func TestTheCardAnchorClaimNeedsAVerifiedBundle(t *testing.T) {
	c := witnessV0Seller(t)
	b := readBundle(t, c.card(t, "standard_history/1", true))
	root := b["root"].(string)
	result := aacbundle.VerificationResult{Disclosures: []aacbundle.DisclosureResult{{CapsuleID: root, Member: "agent_input", Status: disclosure.Match}}}
	directory := parseDirectory(t, c.directory(t))

	claim, _, present := cardAnchorWitnessClaim(b, result, map[string]string{root: envelope.Authored}, nil, &directory)
	require.True(t, present)
	assert.Equal(t, "withheld", claim.Status)
	assert.Equal(t, []string{"checkpoint_unverified"}, claim.Findings)

	for _, state := range []string{"", "invalid"} {
		claim, _, _ = cardAnchorWitnessClaim(b, result, map[string]string{root: state}, bundleStatement(t, b), &directory)
		assert.Equal(t, "fail", claim.Status, state)
		assert.Equal(t, []string{"card_unverified"}, claim.Findings, state)
	}
}
