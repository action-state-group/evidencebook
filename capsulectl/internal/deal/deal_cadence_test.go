package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/action-state-group/checkpointed-local-log/go/witness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingWitness is an endpoint that only counts connections: it never
// answers, so every delivery to it stays pending.
func countingWitness(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var n atomic.Int64
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return "https://" + l.Addr().String(), &n
}

// clockAt lets a test move dealClock.
func clockAt(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	now := start
	old := cli.DealClock
	cli.DealClock = func() time.Time { return now }
	t.Cleanup(func() { cli.DealClock = old })
	return &now
}

// cadenceProfile configures a witness and a cadence on the fixture profile.
func cadenceFixture(t *testing.T, endpoint string, pinned ed25519.PublicKey, interval, jitter string, pad uint64) cli.Profile {
	t.Helper()
	dealFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	p.Checkpoint.Endpoint, p.Checkpoint.PublicKey = endpoint, hex.EncodeToString(pinned)
	p.Cadence.Interval, p.Cadence.Jitter, p.Cadence.PadBucket = interval, jitter, pad
	require.NoError(t, cli.SaveProfile(p, true))
	return p
}

func retailDeal(t *testing.T) string {
	t.Helper()
	dealID := dealRun(t, "open", "--input", filepath.Join(retailDemo, "open.json"))["deal_id"].(string)
	dealRun(t, "check", "--deal", dealID, "--input", filepath.Join(retailDemo, "check-pay.json"))
	dealRun(t, "note", "--deal", dealID, "--kind", "act", "--input", filepath.Join(retailDemo, "act-pay.json"))
	return dealID
}

// deliver stores, for the cadence checkpoint at size, a receipt signed by
// signer, as a witness delivery would.
func deliver(t *testing.T, p cli.Profile, size uint64, signer ed25519.PrivateKey) {
	t.Helper()
	storeReceipt(t, p, size, signer)
	// The cadence records the receipt as the next deal command does
	// (witnessNativeTicks): the tick's leaves become witnessed.
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { _ = s.close() }()
	defer func() { require.NoError(t, s.close()) }()
	c, cp, err := s.cadenceOf(t.Context())
	require.NoError(t, err)
	require.NoError(t, s.witnessNativeTicks(t.Context(), c, cp))
}

// storeReceipt stores, on the cadence log, a witness receipt signed by
// signer for the cadence checkpoint at size, as a delivery would; nothing
// tells the cadence.
func storeReceipt(t *testing.T, p cli.Profile, size uint64, signer ed25519.PrivateKey) {
	t.Helper()
	target, err := openOwnedTarget(t.Context(), p, cli.UseCLL, &dealStoreLock{held: true})
	require.NoError(t, err)
	defer func() { require.NoError(t, target.Close()) }()
	service, err := cli.ServiceID(p)
	require.NoError(t, err)
	state, err := target.Log.GetWitness(t.Context(), service, size)
	require.NoError(t, err)
	record, err := checkpoint.ParseRecord(state.Checkpoint)
	require.NoError(t, err)
	entry, err := record.EntryHash()
	require.NoError(t, err)
	next := state
	next.Receipt = &cll.WitnessReceiptState{Bytes: mintReceipt(t, entry, signer), EntryHash: hex.EncodeToString(entry), EntryHashScheme: witness.EntryHashSchemeCheckpointDigest, LeafIndex: ptr(int64(0)), TreeSize: ptr(int64(1))}
	require.NoError(t, target.Log.CommitWitness(t.Context(), state.Attempts, next))
}

// deliverAnchorReceipt is deliver for a scrapi+https:// witness: its
// Receipt's leaf is SHA-256 of the checkpoint statement (the anchor digest),
// as CLL's SCRAPI client stores it.
func deliverAnchorReceipt(t *testing.T, p cli.Profile, size uint64, signer ed25519.PrivateKey) {
	t.Helper()
	target, err := openOwnedTarget(t.Context(), p, cli.UseCLL, &dealStoreLock{held: true})
	require.NoError(t, err)
	service, err := cli.ServiceID(p)
	require.NoError(t, err)
	state, err := target.Log.GetWitness(t.Context(), service, size)
	require.NoError(t, err)
	entry := sha256.Sum256(state.Checkpoint)
	next := state
	next.Receipt = &cll.WitnessReceiptState{Bytes: mintReceipt(t, entry[:], signer), EntryHash: hex.EncodeToString(entry[:]), LeafIndex: ptr(int64(0)), TreeSize: ptr(int64(1))}
	require.NoError(t, target.Log.CommitWitness(t.Context(), state.Attempts, next))
	require.NoError(t, target.Close())
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	c, cp, err := s.cadenceOf(t.Context())
	require.NoError(t, err)
	require.NoError(t, s.witnessNativeTicks(t.Context(), c, cp))
}

func cadenceSize(t *testing.T, p cli.Profile) uint64 {
	t.Helper()
	target, err := cli.OpenTarget(t.Context(), p, cli.UseCLLRead)
	require.NoError(t, err)
	defer func() { require.NoError(t, target.Close()) }()
	state, err := target.Log.LoadCLL(t.Context())
	require.NoError(t, err)
	require.NotNil(t, state.Checkpoint)
	return state.Checkpoint.Size
}

func cadenceLeaves(t *testing.T, p cli.Profile) uint64 {
	t.Helper()
	target, err := cli.OpenTarget(t.Context(), p, cli.UseCLLRead)
	require.NoError(t, err)
	defer func() { require.NoError(t, target.Close()) }()
	state, err := target.Log.LoadCLL(t.Context())
	require.NoError(t, err)
	return state.Checkpoint.IndexedSeq
}

// Deal events never contact the witness; only a due tick does.
func TestDealEventsNeverPublish(t *testing.T) {
	endpoint, connections := countingWitness(t)
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	dealID := dealRun(t, "open", "--input", filepath.Join(retailDemo, "open.json"))["deal_id"].(string)
	dealRun(t, "check", "--deal", dealID, "--input", filepath.Join(retailDemo, "check-pay.json"))
	closed := dealRun(t, "close", "--deal", dealID, "--input", writeJSON(t, `{"status":"received","delivered":{"item":"cat sticker"}}`))
	assert.Equal(t, "scheduled", closed["checkpoint"].(map[string]any)["witness"])
	assert.Zero(t, connections.Load(), "opening, checking and closing a deal sends nothing to the witness")
	tick := dealRun(t, "tick")
	assert.Equal(t, "ticked", tick["state"])
	assert.Positive(t, connections.Load(), "a due tick is what reaches the witness")
}

