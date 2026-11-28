package deal

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// dealWitnessSees is said wherever the witness is configured.
const dealWitnessSees = "The witness sees one checkpoint per tick of this profile's checkpoint cadence: hashes, a size that grows by the same amount every tick, and a time on the cadence. It never sees content, how many deals there are, or when they happen."

// dealWitnessSeesNow is said after a stamp made on demand (deal tick --now):
// that stamp is the exception ASSURANCE.md names.
const dealWitnessSeesNow = "This stamp was made on demand, off the cadence, so the witness sees the minute it was made. Otherwise the witness sees hashes, a size that grows by the same amount every tick, and a time on the cadence; never content or how many deals there are."

// dealCadenceSchema is the table released builds kept their ticks in. The
// cadence now keeps its own (evidencebook's cadence_ticks); this one is read
// once, and again after a released build wrote to it, by migrate.
var dealCadenceSchema = `CREATE TABLE IF NOT EXISTS deal_cadence (
	tick INTEGER PRIMARY KEY,
	at TEXT NOT NULL,
	due_next TEXT NOT NULL,
	entry_seq INTEGER NOT NULL,
	checkpoint_size INTEGER NOT NULL,
	checkpoint BLOB NOT NULL,
	leaves TEXT NOT NULL
)`

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *dealSession) dealIDs(ctx context.Context) ([]string, error) { return s.index.dealIDs(ctx) }

// localOnly is the profile with its witness removed: a deal's own checkpoint
// is signed but never queued for, or sent to, the witness.
func localOnly(p cli.Profile) cli.Profile {
	p.Checkpoint.Endpoint = ""
	return p
}

// cadenceProfile makes sure the profile has a cadence log, giving a profile
// made before there was one its own random log id.
func (s *dealSession) cadenceProfile() (cli.Profile, error) {
	if s.p.LogID != "" {
		return s.p, nil
	}
	suffix, err := randomHex(8)
	if err != nil {
		return cli.Profile{}, err
	}
	s.p.LogID = "deal-cadence/" + suffix
	return s.p, cli.SaveProfile(s.p, true)
}

// tick publishes the cadence checkpoint when a tick is due, and retries any
// delivery still pending either way. The time a tick is due depends only on
// the previous tick and a random jitter, never on what the deals did. The
// tick itself is evidencebook's cadence over the shelf: it checkpoints the
// live logs only, with the occupancy of every log the profile ever had
// (capsule-cli#242 note §2.5).
//
// budget bounds the time spent delivering pending checkpoints
// (deliverCadence); zero is no limit.
//
// With now set it stamps at once, whatever the schedule (evidencebook's
// Cadence.Stamp, the on-demand path deal agree and deal ruling already
// take), on a shelf that is a log of book facts; on one not yet migrated
// it falls back to the scheduled tick.
func (s *dealSession) tick(ctx context.Context, stampNow bool, budget time.Duration) (map[string]any, error) {
	c, p, err := s.cadenceOf(ctx)
	if err != nil {
		return nil, err
	}
	service, err := cli.ServiceID(p)
	if err != nil {
		return nil, err
	}
	if err := s.coverLiveSteps(ctx, c); err != nil {
		return nil, err
	}
	now := cli.DealClock()
	tick, onDemand := c.Tick, false
	if stampNow {
		migrated, err := c.Migrated(ctx)
		if err != nil {
			return nil, err
		}
		if migrated {
			tick, onDemand = c.Stamp, true
		}
	}
	result, err := tick(ctx, now)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"cadence_log": p.LogID}
	if stampNow {
		// false when the shelf holds no x-deal-v1 book yet (not migrated):
		// the scheduled tick ran instead, so a caller knows why nothing was stamped.
		out["stamped_on_demand"] = onDemand
	}
	if result.Ticked {
		// The cut time only sharpens the History card's guard (cutOf falls back to
		// the coarse tick time), so failing to keep it never fails a committed tick.
		if err := recordSHCut(p, result.Tick.N, now); err != nil {
			out["cut_time_not_recorded"] = true
		}
		// A book whose status could not be built keeps its last fact: say how many,
		// never why (the reason can hold record text), so a stale fact is not silent.
		if n := len(result.Unstated); n > 0 {
			out["books_with_stale_status"] = n
		}
		if result.BeforeStamp != "" {
			out["naming_rulings_failed"] = true
		}
		out["state"], out["tick"], out["checkpoint"], out["due"] = "ticked", result.Tick.N, result.Tick.CheckpointSize, result.Tick.DueNext.Format(time.RFC3339)
	} else {
		out["state"], out["due"] = "not_due", result.DueNext.Format(time.RFC3339)
	}
	if service == "" {
		out["witness"] = "not_configured"
		return out, nil
	}
	out["witness_sees"] = dealWitnessSees
	if onDemand {
		out["witness_sees"] = dealWitnessSeesNow
	}
	if err := s.settleEnded(ctx, c); err != nil {
		return nil, err
	}
	delivered, deliveredTicks, pending, err := s.deliverCadence(ctx, c, p, service, now, budget)
	if err != nil {
		return nil, err
	}
	out["delivered"], out["delivered_ticks"], out["pending"] = delivered, deliveredTicks, pending
	return out, nil
}

