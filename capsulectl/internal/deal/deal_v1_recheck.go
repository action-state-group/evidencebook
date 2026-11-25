// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// recheckAtSend re-runs a pay check's evaluation at `deal settle --sent`
// against what has happened since it was sealed (evidencebook#718,
// write-path §13 item 5, Sherlock2's condition 1). Two checks that both
// passed before either payment left each saw the limits without the other;
// the send is where the money leaves, so the check is asked again there.
//
// The deal's own evaluation runs on the events the check read, its payee
// companion included (it changes only when the counterparty memory or the
// pinned materiality did: a pay check's evaluation reads no acts). The
// payments made since are counted by the rules checker, whose history holds
// every v1 act, each send counted from the moment it was sent, this deal's
// and every other's. A send is refused, and nothing is sealed, when the
// evaluation answers worse or with a difference the check did not have, or
// when the configured rules checker cannot answer now, answers worse, or
// fails a rule the check did not fail: the user runs deal check again.
//
// A check that read the counterparty's History (`deal check --history`) is
// asked again on the same History: h must be the documents and keys whose
// digests its verdict sealed (capsule-cli#327 §5.4), so the re-run is a
// re-run of the bound input, and a deny History alone causes still asks.
func (s *dealSession) recheckAtSend(ctx context.Context, dealID string, events []sealedEvent, checkDigest string, h *receivedHistory) error {
	// The check ran as a v1 check does (useV1Check): the deal's key and the
	// v1 records the rules checker reads.
	s.dkey = dealprofile.DealKey(s.secret, dealID)
	s.v1 = true
	// The journal check record is the snapshot step's; its verdict is the
	// check step that names the snapshot.
	si := slices.IndexFunc(events, func(se sealedEvent) bool { return se.Digest == checkDigest && se.Event.Snapshot != nil })
	if si < 0 {
		return fmt.Errorf("%w: check %s has no local step", cli.ErrConflict, checkDigest)
	}
	ci := slices.IndexFunc(events, func(se sealedEvent) bool {
		return se.Event.Check != nil && se.Event.Check.Snapshot == events[si].CapsuleID
	})
	if ci < 0 {
		return fmt.Errorf("%w: check %s has no verdict step", cli.ErrConflict, checkDigest)
	}
	orig := events[ci].Event.Check
	var sealed *xdealv1.ReceivedHistoryDigests
	if orig.Rules != nil {
		sealed = orig.Rules.ReceivedHistory
	}
	switch {
	case sealed != nil && h == nil:
		return cli.InputError("check " + checkDigest + " read the counterparty's History: give the same --history and --counterparty-key to deal settle --sent, so the payment is checked against what the check read. Nothing was sent")
	case sealed == nil && h != nil:
		return cli.InputError("--history goes with a check that read the counterparty's History; check " + checkDigest + " read none")
	}
	// The events the check read: up to its snapshot and the payee companion
	// sealed after it, before the verdict.
	base := append([]sealedEvent{}, events[:ci]...)
	state, err := foldDeal(base)
	if err != nil {
		return err
	}
	if state.memory, err = s.counterpartyMemory(ctx, dealID); err != nil {
		return err
	}
	if state.materiality, err = cli.PinnedMateriality(s.p); err != nil {
		return err
	}
	snap := *events[si].Event.Snapshot
	now := evaluateDeal(state, snap)
	var added []string
	for _, d := range now.Differences {
		if !slices.ContainsFunc(orig.Differences, func(o dealDifference) bool { return o.Question == d.Question && o.Rule == d.Rule }) {
			added = append(added, d.Text)
		}
	}
	if checkRank(now.Verdict) > checkRank(orig.Verdict) || len(added) > 0 {
		// What the deal's own evaluation reads changed (the counterparty
		// memory, the pinned materiality), not what was paid.
		return recheckRefusal(dealID, "your check no longer passes now", now.Verdict, added)
	}
	if len(s.p.RulesChecker.Command) == 0 {
		return nil
	}
	input, history, err := s.rulesInput(ctx, events[si].CapsuleID, base)
	if err != nil {
		return err
	}
	var rules dealRules
	if h != nil {
		// The same History the verdict bound: the same documents, in the
		// same order, and the same member (keys, statuses and documents).
		_, inputSHA256, err := withReceivedHistory(input, h)
		if err != nil {
			return err
		}
		if !slices.Equal(h.historySHA256(), sealed.HistorySHA256) || inputSHA256 != sealed.InputSHA256 {
			return cli.InputError("--history is not the History check " + checkDigest + " read: its digests differ from the ones its verdict sealed. Nothing was sent")
		}
		if rules, err = runRulesGivenHistory(ctx, s.p, dealRole(events), input, history, h); err != nil {
			return err
		}
	} else {
		rules = runRulesChecker(ctx, s.p, dealRole(events), input, history)
	}
	checked := orig.Rules != nil && orig.Rules.Status == "evaluated"
	if rules.Status != "evaluated" {
		if !checked {
			// The user approved paying with the rules unchecked, and they are
			// still unchecked: the send is what was approved.
			return nil
		}
		return cli.InputError("your rules checker could not answer (" + rules.Reason + "), so the payment cannot be checked against what was paid since. Nothing was sent: run deal settle --sent again once the checker answers")
	}
	if refuse, verdict, why := rulesChange(orig.Rules, rules); refuse {
		return recheckRefusal(dealID, paidSince, verdict, why)
	}
	return nil
}

