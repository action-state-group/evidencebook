// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	evidencebook "github.com/action-state-group/evidencebook"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withheldV1 writes the buyer's withheld bundle of h's deal and returns its path.
func withheldV1(t *testing.T, h *handshakeV1) string {
	t.Helper()
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "withheld.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", file, "--withheld")
	return file
}

// forgedWithheldBundle is the withheld form of a book that has logID as its
// id but lives on another shelf, under other keys: it verifies on its own.
func forgedWithheldBundle(t *testing.T, logID string) string {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	signer, err := evidencebook.NewEd25519Signer(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{41}, ed25519.SeedSize)))
	require.NoError(t, err)
	originals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "originals.jsonl"), "forger", signer.PublicKey())
	require.NoError(t, err)
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{Path: filepath.Join(dir, "forger.db"), LockPath: filepath.Join(dir, "forger.lock"),
		Operator: "forger", Signer: signer, CheckpointKey: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42}, ed25519.SeedSize)), Originals: originals})
	require.NoError(t, err)
	t.Cleanup(func() { _ = shelf.Release() })
	book, err := shelf.Open(ctx, logID)
	require.NoError(t, err)
	rec, err := book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: "forged", Payloads: [][]byte{[]byte("forged")}})
	require.NoError(t, err)
	cp, err := book.Checkpoint(ctx)
	require.NoError(t, err)
	bundle, err := book.Bundle(ctx, evidencebook.BundleRequest{Root: rec.RecordID, Payloads: evidencebook.PayloadsAll, At: &cp,
		DisclosureRecordedElsewhere: true, ProducerKey: signer.PublicKey()})
	require.NoError(t, err)
	withheld, err := withheldForm(bundle.JSON)
	require.NoError(t, err)
	path := filepath.Join(dir, "forged.json")
	require.NoError(t, os.WriteFile(path, withheld, 0o600))
	return path
}

// --withheld goes with --bundle and only as your own file: it is the bundle
// a countersigner is asked to sign, not a copy for the other side.
func TestAV1WithheldFlagRefusals(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	file := filepath.Join(t.TempDir(), "w.json")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--withheld"), "use it with --bundle FILE")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--bundle", file, "--withheld", "--share", "counterparty", "--to", "Kettle Shop"), "drop --share")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--out", file, "--bundle", file, "--withheld"), "choose one")
	_, err := os.Stat(file)
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing was written")
}

// The withheld bundle leaves the device, so the real share gate reads its
// exact bytes, as for a copy for the other side, before it is written; a
// gate that refuses leaves no file.
func TestAV1WithheldBundlePassesTheShareGate(t *testing.T) {
	h := agreedBoth(t, oneForum)
	h.buyer.act(t)
	var gotAudience string
	var gotBytes []byte
	v1ShareGate = func(data []byte, events []sealedEvent, audience string, own ...string) error {
		gotAudience, gotBytes = audience, data
		return dealShareGate(data, events, audience, own...)
	}
	t.Cleanup(func() { v1ShareGate = dealShareGate })
	file := filepath.Join(t.TempDir(), "w.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", file, "--withheld")
	assert.Equal(t, "counterparty", gotAudience)
	written, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, scrubPaymentRefs(written), gotBytes)

	v1ShareGate = func([]byte, []sealedEvent, string, ...string) error {
		return cli.InputError("the gate refused")
	}
	refused := filepath.Join(t.TempDir(), "refused.json")
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--bundle", refused, "--withheld"), "the gate refused")
	_, err = os.Stat(refused)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// `deal countersign` on a v1 deal refuses what it cannot vouch for: a