// deliverCadence makes one delivery attempt of the cadence checkpoints
// still pending for the witness, oldest first, and reports each verified receipt to the
// cadence (Witnessed), which records its leaves as witnessed and lets ended
// deals leave the live set. A receipt stored but not reported (an error in
// between) leaves the session unmigrated, so close does not mark the store
// seen and the next open's repair reports it (witnessNativeTicks); a
// receipt a released build stored is found the same way.
//
// It stops early in two cases, leaving the rest pending and untouched for
// a later tick, so one tick never takes a witness round trip per pending
// checkpoint (evidencebook#937):
//   - a checkpoint comes back undelivered and not failed for good: the
//     witness holds every later one until this one has a receipt, and the
//     next call would send this one again first (cli.DeliverWitness);
//   - the delivery budget is spent (zero is no limit): it is one deadline
//     for every witness request of the run (cli.DeliverWitnessBy), so a
//     request under way at the deadline is ended and recorded like any
//     timed-out delivery, and a row with less than deliverFloor left is not
//     sent. Storing a result and checking a receipt come after the deadline.
//
// Oldest first is the protocol's order, not a choice: the witness refuses a
// checkpoint whose predecessor it has not registered. Each row not tried
// keeps the reason of its stored state and says it was not tried.
//
// Each delivered checkpoint is also listed in deliveredTicks with the number
// of the cadence tick (the stamp) that holds it, in delivered's order, and
// each pending row names that number as "tick" when a tick holds it
// (evidencebook#1083): a stamp cut by `deal agree` or `deal ruling` is
// delivered by a later tick, and its reader numbers it from here rather
// than from its size.
func (s *dealSession) deliverCadence(ctx context.Context, c *evidencebook.Cadence, p cli.Profile, service string, now time.Time, budget time.Duration) (delivered []uint64, deliveredTicks []map[string]any, pending []map[string]any, err error) {
	t, err := openOwnedTarget(ctx, p, cli.UseCLL, s)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { err = errors.Join(err, t.Close()) }()
	if err := cli.RearmRefusedLog(ctx, t.Log, service, now); err != nil {
		return nil, nil, nil, err
	}
	waiting, err := t.Log.PendingWitnesses(ctx, now.Add(24*time.Hour), cll.MaxWitnesses)
	if err != nil {
		return nil, nil, nil, err
	}
	if waiting, err = withReanchorDue(ctx, t.Log, waiting, p, service); err != nil {
		return nil, nil, nil, err
	}
	var sizes []uint64
	for _, w := range waiting {
		if w.WitnessID == service {
			sizes = append(sizes, w.CheckpointSize)
		}
	}
	ticks, err := ticksAtSizes(ctx, c, sizes)
	if err != nil {
		return nil, nil, nil, err
	}
	// pendingRow is a pending row for the checkpoint at size, with the
	// number of the tick that holds it when one does.
	pendingRow := func(size uint64, members map[string]any) map[string]any {
		members["checkpoint"] = size
		if n, ok := ticks[size]; ok {
			members["tick"] = n
		}
		return members
	}
	delivered, deliveredTicks, pending = []uint64{}, []map[string]any{}, []map[string]any{}
	var deadline time.Time
	if budget > 0 {
		deadline = dealDeliverNow().Add(budget)
	}
	stopped := false
	for _, w := range waiting {
		if w.WitnessID != service {
			continue
		}
		if !stopped && !deadline.IsZero() && deadline.Sub(dealDeliverNow()) < deliverFloor {
			stopped = true
		}
		if stopped {
			reason, text := cli.WitnessPendingReason(w, p.Checkpoint.Endpoint)
			pending = append(pending, pendingRow(w.CheckpointSize, map[string]any{"reason": reason, "text": text, "not_tried_this_run": true}))
			continue
		}
		// A delivery can store a receipt and still fail (a cancelled read
		// after the commit), so the session is unmigrated from before the
		// attempt until the receipt, if one landed, is reported.
		migrated := s.migrated
		s.migrated = false
		// A delivery error is pending; what the delivery did, a receipt
		// included, is read back from the log below, not from its return,
		// which an error leaves empty.
		_, _ = dealDeliverWitness(ctx, p, t.Log, service, w.CheckpointSize, deadline)
		receipt, ok, err := verifiedReceipt(ctx, p, t.Log, service, w.CheckpointSize)
		if err != nil {
			return nil, nil, nil, err
		}
		if !ok {
			state, err := t.Log.GetWitness(ctx, service, w.CheckpointSize)
			if err != nil {
				return nil, nil, nil, err
			}
			reason, text := cli.WitnessPendingReason(state, p.Checkpoint.Endpoint)
			row := pendingRow(w.CheckpointSize, map[string]any{"reason": reason, "text": text})
			// No attempt recorded: the delivery sent nothing, because the
			// budget crossed capsule-cli's floor after the check above or the
			// row's retry is not due yet.
			if state.Attempts == w.Attempts && !state.Permanent {
				row["not_tried_this_run"] = true
			}
			pending = append(pending, row)
			s.migrated = migrated
			// Failed for good, the witness decides on the next one without
			// it (selectedWitness); otherwise every later one waits for it,
			// a refused one due for its re-anchor included.
			stopped = !failedForGood(state, p)
			continue
		}
		n, found := ticks[w.CheckpointSize]
		// A cadence checkpoint no tick holds (a released build's core write,
		// note §3.3) is witnessed on the log but not part of the cadence.
		if found {
			check, err := c.Witnessed(ctx, n, *receipt)
			if err != nil {
				return nil, nil, nil, err
			}
			if check.State != evidencebook.WitnessVerified {
				// Nothing was stored, so the checkpoint is reported pending
				// again rather than delivered.
				pending = append(pending, pendingRow(w.CheckpointSize, map[string]any{"reason": "not_checked", "text": check.Reason}))
				s.migrated = migrated
				continue
			}
			delivered = append(delivered, w.CheckpointSize)
			deliveredTicks = append(deliveredTicks, map[string]any{"checkpoint": w.CheckpointSize, "tick": n})
		}
		s.migrated = migrated
	}
	return delivered, deliveredTicks, pending, nil
}

