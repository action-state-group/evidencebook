package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/action-state-group/checkpointed-local-log/go/witness"
	"github.com/action-state-group/evidencebook"
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

// stall is a delivery that sends nothing and records no attempt, as
// cli.DeliverWitnessBy does when the budget crosses its floor or a row's
// retry is not due: every checkpoint stays pending.
func (h *deliveryHook) stall() {
	dealDeliverWitness = func(ctx context.Context, _ cli.Profile, store cll.WitnessStateStore, service string, size uint64, _ time.Time) (cll.WitnessState, error) {
		h.calls = append(h.calls, size)
		return store.GetWitness(ctx, service, size)
	}
}

// slow is a witness that takes latency per delivery and then answers with a
// receipt signed by key. A delivery that would outlast the deadline ends at
// it with a timed-out attempt recorded, as cli.DeliverWitnessBy does.
func (h *deliveryHook) slow(latency time.Duration, key ed25519.PrivateKey) {
	dealDeliverWitness = func(ctx context.Context, _ cli.Profile, store cll.WitnessStateStore, service string, size uint64, deadline time.Time) (cll.WitnessState, error) {
		h.calls = append(h.calls, size)
		if !deadline.IsZero() && h.now.Add(latency).After(deadline) {
			h.now = deadline
			return h.timedOut(ctx, store, service, size), nil
		}
		h.now = h.now.Add(latency)
		return h.receipt(ctx, store, service, size, key), nil
	}
}

// timedOut records a delivery that reached the witness and ran out of time.
func (h *deliveryHook) timedOut(ctx context.Context, store cll.WitnessStateStore, service string, size uint64) cll.WitnessState {
	state, err := store.GetWitness(ctx, service, size)
	require.NoError(h.t, err)
	next := state
	next.Attempts++
	next.LastError = "witness returned HTTP 503: " + cli.WitnessTimedOut
	require.NoError(h.t, store.CommitWitness(ctx, state.Attempts, next))
	return next
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

	h.calls = nil
	tick := dealRun(t, "tick")
	rows := pendingRows(t, tick)
	require.Len(t, rows, 3, "%v", tick)
	assert.Equal(t, []uint64{uint64(rows[0]["checkpoint"].(float64))}, h.calls, "only the oldest was handed to the delivery")
	// The stalled delivery recorded no attempt, as cli.DeliverWitnessBy does
	// when the budget crosses its floor after deliverCadence's own check, so
	// the oldest is not tried this run either.
	for _, row := range rows {
		assert.Equal(t, true, row["not_tried_this_run"], "%v", row)
		assert.Equal(t, "not_attempted", row["reason"], "a row not tried keeps its stored reason")
	}
	sizes := []uint64{uint64(rows[0]["checkpoint"].(float64)), uint64(rows[1]["checkpoint"].(float64)), uint64(rows[2]["checkpoint"].(float64))}
	assert.Less(t, sizes[0], sizes[1])
	assert.Less(t, sizes[1], sizes[2])
	// Each pending row names the tick that holds it (evidencebook#1083).
	for i, row := range rows {
		assert.Equal(t, float64(i+1), row["tick"], "%v", row)
	}
	assert.Equal(t, []any{}, tick["delivered_ticks"])

	// 3s a delivery against a 5s budget: the first is delivered, the
	// second starts with 2s left and is ended at the deadline (capsule-cli
	// #645), and the third is not sent.
	h.calls = nil
	h.slow(3*time.Second, key)
	start := h.now
	tick = dealRun(t, "tick")
	assert.Equal(t, sizes[:2], h.calls)
	assert.Equal(t, []any{float64(sizes[0])}, tick["delivered"])
	assert.Equal(t, []any{map[string]any{"checkpoint": float64(sizes[0]), "tick": float64(1)}}, tick["delivered_ticks"])
	rows = pendingRows(t, tick)
	require.Len(t, rows, 2)
	assert.Equal(t, float64(sizes[1]), rows[0]["checkpoint"])
	assert.Equal(t, float64(2), rows[0]["tick"], "a row that timed out names its tick")
	assert.Equal(t, float64(3), rows[1]["tick"], "a row left after the stop names its tick")
	assert.Equal(t, "witness_timeout", rows[0]["reason"], "%v", rows[0])
	assert.NotContains(t, rows[0], "not_tried_this_run")
	assert.Equal(t, float64(sizes[2]), rows[1]["checkpoint"])
	assert.Equal(t, true, rows[1]["not_tried_this_run"])
	assert.Equal(t, 5*time.Second, h.now.Sub(start), "never past the budget")

	// The next tick resumes with what was left.
	h.calls = nil
	h.slow(time.Second, key)
	tick = dealRun(t, "tick")
	assert.Equal(t, sizes[1:], h.calls)
	assert.Equal(t, []any{float64(sizes[1]), float64(sizes[2])}, tick["delivered"])
	assert.Equal(t, []any{
		map[string]any{"checkpoint": float64(sizes[1]), "tick": float64(2)},
		map[string]any{"checkpoint": float64(sizes[2]), "tick": float64(3)},
	}, tick["delivered_ticks"], "in delivered's order")
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
	tick = dealRun(t, "tick", "--wait-up-to", "2m", "--deliver-for", "4s")
	// Each poll has its own budget: one 3s delivery in each leaves under
	// the 2s floor, so the first delivers the oldest, the second the next
	// oldest, and the new tick is left pending.
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
	h.slow(4*time.Second, key)
	tick := dealRun(t, "tick")
	require.Equal(t, "ticked", tick["state"])
	assert.Equal(t, []any{tick["checkpoint"]}, tick["delivered"])
	assert.Equal(t, []any{map[string]any{"checkpoint": tick["checkpoint"], "tick": tick["tick"]}}, tick["delivered_ticks"],
		"a due tick names its own checkpoint's tick")
	assert.Empty(t, tick["pending"])
}