// rulesChange compares the rules a check's user approved with the rules'
// answer now, both evaluated or orig not (then nothing was approved). A rule
// the ruleset declares but does not measure is dropped from both at the
// start (isDeclaredRule: it never moves the verdict or reaches the card, so
// it was never shown, approved or failing), and no comparison below sees
// one. It refuses a deny; a verdict ranked above the check's (allow <
// escalate, not_evaluable < deny); and any rule failing or not evaluable now
// that the check did not have in that same state, by its id. why are those
// rules' words.
func rulesChange(orig *dealRules, now dealRules) (refuse bool, verdict string, why []string) {
	approved := map[[2]string]bool{}
	origVerdict := ""
	// A deny History alone caused asks, then and now (D1 condition 1).
	nowVerdict := effectiveRulesVerdict(&now)
	if orig != nil && orig.Status == "evaluated" {
		origVerdict = effectiveRulesVerdict(orig)
		for _, f := range substantiveFindings(orig.Findings) {
			approved[[2]string{f.ID, f.Verdict}] = true
		}
	}
	var failing []string
	for _, f := range substantiveFindings(now.Findings) {
		if f.Verdict != "fail" && f.Verdict != "not_evaluable" {
			continue
		}
		if nowVerdict != "deny" && approved[[2]string{f.ID, f.Verdict}] {
			continue
		}
		text := f.ID
		if f.Reason != "" {
			text += ": " + f.Reason
		}
		failing = append(failing, text)
	}
	switch {
	case nowVerdict == "deny":
		return true, "denied by your rules", failing
	case rulesRank(nowVerdict) > rulesRank(origVerdict) || len(failing) > 0:
		return true, "rules: " + nowVerdict, failing
	}
	return false, "", nil
}

// substantiveFindings are findings less the declared, not measured ones.
func substantiveFindings(fs []dealRulesFinding) []dealRulesFinding {
	var out []dealRulesFinding
	for _, f := range fs {
		if !isDeclaredRule(f) {
			out = append(out, f)
		}
	}
	return out
}

// checkRank orders a check's verdicts from the most permissive.
func checkRank(v string) int {
	switch v {
	case "pass", "":
		return 0
	case "pause":
		return 1
	}
	return 2
}

// rulesRank orders the rules checker's verdicts (external-check-result/v0:
// allow; escalate and not_evaluable, which both ask; deny) from the most
// permissive; an unknown one ranks as deny. A check whose rules were not
// evaluated ranks as allow, so any verdict the re-run gives is compared by
// its findings.
func rulesRank(v string) int {
	switch v {
	case "allow", "":
		return 0
	case "escalate", "not_evaluable":
		return 1
	}
	return 2
}

// paidSince leads a refusal the rules give over the history, which holds the
// payments made since the check.
const paidSince = "your check no longer passes: payments made since it was sealed change the result"

func recheckRefusal(dealID, lead, verdict string, why []string) error {
	msg := lead + " (now " + verdict + ")"
	if len(why) > 0 {
		msg += ": " + strings.Join(why, "; ")
	}
	return cli.InputError(msg + ". Nothing was sent. Run deal check --deal " + dealID + " again")
}