// dealDeliverWitness is cli.DeliverWitnessBy, a test hook for a delivery
// that stores its receipt and then fails.
var dealDeliverWitness = cli.DeliverWitnessBy

// deliverFloor is the least time left before the delivery deadline at which
// deliverCadence still sends a row. It is capsule-cli's own floor
// (witnessBudgetFloor, 2 s at 16ec5bc9, unexported), below which
// DeliverWitnessBy sends nothing and records no attempt; checking it here
// lets the row be reported not tried this run rather than read back as an
// unchanged pending row.
const deliverFloor = 2 * time.Second

// dealDeliverNow is the clock the delivery budget is measured on: the wall
// clock, not cli.DealClock, which tests and demos may pin. A test hook.
var dealDeliverNow = time.Now

// dealDeliverBudget is how long a deal tick run without --wait-up-to (a
// person or a host waiting on it, as with --now) spends delivering pending
// checkpoints before leaving the rest for a later tick.
const dealDeliverBudget = 5 * time.Second

// ticksAtSizes maps each of sizes that a cadence tick's checkpoint has to
// that tick's number, in one walk back from the newest tick that stops below
// the smallest size: pending deliveries are for recent ticks, and one walk
// for the whole run keeps a long backlog from costing a walk per row.
func ticksAtSizes(ctx context.Context, c *evidencebook.Cadence, sizes []uint64) (map[uint64]uint64, error) {
	ticks := map[uint64]uint64{}
	if len(sizes) == 0 {
		return ticks, nil
	}
	want := make(map[uint64]bool, len(sizes))
	least := sizes[0]
	for _, size := range sizes {
		want[size] = true
		least = min(least, size)
	}
	last, ok, err := c.Last(ctx)
	if err != nil || !ok {
		return ticks, err
	}
	for n := last.N; n >= 1 && len(ticks) < len(want); n-- {
		tick, err := c.Get(ctx, n)
		if err != nil {
			return nil, err
		}
		if want[tick.CheckpointSize] {
			if _, seen := ticks[tick.CheckpointSize]; !seen {
				ticks[tick.CheckpointSize] = n
			}
		}
		if tick.CheckpointSize != 0 && tick.CheckpointSize < least {
			break
		}
	}
	return ticks, nil
}

