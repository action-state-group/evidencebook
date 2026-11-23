package book

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A book store split into one book per month (evidencebook#689), through the
// real commands: the holder creates it with `book window init`, records in
// September and October, rolls into December, hands over the chain of
// September to November; the requester asks each window pinned to its final
// checkpoint, seals one Result over the three answers with `report derive
// --chain`, and `report coverage` and `report build` check the range.

type windowExchange struct {
	*packExchange
	set   func(time.Time)
	chain string
}

func newWindowExchange(t *testing.T) *windowExchange {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	a, _ := bookProfile(t, "a")
	initBook(t, a)
	b, bKeys := bookProfile(t, "b")
	out, err := invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "month")
	require.NoError(t, err, out)
	var created windowInitResult
	require.NoError(t, json.Unmarshal([]byte(out), &created), out)
	assert.Equal(t, evidencebook.WindowMonth, created.Period)
	assert.Equal(t, "2026-09-01T00:00:00Z", created.Since)
	assert.Equal(t, "window: month (calendar months, UTC), fixed for this store", created.Notice)

	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC))
	appendHalves(t, b, half{"x2", "r2", "p2"})
	set(time.Date(2026, 12, 2, 10, 0, 0, 0, time.UTC))

	x := &windowExchange{packExchange: &packExchange{evidenceExchange: &evidenceExchange{t: t, dir: t.TempDir(), b: b, bKeys: bKeys}}, set: set}
	digest, err := documentDigest([]byte(cliPackDocument))
	require.NoError(t, err)
	x.digest = digest
	x.writePack("pack.json", packFileBody(""))
	return x
}

// answer asks the pack over one window of the chain, pinned to its final
// checkpoint, and records the answer.
func (x *windowExchange) answer(window evidencebook.ChainWindow, n int) string {
	x.t.Helper()
	sum := sha256.Sum256(window.FinalCheckpoint)
	name := fmt.Sprintf("w%d", n)
	sent := x.ask(name, `{"subject":{"full_history":null},"coverage":{"expected_pin":"`+hex.EncodeToString(sum[:])+`"},"derivation":"`+x.digest+`"}`)
	reply := x.respond(name)
	require.NotNil(x.t, reply.Envelope, "%+v", reply.Refusal)
	out, err := invoke(x.t, "", "request", "--profile", "a", "--for", sent.RecordID, "--response", filepath.Join(x.dir, name+".response"),
		"--responder-key", hex.EncodeToString(x.bKeys.record), "--responder-checkpoint-key", hex.EncodeToString(x.bKeys.checkpoint))
	require.NoError(x.t, err, out)
	var got struct {
		RecordID string `json:"record_id"`
		Outcome  string `json:"outcome"`
	}
	require.NoError(x.t, json.Unmarshal([]byte(out), &got), out)
	require.Equal(x.t, evidencebook.OutcomeArtifact, got.Outcome, out)
	return got.RecordID
}

// windowsResult is one claim citing every answer.
func (x *windowExchange) windowsResult(responses []string) string {
	var evidence []any
	for _, r := range responses {
		evidence = append(evidence, map[string]any{"digest": r, "digest_alg": "SHA-256"})
	}
	claim := map[string]any{
		"id": "has-record", "contract_ref": cliPackContract, "requirement_ref": "has-record", "tier": "recomputed", "grade": "self-attested",
		"sufficiency": "SATISFIED", "verdict": "met", "proofs": []any{}, "evidence": evidence,
		"presentation": map[string]any{"kind": "disclosure", "status": "SATISFIED", "evidence": evidence},
	}
	raw, err := json.Marshal(map[string]any{
		"result_version": "evidence-result-v0", "generated_at": "2026-12-02T00:00:00Z", "claims": []any{claim},
		"aggregate": map[string]any{"buckets": map[string]any{"met": []any{"has-record"}, "not_met": []any{}, "not_evaluable": []any{}},
			"coverage": map[string]any{"evaluated_population": 1, "excluded_not_applicable": 0, "unknown_count": 0}},
	})
	require.NoError(x.t, err)
	path := filepath.Join(x.dir, "windows-result.json")
	require.NoError(x.t, os.WriteFile(path, raw, 0o600))
	return path
}

