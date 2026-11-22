// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
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
func (s *dealSession) recheckAtSend(ctx context.Context, dealID string, events []sealedEvent, checkDigest string) error {
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
	rules := runRulesChecker(ctx, s.p, dealRole(events), input, history)
	checked := orig.Rules != nil && orig.Rules.Status == "evaluated"
	if rules.Status != "evaluated" {
		if !checked {
			// The user approved paying with the rules unchecked, and they are
			// still unchecked: the send is what was approved.
			return nil
		}
		return cli.InputError("your rules checker could not answer (" + rules.Reason + "), so the payment cannot be checked against what was paid since. Nothing was sent: run deal settle --sent again once the checker answers")
	}
	if rules.Verdict == "deny" {
		return recheckRefusal(dealID, paidSince, "denied by your rules", failingTexts(&rules, nil))
	}
	// A rule the user approved is one the check's rules failed or could not
	// evaluate, by its id and that state; any rule failing now that the
	// check did not fail is new.
	approved := map[[2]string]bool{}
	if checked {
		for _, f := range orig.Rules.Findings {
			approved[[2]string{f.ID, f.Verdict}] = true
		}
	}
	origRules := ""
	if checked {
		origRules = orig.Rules.Verdict
	}
	newly := failingTexts(&rules, approved)
	if rulesRank(rules.Verdict) > rulesRank(origRules) || len(newly) > 0 {
		return recheckRefusal(dealID, paidSince, "rules: "+rules.Verdict, newly)
	}
	return nil
}

// failingTexts are the words of each rule the rules failed or could not
// evaluate that is not in approved (by id and state), as the card words them.
func failingTexts(r *dealRules, approved map[[2]string]bool) []string {
	var out []string
	for _, f := range r.Findings {
		// A rule the ruleset declares but does not measure never changes
		// the verdict or reaches the card (isDeclaredRule): it is no failure.
		if (f.Verdict != "fail" && f.Verdict != "not_evaluable") || isDeclaredRule(f) {
			continue
		}
		if approved[[2]string{f.ID, f.Verdict}] {
			continue
		}
		text := f.ID
		if f.Reason != "" {
			text += ": " + f.Reason
		}
		out = append(out, text)
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
