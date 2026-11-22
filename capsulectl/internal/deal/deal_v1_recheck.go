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
// the send is where the money leaves, so the check is asked again there, with
// every act counted since: this deal's other sends and payments, and, through
// the rules checker's history, every v1 deal's. The state is the deal's as
// the check saw it (its events up to the checked snapshot) plus every act
// sealed since, and the snapshot is the one the check evaluated. A send the
// check would now answer worse than it did (a later verdict, a new
// difference, or a rules verdict that no longer passes) is refused, and
// nothing is sealed: the user runs deal check again.
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
	base := append([]sealedEvent{}, events[:si+1]...)
	for _, se := range events[ci+1:] {
		if se.Event.Kind == "act" && se.Event.Act != nil {
			base = append(base, se)
		}
	}
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
	if verdictRank(now.Verdict) > verdictRank(orig.Verdict) || len(added) > 0 {
		return recheckRefusal(dealID, now.Verdict, added)
	}
	if len(s.p.RulesChecker.Command) == 0 || orig.Rules == nil || orig.Rules.Status != "evaluated" {
		return nil
	}
	input, history, err := s.rulesInput(ctx, events[si].CapsuleID, base)
	if err != nil {
		return err
	}
	rules := runRulesChecker(ctx, s.p, dealRole(events), input, history)
	if rules.Status == "evaluated" && verdictRank(rules.Verdict) > verdictRank(orig.Rules.Verdict) {
		var why []string
		for _, f := range rulesDifferences(&rules) {
			why = append(why, f.Text)
		}
		return recheckRefusal(dealID, "rules: "+rules.Verdict, why)
	}
	return nil
}

// verdictRank orders verdicts from the most permissive.
func verdictRank(v string) int {
	switch v {
	case "pass", "allow", "":
		return 0
	case "pause", "ask":
		return 1
	}
	return 2
}

func recheckRefusal(dealID, verdict string, why []string) error {
	msg := "the check no longer holds against what was paid since it was sealed (now " + verdict + ")"
	if len(why) > 0 {
		msg += ": " + strings.Join(why, "; ")
	}
	return cli.InputError(msg + ". Nothing was sent: run deal check --deal " + dealID + " again")
}