func TestAReportOverMonthWindowsThroughTheCommands(t *testing.T) {
	x := newWindowExchange(t)

	out, err := invoke(t, "", "book", "window", "roll", "--profile", "b")
	require.NoError(t, err, out)
	var rolled windowListResult
	require.NoError(t, json.Unmarshal([]byte(out), &rolled), out)
	var labels []string
	for _, w := range rolled.Windows {
		labels = append(labels, w.Label)
	}
	assert.Equal(t, []string{"2026-09", "2026-10", "2026-11", "2026-12"}, labels, "November had no record and still has its book")
	assert.True(t, rolled.Windows[2].Closed)
	assert.False(t, rolled.Windows[3].Closed)

	chainPath := filepath.Join(x.dir, "chain.json")
	out, err = invoke(t, "", "book", "window", "chain", "--profile", "b", "--from", "2026-09", "--to", "2026-11", "--out", chainPath)
	require.NoError(t, err, out)
	var chain evidencebook.WindowChain
	require.NoError(t, json.Unmarshal(mustRead(t, chainPath), &chain))
	require.Len(t, chain.Windows, 3)
	_, findings := evidencebook.VerifyWindowChain(chain, evidencebook.VerifyChainOptions{CheckpointKey: hex.EncodeToString(x.bKeys.checkpoint)})
	assert.Empty(t, findings)

	var responses []string
	for i, w := range chain.Windows {
		responses = append(responses, x.answer(w, i+1))
	}
	bundle := filepath.Join(x.dir, "bundle.json")
	args := []string{"report", "derive", "--profile", "a", "--chain", chainPath, "--pack", x.digest, "--result", x.windowsResult(responses), "--out", bundle}
	for _, r := range responses {
		args = append(args, "--response", r)
	}
	out, err = invoke(t, "", args...)
	require.NoError(t, err, out)
	var derived reportWindowsResult
	require.NoError(t, json.Unmarshal([]byte(out), &derived), out)
	assert.Equal(t, "2026-09", derived.From)
	assert.Equal(t, "2026-11", derived.To)
	assert.NotEmpty(t, derived.BundleDigest)

	holderKey := hex.EncodeToString(x.bKeys.checkpoint)
	out, err = invoke(t, "", "report", "coverage", "--bundle", bundle, "--holder-checkpoint-key", holderKey)
	require.NoError(t, err, out)
	var coverage reportCoverageResult
	require.NoError(t, json.Unmarshal([]byte(out), &coverage), out)
	assert.True(t, coverage.Covered)
	assert.Len(t, coverage.Windows, 3)
	assert.Equal(t, x.digest, coverage.Derivation)
	assert.Equal(t, []string{"Without --witness-directory, the windows' continuity is as the holder signed it.", "No check shows that an answer holds every row of its window."}, coverage.Limits)

	// Witnesses named, no receipt carried: every window is named unwitnessed.
	directory := writeDirectory(t, map[string]interface{}{"name": "w", "endpoint": "https://witness.example", "binding": "cll", "key_ids": []string{strings.Repeat("ab", 32)}})
	out, err = invoke(t, "", "report", "coverage", "--bundle", bundle, "--holder-checkpoint-key", holderKey, "--witness-directory", directory)
	require.Error(t, err)
	assert.Contains(t, out, "window 2026-09 not witnessed")
	assert.NotContains(t, out, `"limits"`, "limits are printed only for a range that passes")
	_, err = invoke(t, "", "report", "derive", "--profile", "a", "--chain", chainPath, "--pack", x.digest, "--result", x.windowsResult(responses), "--out", filepath.Join(x.dir, "u.json"), "--response", responses[0], "--allow-unproven")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--allow-unproven does not apply with --chain")

	other, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	out, err = invoke(t, "", "report", "coverage", "--bundle", bundle, "--holder-checkpoint-key", hex.EncodeToString(other))
	require.Error(t, err, "under a key the verifier did not obtain, nothing is covered")
	assert.Contains(t, out, `"covered":false`)

	out, err = invoke(t, "", "verify", "--bundle", bundle)
	require.NoError(t, err, out)
	assert.Contains(t, out, `"verdict":"VALID"`)

	page := filepath.Join(x.dir, "report.html")
	_, err = invoke(t, "", "report", "build", "--bundle", bundle, "--card", "outcome", "--out", page)
	require.Error(t, err, "a report over windows needs the holder's key")
	assert.Contains(t, err.Error(), "--holder-checkpoint-key")
	out, err = invoke(t, "", "report", "build", "--bundle", bundle, "--card", "outcome", "--out", page, "--holder-checkpoint-key", holderKey)
	require.NoError(t, err, out)
	html := string(mustRead(t, page))
	assert.Contains(t, html, "<strong>Covers 2026-09 to 2026-11.</strong> Each of the 3 windows has its answer")

	// The answers were recorded in December, the window open when asked;
	// the closed windows were not written.
	out, err = invoke(t, "", "book", "window", "list", "--profile", "b")
	require.NoError(t, err, out)
	var listed windowListResult
	require.NoError(t, json.Unmarshal([]byte(out), &listed), out)
	for i, w := range listed.Windows[:3] {
		assert.Equal(t, rolled.Windows[i].Final, w.Final, w.Label)
	}

	// The page sealed in December's window still checks once the store has
	// rolled into January: its seal is in December's book.
	_, sealed, err := runReportBuild(t, bundle, "--card", "outcome", "--holder-checkpoint-key", holderKey, "--profile", "b", "--seal")
	require.NoError(t, err)
	x.set(time.Date(2027, 1, 5, 10, 0, 0, 0, time.UTC))
	_, err = invoke(t, "", "book", "window", "roll", "--profile", "b")
	require.NoError(t, err)
	checked, err := checkPageFile(t, x.b, sealed)
	require.NoError(t, err)
	assert.Equal(t, "match", checked.Page)

	// A report over windows gets no permalink, a diagnostic page or coverage
	// flags it does not use.
	for name, extra := range map[string][]string{
		"permalink":  {"--holder-checkpoint-key", holderKey, "--permalink"},
		"diagnostic": {"--holder-checkpoint-key", holderKey, "--diagnostic"},
	} {
		_, _, err := runReportBuild(t, bundle, append([]string{"--card", "outcome"}, extra...)...)
		require.Error(t, err, name)
	}
	_, _, err = runReportBuild(t, bundle, "--card", "outcome", "--holder-checkpoint-key", holderKey, "--witness-directory", filepath.Join(x.dir, "absent.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--witness-directory")

	// A windowed requester store derives only over answers in its current
	// window.
	_, err = invoke(t, "", "report", "derive", "--profile", "b", "--chain", chainPath, "--pack", x.digest, "--result", x.windowsResult(responses), "--out", filepath.Join(x.dir, "b.json"), "--response", responses[0])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in this store's current window")

	// Two answers for three windows: the gap is named and nothing is written.
	missing := filepath.Join(x.dir, "missing.json")
	args = []string{"report", "derive", "--profile", "a", "--chain", chainPath, "--pack", x.digest, "--result", x.windowsResult([]string{responses[0], responses[2]}), "--out", missing, "--response", responses[0], "--response", responses[2]}
	_, err = invoke(t, "", args...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "window 2026-10 has no answer")
	_, statErr := os.Stat(missing)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
	// A rerun over the same chain reuses its record: the requester's book
	// holds one window_chain record however many runs there were.
	withBook(t, x.a(), func(book *evidencebook.Book) {
		chains, err := book.Query(t.Context(), evidencebook.Filter{RecordType: evidencebook.RecordTypeWindowChain})
		require.NoError(t, err)
		assert.Len(t, chains, 1)
	})
}

func TestAWindowedStoreIsCreatedOnceAndKeepsItsWindow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "fortnight")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--period must be")
	_, err = invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "quarter")
	require.NoError(t, err)
	_, err = invoke(t, "", "book", "window", "init", "--profile", "b", "--period", "month")
	require.Error(t, err, "a store's window is fixed when it is created")
	_, err = invoke(t, "", "store", "init", "--profile", "b")
	require.NoError(t, err, "store init leaves a windowed store as it is")
	_, statErr := os.Stat(filepath.Join(b.Connection.Database, "book", "log.jsonl"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
	policy, err := evidencebook.ReadShelfWindow(t.Context(), shelfFile(b))
	require.NoError(t, err)
	assert.Equal(t, evidencebook.WindowQuarter, policy.Period)
	_, err = invoke(t, "", "book", "migrate", "--profile", "b", "--log-id", "b2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "split by window")

	a, _ := bookProfile(t, "a")
	initBook(t, a)
	_, err = invoke(t, "", "book", "window", "list", "--profile", "a")
	require.Error(t, err, "a JSONL book has no windows")
	assert.Contains(t, err.Error(), "not split by window")
}

func TestAWindowedStoreClosesOnlyItsCurrentWindow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "b")
	require.NoError(t, err)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC))
	// A day inside September, the current window, closes.
	out, err := invoke(t, "", "close", "--profile", "b", "--counterparty", "a", "--period", "day", "--date", "2026-09-10")
	require.NoError(t, err, out)
	// August is before the store began, and September is not over: once it
	// is, its records are in a closed window's book.
	set(time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC))
	_, err = invoke(t, "", "close", "--profile", "b", "--counterparty", "a", "--period", "month", "--date", "2026-09-01")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not inside the current window 2026-10")
}

