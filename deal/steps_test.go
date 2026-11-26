// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func passVerdict(card string) xdealv1.VerdictBody {
	return xdealv1.VerdictBody{VerdictBody: dealprofile.VerdictBody{Result: "pass", Differences: []dealprofile.Difference{}, Options: []string{},
		CardCommitment: card, Rules: &dealprofile.VerdictBodyRules{Status: "not_configured"}}}
}

// approvalStep is the person's answer to verdict, as a journal step.
func approvalStep(verdict, card, said string) JournalStep {
	return JournalStep{Kind: xdealv1.TypeApproval, Build: func(StepFacts) (StepParts, error) {
		ref := xdealv1.RefTo(verdict)
		body := map[string]any{"choice": "proceed", "proceed": true, "approver": "user", "said_commitment": said}
		if card != "" {
			body["card_commitment"] = card
		}
		return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeApproval,
			Block: map[string]any{"refs": []any{map[string]any{"rel": "approves", "type": ref.Type, "digest_alg": ref.DigestAlg, "digest": ref.Digest}}},
			Body:  body}}, nil
	}}
}

// A deal opened, checked, approved and acted on with this package alone.
func TestCheckApproveAndPerformWithTheLibraryAlone(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	card := nonceOf("library card")
	checked, err := seller.store.Check(ctx, dealID, CheckIn{Check: xdealv1.CheckBody{CheckBody: dealprofile.CheckBody{Action: "commit"}}, Verdict: passVerdict(card)})
	require.NoError(t, err)
	seller.tick()
	_, err = seller.store.SealJournal(ctx, dealID, approvalStep(checked.Verdict, card, nonceOf("said")))
	require.NoError(t, err)

	journal, err := seller.store.DealJournal(ctx, dealID)
	require.NoError(t, err)
	require.Len(t, journal, 3)
	assert.Equal(t, dealID, journal[0].Block.DealID)
	cb, ok := journal[0].Body.(*xdealv1.CheckBody)
	require.True(t, ok, "%T", journal[0].Body)
	assert.Len(t, cb.Nonce, 64, "the store drew the check's nonce")
	require.Len(t, journal[1].Block.Refs, 1)
	assert.Equal(t, checked.Check, journal[1].Block.Refs[0].Digest, "the verdict cites its check")

	steps, err := seller.store.Steps(ctx, dealID)
	require.NoError(t, err)
	require.Len(t, steps, 3)
	assert.Equal(t, []int64{1, 2, 3}, []int64{steps[0].N, steps[1].N, steps[2].N})
	assert.Equal(t, json.RawMessage("null"), steps[0].Local, "a step written with no local form")

	seller.tick()
	got, err := seller.store.Perform(ctx, dealID, PerformIn{Check: checked.Check, Act: "deliver", Observed: "carrier_scan"})
	require.NoError(t, err)
	require.NotNil(t, got.Authorization)
	assert.True(t, got.Authorization.NeedsPersonSignature)
	assert.False(t, got.Authorization.RulesNotRecorded)
}

func TestCheckRefusesAVerdictWithNoRules(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	v := passVerdict(nonceOf("card"))
	v.Rules = nil
	_, err := seller.store.Check(ctx, dealID, CheckIn{Check: xdealv1.CheckBody{CheckBody: dealprofile.CheckBody{Action: "commit"}}, Verdict: v})
	assert.Contains(t, inputReason(t, err), "verdict carries no rules")
	steps, err := seller.store.Steps(ctx, dealID)
	require.NoError(t, err)
	assert.Empty(t, steps)
}