// The stamp `deal agree` cuts (Cadence.Stamp, deal/open.go) is delivered by
// a later tick, which names it by its tick number, pending and delivered,
// so a reader never infers the number from the size (evidencebook#1083).
// `deal ruling` cuts its stamp with the same call (caseProof).
func TestDealTickNumbersTheStampAgreeCut(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	seller := thisDealParty(t)
	newV1Side(t) // the buyer, who offers
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	offer := dealRun(t, "offer", "--input", writeJSON(t, offerTo(seller.signer)), "--out", offerFile)["offer"].(string)
	party(t, seller.config)
	// The buyer's fixture set the deal clock; the seller's run is timed from here.
	h := hookDelivery(t)
	clock := clockAt(t, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"], "a scheduled tick, delivery stalled")
	require.Equal(t, "kept, and counted", dealRun(t, "receive", offerFile)["record"])
	before := lastTick(t)
	*clock = clock.Add(10 * time.Minute)
	agreed := dealRun(t, "agree", offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "agreement.json"))
	require.Equal(t, true, agreed["stamp_proof"], "%v", agreed)
	stamp := lastTick(t)
	require.Equal(t, before.N+1, stamp.N, "agree cut one stamp")

	// Not due: the run only retries what is pending, oldest first. Stalled,
	// every row stays pending, numbered one after another, the agree stamp
	// last.
	*clock = clock.Add(time.Minute)
	tick := dealRun(t, "tick")
	require.Equal(t, "not_due", tick["state"], "%v", tick)
	rows := pendingRows(t, tick)
	require.Len(t, rows, int(stamp.N), "%v", tick)
	for i, row := range rows {
		assert.Equal(t, float64(i+1), row["tick"], "%v", row)
	}
	assert.Equal(t, float64(stamp.CheckpointSize), rows[len(rows)-1]["checkpoint"])

	// Not due again, and the witness answers: all are delivered, numbered.
	h.slow(time.Second, key)
	tick = dealRun(t, "tick", "--deliver-for", "0")
	require.Equal(t, "not_due", tick["state"], "%v", tick)
	assert.Empty(t, tick["pending"])
	ticks := tick["delivered_ticks"].([]any)
	require.Len(t, ticks, int(stamp.N), "%v", tick)
	assert.Equal(t, map[string]any{"checkpoint": float64(stamp.CheckpointSize), "tick": float64(stamp.N)}, ticks[len(ticks)-1],
		"the agree stamp is numbered when delivered")
	for i, row := range ticks {
		assert.Equal(t, tick["delivered"].([]any)[i], row.(map[string]any)["checkpoint"], "in delivered's order")
	}
}