// dealWitnessState says, for the deal checkpoint a report carries, where it
// stands with the witness, truthfully: not_configured, scheduled (not yet in
// a tick), pending (in a tick whose delivery has not completed) or witnessed
// (with the whole chain a verifier needs), or unverified (a receipt is held
// but no longer verifies for the profile's witness).
//
// Witnessed comes in two extents (evidencebook's Cadence.Chain, which is
// capsule-cli's cadenceChain member for member). "all": a witnessed tick
// holds this very checkpoint. "part": a witnessed tick holds an EARLIER
// checkpoint of the deal; the chain then carries that checkpoint and an MMR
// consistency proof from it to the current one, so a verifier sees exactly
// which steps the witness covers, and the deal adds its step counts.
func (s *dealSession) dealWitnessState(ctx context.Context, dealID string, statement []byte) (_ map[string]interface{}, err error) {
	service, err := cli.ServiceID(s.p)
	if err != nil || service == "" || s.p.LogID == "" {
		return map[string]interface{}{"state": "not_configured"}, err
	}
	current, err := checkpoint.ParseRecord(statement)
	if err != nil {
		return nil, err
	}
	c, p, err := s.cadenceOf(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := c.Chain(ctx, statement)
	if errors.Is(err, evidencebook.ErrNotFound) {
		return s.notYetWitnessed(ctx, c, p, service, dealID, current)
	}
	if err != nil {
		return nil, err
	}
	chain, err := bundleValue(raw)
	if err != nil {
		return nil, err
	}
	// The cadence recorded the receipt when it verified; the chain counts
	// only if it still verifies for the witness this profile names now (a
	// changed endpoint or key leaves the witness unverified: a check that
	// failed, which is not a check not yet made).
	if ok, err := chainVerifiesNow(ctx, p, service, chain); err != nil {
		return nil, err
	} else if !ok {
		return witnessUnverified(), nil
	}
	if chain["extent"] != "part" {
		return chain, nil
	}
	sizeNumber, _ := chain["size"].(json.Number)
	size, err := strconv.ParseUint(string(sizeNumber), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: the cadence chain's size: %v", cli.ErrConflict, err)
	}
	witnessedSteps, err := s.stepsIn(ctx, dealID, mmrLeafCount(size))
	if err != nil {
		return nil, err
	}
	steps, err := s.stepsIn(ctx, dealID, mmrLeafCount(current.MMRSize))
	if err != nil {
		return nil, err
	}
	chain["steps_witnessed"], chain["steps"] = cli.Integer(witnessedSteps), cli.Integer(steps)
	if chain["rest"], err = s.notYetWitnessed(ctx, c, p, service, dealID, current); err != nil {
		return nil, err
	}
	return chain, nil
}

// witnessUnverified is the state of a deal checkpoint whose witness receipt
// is held but does not verify under the profile's witness (a tampered
// receipt, one signed by another key, a rotated witness key or a changed
// endpoint, under which the chain no longer verifies): the check failed, so it is
// neither not-yet-witnessed nor witnessed (actionstate-muse#311 §3.3,
// "Unknown or failed": the check ran and failed).
func witnessUnverified() map[string]interface{} {
	return map[string]interface{}{"state": "unverified"}
}

// chainVerifiesNow reports whether the cadence checkpoint a chain names has
// a receipt, on the cadence log, that verifies for the profile's current
// witness service and key. A chain exists only because the cadence recorded a
// verified receipt, so false here is a check that now fails (a tampered
// receipt, a rotated key or a changed endpoint: the current service holds no
// receipt), never one not yet made.
func chainVerifiesNow(ctx context.Context, p cli.Profile, service string, chain map[string]interface{}) (ok bool, err error) {
	cadence, _ := chain["cadence"].(map[string]interface{})
	cp, _ := cadence["checkpoint"].(map[string]interface{})
	cose, _ := cp["cose"].(string)
	statement, err := base64.RawURLEncoding.DecodeString(cose)
	if err != nil {
		return false, nil
	}
	record, err := checkpoint.ParseRecord(statement)
	if err != nil {
		return false, nil
	}
	t, err := cli.OpenTarget(ctx, p, cli.UseCLLRead)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, t.Close()) }()
	state, err := t.Log.GetWitness(ctx, service, record.MMRSize)
	if errors.Is(err, cll.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state.Receipt != nil && cli.VerifyWitness(p, state) == nil, nil
}

