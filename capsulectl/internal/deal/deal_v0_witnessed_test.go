// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v0WitnessFixture is an x-deal-v0 deal whose holder's book a witness
// covered at its checkpoint, with the witness's key and directory.
type v0WitnessFixture struct {
	p         cli.Profile
	dealID    string
	endpoint  string
	public    ed25519.PublicKey
	key       ed25519.PrivateKey
	directory string
	dir       string
	now       *time.Time
}

const v0WitnessAt = "2026-10-04T09:05:00Z"

func newV0WitnessFixture(t *testing.T, iat time.Time) v0WitnessFixture {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	dealID := retailDeal(t)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliverAt(t, p, cadenceSize(t, p), key, iat)
	return v0WitnessFixture{p: p, dealID: dealID, endpoint: endpoint, public: public, key: key,
		directory: writeDirectory(t, rawKeyRow(endpoint, public)), dir: t.TempDir(), now: now}
}

// copyAndProof writes the holder's copy and `deal checkpoint proof` for it.
func (f v0WitnessFixture) copyAndProof(t *testing.T, name string) (string, string, map[string]any) {
	t.Helper()
	copyPath := filepath.Join(f.dir, name+".json")
	dealRun(t, "report", "--deal", f.dealID, "--bundle", copyPath)
	proofPath := filepath.Join(f.dir, name+".proof.json")
	out := dealRun(t, "checkpoint", "proof", "--deal", f.dealID, "--copy", copyPath, "--out", proofPath)
	return copyPath, proofPath, out
}

func v0WitnessedOf(t *testing.T, args ...string) (map[string]any, string) {
	t.Helper()
	report := verifyReport(t, append([]string{"--bundle"}, args...)...)
	w, _ := v0Entry(t, report)["witnessed"].(map[string]any)
	return w, report["verdict"].(string)
}

// The holder's chain beside an x-deal-v0 copy gives its witness time
// (u219): verified with the directory's name for the witness and the
// receipt's own time; never a change of verdict; withheld with no
// directory; and the copy itself still carries no chain.
func TestV0WitnessedFromTheHoldersChain(t *testing.T) {
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	copyPath, proofPath, out := f.copyAndProof(t, "all")
	assert.Equal(t, "all", out["extent"])
	assert.NotContains(t, out, "note")
	assert.NotContains(t, string(mustRead(t, copyPath)), DealParentWitnessExtension, "the copy carries no chain (u225)")

	_, plain := v0WitnessedOf(t, copyPath)
	w, verdict := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", f.directory)
	assert.Equal(t, plain, verdict, "the witness time never changes the verdict")
	assert.Equal(t, map[string]any{"status": "verified", "witness": "test witness", "at": v0WitnessAt}, w)

	// The member has the viewer contract's shape (capsule-viewer
	// tests/testdata/deal-v0-report, main-standalone-keep.verify.json).
	raw, err := os.ReadFile("testdata/deal-v0-report/countersigned-keep.verify.json")
	require.NoError(t, err)
	var fixture map[string]any
	require.NoError(t, json.Unmarshal(raw, &fixture))
	want := v0Entry(t, fixture)["witnessed"].(map[string]any)
	for k := range want {
		assert.Contains(t, w, k)
	}

	none, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath)
	assert.Equal(t, "withheld", none["status"], "no directory")
	unknown, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", writeDirectory(t, rawKeyRow("https://other.example", f.public)))
	assert.Equal(t, "withheld", unknown["status"], "a witness the directory does not list")
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	wrongKey, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", writeDirectory(t, rawKeyRow(f.endpoint, other)))
	assert.Equal(t, "failed", wrongKey["status"], "a receipt that does not verify, and none that does")

	// A failing receipt beside one that verifies: the verified one counts.
	var proof v0ChainProof
	require.NoError(t, json.Unmarshal(mustRead(t, proofPath), &proof))
	inner := proof.Chain["parent"].(map[string]any)
	good := inner["witnesses"].([]any)[0].(map[string]any)
	bad := map[string]any{}
	for k, v := range good {
		bad[k] = v
	}
	bad["receipt_b64"] = strings.Repeat("A", 40)
	inner["witnesses"] = []any{bad, good}
	mixed := filepath.Join(f.dir, "mixed.proof.json")
	data, err := json.Marshal(proof)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(mixed, data, 0o600))
	m, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", mixed, "--witness-directory", f.directory)
	assert.Equal(t, "verified", m["status"])
}