// A2: a witness that is down never stops a deal; the receipt says pending
// until a later delivery, and then witnessed, with a chain verify checks.
func TestDealWitnessStatesAreShownAsTheyAre(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	dealID := retailDeal(t)
	dir := t.TempDir()
	report := func(name string) map[string]any {
		return dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(dir, name+".json"), "--html", filepath.Join(dir, name+".html"))
	}
	assert.Equal(t, "scheduled", report("before")["assurance"].(map[string]any)["witness_state"], "not in a tick yet")

	tick := dealRun(t, "tick")
	require.Equal(t, "ticked", tick["state"])
	assert.Len(t, tick["pending"], 1, "the witness is down: the delivery is pending")
	pending := report("down")
	assert.Equal(t, "pending", pending["assurance"].(map[string]any)["witness_state"])
	assert.Equal(t, "self_attested", pending["assurance"].(map[string]any)["rung"], "pending is never shown as witnessed")
	assert.Contains(t, pending["assurance"].(map[string]any)["text"], "This checkpoint was sent at a cadence tick, but no witness signature has come back yet. Witness pending")

	deliver(t, p, cadenceSize(t, p), key)
	witnessed := report("up")
	assurance := witnessed["assurance"].(map[string]any)
	// The copy carries no witness chain (u219, u225): its rung is the copy's,
	// and the holder's own witnessing is said in attributed voice.
	assert.Equal(t, "self_attested", assurance["rung"])
	assert.Equal(t, "not_carried", assurance["witness_state"])
	assert.Equal(t, "all", assurance["holder_witnessed"].(map[string]any)["extent"])
	assert.Contains(t, assurance["text"], "Your own profile reports this checkpoint witnessed by")
	assert.NotContains(t, strings.ToLower(assurance["text"].(string)), "verified")
	page := string(mustRead(t, filepath.Join(dir, "up.html")))
	// The page states no witness of its own: the core's lists and verdict
	// stand alone (Steven's banner ruling on capsule-cli#341).
	assert.NotContains(t, page, "Witnessed through")

	copyPath := filepath.Join(dir, "up.json")
	assert.NotContains(t, readBundle(t, copyPath)["extensions"], DealParentWitnessExtension)
	result, err := verifyWithDirectory(t, copyPath, writeDirectory(t, rawKeyRow(endpoint, public)))
	require.NoError(t, err)
	assert.Equal(t, "VALID", result["verdict"])
	assert.NotContains(t, result, "reader_witnesses", "the copy claims no witness")

	// The holder's own chain, as an older copy carried it, still verifies.
	bundlePath := withHolderChain(t, p, dealID, copyPath)
	result, err = verifyWithDirectory(t, bundlePath, writeDirectory(t, rawKeyRow(endpoint, public)))
	require.NoError(t, err)
	status, _ := bundleWitnesses(result)
	assert.Equal(t, "pass", status, "deal checkpoint -> tree path -> cadence entry -> witnessed cadence checkpoint")
	requireParentWitness(t, mustRead(t, bundlePath), "")
	requireParentWitness(t, mustRead(t, bundlePath), writeDirectory(t, rawKeyRow(endpoint, public)))
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	result, _ = verifyWithDirectory(t, bundlePath, writeDirectory(t, rawKeyRow(endpoint, other)))
	status, _ = bundleWitnesses(result)
	assert.Equal(t, "fail", status)
	result, _ = verifyWithDirectory(t, bundlePath, "")
	status, _ = bundleWitnesses(result)
	assert.Equal(t, "withheld", status, "without a directory the receipt is not checked")

	// Any change to the chain fails.
	var bundle map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, bundlePath), &bundle))
	chain := bundle["extensions"].(map[string]any)[DealParentWitnessExtension].(map[string]any)
	path := chain["path"].([]any)
	require.Len(t, path, DealCadenceDepth, "the path is always the same length")
	for _, tamper := range []func(map[string]any){
		func(c map[string]any) { c["salt"] = strings.Repeat("0", 64) },
		func(c map[string]any) { c["path"].([]any)[3] = strings.Repeat("ab", 32) },
		func(c map[string]any) { c["index"] = json.Number("7") },
	} {
		var copy map[string]any
		require.NoError(t, json.Unmarshal(mustRead(t, bundlePath), &copy))
		tamper(copy["extensions"].(map[string]any)[DealParentWitnessExtension].(map[string]any))
		path := filepath.Join(t.TempDir(), "tampered.json")
		raw, err := json.Marshal(copy)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0o600))
		result, _ := verifyWithDirectory(t, path, writeDirectory(t, rawKeyRow(endpoint, public)))
		status, findings := bundleWitnesses(result)
		assert.Equal(t, "fail", status)
		assert.Contains(t, findings, "cadence_entry_not_included")
	}
}

// A receipt that does not verify under the profile's witness is a witness
// check that failed, which the report tells apart from one not yet made
// (evidencebook-deal#113, actionstate-muse#311 §3.3).
func TestDealReceiptUnderAnotherKeyIsUnverified(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	dealID := retailDeal(t)
	dealRun(t, "tick")
	deliver(t, p, cadenceSize(t, p), stranger)
	report := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assurance := report["assurance"].(map[string]any)
	assert.Equal(t, "unverified", assurance["witness_state"])
	assert.Equal(t, "self_attested", assurance["rung"])
	assert.Contains(t, assurance["text"], "Witness signature did not verify")
	assert.Equal(t, "unverified", asGrade(t, report["grade"]).Witness.State)
}