// bundleValue is v as a bundle's members hold JSON: decoded objects, with
// every number a json.Number, as cli.Integer writes them, so the bundle's
// canonical form (JCS) takes it.
func bundleValue(v map[string]any) (map[string]interface{}, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var out map[string]interface{}
	if err := d.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// notYetWitnessed is where a deal checkpoint no witnessed tick holds
// stands: pending when the newest tick holding the deal's log holds this
// checkpoint, or an earlier one with the same steps (only the deal's sealed
// reports came after it), and its delivery has not completed; scheduled
// otherwise, with the next tick's due time. Only that one tick is read
// (note §2.5): a live log is in every tick.
func (s *dealSession) notYetWitnessed(ctx context.Context, c *evidencebook.Cadence, p cli.Profile, service, dealID string, current checkpoint.Record) (_ map[string]interface{}, err error) {
	rest := map[string]interface{}{"state": "scheduled", "cadence": s.cadenceWords()}
	last, ok, err := c.Last(ctx)
	if err != nil || !ok {
		return rest, err
	}
	rest["due"] = last.DueNext.Format(time.RFC3339)
	leaf, in := last.Leaves.Deals[cli.DealLogID(dealID)]
	if !in || leaf.Size > current.MMRSize {
		return rest, nil
	}
	held, err := s.stepsIn(ctx, dealID, mmrLeafCount(leaf.Size))
	if err != nil {
		return nil, err
	}
	steps, err := s.stepsIn(ctx, dealID, mmrLeafCount(current.MMRSize))
	if err != nil {
		return nil, err
	}
	if held != steps {
		return rest, nil
	}
	t, err := cli.OpenTarget(ctx, p, cli.UseCLLRead)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, t.Close()) }()
	state, err := t.Log.GetWitness(ctx, service, last.CheckpointSize)
	if err != nil && !errors.Is(err, cll.ErrNotFound) {
		return nil, err
	}
	// A tick whose receipt verifies is not pending: what is not yet
	// witnessed (only sealed reports since) waits for the next tick.
	if err == nil && state.Receipt != nil && cli.VerifyWitness(p, state) == nil {
		return rest, nil
	}
	// A receipt that came back but does not verify is a failed check, not a
	// delivery still pending.
	if err == nil && state.Receipt != nil {
		return witnessUnverified(), nil
	}
	reason, text := cli.WitnessPendingReason(state, p.Checkpoint.Endpoint)
	return map[string]interface{}{"state": "pending", "tick": cli.Integer(last.N), "reason": reason, "text": text, "cadence": s.cadenceWords()}, nil
}

func trimEndpoint(endpoint string) string {
	for len(endpoint) > 0 && endpoint[len(endpoint)-1] == '/' {
		endpoint = endpoint[:len(endpoint)-1]
	}
	return endpoint
}