// A record the authorization walk would refuse is never sealed: the
// person's approval must commit to the card it answered.
func TestSealJournalRefusesWhatTheWalkRefuses(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	card := nonceOf("card")
	checked, err := seller.store.Check(ctx, dealID, CheckIn{Check: xdealv1.CheckBody{CheckBody: dealprofile.CheckBody{Action: "commit"}}, Verdict: passVerdict(card)})
	require.NoError(t, err)
	_, err = seller.store.SealJournal(ctx, dealID, approvalStep(checked.Verdict, "", nonceOf("said")))
	assert.Contains(t, inputReason(t, err), "commits to the card it answered")
	// A confirm_limits approval whose approves ref names no intent.
	confirm := JournalStep{Kind: xdealv1.TypeApproval, Build: func(StepFacts) (StepParts, error) {
		ref := xdealv1.RefTo(checked.Verdict)
		return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeApproval,
			Block: map[string]any{"refs": []any{map[string]any{"rel": "approves", "type": ref.Type, "digest_alg": ref.DigestAlg, "digest": ref.Digest}}},
			Body: map[string]any{"choice": xdealv1.ChoiceConfirm, "proceed": true, "approver": "user", "said_commitment": nonceOf("s"),
				"limits": map[string]any{"previous": map[string]any{}, "new": map[string]any{"allowed": []any{"pay"}}}}}}, nil
	}}
	_, err = seller.store.SealJournal(ctx, dealID, confirm)
	assert.Contains(t, inputReason(t, err), "names no earlier intent")
	steps, err := seller.store.Steps(ctx, dealID)
	require.NoError(t, err)
	assert.Len(t, steps, 2, "only the check and the verdict")
}

// A valid confirm_limits approval seals, carries no deal id, and puts the
// limits in force for an act under a standing intent.
func TestSealJournalConfirmsLimits(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	intentStep := func(label string, allowed ...any) JournalStep {
		return JournalStep{Kind: "intent", Build: func(StepFacts) (StepParts, error) {
			return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeIntent, Body: map[string]any{"verbatim_commitment": nonceOf(label), "allowed": allowed}}}, nil
		}}
	}
	// The first intent is in force; a wider later one is a proposal until
	// the person confirms it.
	steps, err := seller.store.SealJournal(ctx, dealID, intentStep("first", "pay"), intentStep("wider", "pay", "commit"))
	require.NoError(t, err)
	intent := steps[1].Digest
	seller.tick()
	confirmed, err := seller.store.SealJournal(ctx, dealID, JournalStep{Kind: xdealv1.TypeApproval, Build: func(StepFacts) (StepParts, error) {
		ref := xdealv1.RefTo(intent)
		return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeApproval,
			Block: map[string]any{"refs": []any{map[string]any{"rel": "approves", "type": ref.Type, "digest_alg": ref.DigestAlg, "digest": ref.Digest}}},
			Body: map[string]any{"choice": xdealv1.ChoiceConfirm, "proceed": true, "approver": "user", "said_commitment": nonceOf("s"),
				"limits": map[string]any{"previous": map[string]any{"allowed": []any{"pay"}}, "new": map[string]any{"allowed": []any{"pay", "commit"}}}}}}, nil
	}})
	require.NoError(t, err)
	journal, err := seller.store.DealJournal(ctx, dealID)
	require.NoError(t, err)
	require.Len(t, journal, 3, "all bound by the deal's steps")
	assert.Empty(t, journal[2].Block.DealID, "a confirm_limits approval spans deals")
	_ = confirmed

	// The act the confirmed limits allow, under the standing intent.
	check := seller.approvedCheck(dealID, "commit", "standing_intent", "confirmed")
	got, err := seller.store.Perform(ctx, dealID, PerformIn{Check: check, Act: "deliver", Observed: "scan"})
	require.NoError(t, err)
	assert.Len(t, got.Authorization.LimitApprovals, 1)
}

func TestSealJournalWaitsWhileAPaymentIsInFlight(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	check := strings.Repeat("c", 64)
	_, err := seller.db.ExecContext(ctx, `INSERT INTO deal_v1_sends (deal_id, check_digest, event_n, amount_minor, currency, ref_type, ref_value, ref_json) VALUES (?, ?, 1, 6000, 'USD', 'ln.payment_hash', 'x', '{}')`, dealID, check)
	require.NoError(t, err)
	change := JournalStep{Kind: "change", Build: func(StepFacts) (StepParts, error) {
		return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeDetailChange, Body: map[string]any{"source": "counterparty", "changed": []any{"when"}}}}, nil
	}}
	_, err = seller.store.SealJournal(ctx, dealID, change)
	assertCoded(t, err, CodeInFlight, dealID, check, "", "ln.payment_hash", xdealv1.TypeDetailChange)

	_, err = seller.db.ExecContext(ctx, `UPDATE deal_v1_sends SET leg_digest=? WHERE deal_id=?`, strings.Repeat("d", 64), dealID)
	require.NoError(t, err)
	_, err = seller.store.SealJournal(ctx, dealID, change)
	require.NoError(t, err, "once the payment is settled or failed")
}