// A deal witnessed and then re-keyed has a recorded chain the current witness
// service no longer backs: a check that fails, so unverified, not "not yet".
// It is witnessed again once the new witness signs a later tick.
func TestDealChangedWitnessKeyIsUnverified(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	other, otherKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	dealID := retailDeal(t)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	first := dealRun(t, "report", "--deal", dealID)
	assert.Equal(t, "not_carried", first["grade"].(map[string]any)["witness"].(map[string]any)["state"], "the grade is the copy's")
	assert.Contains(t, first["assurance"], "holder_witnessed")

	p.Checkpoint.PublicKey = hex.EncodeToString(other)
	require.NoError(t, cli.SaveProfile(p, true))
	report := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assert.Equal(t, "unverified", report["assurance"].(map[string]any)["witness_state"])
	assert.Equal(t, "unverified", asGrade(t, report["grade"]).Witness.State)
	status := dealRun(t, "checkpoint", "status", "--deal", dealID)
	assert.Equal(t, "unverified", status["witness"].(map[string]any)["state"])

	// The new witness signs a later tick: witnessed again.
	*now = now.Add(10 * time.Minute)
	dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))
	*now = now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), otherKey)
	again := dealRun(t, "report", "--deal", dealID)
	assert.Equal(t, "not_carried", again["grade"].(map[string]any)["witness"].(map[string]any)["state"])
	assert.Equal(t, "all", again["assurance"].(map[string]any)["holder_witnessed"].(map[string]any)["extent"])
}

func TestDealTamperedReceiptIsUnverified(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	dealID := retailDeal(t)
	dealRun(t, "tick")
	size := cadenceSize(t, p)
	deliver(t, p, size, key)

	target, err := openOwnedTarget(t.Context(), p, cli.UseCLL, &dealStoreLock{held: true})
	require.NoError(t, err)
	service, err := cli.ServiceID(p)
	require.NoError(t, err)
	state, err := target.Log.GetWitness(t.Context(), service, size)
	require.NoError(t, err)
	next := state
	tampered := append([]byte(nil), state.Receipt.Bytes...)
	tampered[len(tampered)-1] ^= 0xff
	r := *state.Receipt
	r.Bytes = tampered
	next.Receipt = &r
	require.NoError(t, target.Log.CommitWitness(t.Context(), state.Attempts, next))
	require.NoError(t, target.Close())

	report := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assert.Equal(t, "unverified", report["assurance"].(map[string]any)["witness_state"])
	assert.Equal(t, "unverified", asGrade(t, report["grade"]).Witness.State)
}

// Witnessed in part with a later tick whose receipt fails: the rest reads as
// a failed check, never as pending.
func TestDealWitnessedInPartWithAnUnverifiedRest(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	dealID := retailDeal(t)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	*now = now.Add(10 * time.Minute)
	dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))
	*now = now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), stranger)
	a := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))["assurance"].(map[string]any)
	assert.Equal(t, "self_attested", a["rung"], "the copy carries no chain")
	assert.Equal(t, "part", a["holder_witnessed"].(map[string]any)["extent"])
	assert.Contains(t, a["text"], "Your own profile reports steps 1 to")
	assert.Contains(t, a["text"], "the later steps did not verify")
	assert.NotContains(t, a["text"], "next tick")
	assert.NotContains(t, a["text"], "this checkpoint witnessed", "part coverage never says this checkpoint is witnessed")
}

// A deal never sent to the witness is not a failed check.
func TestDealNeverSentToTheWitnessStaysScheduled(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	dealID := retailDeal(t)
	report := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assert.Equal(t, "scheduled", report["assurance"].(map[string]any)["witness_state"])
}

// The tick times follow the clock and the jitter alone, and every tick adds
// the same number of entries to the published log, whatever the deals did.
func TestDealTickTimingAndSizeDoNotDependOnActivity(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	var leaves []uint64
	for hour, deals := range []int{0, 3, 0, 1} {
		for range deals {
			retailDeal(t)
		}
		*now = time.Date(2026, 10, 4, 9+hour, 0, 0, 0, time.UTC)
		tick := dealRun(t, "tick")
		require.Equal(t, "ticked", tick["state"], "hour %d", hour)
		assert.Equal(t, time.Date(2026, 10, 4, 10+hour, 0, 0, 0, time.UTC).Format(time.RFC3339), tick["due"])
		*now = now.Add(30 * time.Minute)
		retailDeal(t)
		assert.Equal(t, "not_due", dealRun(t, "tick")["state"], "activity never brings a tick forward")
		leaves = append(leaves, cadenceLeaves(t, p))
	}
	assert.Equal(t, []uint64{1, 2, 3, 4}, leaves, "one entry per tick, with 0, 3, 0 or 1 new deals")
}

func TestDealTickJitterStaysInItsWindow(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	cadenceFixture(t, endpoint, public, "1h", "10m", 0)
	start := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	now := clockAt(t, start)
	tick := dealRun(t, "tick")
	due, err := time.Parse(time.RFC3339, tick["due"].(string))
	require.NoError(t, err)
	assert.False(t, due.Before(now.Add(50*time.Minute)))
	assert.False(t, due.After(now.Add(70*time.Minute)))
}

// With a bucket K > 1, every published leaf count is a multiple of K; the
// padding records are skipped everywhere a deal is read.
func TestDealCadencePadsToTheBucket(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 4)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	dealID := retailDeal(t)
	for hour := range 3 {
		*now = time.Date(2026, 10, 4, 9+hour, 0, 0, 0, time.UTC)
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		assert.Zero(t, cadenceLeaves(t, p)%4, "leaf count %d", cadenceLeaves(t, p))
	}
	assert.Equal(t, uint64(12), cadenceLeaves(t, p))
	deliver(t, p, cadenceSize(t, p), key)
	bundlePath := filepath.Join(t.TempDir(), "b.json")
	report := dealRun(t, "report", "--deal", dealID, "--bundle", bundlePath)
	assert.Equal(t, "all", report["assurance"].(map[string]any)["holder_witnessed"].(map[string]any)["extent"])
	assert.NotContains(t, strings.ToLower(report["trail"].(string)), "padding")
	for _, item := range report["did"].([]any) {
		assert.NotContains(t, strings.ToLower(item.(map[string]any)["text"].(string)), "padding")
	}
	result, err := verifyWithDirectory(t, withHolderChain(t, p, dealID, bundlePath), writeDirectory(t, rawKeyRow(endpoint, public)))
	require.NoError(t, err)
	status, _ := bundleWitnesses(result)
	assert.Equal(t, "pass", status, "padding entries are ordinary leaves to the proof")
}

func TestDealInitTurnsTheWitnessOnByDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, err := invoke(t, "", "deal", "init", "--profile", "w", "--dir", filepath.Join(t.TempDir(), "w"))
	require.NoError(t, err, out)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "scheduled", result["witness"])
	assert.Equal(t, dealWitnessSees, result["witness_sees"])
	p, err := cli.LoadProfile("w")
	require.NoError(t, err)
	assert.Equal(t, DealDefaultWitness, p.Checkpoint.Endpoint)
	assert.Equal(t, DealDefaultWitnessKey, p.Checkpoint.PublicKey)
	assert.True(t, strings.HasPrefix(p.LogID, "deal-cadence/"))

	out, err = invoke(t, "", "deal", "init", "--profile", "n", "--dir", filepath.Join(t.TempDir(), "n"), "--no-witness")
	require.NoError(t, err, out)
	p, err = cli.LoadProfile("n")
	require.NoError(t, err)
	assert.Empty(t, p.Checkpoint.Endpoint)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

// A delivery that never reached the witness (as a host's unanswered
// network-consent prompt looks from here) is shown as such, never silently.
func TestDealBlockedDeliveryIsShownAsNetworkConsent(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	dealID := retailDeal(t)
	tick := dealRun(t, "tick")
	pending := tick["pending"].([]any)
	require.Len(t, pending, 1)
	row := pending[0].(map[string]any)
	assert.Equal(t, "network_consent_needed", row["reason"])
	assert.True(t, strings.HasPrefix(row["text"].(string), "pending: network consent needed"))
	assert.Contains(t, row["text"], `"Always allow this site"`)
	withBundle := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assurance := withBundle["assurance"].(map[string]any)
	assert.Equal(t, "pending", assurance["witness_state"])
	assert.Equal(t, "network_consent_needed", assurance["witness_reason"])
	assert.Contains(t, assurance["text"], "Witness pending: network consent needed")
}

// A delivery the witness failed for good is not said to be retried, on
// the tick or on the receipt (capsule-cli#335).
func TestDealPermanentDeliveryIsNotSaidToBeRetried(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	p := cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	dealID := retailDeal(t)
	tick := dealRun(t, "tick")
	size := uint64(tick["checkpoint"].(float64))
	service, err := cli.ServiceID(p)
	require.NoError(t, err)
	target, err := openOwnedTarget(t.Context(), p, cli.UseCLL, &dealStoreLock{held: true})
	require.NoError(t, err)
	state, err := target.Log.GetWitness(t.Context(), service, size)
	require.NoError(t, err)
	conflict := state
	conflict.Permanent = true
	conflict.LastError = "witness returned HTTP 400: " + cli.WitnessFailureBody(409)
	require.NoError(t, target.Log.CommitWitness(t.Context(), state.Attempts, conflict))
	require.NoError(t, target.Close())

	withBundle := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assurance := withBundle["assurance"].(map[string]any)
	assert.Equal(t, "pending", assurance["witness_state"])
	assert.NotContains(t, assurance, "witness_reason")
	assert.Contains(t, assurance["text"], "This checkpoint was sent at a cadence tick. Witness not delivered: the witness answered with an error (HTTP 409); this delivery is not retried.")
	assert.NotContains(t, assurance["text"], "retried at every tick")

	// A refused delivery is said with its reason and the standing grant.
	target, err = openOwnedTarget(t.Context(), p, cli.UseCLL, &dealStoreLock{held: true})
	require.NoError(t, err)
	refused := conflict
	refused.Permanent = false
	refused.LastError = "witness returned HTTP 503: " + cli.WitnessFailureBody(403)
	require.NoError(t, target.Log.CommitWitness(t.Context(), conflict.Attempts, refused))
	require.NoError(t, target.Close())
	withBundle = dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assurance = withBundle["assurance"].(map[string]any)
	assert.Equal(t, "refused", assurance["witness_reason"])
	assert.Contains(t, assurance["text"], "Witness pending: the request was refused (HTTP 403), by the witness or by a network gate on this host; it is tried again on a later capsulectl run, after a wait that grows up to an hour.")
	assert.Contains(t, assurance["text"], `a standing grant ("Always allow this site")`)

	// A delivery that reached the witness and timed out is a slow witness, passed on as such, never a consent ask
	// (capsule-cli#644).
	target, err = openOwnedTarget(t.Context(), p, cli.UseCLL, &dealStoreLock{held: true})
	require.NoError(t, err)
	slow := refused
	slow.LastError = "witness returned HTTP 503: " + cli.WitnessTimedOut
	require.NoError(t, target.Log.CommitWitness(t.Context(), refused.Attempts, slow))
	require.NoError(t, target.Close())
	withBundle = dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assurance = withBundle["assurance"].(map[string]any)
	assert.Equal(t, "witness_timeout", assurance["witness_reason"])
	assert.Contains(t, assurance["text"], "the witness was reached but its answer did not come in time")
	assert.NotContains(t, assurance["text"], "network consent")
}

