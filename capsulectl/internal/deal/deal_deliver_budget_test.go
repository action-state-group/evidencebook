package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/action-state-group/checkpointed-local-log/go/witness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deliveryHook replaces the witness delivery and the delivery clock for one
// test, and records the checkpoint size of every delivery it was asked for.
type deliveryHook struct {
	t     *testing.T
	now   time.Time
	calls []uint64
}

func hookDelivery(t *testing.T) *deliveryHook {
	t.Helper()
	h := &deliveryHook{t: t, now: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
	oldDeliver, oldNow := dealDeliverWitness, dealDeliverNow
	t.Cleanup(func() { dealDeliverWitness, dealDeliverNow = oldDeliver, oldNow })
	dealDeliverNow = func() time.Time { return h.now }
	h.stall()
	return h
}

// stall is a witness that never answers in time: nothing is stored, so
// every checkpoint stays pending.
func (h *deliveryHook) stall() {
	dealDeliverWitness = func(ctx context.Context, _ cli.Profile, store cll.WitnessStateStore, service string, size uint64) (cll.WitnessState, error) {
		h.calls = append(h.calls, size)
		return store.GetWitness(ctx, service, size)
	}
}

// slow is a witness that takes latency per delivery and then answers with a
// receipt signed by key.
func (h *deliveryHook) slow(latency time.Duration, key ed25519.PrivateKey) {
	dealDeliverWitness = func(ctx context.Context, _ cli.Profile, store cll.WitnessStateStore, service string, size uint64) (cll.WitnessState, error) {
		h.calls = append(h.calls, size)
		h.now = h.now.Add(latency)
		return h.receipt(ctx, store, service, size, key), nil
	}
}

func (h *deliveryHook) receipt(ctx context.Context, store cll.WitnessStateStore, service string, size uint64, key ed25519.PrivateKey) cll.WitnessState {
	t := h.t
	state, err := store.GetWitness(ctx, service, size)
	require.NoError(t, err)
	record, err := checkpoint.ParseRecord(state.Checkpoint)
	require.NoError(t, err)
	entry, err := record.EntryHash()
	require.NoError(t, err)
	next := state
	next.Attempts++
	next.LastError = ""
	next.Permanent = false
	next.Receipt = &cll.WitnessReceiptState{Bytes: mintReceipt(t, entry, key), EntryHash: hex.EncodeToString(entry), EntryHashScheme: witness.EntryHashSchemeCheckpointDigest, LeafIndex: ptr(int64(0)), TreeSize: ptr(int64(1))}
	require.NoError(t, store.CommitWitness(ctx, state.Attempts, next))
	return next
}

// backlog builds n cadence ticks one interval apart whose deliveries all
// stalled, and returns the clock it set.
func backlog(t *testing.T, h *deliveryHook, n int) *time.Time {
	t.Helper()
	clock := clockAt(t, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	for i := 0; i < n; i++ {
		h.calls = nil
		tick := dealRun(t, "tick")
		require.Equal(t, "ticked", tick["state"], "%v", tick)
		// Only the oldest is sent: the witness holds every later one until
		// it has a receipt, so sending them would only repeat the oldest.
		require.Len(t, h.calls, 1, "tick %d", i+1)
		*clock = clock.Add(time.Minute)
	}
	return clock
}

func witnessPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return public, key
}

func pendingRows(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, row := range out["pending"].([]any) {
		rows = append(rows, row.(map[string]any))
	}
	return rows
}

// A backlog against a slow witness: one tick spends about the delivery
// budget (5s by default) and leaves the rest pending, untouched and in
// order, and the next tick resumes from the oldest (evidencebook#937).
func TestDealTickStopsDeliveringAtTheBudget(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1m", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clock := backlog(t, h, 3)
	*clock = clock.Add(-30 * time.Second) // no tick due

	tick := dealRun(t, "tick")
	rows := pendingRows(t, tick)
	require.Len(t, rows, 3, "%v", tick)
	assert.Equal(t, "not_attempted", rows[0]["reason"])
	assert.NotContains(t, rows[0], "not_tried_this_run", "the oldest was tried")
	for _, row := range rows[1:] {
		assert.Equal(t, true, row["not_tried_this_run"], "%v", row)
		assert.Equal(t, "not_attempted", row["reason"], "a row not tried keeps its stored reason")
	}
	sizes := []uint64{uint64(rows[0]["checkpoint"].(float64)), uint64(rows[1]["checkpoint"].(float64)), uint64(rows[2]["checkpoint"].(float64))}
	assert.Less(t, sizes[0], sizes[1])
	assert.Less(t, sizes[1], sizes[2])

	// 3s a delivery against a 5s budget: two go, the third waits.
	h.calls = nil
	h.slow(3*time.Second, key)
	start := h.now
	tick = dealRun(t, "tick")
	assert.Equal(t, sizes[:2], h.calls)
	assert.Equal(t, []any{float64(sizes[0]), float64(sizes[1])}, tick["delivered"])
	rows = pendingRows(t, tick)
	require.Len(t, rows, 1)
	assert.Equal(t, float64(sizes[2]), rows[0]["checkpoint"])
	assert.Equal(t, true, rows[0]["not_tried_this_run"])
	assert.Equal(t, 6*time.Second, h.now.Sub(start), "the budget plus at most one delivery")

	// The next tick resumes with what was left.
	h.calls = nil
	tick = dealRun(t, "tick")
	assert.Equal(t, sizes[2:], h.calls)
	assert.Equal(t, []any{float64(sizes[2])}, tick["delivered"])
	assert.Empty(t, tick["pending"])
}

// --deliver-for 0 delivers the whole backlog in one tick, as before.
func TestDealTickDeliverForZeroIsNoLimit(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1m", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clock := backlog(t, h, 3)
	*clock = clock.Add(-30 * time.Second) // no tick due
	h.calls = nil
	h.slow(3*time.Second, key)
	tick := dealRun(t, "tick", "--deliver-for", "0")
	assert.Len(t, h.calls, 3)
	assert.Len(t, tick["delivered"], 3)
	assert.Empty(t, tick["pending"])
}

// A scheduled run (--wait-up-to) is not limited by default: nobody waits on
// it, and a limit there would let a slow witness's backlog grow.
func TestDealTickWaitUpToIsNotLimitedByDefault(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clock := clockAt(t, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	for i := 0; i < 3; i++ {
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		*clock = clock.Add(time.Hour)
	}
	// The next tick is due within the wait, so the run waits for it, ticks
	// and returns.
	*clock = clock.Add(-time.Minute)
	fakeSleep(t, clock)
	h.calls = nil
	h.slow(3*time.Second, key)
	tick := dealRun(t, "tick", "--wait-up-to", "2m")
	assert.Equal(t, float64(1), tick["published_this_run"], "%v", tick)
	// The first poll delivers the backlog, the second its own tick; the
	// output is the last poll's.
	assert.Len(t, h.calls, 4, "the backlog and the new tick")
	assert.Len(t, tick["delivered"], 1)
	assert.Empty(t, tick["pending"])

	// An explicit budget still applies to such a run.
	*clock = clock.Add(time.Hour)
	h.stall()
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	*clock = clock.Add(time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	*clock = clock.Add(time.Hour - time.Minute)
	h.calls = nil
	h.slow(3*time.Second, key)
	tick = dealRun(t, "tick", "--wait-up-to", "2m", "--deliver-for", "1s")
	// Each poll has its own budget: one delivery in the first, the next
	// oldest in the second, and the new tick is left pending.
	assert.Len(t, h.calls, 2, "%v", tick)
	rows := pendingRows(t, tick)
	require.Len(t, rows, 1)
	assert.Equal(t, tick["checkpoint"], rows[0]["checkpoint"])
	assert.Equal(t, true, rows[0]["not_tried_this_run"])
}

// `deal tick --now` on a backlog: rows go oldest first, so the new stamp
// is the last in line and is reported pending, not tried, when the budget
// runs out before it.
func TestDealTickNowWithABacklogLeavesTheStampPending(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	h := hookDelivery(t)
	clock := clockAt(t, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	for i := 0; i < 3; i++ {
		require.Equal(t, "ticked", dealRun(t, "tick")["state"])
		*clock = clock.Add(time.Hour)
	}
	*clock = clock.Add(-30 * time.Minute)
	h.calls = nil
	h.slow(3*time.Second, key)
	now := dealRun(t, "tick", "--now")
	require.Equal(t, true, now["stamped_on_demand"], "%v", now)
	stamp := now["checkpoint"].(float64)
	assert.Len(t, h.calls, 2)
	rows := pendingRows(t, now)
	require.NotEmpty(t, rows)
	last := rows[len(rows)-1]
	assert.Equal(t, stamp, last["checkpoint"], "the stamp is the newest, last in line")
	assert.Equal(t, true, last["not_tried_this_run"])
	assert.NotContains(t, now["delivered"], stamp)
}

// With nothing pending beyond the tick's own checkpoint, a tick delivers it
// as before.
func TestDealTickWithNoBacklogDeliversItsCheckpoint(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clockAt(t, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	h.slow(30*time.Second, key)
	tick := dealRun(t, "tick")
	require.Equal(t, "ticked", tick["state"])
	assert.Equal(t, []any{tick["checkpoint"]}, tick["delivered"], "a delivery longer than the budget still finishes")
	assert.Empty(t, tick["pending"])
}

// A checkpoint the witness failed for good does not hold the ones after
// it: the witness decides on those without it.
func TestDealTickGoesPastACheckpointFailedForGood(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1m", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clock := backlog(t, h, 3)
	*clock = clock.Add(-30 * time.Second) // no tick due
	h.calls = nil
	first := true
	dealDeliverWitness = func(ctx context.Context, _ cli.Profile, store cll.WitnessStateStore, service string, size uint64) (cll.WitnessState, error) {
		h.calls = append(h.calls, size)
		if !first {
			return h.receipt(ctx, store, service, size, key), nil
		}
		first = false
		state, err := store.GetWitness(ctx, service, size)
		require.NoError(t, err)
		failed := state
		failed.Attempts++
		failed.Permanent = true
		failed.LastError = "witness returned HTTP 400: " + cli.WitnessFailureBody(409)
		require.NoError(t, store.CommitWitness(ctx, state.Attempts, failed))
		return failed, nil
	}
	tick := dealRun(t, "tick")
	assert.Len(t, h.calls, 3)
	assert.Len(t, tick["delivered"], 2)
	rows := pendingRows(t, tick)
	require.Len(t, rows, 1)
	assert.Equal(t, "witness_error", rows[0]["reason"])
}

// Through the real delivery, a witness that cannot be reached is sent the
// oldest pending checkpoint only, and the rest keep their stored reason.
func TestDealTickStopsAtAnUnreachableWitness(t *testing.T) {
	public, _ := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1m", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clock := backlog(t, h, 3)
	*clock = clock.Add(time.Hour) // every retry is due again
	h.calls = nil
	dealDeliverWitness = func(ctx context.Context, p cli.Profile, store cll.WitnessStateStore, service string, size uint64) (cll.WitnessState, error) {
		h.calls = append(h.calls, size)
		return cli.DeliverWitness(ctx, p, store, service, size)
	}
	tick := dealRun(t, "tick")
	require.Len(t, h.calls, 1, "%v", tick)
	rows := pendingRows(t, tick)
	require.Len(t, rows, 4, "the backlog and the new tick")
	assert.NotContains(t, rows[0], "not_tried_this_run")
	assert.NotEqual(t, "not_attempted", rows[0]["reason"], "the real delivery was attempted: %v", rows[0])
	for _, row := range rows[1:] {
		assert.Equal(t, true, row["not_tried_this_run"])
	}
}

func TestDealTickRefusesANegativeBudget(t *testing.T) {
	public, _ := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	_, err := invoke(t, "", "--profile", "deal", "deal", "tick", "--deliver-for", "-1s")
	require.Error(t, err)
}