func TestARollQueuesEachFinalCheckpointForTheWitness(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	witnessKey, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	b.Checkpoint.Endpoint = "https://witness.example.invalid"
	b.Checkpoint.PublicKey = hex.EncodeToString(witnessKey)
	require.NoError(t, cli.SaveProfile(b, true))
	_, err = invoke(t, "", "book", "window", "init", "--profile", "b")
	require.NoError(t, err)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC))
	_, err = invoke(t, "", "book", "window", "roll", "--profile", "b")
	require.NoError(t, err)
	service, err := cli.ServiceID(b)
	require.NoError(t, err)
	for _, label := range []string{"2026-09", "2026-10"} {
		states, err := windowWitnessStore(b, label).load()
		require.NoError(t, err)
		require.Len(t, states, 1, label)
		assert.Equal(t, service, states[0].WitnessID)
	}
	// The core's checkpoint of the current window keeps its own delivery.
	out, err := invoke(t, "", "cll", "checkpoint", "create", "--profile", "b")
	require.NoError(t, err, out)
	assert.Contains(t, out, `"log_id":"b/2026-11"`)
	states, err := windowWitnessStore(b, "2026-11").load()
	require.NoError(t, err)
	assert.Len(t, states, 1)
}

// A verb that only reads opens the store with throwaway keys and never
// rolls it: a roll writes window records.
func TestAReadingVerbNeverRollsAWindowedStore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	_, err := invoke(t, "", "book", "window", "init", "--profile", "b")
	require.NoError(t, err)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC))
	_ = listLog(t, "b") // cll list reads the newest window (published capsules only)
	_, err = invoke(t, "", "report", "check", "--profile", "b", "--page", writeJSON(t, "<html></html>"))
	require.Error(t, err, "not a report page; refused before the store is opened")
	out, err := invoke(t, "", "book", "window", "list", "--profile", "b")
	require.NoError(t, err, out)
	var windows windowListResult
	require.NoError(t, json.Unmarshal([]byte(out), &windows), out)
	require.Len(t, windows.Windows, 1, "still September alone")
	assert.False(t, windows.Windows[0].Closed)

	// A live windowed store refuses a caller time.
	t.Setenv(SourceDateEpochEnv, "1790000000")
	_, err = invoke(t, "", "book", "window", "roll", "--profile", "b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "live windowed store")
}