func TestWitnessPendingReasons(t *testing.T) {
	reason, text := cli.WitnessPendingReason(cll.WitnessState{Attempts: 1, LastError: "witness returned HTTP 503: " + cli.WitnessNotReached}, DealDefaultWitness)
	assert.Equal(t, "network_consent_needed", reason)
	assert.Contains(t, text, `a standing grant ("Always allow this site") for agentactioncapsule.org`)
	assert.Contains(t, text, "The witness is witness.agentactioncapsule.org; a grant for agentactioncapsule.org covers it and all its subdomains.")
	reason, _ = cli.WitnessPendingReason(cll.WitnessState{Attempts: 1, LastError: "witness returned HTTP 503: checkpoint submission failed; response details suppressed"}, DealDefaultWitness)
	assert.Equal(t, "witness_error", reason, "a witness that answered is not a consent problem")
	reason, _ = cli.WitnessPendingReason(cll.WitnessState{}, DealDefaultWitness)
	assert.Equal(t, "not_attempted", reason)
	// What the witness answered is kept (its status, never its body) and said.
	reason, text = cli.WitnessPendingReason(cll.WitnessState{Attempts: 1, LastError: "witness returned HTTP 503: " + cli.WitnessFailureBody(429)}, DealDefaultWitness)
	assert.Equal(t, "witness_error", reason)
	assert.Equal(t, "pending: the witness answered with an error (HTTP 429); it is tried again on a later capsulectl run, after a wait that grows up to an hour.", text)
	// A conflict is failed for good, and not said to be retried.
	reason, text = cli.WitnessPendingReason(cll.WitnessState{Attempts: 1, Permanent: true, LastError: "witness returned HTTP 400: " + cli.WitnessFailureBody(409)}, DealDefaultWitness)
	assert.Equal(t, "witness_error", reason)
	assert.Equal(t, "not delivered: the witness answered with an error (HTTP 409); this delivery is not retried.", text)
	// A 403 may be a network gate on this host (capsule-cli#335): it is not
	// said as the witness's answer, and it names the standing grant, whether
	// stored now or by an earlier build.
	for _, stored := range []string{"witness returned HTTP 503: " + cli.WitnessFailureBody(403), "witness returned HTTP 400: checkpoint submission failed (the witness answered HTTP 403); response details suppressed"} {
		reason, text = cli.WitnessPendingReason(cll.WitnessState{Attempts: 1, LastError: stored}, DealDefaultWitness)
		assert.Equal(t, "refused", reason)
		assert.True(t, strings.HasPrefix(text, "pending: the request was refused (HTTP 403), by the witness or by a network gate on this host;"))
		assert.NotContains(t, text, "the witness answered")
		assert.Contains(t, text, `a standing grant ("Always allow this site") for agentactioncapsule.org`)
	}
}

// A cadence delivery a build before capsule-cli#335 stored as failed for
// good after a 403 is re-armed by the next tick and delivered.
func TestTickRearmsARefusedDelivery(t *testing.T) {
	public, witnessKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	p := cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	retailDeal(t)
	tick := dealRun(t, "tick")
	require.Equal(t, "ticked", tick["state"])
	size := uint64(tick["checkpoint"].(float64))
	service, err := cli.ServiceID(p)
	require.NoError(t, err)
	target, err := openOwnedTarget(t.Context(), p, cli.UseCLL, &dealStoreLock{held: true})
	require.NoError(t, err)
	state, err := target.Log.GetWitness(t.Context(), service, size)
	require.NoError(t, err)
	refused := state
	refused.Permanent = true
	refused.LastError = "witness returned HTTP 400: checkpoint submission failed (the witness answered HTTP 403); response details suppressed"
	require.NoError(t, target.Log.CommitWitness(t.Context(), state.Attempts, refused))
	require.NoError(t, target.Close())

	old := dealDeliverWitness
	t.Cleanup(func() { dealDeliverWitness = old })
	var sent []uint64
	dealDeliverWitness = func(_ context.Context, p cli.Profile, store cll.WitnessStateStore, service string, size uint64, _ time.Time) (cll.WitnessState, error) {
		state, err := store.GetWitness(t.Context(), service, size)
		require.NoError(t, err)
		require.False(t, state.Permanent, "re-armed before delivery")
		record, err := checkpoint.ParseRecord(state.Checkpoint)
		require.NoError(t, err)
		entry, err := record.EntryHash()
		require.NoError(t, err)
		next := state
		next.Attempts++
		next.LastError = ""
		next.Receipt = &cll.WitnessReceiptState{Bytes: mintReceipt(t, entry, witnessKey), EntryHash: hex.EncodeToString(entry), EntryHashScheme: witness.EntryHashSchemeCheckpointDigest, LeafIndex: ptr(int64(0)), TreeSize: ptr(int64(1))}
		require.NoError(t, store.CommitWitness(t.Context(), state.Attempts, next))
		sent = append(sent, size)
		return next, nil
	}
	again := dealRun(t, "tick")
	assert.Equal(t, "not_due", again["state"])
	assert.Equal(t, []uint64{size}, sent)
	assert.Equal(t, []any{float64(size)}, again["delivered"])
	assert.Empty(t, again["pending"])
}

func TestDoctorExplainsWitnessConsent(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	out, err := invoke(t, "", "doctor", "--profile", "deal", "--check-witness")
	require.NoError(t, err, out)
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	w := report["witness"].(map[string]any)
	assert.Equal(t, false, w["reachable"])
	assert.Equal(t, cli.WitnessConsentText("https://127.0.0.1:1"), w["consent"])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusMethodNotAllowed) }))
	t.Cleanup(server.Close)
	cadenceFixture(t, server.URL, public, "1h", "0s", 0)
	out, err = invoke(t, "", "doctor", "--profile", "deal", "--check-witness")
	require.NoError(t, err, out)
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	assert.Contains(t, report["witness"].(map[string]any)["consent"], `"Always allow this site"`)
}

// The witness's grant text is read by agents: it states what the grant is and
// why, and never tells the reader what to do.
func TestWitnessGrantTextIsAStatement(t *testing.T) {
	for _, text := range []string{
		cli.WitnessGrantText(DealDefaultWitness),
		cli.WitnessConsentText(DealDefaultWitness),
		cli.WitnessConsentReached(DealDefaultWitness),
	} {
		assert.NotContains(t, text, "If your agent")
		assert.NotContains(t, text, "choose")
		assert.Contains(t, text, "agentactioncapsule.org")
	}
}