// countersignature that does not verify (the file is left as it was), and a
// bundle that is not this deal's (the other side's book, or a bundle whose
// checkpoint this book does not hold).
func TestAV1CountersignRefusesWhatItCannotVouchFor(t *testing.T) {
	h := agreedBoth(t, offerTo)
	file := withheldV1(t, h)
	before, err := os.ReadFile(file)
	require.NoError(t, err)
	entry, signerKey := countersignerEntry(t, strings.Repeat("ab", 32), nil) // over another digest
	directory := countersignerDirectoryFile(t, "Example Countersigner", signerKey)
	assert.Contains(t, v1Err(t, "countersign", "--deal", h.dealID, "--bundle", file, "--entry", writeEntry(t, entry), "--directory", directory),
		"refusing to show a countersignature that does not verify")
	after, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, before, after, "nothing was written")

	// The seller's withheld bundle is of the seller's book, not the buyer's.
	h.seller.act(t)
	sellers := filepath.Join(t.TempDir(), "seller.json")
	dealRun(t, "report", "--deal", h.dealID, "--bundle", sellers, "--withheld")
	h.buyer.act(t)
	assert.Contains(t, v1Err(t, "countersign", "--deal", h.dealID, "--bundle", sellers, "--directory", directory), "its log is not this deal's book")

	// A bundle edited after it was written does not verify.
	b := readBundleFile(t, file)
	b["checkpoint"].(map[string]interface{})["cose"] = "AAAA"
	edited := writeJSONFile(t, "edited.json", b)
	assert.Contains(t, v1Err(t, "countersign", "--deal", h.dealID, "--bundle", edited, "--directory", directory), "is not this deal's bundle: it does not verify")

	// A bundle that verifies, of a book with this deal's log id kept by
	// someone else: its checkpoint is not one this deal's book holds.
	logID := readBundleFile(t, file)["completeness_certificate"].(map[string]interface{})["log_id"].(string)
	forged := forgedWithheldBundle(t, logID)
	assert.Contains(t, v1Err(t, "countersign", "--deal", h.dealID, "--bundle", forged, "--directory", directory), "its checkpoint is not one this deal's book holds")

	// No directory, on the command or the profile.
	assert.Contains(t, v1Err(t, "countersign", "--deal", h.dealID, "--bundle", file), "--directory is required")
}

// A countersignature by the producer's own key is not a rung: the withheld
// bundle declares the profile's signing key as its producer key (a deal
// profile also trusts its own key: the store refuses records otherwise), so
// even a directory that lists that key leaves it self_countersigned, on `deal
// countersign` and in the report.
func TestAV1SelfCountersignatureIsNotIndependent(t *testing.T) {
	h := agreedBoth(t, offerTo)
	file := withheldV1(t, h)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	own, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	ownHex := hex.EncodeToString(own.Public().(ed25519.PublicKey))
	b := readBundleFile(t, file)
	declared := b["extensions"].(map[string]interface{})[producerKeyExtension].(map[string]interface{})
	assert.Equal(t, ownHex, declared["public_key"], "the producer key is the profile's signing key")

	entry := cli.CountersignatureEntry{
		Type: cli.CountersignAPI, Over: mustDigest(t, b),
		Signer:    cli.CountersignSigner{ID: "me", KeyID: ownHex},
		Statement: cli.CountersignStatement{Checks: []cli.CountersignCheck{{Name: "everything", Result: "established"}}, RecomputedAt: "2026-10-04T00:00:00Z", Scope: cli.CountersignScope{LedgerID: "deal"}},
	}
	signCountersignEntry(t, own, &entry)
	directory := countersignerDirectoryFile(t, "Looks Independent", ownHex)
	attached := dealRun(t, "countersign", "--deal", h.dealID, "--bundle", file, "--entry", writeEntry(t, entry), "--directory", directory)
	assert.Equal(t, "self_countersigned", asView(t, attached["countersign"]).Finding)

	_, err = invoke(t, "", "profile", "update", "--profile", "deal", "--countersign-directory", directory)
	require.NoError(t, err)
	stamp := asStamp(t, dealRun(t, "report", "--deal", h.dealID)["countersign"])
	assert.Empty(t, stamp.Rung)
	assert.Equal(t, "self_countersigned", stamp.Finding)
	assert.Empty(t, stamp.Covers)
}