func (x *windowExchange) a() cli.Profile {
	p, err := cli.LoadProfile("a")
	require.NoError(x.t, err)
	return p
}

// A bundle that is not a report over windows takes no coverage flags.
func TestReportBuildRefusesCoverageFlagsOnAnotherReport(t *testing.T) {
	bundle, _ := bookFormBundle(t)
	_, _, err := runReportBuild(t, bundle, "--card", "outcome", "--holder-checkpoint-key", strings.Repeat("ab", 32))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cites no window chain")
}

func TestWindowInitRefusesAProfileWithAnUnmigratedLog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, day1)
	p, _ := bookProfile(t, "a")
	legacyLog(t, p, 1, 0, false)
	_, err := invoke(t, "", "book", "window", "init", "--profile", "a")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "book migrate")
	_, statErr := os.Stat(shelfFile(p))
	assert.ErrorIs(t, statErr, os.ErrNotExist, "nothing was created")
}

// A new store is split by month windows (evidencebook#805), and store init
// prints the window it fixed; --period none keeps one JSONL book.
func TestStoreInitSplitsANewStoreByMonth(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	var notice strings.Builder
	noticeOut = &notice
	t.Cleanup(func() { noticeOut = os.Stderr })
	_, err := invoke(t, "", "store", "init", "--profile", "b")
	require.NoError(t, err)
	assert.Equal(t, "capsulectl: window: month (calendar months, UTC), fixed for this store\n", notice.String())
	policy, err := evidencebook.ReadShelfWindow(t.Context(), shelfFile(b))
	require.NoError(t, err)
	assert.Equal(t, evidencebook.WindowMonth, policy.Period)
	assert.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), policy.Since)

	a, _ := bookProfile(t, "a")
	out, err := invoke(t, "", "book", "window", "init", "--profile", "a", "--period", "none")
	require.NoError(t, err)
	assert.Contains(t, out, `"period":"none"`)
	_, err = invoke(t, "", "store", "init", "--profile", "a")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(a.Connection.Database, "book", "log.jsonl"))
	_, statErr := os.Stat(shelfFile(a))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