// A deal is never called witnessed at the moment it happens: until a tick has
// carried it to the witness, the receipt says witness pending, and why.
func TestDealIsWitnessPendingUntilATick(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "5m", "2m", 0)
	dealID := retailDeal(t)
	page := filepath.Join(t.TempDir(), "r.html")
	report := dealRun(t, "report", "--deal", dealID, "--html", page)
	a := report["assurance"].(map[string]any)
	assert.Equal(t, "scheduled", a["witness_state"])
	assert.Equal(t, "self_attested", a["rung"])
	assert.Contains(t, a["text"], "Witness pending. A receipt is not witnessed at the moment it is made: its checkpoint goes to the witness at the next tick of this profile's checkpoint cadence, within about 7m (every 5m, give or take 2m)")
	assert.NotContains(t, a["text"], "an hour by default", "the profile's own cadence, not the default")
	html := string(mustRead(t, page))
	assert.NotContains(t, html, "Sealed by my agent, witness pending.", "the page states no witness of its own (capsule-cli#341)")
	for _, text := range []string{a["text"].(string), html} {
		assert.NotContains(t, text, "witnessed at the time")
		assert.NotContains(t, text, "Witnessed when")
	}

	// The tick at 18:07 carries the deal's checkpoint; once its receipt is
	// back, the same deal is witnessed, and says when its checkpoint was cut.
	now := clockAt(t, time.Date(2026, 9, 27, 18, 7, 31, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	*now = now.Add(time.Minute)
	deliver(t, p, cadenceSize(t, p), key)
	page = filepath.Join(t.TempDir(), "r2.html")
	report = dealRun(t, "report", "--deal", dealID, "--html", page)
	a = report["assurance"].(map[string]any)
	assert.Equal(t, "self_attested", a["rung"], "the copy carries no chain (u225)")
	assert.Equal(t, "not_carried", a["witness_state"])
	assert.Contains(t, a["text"], "Your own profile reports this checkpoint witnessed by")
	assert.NotContains(t, embeddedBundle(t, string(mustRead(t, page)))["extensions"], DealParentWitnessExtension, "the page's bundle carries no chain")
}

// The report names its rung from what the copy carries, and the holder's own
// witnessing in attributed voice (u225): a step sealed after the last tick
// is witnessed through the earlier checkpoint, with a consistency proof to
// the current one, and the holder's line says which steps the witness
// covers. The chain itself, as copies carried it before u225, still
// verifies under every name it was carried under.
func TestDealReportRungWitnessedInPart(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	dealID := retailDeal(t)
	dir := t.TempDir()
	report := func(name string) (map[string]any, string) {
		path := filepath.Join(dir, name+".json")
		return dealRun(t, "report", "--deal", dealID, "--bundle", path, "--html", filepath.Join(dir, name+".html"))["assurance"].(map[string]any), path
	}
	directory := writeDirectory(t, rawKeyRow(endpoint, public))

	none, _ := report("none")
	assert.Equal(t, "self_attested", none["rung"])
	assert.True(t, strings.HasPrefix(none["text"].(string), "Sealed by my agent"))

	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	all, _ := report("all")
	// The copy carries no chain (u225): its rung is its own, and the holder's
	// witnessing is said in attributed voice.
	assert.Equal(t, "self_attested", all["rung"])
	assert.Equal(t, "all", all["holder_witnessed"].(map[string]any)["extent"])
	assert.Contains(t, all["text"], "Your own profile reports this checkpoint witnessed by")

	// One more step after the tick.
	*now = now.Add(10 * time.Minute)
	dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))
	part, copyPath := report("part")
	assert.Equal(t, "self_attested", part["rung"])
	held := part["holder_witnessed"].(map[string]any)
	assert.Equal(t, "part", held["extent"])
	k, n := held["steps_witnessed"].(float64), held["steps"].(float64)
	assert.Equal(t, k+1, n)
	assert.Contains(t, part["text"], fmt.Sprintf("Your own profile reports steps 1 to %d of %d witnessed by", int(k), int(n)))
	assert.Contains(t, part["text"], "the later steps go to the witness at the next tick")
	assert.NotContains(t, part["text"], "this checkpoint witnessed")
	// The holder's chain, as copies carried it before u225, still verifies.
	partPath := withHolderChain(t, p, dealID, copyPath)
	// What the page shows a reader is checked rendered, in Chrome
	// (TestPresentationDealWitnessLine): the page's source always carries
	// the deal view's words, so it proves nothing here.
	result, err := verifyWithDirectory(t, partPath, directory)
	require.NoError(t, err)
	status, findings := bundleWitnesses(result)
	assert.Equal(t, "pass", status, "earlier checkpoint -> consistency -> current checkpoint, and the earlier one's chain: %v", findings)

	// The chain is written under parent-log-witness/v1, and read under it and
	// under the three names bundles carried it under before (x-deal-cadence-v0
	// named its log deal_log_id): each verifies, and the rung reads the same.
	{
		var written map[string]any
		require.NoError(t, json.Unmarshal(mustRead(t, copyPath), &written))
		exts := written["extensions"].(map[string]any)
		for _, name := range []string{DealParentWitnessExtension, DealCadenceExtension, "cadence-witness/v0", "x-deal-cadence-v0"} {
			require.NotContains(t, exts, name, "a new copy carries no chain")
		}
	}
	for _, name := range []string{DealParentWitnessExtension, "x-cadence-witness/v0", "cadence-witness/v0", "x-deal-cadence-v0"} {
		var renamed map[string]any
		require.NoError(t, json.Unmarshal(mustRead(t, partPath), &renamed))
		exts := renamed["extensions"].(map[string]any)
		chain := exts[DealParentWitnessExtension].(map[string]any)
		delete(exts, DealParentWitnessExtension)
		if name != DealParentWitnessExtension {
			// The names before parent-log-witness/v1 carried it as "cadence".
			chain["cadence"] = chain["parent"]
			delete(chain, "parent")
		}
		if name == "x-deal-cadence-v0" {
			chain["deal_log_id"] = chain["log_id"]
			delete(chain, "log_id")
		}
		exts[name] = chain
		path := filepath.Join(t.TempDir(), "renamed.json")
		raw, err := json.Marshal(renamed)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0o600))
		result, _ := verifyWithDirectory(t, path, directory)
		status, findings := bundleWitnesses(result)
		assert.Equal(t, "pass", status, "%s: %v", name, findings)
		assert.Equal(t, "witnessed_in_part", dealAssuranceRung(renamed, nil)["rung"], name)
	}

	// Tampering with the earlier checkpoint or its proof fails.
	for name, tamper := range map[string]func(map[string]any){
		"proof": func(c map[string]any) {
			proof := c["earlier"].(map[string]any)["consistency_proof"].(map[string]any)
			peaks := proof["new_peaks"].([]any)
			peaks[0] = strings.Repeat("ab", 32)
		},
		"checkpoint": func(c map[string]any) {
			c["earlier"].(map[string]any)["checkpoint"].(map[string]any)["cose"] = c["parent"].(map[string]any)["checkpoint"].(map[string]any)["cose"]
		},
		"earlier removed": func(c map[string]any) { delete(c, "earlier") },
	} {
		var copy map[string]any
		require.NoError(t, json.Unmarshal(mustRead(t, partPath), &copy))
		tamper(copy["extensions"].(map[string]any)[DealParentWitnessExtension].(map[string]any))
		path := filepath.Join(t.TempDir(), "tampered.json")
		raw, err := json.Marshal(copy)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0o600))
		result, _ := verifyWithDirectory(t, path, directory)
		status, _ := bundleWitnesses(result)
		assert.Equal(t, "fail", status, name)
	}

	// The next tick holds the current checkpoint but its receipt has not come
	// back: still witnessed in part, the rest pending.
	*now = time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	pending, _ := report("pending")
	assert.Equal(t, "part", pending["holder_witnessed"].(map[string]any)["extent"])
	assert.Contains(t, pending["text"], "the later steps are not witnessed yet")

	// Once that receipt is in, every step is witnessed.
	deliver(t, p, cadenceSize(t, p), key)
	full, _ := report("full")
	assert.Equal(t, "all", full["holder_witnessed"].(map[string]any)["extent"])
	plain := dealRun(t, "report", "--deal", dealID)["assurance"].(map[string]any)
	assert.Equal(t, "all", plain["holder_witnessed"].(map[string]any)["extent"], "the plain report says it too")
}