// Every record Capsule in a withheld bundle carries exactly the members
// withheldCapsuleMembers lists, on both sides' books and across record kinds
// (agreement, statement, dispute), and a Capsule with any other member is
// refused: the docs name that set as what a countersigner sees.
func TestAV1WithheldCapsulesCarryOnlyTheListedMembers(t *testing.T) {
	h := agreedBoth(t, oneForum)
	for _, side := range []v1Side{h.buyer, h.seller} {
		side.act(t)
		dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	}
	h.buyer.act(t)
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", filepath.Join(t.TempDir(), "d.json"))
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	seen := map[string]bool{}
	for _, side := range []v1Side{h.buyer, h.seller} {
		side.act(t)
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		file := filepath.Join(t.TempDir(), "w.json")
		dealRun(t, "report", "--deal", h.dealID, "--bundle", file, "--withheld")
		records := readBundleFile(t, file)["records"].([]interface{})
		require.GreaterOrEqual(t, len(records), 2)
		for _, r := range records {
			capsule := r.(map[string]interface{})
			for member := range capsule {
				assert.Contains(t, withheldCapsuleMembers, member)
				seen[member] = true
			}
			assert.Equal(t, map[string]interface{}{"compute_attestation": map[string]interface{}{"agent_input_digest": capsule["model_attestation"].(map[string]interface{})["compute_attestation"].(map[string]interface{})["agent_input_digest"]}},
				capsule["model_attestation"], "only the header's digest")
		}
	}
	for _, member := range withheldCapsuleMembers {
		if member == "settlement" {
			continue // a payment leg's: TestAV1WithheldBundleOfADealWithPayments
		}
		assert.True(t, seen[member], "listed member %s appears in some Capsule", member)
	}

	b := readBundleFile(t, withheldV1(t, h))
	b["records"].([]interface{})[0].(map[string]interface{})["evidence_record_header"] = map[string]interface{}{"record_type": "x"}
	b["extensions"] = map[string]interface{}{producerKeyExtension: b["extensions"].(map[string]interface{})[producerKeyExtension]}
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	_, err = withheldForm(raw)
	require.ErrorContains(t, err, `carries "evidence_record_header"`)

	b = readBundleFile(t, withheldV1(t, h))
	b["records"].([]interface{})[0].(map[string]interface{})["model_attestation"].(map[string]interface{})["compute_attestation"].(map[string]interface{})["agent_output_digest"] = "00"
	raw, err = json.Marshal(b)
	require.NoError(t, err)
	_, err = withheldForm(raw)
	require.ErrorContains(t, err, "a model_attestation other than the header's digest")
}