func TestSealJournalScansTheCallersLocalValues(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	// A local value the record carries. Digests and times are exempt from
	// the scan, and a message's other values are tokens, so the value here
	// is a member name, which the scan reads too.
	content, sent := nonceOf("content"), "2026-11-01T09:30:00Z"
	msg := func(local string) JournalStep {
		return JournalStep{Kind: "message", Build: func(StepFacts) (StepParts, error) {
			return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeMessage, Body: map[string]any{"from": "counterparty", "content_commitment": content, "sent_at": sent},
				LocalValues: []string{local}}}, nil
		}}
	}
	_, err := seller.store.SealJournal(ctx, dealID, msg("content_commitment"))
	assert.Contains(t, inputReason(t, err), "carries what it may not")
	_, err = seller.store.SealJournal(ctx, dealID, msg("alice-the-seller"))
	require.NoError(t, err)
}

func TestSealJournalLocalOnlySteps(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	named := strings.Repeat("b", 64)
	steps, err := seller.store.SealJournal(ctx, dealID,
		JournalStep{Kind: "open", Build: func(f StepFacts) (StepParts, error) {
			assert.Equal(t, int64(1), f.N)
			assert.Empty(t, f.Prev)
			return StepParts{Local: json.RawMessage(`{"kind":"open"}`)}, nil
		}},
		JournalStep{Kind: "act", Build: func(f StepFacts) (StepParts, error) {
			sum := sha256.Sum256([]byte(`{"kind":"open"}`))
			assert.Equal(t, hex.EncodeToString(sum[:]), f.Prev, "the step before it, by its local form's digest")
			return StepParts{Local: json.RawMessage(`{"kind":"act"}`), Digest: named}, nil
		}},
	)
	require.NoError(t, err)
	assert.Equal(t, named, steps[1].Digest)
	journal, err := seller.store.DealJournal(ctx, dealID)
	require.NoError(t, err)
	assert.Empty(t, journal, "local steps seal nothing")

	_, err = seller.store.SealJournal(ctx, dealID, JournalStep{Kind: "x", Build: func(StepFacts) (StepParts, error) {
		return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeMessage, Block: map[string]any{"deal_id": "deal-x"}, Body: map[string]any{}}}, nil
	}})
	assert.Contains(t, inputReason(t, err), "block member deal_id is the store's")
}

// An append that fails before the record is stored withdraws the batch; one
// that fails after leaves it, and the next read finishes it.
func TestSealJournalRecovery(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	step := JournalStep{Kind: "change", Build: func(StepFacts) (StepParts, error) {
		return StepParts{Local: json.RawMessage(`{}`), Record: &JournalRecord{RecordType: xdealv1.TypeDetailChange, Body: map[string]any{"source": "user", "changed": []any{"when"}}}}, nil
	}}
	stop := errors.New("the process died")
	appendJournalHook = func() error { return stop }
	t.Cleanup(func() { appendJournalHook, appendJournalAfterHook = nil, nil })
	_, err := seller.store.SealJournal(ctx, dealID, step)
	require.ErrorIs(t, err, stop)
	steps, err := seller.store.Steps(ctx, dealID)
	require.NoError(t, err)
	assert.Empty(t, steps, "withdrawn: nothing reached the journal")

	appendJournalHook = nil
	appendJournalAfterHook = func() error { return stop }
	_, err = seller.store.SealJournal(ctx, dealID, step)
	require.ErrorIs(t, err, stop)
	appendJournalAfterHook = nil
	var pending int
	require.NoError(t, seller.db.QueryRowContext(ctx, `SELECT count(*) FROM deal_v1_events WHERE deal_id=? AND payload IS NOT NULL`, dealID).Scan(&pending))
	assert.Equal(t, 1, pending, "kept: its record reached the journal")
	stored, err := seller.store.StoredSteps(ctx, dealID)
	require.NoError(t, err)
	assert.Len(t, stored, 1)
	require.NoError(t, seller.db.QueryRowContext(ctx, `SELECT count(*) FROM deal_v1_events WHERE deal_id=? AND payload IS NOT NULL`, dealID).Scan(&pending))
	assert.Equal(t, 1, pending, "StoredSteps writes nothing")
	steps, err = seller.store.Steps(ctx, dealID)
	require.NoError(t, err)
	assert.Len(t, steps, 1)
	require.NoError(t, seller.db.QueryRowContext(ctx, `SELECT count(*) FROM deal_v1_events WHERE deal_id=? AND payload IS NOT NULL`, dealID).Scan(&pending))
	assert.Zero(t, pending, "the read finished it")
	journal, err := seller.store.DealJournal(ctx, dealID)
	require.NoError(t, err)
	assert.Len(t, journal, 1, "sealed once")
}