// A receipt that verifies but states no time gives no time.
func TestV0WitnessedWithNoTimeIsWithheld(t *testing.T) {
	f := newV0WitnessFixture(t, time.Time{})
	copyPath, proofPath, _ := f.copyAndProof(t, "all")
	w, _ := v0WitnessedOf(t, copyPath, "--stamp-proof", proofPath, "--witness-directory", f.directory)
	assert.Equal(t, "withheld", w["status"])
}

// A chain witnessed for an earlier checkpoint, after a later step: its time
// is not the copy's, so none is given; another deal's chain fails.
func TestV0WitnessedOnlyForTheCopysOwnCheckpoint(t *testing.T) {
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	allCopy, allProof, _ := f.copyAndProof(t, "all")
	*f.now = f.now.Add(10 * time.Minute)
	dealRun(t, "note", "--deal", f.dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))
	partCopy, partProof, out := f.copyAndProof(t, "part")
	assert.Equal(t, "part", out["extent"])
	assert.Contains(t, out["note"], "earlier checkpoint")
	w, _ := v0WitnessedOf(t, partCopy, "--stamp-proof", partProof, "--witness-directory", f.directory)
	assert.Equal(t, "withheld", w["status"])
	assert.Contains(t, w["reason"], "earlier checkpoint")
	// The chain of the earlier copy beside the later one names another
	// checkpoint.
	stale, _ := v0WitnessedOf(t, partCopy, "--stamp-proof", allProof, "--witness-directory", f.directory)
	assert.Equal(t, "failed", stale["status"])
	still, _ := v0WitnessedOf(t, allCopy, "--stamp-proof", allProof, "--witness-directory", f.directory)
	assert.Equal(t, "verified", still["status"])
}

// checkpoint proof refuses a copy of another deal.
func TestV0CheckpointProofRefusesAnotherDealsCopy(t *testing.T) {
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	copyPath, _, _ := f.copyAndProof(t, "all")
	other := retailDeal(t)
	_, err := invoke(t, "", "--profile", "deal", "deal", "checkpoint", "proof", "--deal", other, "--copy", copyPath, "--out", filepath.Join(t.TempDir(), "p.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a copy of deal "+other)
}

// The page: the holder's own page with --witness-directory says the witness
// time in capsulectl's voice (u219); without it, no time; a shared page
// takes no --witness-directory (CAPSULECTL_CHROME).
func TestV0PageDrawsTheHoldersWitnessTime(t *testing.T) {
	chrome := chromeOrSkip(t)
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	with := reportV0Page(t, chrome, f.dealID, "", "--witness-directory", f.directory)
	require.Len(t, with.Rungs["witness"], 1)
	assert.Contains(t, with.Rungs["witness"][0], "test witness")
	assert.NotEqual(t, "No witness result was reported to this page.", with.Rungs["witness"][0])
	without := reportV0Page(t, chrome, f.dealID, "")
	assert.Equal(t, []string{"No witness result was reported to this page."}, without.Rungs["witness"])

	_, err := invoke(t, "", "--profile", "deal", "deal", "report", "--deal", f.dealID, "--html", filepath.Join(t.TempDir(), "s.html"),
		"--share", cli.DealAudienceCounterparty, "--to", "the shop", "--witness-directory", f.directory)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a shared copy carries none")
}

// A copy whose report is sealed in the deal's log reads its witness time the
// same way (the viewer's contract reads every report form).
func TestV0WitnessedForAnInLogKeepCopy(t *testing.T) {
	inLogReports(t)
	f := newV0WitnessFixture(t, time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC))
	// Sealing the report in the log adds a record after the first tick, so
	// the copy's checkpoint is witnessed only by a later tick.
	copyPath := filepath.Join(f.dir, "inlog.json")
	dealRun(t, "report", "--deal", f.dealID, "--bundle", copyPath)
	*f.now = f.now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliverAt(t, f.p, cadenceSize(t, f.p), f.key, time.Date(2026, 10, 4, 11, 5, 0, 0, time.UTC))
	proofPath := filepath.Join(f.dir, "inlog.proof.json")
	assert.Equal(t, "all", dealRun(t, "checkpoint", "proof", "--deal", f.dealID, "--copy", copyPath, "--out", proofPath)["extent"])
	report := verifyReport(t, "--bundle", copyPath, "--stamp-proof", proofPath, "--witness-directory", f.directory)
	entry := v0Entry(t, report)
	require.NotEqual(t, "standalone", entry["report"], "the report is sealed in the log")
	assert.Equal(t, "verified", entry["witnessed"].(map[string]any)["status"], "%v", entry["witnessed"])
}
