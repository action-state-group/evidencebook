package cmd_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/capsulectl/internal/packfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The committed windowed deal store (evidencebook#852) through built
// binaries only, as the tau2 demo runs it: `deal window chain`, one `deal
// window respond` per window and per placed deal, `book report derive
// --chain` and `book report coverage`. The store is written in process under
// cli.DealClock (internal/deal's TestWriteWindowStoreFixture), since a
// released capsulectl has no clock flag; these commands run at the real clock,
// when both of its months have ended.

const windowStoreDir = "../internal/deal/testdata/deal/window-store"

const windowStorePack = `{"pack":"window-store-pack","version":1,"rows":["has-record"]}`

// runCapsulectl runs the built capsulectl in config and returns its output;
// a non-zero exit fails the test unless fail is set, which returns the output
// of the failed run.
func runCapsulectl(t *testing.T, bin, config string, fail bool, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+config, "CAPSULECTL_PLUGIN_ROOTS="+filepath.Join(filepath.Dir(bin), "plugins"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if fail {
		require.Error(t, err, "capsulectl %v: %s", args, stdout.String())
		return stdout.String() + stderr.String()
	}
	require.NoError(t, err, "capsulectl %v: %s%s", args, stdout.String(), stderr.String())
	return stdout.String()
}

func buildInstalledCapsulectl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "plugins"), 0o700))
	for path, pkg := range map[string]string{
		filepath.Join(dir, "capsulectl"):                 "github.com/action-state-group/capsule-cli/cmd/capsulectl",
		filepath.Join(dir, "plugins", "capsulectl-deal"): "github.com/action-state-group/evidencebook/capsulectl/cmd/capsulectl-deal",
		filepath.Join(dir, "plugins", "capsulectl-book"): "github.com/action-state-group/evidencebook/capsulectl/cmd/capsulectl-book",
	} {
		out, err := exec.Command("go", "build", "-o", path, pkg).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return filepath.Join(dir, "capsulectl")
}

func decode(t *testing.T, out string, into any) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(out), into), out)
}

func TestTheWindowStoreFixtureThroughTheBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("builds three binaries")
	}
	bin := buildInstalledCapsulectl(t)
	work, config := t.TempDir(), t.TempDir()

	// Copy the store before use: chain and respond write to it.
	storeCopy := filepath.Join(work, "store")
	require.NoError(t, os.MkdirAll(storeCopy, 0o700))
	entries, err := os.ReadDir(filepath.Join(windowStoreDir, "store"))
	require.NoError(t, err, "the fixture is regenerated with EB_WRITE_WINDOW_STORE=1 go test -run TestWriteWindowStoreFixture ./internal/deal")
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(windowStoreDir, "store", e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(storeCopy, e.Name()), raw, 0o600))
	}
	profile, err := os.ReadFile(filepath.Join(windowStoreDir, "deal.yaml"))
	require.NoError(t, err)
	profiles := filepath.Join(config, "capsule", "profiles")
	require.NoError(t, os.MkdirAll(profiles, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profiles, "deal.yaml"), bytes.ReplaceAll(profile, []byte("@STORE@"), []byte(storeCopy)), 0o600))
	t.Setenv("XDG_CONFIG_HOME", config)
	holder, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	holderKey, checkpointKey := holder.TrustedKeys[0], holder.Checkpoint.TrustedKeys[0]

	// The requester: its own book profile, keys from `key generate`.
	seeds := map[string]string{}
	for _, name := range []string{"record", "checkpoint"} {
		seed := filepath.Join(work, name+".seed")
		var made struct {
			PublicKey string `json:"public_key"`
		}
		decode(t, runCapsulectl(t, bin, config, false, "key", "generate", "--output", seed), &made)
		seeds[name] = made.PublicKey
	}
	require.NoError(t, os.WriteFile(filepath.Join(profiles, "req.yaml"), []byte(fmt.Sprintf(`name: req
type: jsonl
log_id: req
log: book
namespace: capsule
operator: req-operator
connection:
    database: %s
signing:
    file: %s
trusted_keys:
    - %s
checkpoint:
    signing:
        file: %s
    trusted_keys:
        - %s
`, filepath.Join(work, "req"), filepath.Join(work, "record.seed"), seeds["record"], filepath.Join(work, "checkpoint.seed"), seeds["checkpoint"])), 0o600))

	runCapsulectl(t, bin, config, false, "--profile", "req", "store", "init")

	chainFile := filepath.Join(work, "chain.json")
	var chained struct {
		Windows int `json:"windows"`
	}
	decode(t, runCapsulectl(t, bin, config, false, "--profile", "deal", "deal", "window", "chain", "--from", "2026-09", "--to", "2026-10", "--out", chainFile), &chained)
	require.Equal(t, 2, chained.Windows)
	var chain evidencebook.WindowChain
	chainRaw, err := os.ReadFile(chainFile)
	require.NoError(t, err)
	decode(t, string(chainRaw), &chain)
	_, findings := evidencebook.VerifyWindowChain(chain, evidencebook.VerifyChainOptions{CheckpointKey: checkpointKey})
	require.Empty(t, findings)

	// One pin per window and per placed deal.
	type pin struct {
		name  string
		bytes []byte
	}
	var pins []pin
	var dealIDs []string
	for _, w := range chain.Windows {
		require.Len(t, w.Deals, 1, "window %s holds one deal", w.Label)
		pins = append(pins, pin{"window-" + w.Label, w.FinalCheckpoint}, pin{"deal-" + w.Label, w.Deals[0].Latest})
		dealIDs = append(dealIDs, w.Deals[0].LogID)
	}

	packFile := filepath.Join(work, "pack.json")
	require.NoError(t, os.WriteFile(packFile, []byte(`{"document":`+windowStorePack+`,"recipients":["req"],"forms":["full_history"],"fields":["record_type","committed_at"]}`), 0o600))
	digest, err := packfile.Digest([]byte(windowStorePack))
	require.NoError(t, err)

	answers := map[string]string{}
	for _, p := range pins {
		sum := sha256.Sum256(p.bytes)
		request := filepath.Join(work, p.name+".json")
		require.NoError(t, os.WriteFile(request, []byte(`{"subject":{"full_history":null},"coverage":{"expected_pin":"`+hex.EncodeToString(sum[:])+`"},"derivation":"`+digest+`"}`), 0o600))
		var sent cli.EvidenceRequestResult
		decode(t, runCapsulectl(t, bin, config, false, "book", "request", "--profile", "req", "--request", request, "--responder", "deal", "--output", filepath.Join(work, p.name+".sent")), &sent)
		var responded struct {
			Outcome string `json:"outcome"`
		}
		decode(t, runCapsulectl(t, bin, config, false, "--profile", "deal", "deal", "window", "respond", "--request", filepath.Join(work, p.name+".sent"), "--requester", "req", "--output", filepath.Join(work, p.name+".reply"), "--pack", packFile), &responded)
		require.Equal(t, evidencebook.OutcomeArtifact, responded.Outcome, p.name)
		var recorded struct {
			RecordID string `json:"record_id"`
			Outcome  string `json:"outcome"`
		}
		decode(t, runCapsulectl(t, bin, config, false, "book", "request", "--profile", "req", "--for", sent.RecordID, "--response", filepath.Join(work, p.name+".reply"), "--responder-key", holderKey, "--responder-checkpoint-key", checkpointKey), &recorded)
		require.Equal(t, evidencebook.OutcomeArtifact, recorded.Outcome, p.name)
		answers[p.name] = recorded.RecordID
	}

	result := func(ids ...string) string {
		var evidence []any
		for _, id := range ids {
			evidence = append(evidence, map[string]any{"digest": id, "digest_alg": "SHA-256"})
		}
		claim := map[string]any{
			"id": "has-records", "contract_ref": "ec:window-store-pack@1", "requirement_ref": "has-record", "tier": "recomputed", "grade": "self-attested",
			"sufficiency": "SATISFIED", "verdict": "met", "evidence": evidence, "proofs": []any{},
			"presentation": map[string]any{"kind": "disclosure", "status": "SATISFIED", "evidence": evidence},
		}
		body, err := json.Marshal(map[string]any{
			"result_version": "evidence-result-v0", "generated_at": "2026-11-24T00:00:00Z", "claims": []any{claim},
			"aggregate": map[string]any{"buckets": map[string]any{"met": []any{"has-records"}, "not_met": []any{}, "not_evaluable": []any{}},
				"coverage": map[string]any{"evaluated_population": 1, "excluded_not_applicable": 0, "unknown_count": 0}},
		})
		require.NoError(t, err)
		path := filepath.Join(work, fmt.Sprintf("result-%d.json", len(ids)))
		require.NoError(t, os.WriteFile(path, body, 0o600))
		return path
	}
	derive := func(out string, fail bool, names ...string) string {
		var ids []string
		args := []string{"book", "report", "derive", "--profile", "req", "--chain", chainFile, "--pack", digest, "--out", out}
		for _, n := range names {
			ids = append(ids, answers[n])
			args = append(args, "--response", answers[n])
		}
		return runCapsulectl(t, bin, config, fail, append(args, "--result", result(ids...))...)
	}

	// Without the deals' answers, the gap is named and no bundle is written.
	missing := derive(filepath.Join(work, "missing.json"), true, "window-2026-09", "window-2026-10")
	assert.Contains(t, missing, "has no answer")
	assert.NoFileExists(t, filepath.Join(work, "missing.json"))

	bundle := filepath.Join(work, "bundle.json")
	derive(bundle, false, "window-2026-09", "deal-2026-09", "window-2026-10", "deal-2026-10")
	var coverage struct {
		Covered bool     `json:"covered"`
		Deals   []string `json:"deals"`
	}
	decode(t, runCapsulectl(t, bin, config, false, "book", "report", "coverage", "--bundle", bundle, "--holder-checkpoint-key", checkpointKey), &coverage)
	assert.True(t, coverage.Covered)
	require.Len(t, coverage.Deals, 2, "coverage names each placed deal")
	for i, id := range dealIDs {
		assert.Contains(t, coverage.Deals[i], id)
		assert.Contains(t, coverage.Deals[i], "window "+chain.Windows[i].Label)
	}
	assert.NotContains(t, strings.Join(coverage.Deals, "\n"), "no answer")
}