// A batch whose second record's append fails after its first reached the
// journal keeps every step, and the next read appends only what is missing,
// once, with N and Prev as built.
func TestSealJournalPartialBatchRecovery(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	change := func(what string) JournalStep {
		return JournalStep{Kind: "change", Build: func(StepFacts) (StepParts, error) {
			return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeDetailChange, Body: map[string]any{"source": "user", "changed": []any{what}}}}, nil
		}}
	}
	var prevs []string
	local := JournalStep{Kind: "open", Build: func(f StepFacts) (StepParts, error) {
		prevs = append(prevs, f.Prev)
		return StepParts{Local: json.RawMessage(`{"open":true}`)}, nil
	}}
	appends := 0
	stop := errors.New("the second append failed")
	appendJournalHook = func() error {
		appends++
		if appends == 2 {
			return stop
		}
		return nil
	}
	t.Cleanup(func() { appendJournalHook = nil })
	_, err := seller.store.SealJournal(ctx, dealID, local, change("when"), change("place"))
	require.ErrorIs(t, err, stop)
	appendJournalHook = nil

	steps, err := seller.store.Steps(ctx, dealID)
	require.NoError(t, err)
	require.Len(t, steps, 3, "kept: the first record reached the journal")
	assert.Equal(t, []int64{1, 2, 3}, []int64{steps[0].N, steps[1].N, steps[2].N})
	assert.Equal(t, []string{""}, prevs)
	journal, err := seller.store.DealJournal(ctx, dealID)
	require.NoError(t, err)
	require.Len(t, journal, 2, "each record once")
	again, err := seller.store.Steps(ctx, dealID)
	require.NoError(t, err)
	assert.Len(t, again, 3)
	journal, err = seller.store.DealJournal(ctx, dealID)
	require.NoError(t, err)
	assert.Len(t, journal, 2, "nothing appended twice")
}

// A batch whose first record's append fails is withdrawn whole: a deal is
// never left with a local step but not the record beside it. (Moved from
// capsulectl-deal's TestAV1OpeningIsStoredWithItsIntentOrNotAtAll.)
func TestSealJournalWithdrawsAWholeBatch(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	stop := errors.New("stopped")
	appendJournalHook = func() error { return stop }
	t.Cleanup(func() { appendJournalHook = nil })
	_, err := seller.store.SealJournal(ctx, dealID,
		JournalStep{Kind: "open", Build: func(StepFacts) (StepParts, error) { return StepParts{Local: json.RawMessage(`{"open":true}`)}, nil }},
		JournalStep{Kind: "intent", Build: func(StepFacts) (StepParts, error) {
			return StepParts{Record: &JournalRecord{RecordType: xdealv1.TypeIntent, Body: map[string]any{"verbatim_commitment": nonceOf("v"), "allowed": []any{"pay"}}}}, nil
		}})
	require.ErrorIs(t, err, stop)
	steps, err := seller.store.Steps(ctx, dealID)
	require.NoError(t, err)
	assert.Empty(t, steps, "the opening and the intent both withdrawn")
}