// lastTick is the selected profile's newest cadence tick.
func lastTick(t *testing.T) evidencebook.CadenceTick {
	t.Helper()
	var last evidencebook.CadenceTick
	withSession(t, func(s *dealSession) {
		c, _, err := s.cadenceOf(t.Context())
		require.NoError(t, err)
		var ok bool
		last, ok, err = c.Last(t.Context())
		require.NoError(t, err)
		require.True(t, ok)
	})
	return last
}

// ticksAtSizes finds every size a tick holds in one walk, and leaves out a
// size no tick holds.
func TestTicksAtSizes(t *testing.T) {
	public, _ := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1m", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	backlog(t, h, 3)
	withSession(t, func(s *dealSession) {
		c, _, err := s.cadenceOf(t.Context())
		require.NoError(t, err)
		want := map[uint64]uint64{}
		for n := uint64(1); n <= 3; n++ {
			tick, err := c.Get(t.Context(), n)
			require.NoError(t, err)
			want[tick.CheckpointSize] = n
		}
		var sizes []uint64
		var newest uint64
		for size := range want {
			sizes = append(sizes, size)
			newest = max(newest, size)
		}
		sizes = append(sizes, newest+1) // no tick's

		got, err := ticksAtSizes(t.Context(), c, sizes)
		require.NoError(t, err)
		assert.Equal(t, want, got)
		none, err := ticksAtSizes(t.Context(), c, nil)
		require.NoError(t, err)
		assert.Empty(t, none)
	})
}

// A delivery longer than the budget is ended at the budget and tried again
// at a later tick; before capsule-cli#645 it ran to the client's 30s.
func TestDealTickEndsADeliveryAtTheBudget(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1h", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clockAt(t, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	h.slow(30*time.Second, key)
	start := h.now
	tick := dealRun(t, "tick")
	require.Equal(t, "ticked", tick["state"])
	assert.Empty(t, tick["delivered"])
	rows := pendingRows(t, tick)
	require.Len(t, rows, 1)
	assert.Equal(t, "witness_timeout", rows[0]["reason"])
	assert.Equal(t, dealDeliverBudget, h.now.Sub(start))
}

// A budget under capsule-cli's 2s floor sends nothing: every row is left
// as it was, with its stored reason, and says it was not tried.
func TestDealTickBudgetUnderTheFloorSendsNothing(t *testing.T) {
	public, key := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1m", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clock := backlog(t, h, 2)
	*clock = clock.Add(-30 * time.Second) // no tick due
	h.calls = nil
	h.slow(time.Second, key)
	tick := dealRun(t, "tick", "--deliver-for", "1s")
	assert.Empty(t, h.calls)
	assert.Empty(t, tick["delivered"])
	rows := pendingRows(t, tick)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, true, row["not_tried_this_run"], "%v", row)
		assert.Equal(t, "not_attempted", row["reason"], "a row not tried keeps its stored reason")
	}
}