// A later tick still waiting for its receipt never hides an earlier tick,
// holding the same checkpoint, whose receipt verified.
func TestDealReportUsesTheNewestVerifiedTick(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	dealID := retailDeal(t)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	*now = time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	assurance := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))["assurance"].(map[string]any)
	held := assurance["holder_witnessed"].(map[string]any)
	assert.Equal(t, "all", held["extent"])
	assert.Equal(t, "2026-10-04T09:00:00Z", held["at"], "the verified tick, not the pending one")
}

func TestMMRLeafCount(t *testing.T) {
	for leaves, size := range []uint64{0, 1, 3, 4, 7, 8, 10, 11, 15} {
		assert.Equal(t, uint64(leaves), mmrLeafCount(size), "size %d", size)
	}
}

// The poll: a `deal tick` that is not due publishes nothing. With every
// cadence delivery complete, it makes no connection to the witness at all,
// however often it runs. The one thing a poll may send is a retry of a
// cadence delivery still pending (the witness was down at its tick), once
// that delivery's backoff has passed: the checkpoint it carries was cut at
// its tick, so only the retry's time follows the poll, never a deal.
func TestAPollThatIsNotDueDoesNotContactTheWitness(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, connections := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	retailDeal(t)

	tick := dealRun(t, "tick")
	require.Equal(t, "ticked", tick["state"])
	require.Len(t, tick["pending"], 1, "the counting witness accepts nothing: the delivery is pending")
	deliver(t, p, cadenceSize(t, p), key)
	before := connections.Load()
	for range 3 {
		poll := dealRun(t, "tick")
		assert.Equal(t, "not_due", poll["state"])
		assert.Empty(t, poll["pending"])
	}
	assert.Equal(t, before, connections.Load(), "a poll that is not due, with nothing pending, makes no connection to the witness")
}

// The checkpoint cadence by default: a tick every 5m, give or take 1m (288
// a day), and a jitter its own validation accepts. A profile that sets its
// own keeps it.
func TestTheCheckpointCadenceDefaultsToFiveMinutes(t *testing.T) {
	cfg, err := dealCadenceOf(cli.Profile{})
	require.NoError(t, err, "the default passes the cadence's own validation (jitter under half the interval)")
	assert.Equal(t, 5*time.Minute, cfg.Interval)
	assert.Equal(t, time.Minute, cfg.Jitter)
	var p cli.Profile
	p.Cadence.Interval, p.Cadence.Jitter = "1h", "10m"
	own, err := dealCadenceOf(p)
	require.NoError(t, err)
	assert.Equal(t, time.Hour, own.Interval, "a profile's own cadence is kept")
}

// The cadence's consumer name is "checkpoint cadence", in what a user or an
// agent reads: the init hint, both verbs' help and the receipt, which never
// say "deal tick" or "every minute". Its consumer verb is `cll checkpoint
// cadence` (beside the CLI's other checkpoint verbs); `deal tick` stays
// callable. The schedule is a 5-minute poll that waits for each tick due in
// its window; the time to a witness is computed from the profile's cadence.
// Wire names do not change: the cadence log is still deal-cadence/<id>.
func TestTheCheckpointCadenceIsNamedAndTimedFromTheProfile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	init := dealRun(t, "init", "--profile", "deal", "--dir", filepath.Join(t.TempDir(), "d"), "--materiality", neutralMateriality)
	assert.Regexp(t, `^deal-cadence/[0-9a-f]{16}$`, init["cadence_log"], "the wire log id is unchanged")
	next := init["next"].(string)
	assert.Contains(t, next, "checkpoint cadence")
	assert.Contains(t, next, "every 5m, give or take 1m", "the profile's cadence, from its configuration")
	assert.Contains(t, next, "capsulectl --profile deal cll checkpoint cadence --wait-up-to 5m")
	assert.Contains(t, next, "every 5 minutes")
	for _, words := range []string{init["witness_sees"].(string), next} {
		assert.NotContains(t, words, "deal tick")
		assert.NotContains(t, words, "every minute")
	}

	for _, verb := range [][]string{{"deal", "tick"}, {"cll", "checkpoint", "cadence"}} {
		help, err := invoke(t, "", append(verb, "--help")...)
		require.NoError(t, err)
		// The copy: the description and the flags' help, not the usage
		// line, which names the command as typed.
		usage := strings.Index(help, "\nUsage:")
		flags := strings.Index(help, "\nFlags:")
		require.True(t, usage > 0 && flags > usage, help)
		help = help[:usage] + help[flags:]
		assert.Contains(t, help, "checkpoint cadence", verb)
		assert.NotContains(t, help, "deal tick", verb)
		assert.NotContains(t, help, "every minute", verb)
	}

	for words, within := range map[string]string{
		"every 5m, give or take 1m":  "within about 6m",
		"every 1h, give or take 10m": "within about 1h10m",
		"every 30m":                  "within about 30m",
		"every 90m, give or take 1m": "within about 1h31m",
	} {
		assert.Equal(t, ", "+within+" ("+words+")", cadencePhrase(map[string]interface{}{"cadence": words}), words)
	}
	assert.Equal(t, " (unreadable)", cadencePhrase(map[string]interface{}{"cadence": "unreadable"}), "words it cannot read are shown as they are")
}