// The core's checkpoint status reads a window log's checkpoint now that the
// backend declares its window logs (capsule-cli#622).
func TestTheCoreChecksAWindowLogsCheckpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bookClock(t, time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	b, _ := bookProfile(t, "b")
	witnessKey, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	b.Checkpoint.Endpoint = "https://witness.example.invalid"
	b.Checkpoint.PublicKey = hex.EncodeToString(witnessKey)
	require.NoError(t, cli.SaveProfile(b, true))
	_, err = invoke(t, "", "store", "init", "--profile", "b")
	require.NoError(t, err)
	publishFile(t, "b", sealRequestFile(t, "published-1"))
	out, err := invoke(t, "", "cll", "checkpoint", "create", "--profile", "b")
	require.NoError(t, err, out)
	assert.Contains(t, out, `"log_id":"b/2026-11"`)

	assert.True(t, BookBackend{}.OwnsLogID(b, "b/2026-11"))
	assert.False(t, BookBackend{}.OwnsLogID(b, "b/2026-13"), "not a label of the store's period")
	assert.False(t, BookBackend{}.OwnsLogID(b, "b/2026-10"), "before the store's first window")
	assert.False(t, BookBackend{}.OwnsLogID(b, "b/2026-12"), "after the window holding this clock")
	assert.False(t, BookBackend{}.OwnsLogID(b, "c/2026-11"))
	renamed := b
	renamed.LogID = "b2"
	assert.False(t, BookBackend{}.OwnsLogID(renamed, "b2/2026-11"), "the store's windows are under its own base")
	jsonl, _ := storedJSONLProfile(t, "j")
	assert.False(t, BookBackend{}.OwnsLogID(jsonl, "j/2026-11"), "a JSONL book has no window logs")
}