// A deal with payments, as an ARD deal has (evidencebook#998): the buyer pays
// and the seller records it, the seller refunds and the buyer records the
// refund. Each side's withheld bundle carries its own legs' settlement members
// exactly as sealed, nothing else beyond the listed members; `countersign
// request` takes it, and a countersignature over it is stored and stated. A
// settlement member that is not a payer or payee leg is refused.
func TestAV1WithheldBundleOfADealWithPayments(t *testing.T) {
	h, check := buyerChecked(t, v1LnHash)
	legs := map[string][]map[string]any{} // side -> the settlement members its pushed legs carry
	pushed := map[string][]string{}       // side -> its pushed leg files, which the other side receives
	push := func(side string, args ...string) map[string]any {
		file := filepath.Join(t.TempDir(), "leg.json")
		out := dealRun(t, append(args, "--out", file)...)
		legs[side] = append(legs[side], settlementOf(t, file))
		pushed[side] = append(pushed[side], file)
		return out
	}
	dealRun(t, "settle", "--deal", h.dealID, "--check", check, "--sent", "--payment-ref-type", "ln.payment_hash")
	paid := push("buyer", "settle", "--deal", h.dealID, "--check", check, "--status", "settled")

	h.seller.act(t)
	received := push("seller", "settle", "--deal", h.dealID, "--payment-ref-type", "ln.payment_hash", "--payment-ref", v1LnHash,
		"--received", "4470", "--receive-fee", "30", "--status", "settled")
	dealRun(t, "note", "--deal", h.dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening))
	out := dealRun(t, "check", "--deal", h.dealID, "--input", writeJSON(t, `{"action":"cancel","amount_minor":4500,"terms":{"item":"lamp","price_minor":4500,"currency":"USD"}}`))
	require.Equal(t, "pass", out["verdict"], "%v", out)
	refundRef := strings.Repeat("71", 32)
	dealRun(t, "settle", "--deal", h.dealID, "--check", out["check_id"].(string), "--reverses", received["digest"].(string), "--sent", "--payment-ref-type", "ln.payment_hash", "--payment-ref", refundRef)
	push("seller", "settle", "--deal", h.dealID, "--check", out["check_id"].(string), "--reverses", received["digest"].(string), "--status", "settled")

	h.buyer.act(t)
	push("buyer", "settle", "--deal", h.dealID, "--reverses", paid["digest"].(string), "--payment-ref-type", "ln.payment_hash", "--payment-ref", refundRef,
		"--received", "4500", "--receive-fee", "0", "--status", "settled")
	// Each side holds the other's legs too: they stay out of its withheld bundle.
	for _, f := range pushed["buyer"] {
		h.seller.act(t)
		assert.Equal(t, "kept, and counted", dealRun(t, "receive", f)["record"])
	}
	for _, f := range pushed["seller"] {
		h.buyer.act(t)
		assert.Equal(t, "kept, and counted", dealRun(t, "receive", f)["record"])
	}
	clockAt(t, cli.DealClock().Add(72*time.Hour))

	files := map[string]string{}
	for name, side := range map[string]v1Side{"buyer": h.buyer, "seller": h.seller} {
		side.act(t)
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		file := filepath.Join(t.TempDir(), name+".json")
		dealRun(t, "report", "--deal", h.dealID, "--bundle", file, "--withheld")
		files[name] = file
		var shown []map[string]any
		for _, r := range readBundleFile(t, file)["records"].([]interface{}) {
			capsule := r.(map[string]interface{})
			for member := range capsule {
				assert.Contains(t, withheldCapsuleMembers, member)
			}
			if m, ok := capsule["settlement"]; ok {
				var plain map[string]any // json.Number to float64, as settlementOf reads a leg
				raw, err := json.Marshal(m)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(raw, &plain))
				shown = append(shown, plain)
			}
		}
		assert.ElementsMatch(t, legs[name], shown, "%s's bundle shows exactly its own legs, as sealed", name)
		// The docs promise the countersigner never sees a preimage. An
		// x-deal-v1 settle takes none (deal settle has no such input), so the
		// property checked is that no member of that name reaches the file.
		written, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		assert.NotContains(t, strings.ToLower(string(written)), "preimage", "%s's bundle carries no preimage", name)
		err := countersignRequest(t, file)
		assert.Contains(t, err.Error(), "certificate", "%s: the request took the bundle and failed only at the untrusted service", name)
	}
	require.Len(t, legs["buyer"], 2)
	require.Len(t, legs["seller"], 2)

	// A countersignature over the buyer's bundle is stored and stated.
	h.buyer.act(t)
	b := readBundleFile(t, files["buyer"])
	entry, signerKey := countersignerEntry(t, mustDigest(t, b), []cli.CountersignCheck{{Name: "range membership", Result: "established"}})
	directory := countersignerDirectoryFile(t, "Example Countersigner", signerKey)
	attached := dealRun(t, "countersign", "--deal", h.dealID, "--bundle", files["buyer"], "--entry", writeEntry(t, entry), "--directory", directory)
	assert.Equal(t, "countersigned", asView(t, attached["countersign"]).Rung)
	_, err := invoke(t, "", "profile", "update", "--profile", "deal", "--countersign-directory", directory)
	require.NoError(t, err)
	assert.Equal(t, "countersigned", asStamp(t, dealRun(t, "report", "--deal", h.dealID)["countersign"]).Rung)

	// Another value under the name settlement is refused.
	for _, value := range []any{"a string", map[string]interface{}{"leg": "terms"}, map[string]interface{}{}} {
		b := readBundleFile(t, files["buyer"])
		for _, r := range b["records"].([]interface{}) {
			if capsule := r.(map[string]interface{}); capsule["settlement"] != nil {
				capsule["settlement"] = value
				break
			}
		}
		raw, err := json.Marshal(b)
		require.NoError(t, err)
		_, err = withheldForm(raw)
		require.ErrorContains(t, err, "not a payer or payee leg", "%v", value)
	}
}