// dealSleep waits d, or until ctx ends; tests replace it.
var dealSleep = func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The checkpoint cadence's poll (`cll checkpoint cadence`, also `deal
// tick`). A tick is published when the poll runs at or after its due time,
// so the jitter in the due time only shows if the poll runs near it: a
// scheduler that runs it every N minutes passes --wait-up-to N (5m by the
// documented schedule), and the run then waits for each due time inside
// that window and publishes on time. A run with no tick due publishes
// nothing. The waiting happens with the store unlocked, and every publish
// re-checks the due time under the lock.
func dealTickCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "tick", Short: "The checkpoint cadence's poll: when a tick is due, publish the profile's cadence checkpoint to its witness; a run that is not due cuts no tick and only retries any delivery still pending (schedule it every 5 minutes with --wait-up-to 5m; deal events never publish)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		wait, _ := c.Flags().GetDuration("wait-up-to")
		if wait < 0 {
			return cli.InputError("--wait-up-to must not be negative")
		}
		stampNow, _ := c.Flags().GetBool("now")
		if stampNow && wait > 0 {
			return cli.InputError("--now takes one stamp at once; it does not wait (drop --wait-up-to)")
		}
		// Nobody waits on a scheduled run, and a budget there would let a
		// slow witness's backlog grow by a tick for each row it clears.
		budget := dealDeliverBudget
		if wait > 0 {
			budget = 0
		}
		if c.Flags().Changed("deliver-for") {
			budget, _ = c.Flags().GetDuration("deliver-for")
		}
		if budget < 0 {
			return cli.InputError("--deliver-for must not be negative")
		}
		until := cli.DealClock().Add(wait)
		published := 0
		// Merchant emails deferred for want of DNS are sealed first, so
		// their key records are in the checkpoint this tick publishes.
		// A failure there is reported, and never stops the tick.
		emails, err := sealDeferredEmails(c)
		if err != nil {
			emails = map[string]any{"error": err.Error()}
		}
		for {
			var out map[string]any
			err := runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				var err error
				out, err = s.tick(ctx, stampNow, budget)
				return err
			})
			if err != nil {
				return err
			}
			if out["state"] == "ticked" {
				published++
			}
			due, err := time.Parse(time.RFC3339, fmt.Sprint(out["due"]))
			now := cli.DealClock()
			if wait == 0 || err != nil || due.After(until) {
				out["published_this_run"] = published
				if emails != nil {
					out["merchant_emails"] = emails
				}
				return cli.Output(c, out)
			}
			if d := due.Sub(now); d > 0 {
				if err := dealSleep(c.Context(), d); err != nil {
					return err
				}
			}
		}
	}}
	cmd.Flags().Bool("now", false, "Stamp at once, whatever the schedule, as deal agree and deal ruling do: the witness then sees a stamp off the cadence, so its time can say when this profile acted (for demos and tests; a scheduler never passes it)")
	cmd.Flags().Duration("deliver-for", dealDeliverBudget, "How long each poll spends on witness requests for checkpoints still pending, oldest first, before leaving the rest for a later tick; a request still running then is ended and tried again at a later tick, and none starts with under 2s left. 0 is no limit, the default with --wait-up-to, whose runs nobody waits on")
	cmd.Flags().Duration("wait-up-to", 0, "The scheduler's period (for example 5m): wait for each tick due within this long and publish it on time, so the cadence keeps its jitter; the run stays alive up to this long")
	return cmd
}

// cadenceWords is this profile's cadence as a receipt says it, for example
// "every 5m, give or take 2m": the profile's own values, not the default.
func (s *dealSession) cadenceWords() string {
	cfg, err := dealCadenceOf(s.p)
	if err != nil {
		return ""
	}
	words := "every " + ShortDuration(cfg.Interval)
	if cfg.Jitter > 0 {
		words += ", give or take " + ShortDuration(cfg.Jitter)
	}
	return words
}

// aboutDuration writes a wait for a reader: 1h10m, 1h, 7m or 30s.
func aboutDuration(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d >= time.Hour && d%time.Minute == 0:
		return fmt.Sprintf("%dh%dm", d/time.Hour, d%time.Hour/time.Minute)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return d.String()
	}
}

// mmrLeafCount is the number of leaves (deal steps) in an MMR of this size.
func mmrLeafCount(size uint64) uint64 {
	var leaves uint64
	for peak := uint64(1) << 62; peak > 0; peak >>= 1 {
		nodes := 2*peak - 1
		if size >= nodes {
			size -= nodes
			leaves += peak
		}
	}
	return leaves
}

// tickStalenessLine is the receipt's warning that the checkpoint schedule has
// stopped: the newest tick is older than two cadence intervals (Steven's
// ruling, capsule-cli#600). It is "" while the schedule is on time, and for a
// newest tick in the future (a clock set back). The age is measured from the
// tick's own time, not its due time, which a late tick would push forward.
func tickStalenessLine(last time.Time, interval time.Duration, now time.Time, witness bool) string {
	if interval <= 0 || now.Sub(last) <= 2*interval {
		return ""
	}
	covers := "the next tick, once the schedule runs again, covers it"
	if witness {
		covers += " and sends it to your witness"
	}
	return "Checkpoints stopped at " + last.UTC().Format("2006-01-02T15:04Z") + ": the newest checkpoint tick is more than two cadence intervals old (every " + ShortDuration(interval) + "), so nothing may be running the schedule here. A receipt made since then is not yet covered by a checkpoint; " + covers + ". To keep the schedule running, see \"Keeping the poll running\" in the capsule-cli README."
}