// Through the real cli.DeliverWitnessBy on the wall clock, a witness that
// accepts the request and hangs costs the tick its budget, not the witness
// client's 30s, and the attempt is recorded as a slow witness
// (evidencebook#960, capsule-cli#645).
func TestDealTickEndsAHangingWitnessAtTheBudget(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	// Cleanups run last-in first-out: release the handlers, then close.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	// capsule-cli's witness client sends over http.DefaultTransport and
	// accepts only HTTPS; this package's tests do not run in parallel.
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	public, _ := witnessPair(t)
	cadenceFixture(t, server.URL, public, "1h", "0s", 0)
	retailDeal(t)
	clockAt(t, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	budget := 3 * time.Second
	start := time.Now()
	tick := dealRun(t, "tick", "--deliver-for", budget.String())
	elapsed := time.Since(start)
	require.Equal(t, "ticked", tick["state"], "%v", tick)
	assert.GreaterOrEqual(t, elapsed, budget-deliverFloor, "the request ran until the deadline")
	assert.Less(t, elapsed, budget+5*time.Second, "ended at the budget, not at 30s")
	assert.Empty(t, tick["delivered"])
	rows := pendingRows(t, tick)
	require.Len(t, rows, 1)
	assert.Equal(t, tick["checkpoint"], rows[0]["checkpoint"])
	assert.Equal(t, "witness_timeout", rows[0]["reason"], "%v", rows[0])
	assert.NotContains(t, rows[0], "not_tried_this_run")
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
	dealDeliverWitness = func(ctx context.Context, _ cli.Profile, store cll.WitnessStateStore, service string, size uint64, _ time.Time) (cll.WitnessState, error) {
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
		// A 422: a first continuity 409 is re-armed (capsule-cli#648), not failed for good.
		failed.LastError = "witness returned HTTP 400: " + cli.WitnessFailureBody(422)
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
	dealDeliverNow = time.Now // the real delivery reads the wall clock
	dealDeliverWitness = func(ctx context.Context, p cli.Profile, store cll.WitnessStateStore, service string, size uint64, deadline time.Time) (cll.WitnessState, error) {
		h.calls = append(h.calls, size)
		return cli.DeliverWitnessBy(ctx, p, store, service, size, deadline)
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

// A checkpoint the witness refused again after its retry, whose re-anchor
// got no answer, is stored as failed but is still pending (capsule-cli#649).
// The next tick, with no new checkpoint due, hands it to the delivery first
// and holds the later ones, instead of leaving it out of the tick.
func TestDealTickRetriesAReanchorDueCheckpoint(t *testing.T) {
	public, _ := witnessPair(t)
	cadenceFixture(t, "https://127.0.0.1:1", public, "1m", "0s", 0)
	retailDeal(t)
	h := hookDelivery(t)
	clock := backlog(t, h, 3)
	*clock = clock.Add(-30 * time.Second) // no tick due
	h.calls = nil
	var due uint64
	dealDeliverWitness = func(ctx context.Context, _ cli.Profile, store cll.WitnessStateStore, service string, size uint64, _ time.Time) (cll.WitnessState, error) {
		h.calls = append(h.calls, size)
		state, err := store.GetWitness(ctx, service, size)
		require.NoError(t, err)
		if due != 0 {
			return state, nil
		}
		due = size
		// The marks capsule-cli keeps on a row refused again after its re-arm
		// (cli/checkpoint.go witnessRearmed, cli/witness_reanchor.go witnessTip).
		next := state
		next.Attempts++
		next.Permanent = true
		next.LastError = "witness returned HTTP 400: " + cli.WitnessFailureBody(409) +
			"; re-armed once after a continuity refusal; the witness last accepted mmr_size 1 root " + strings.Repeat("ab", 32)
		require.NoError(t, store.CommitWitness(ctx, state.Attempts, next))
		return next, nil
	}
	first := pendingRows(t, dealRun(t, "tick"))
	require.Len(t, h.calls, 1, "a row due for its re-anchor holds the later ones")
	require.NotEmpty(t, first)
	assert.Contains(t, first[0]["text"], "capsulectl proves it again from that checkpoint")

	h.calls = nil
	rows := pendingRows(t, dealRun(t, "tick"))
	assert.Equal(t, []uint64{due}, h.calls, "the re-anchor-due row is tried again, and only it")
	require.Len(t, rows, 3)
	assert.Equal(t, float64(due), rows[0]["checkpoint"])
	assert.Equal(t, "witness_error", rows[0]["reason"])
	assert.Contains(t, rows[0]["text"], "capsulectl proves it again from that checkpoint")
	for _, row := range rows[1:] {
		assert.Equal(t, true, row["not_tried_this_run"])
	}
}