// `cll checkpoint cadence` is the checkpoint cadence's verb: on a deal
// profile it ticks exactly as `deal tick` does, and is not due again at once.
func TestTheCadenceVerbTicks(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	cadenceFixture(t, endpoint, public, "", "", 0)
	run := func() map[string]any {
		out, err := invoke(t, "", "--profile", "deal", "cll", "checkpoint", "cadence")
		require.NoError(t, err, out)
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &m), out)
		return m
	}
	first := run()
	assert.Equal(t, "ticked", first["state"])
	assert.Regexp(t, `^deal-cadence/[0-9a-f]{16}$`, first["cadence_log"])
	assert.Equal(t, "not_due", run()["state"])
	assert.Equal(t, "not_due", dealRun(t, "tick")["state"], "deal tick is the same cadence")
}

// A jitter that is not under half the interval is refused with both values
// and the fix. A profile that set a jitter for the old 1h default and no
// interval is told the interval is now the 5m default.
func TestACadenceJitterTooLargeForItsIntervalSaysWhy(t *testing.T) {
	var onlyJitter cli.Profile
	onlyJitter.Cadence.Jitter = "10m"
	_, err := dealCadenceOf(onlyJitter)
	require.ErrorIs(t, err, cli.ErrInput)
	for _, part := range []string{"cadence.jitter is 10m", "cadence.interval is not set, so it is the default 5m", "under half the interval", "set cadence.interval", "a cadence.jitter under 2m30s"} {
		assert.Contains(t, err.Error(), part)
	}

	var both cli.Profile
	both.Cadence.Interval, both.Cadence.Jitter = "15m", "8m"
	_, err = dealCadenceOf(both)
	require.ErrorIs(t, err, cli.ErrInput)
	for _, part := range []string{"cadence.jitter is 8m", "cadence.interval is 15m", "under 7m30s"} {
		assert.Contains(t, err.Error(), part)
	}

	var negative cli.Profile
	negative.Cadence.Jitter = "-1m"
	_, err = dealCadenceOf(negative)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "cadence.jitter must be at least 0")
}

func TestTickStalenessLine(t *testing.T) {
	last := time.Date(2026, 11, 23, 1, 5, 40, 0, time.UTC)
	iv := 5 * time.Minute
	assert.Empty(t, tickStalenessLine(last, iv, last.Add(iv), false), "fresh: one interval")
	assert.Empty(t, tickStalenessLine(last, iv, last.Add(2*iv), false), "exactly two intervals is not stale")
	assert.Empty(t, tickStalenessLine(last, iv, last.Add(-time.Hour), false), "a tick in the future is not stale")
	assert.Empty(t, tickStalenessLine(last, 0, last.Add(time.Hour), false), "no interval, no line")
	line := tickStalenessLine(last, iv, last.Add(2*iv+time.Second), false)
	assert.Contains(t, line, "Checkpoints stopped at 2026-11-23T01:05Z:")
	assert.Contains(t, line, "every 5m")
	assert.Contains(t, line, "the next tick, once the schedule runs again, covers it.")
	assert.NotContains(t, line, "witness")
	assert.Contains(t, tickStalenessLine(last, iv, last.Add(time.Hour), true), "once the schedule runs again, covers it and sends it to your witness")
	assert.Empty(t, tickStalenessLine(last, time.Hour, last.Add(2*time.Hour), false), "a 1h cadence is on time at 2h")
	assert.Contains(t, tickStalenessLine(last, time.Hour, last.Add(2*time.Hour+time.Minute), false), "every 1h")
}

// A report says the checkpoints stopped when the newest tick is older than two
// intervals, in its assurance and its email, and says nothing while it is fresh.
func TestReportSaysCheckpointsStopped(t *testing.T) {
	dealFixture(t)
	dealID := retailDeal(t)
	// Before the first tick (opening a deal already set the cadence log) the report says nothing and writes no profile.
	pBefore, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	early := dealRun(t, "report", "--deal", dealID)
	assert.NotContains(t, early["assurance"].(map[string]any)["text"], "Checkpoints stopped")
	pAfter, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	assert.Equal(t, pBefore, pAfter, "a report writes no profile")
	assert.Equal(t, "ticked", dealRun(t, "tick")["state"])
	dir := t.TempDir()
	eml := filepath.Join(dir, "fresh.eml")
	fresh := dealRun(t, "report", "--deal", dealID, "--email", eml)
	a := fresh["assurance"].(map[string]any)
	assert.NotContains(t, a["text"], "Checkpoints stopped")
	assert.NotContains(t, a, "checkpoints_stopped")
	raw, err := os.ReadFile(eml)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "Checkpoints stopped")

	old := cli.DealClock
	later := old().Add(time.Hour)
	cli.DealClock = func() time.Time { return later }
	t.Cleanup(func() { cli.DealClock = old })
	eml2 := filepath.Join(dir, "stale.eml")
	stale := dealRun(t, "report", "--deal", dealID, "--email", eml2)
	a = stale["assurance"].(map[string]any)
	assert.Contains(t, a["text"], "Checkpoints stopped at ")
	assert.Contains(t, a["checkpoints_stopped"], "every 5m")
	raw, err = os.ReadFile(eml2)
	require.NoError(t, err)
	_, bodies, _ := emailParts(t, raw)
	assert.Contains(t, bodies["text/plain"], "Checkpoints stopped at ")
	assert.Contains(t, bodies["text/html"], "Checkpoints stopped at ")
}
