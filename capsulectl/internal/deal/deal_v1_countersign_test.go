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
		assert.True(t, seen[member], "listed member %s appears in some Capsule", member)
	}

	b := readBundleFile(t, withheldV1(t, h))
	b["records"].([]interface{})[0].(map[string]interface{})["evidence_record_header"] = map[string]interface{}{"record_type": "x"}
	b["extensions"] = map[string]interface{}{producerKeyExtension: b["extensions"].(map[string]interface{})[producerKeyExtension]}
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	_, err = withheldForm(raw)
	require.ErrorContains(t, err, `carries "evidence_record_header"`)
}