// stalenessLine is tickStalenessLine for this profile's newest tick, or "".
// It is advisory: a profile with no tick yet, or a cadence that cannot be
// read, says nothing rather than failing the report.
func (s *dealSession) stalenessLine(ctx context.Context) string {
	// A profile that never ticked has no cadence log yet; cadenceOf would
	// create one and save the profile, which a report must not do.
	if s.p.LogID == "" {
		return ""
	}
	cfg, err := dealCadenceOf(s.p)
	if err != nil {
		return ""
	}
	c, _, err := s.cadenceOf(ctx)
	if err != nil {
		return ""
	}
	last, ok, err := c.Last(ctx)
	if err != nil || !ok {
		return ""
	}
	service, err := cli.ServiceID(s.p)
	if err != nil {
		return ""
	}
	return tickStalenessLine(last.At, cfg.Interval, cli.DealClock(), service != "")
}

// withStaleness adds the stopped-schedule line to a report's assurance, as
// text and as checkpoints_stopped_at, when the schedule has stopped.
func (s *dealSession) withStaleness(ctx context.Context, assurance map[string]any) map[string]any {
	line := s.stalenessLine(ctx)
	if line == "" {
		return assurance
	}
	assurance["text"] = assurance["text"].(string) + " " + line
	assurance["checkpoints_stopped"] = line
	return assurance
}

// fileWitnessState is this profile's own witness state for the checkpoint a
// deal bundle file was bound to, for `deal report --from-bundle`: the file
// carries no witnessed chain when it was written after u225, so the holder's
// words come from the holder's own book, never from the file. Only a
// witnessed state is taken: it is true of that checkpoint whenever the file
// was written. Any other state (scheduled, pending) is read against the
// deal's current checkpoint and may not hold for an older file, so the file
// says not_carried, as before, as does one whose checkpoint cannot be read.
func (s *dealSession) fileWitnessState(ctx context.Context, dealID string, b map[string]interface{}) (map[string]interface{}, error) {
	cp, _ := b["checkpoint"].(map[string]interface{})
	encoded, _ := cp["cose"].(string)
	statement, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || encoded == "" {
		return map[string]interface{}{"state": "not_carried"}, nil
	}
	state, err := s.dealWitnessState(ctx, dealID, statement)
	if err != nil || state["state"] != "witnessed" {
		return map[string]interface{}{"state": "not_carried"}, err
	}
	return state, nil
}

// failedForGood reports a delivery that is never tried again, as the core's
// own status reads it: a row stored as failed that a later run re-anchors
// (a continuity 409 refused again, capsule-cli#649) is still pending.
func failedForGood(state cll.WitnessState, p cli.Profile) bool {
	_, text := cli.WitnessPendingReason(state, p.Checkpoint.Endpoint)
	return state.Permanent && strings.HasPrefix(text, cli.WitnessNotDelivered)
}

// withReanchorDue is waiting with every row to service that is stored as
// failed but still pending (failedForGood), oldest first. The log's own
// pending list leaves out every row stored as failed, so a re-anchor that
// got no answer (a timeout, a spent budget) would otherwise wait for the
// next checkpoint's delivery, and the tick would report nothing pending.
func withReanchorDue(ctx context.Context, log cll.Backend, waiting []cll.WitnessState, p cli.Profile, service string) ([]cll.WitnessState, error) {
	state, err := log.LoadCLL(ctx)
	if err != nil {
		return nil, err
	}
	out := waiting
	for _, w := range state.Witnesses {
		if w.WitnessID == service && w.Receipt == nil && w.Permanent && !failedForGood(w, p) {
			out = append(out, w)
		}
	}
	if len(out) == len(waiting) {
		return waiting, nil
	}
	slices.SortStableFunc(out, func(a, b cll.WitnessState) int { return cmp.Compare(a.CheckpointSize, b.CheckpointSize) })
	return out, nil
}
