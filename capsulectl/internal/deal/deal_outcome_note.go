package deal

import (
	"context"
	"slices"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// TEMPORARY (evidencebook#462; evidencebook#461's computed row status
// supersedes it): the book owner's own note that the deal did not hold, sealed
// as an outcome_note record in the owner's own book after the deal's final
// close, which it confirms. Each reason is the owner's own failure; a buyer whose
// seller failed does not write one, since the card counts it on the writer's card. Ethan and Steven's phone agreement (relayed by Ethan, evidencebook#462),
// so a History card can count a deal did_not_hold. The note is the holder's:
// nothing here derives it, and a counterparty's record is never appended to
// the book. It is kept for the holder only, in no shared copy.

// dealOutcomeNote is an outcome_note step: why the outcome did not hold.
type dealOutcomeNote struct {
	Reason string `json:"reason"`
}

// outcomeNoteReasons are the profile's outcome_note.reason set.
var outcomeNoteReasons = []string{"dispute_lost", "settled_against", "not_delivered", "other"}

func dealOutcomeNoteCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "outcome-note", Short: "TEMPORARY: note that a finished deal did not hold, so a History card counts it did_not_hold", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	cmd.AddCommand(outcomeNoteSealCommand())
	return cmd
}

func outcomeNoteSealCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "seal", Short: "Seal your own note that a closed deal did not hold", Long: `TEMPORARY (evidencebook#462, until the target design's computed row status):
seal one outcome_note record in your own book, after the deal's final close, saying
the deal did not hold and the failure is your own (--reason: dispute_lost, you lost
a dispute; settled_against, a dispute settled against you; not_delivered, you did not
deliver; other, any other way it did not hold for you). A buyer whose seller failed
does not write it: the card counts it on the writer's own card. The reason is your own
statement, not checked against any ruling. The History card then counts the deal
did_not_hold. The note is yours alone: it is never in a shared copy, and a
counterparty never writes one into your book. A deal takes one.`, Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		reason, _ := c.Flags().GetString("reason")
		if !slices.Contains(outcomeNoteReasons, reason) {
			return cli.InputError("--reason is one of dispute_lost, settled_against, not_delivered or other")
		}
		return runDeal(c, true, func(ctx context.Context, s *dealSession, dealID string, events []sealedEvent) error {
			closeAt := finalClose(events)
			if closeAt < 0 {
				return cli.InputError("an outcome note is for a finished deal: close it first (`deal close`)")
			}
			for _, se := range events {
				if se.Event.Kind == "outcome_note" {
					return cli.InputError("this deal already has its outcome note: a deal takes one")
				}
			}
			se, err := s.seal(ctx, dealID, events, dealEvent{Kind: "outcome_note", OutcomeNote: &dealOutcomeNote{Reason: reason}, Confirms: events[closeAt].CapsuleID})
			if err != nil {
				return err
			}
			out := stepOutput(dealID, se)
			out["outcome"], out["reason"] = "did_not_hold", reason
			return cli.Output(c, out)
		})
	}}
	cmd.Flags().String("deal", "", "The deal id")
	cmd.Flags().String("reason", "", "Why it did not hold: dispute_lost, settled_against, not_delivered or other (required)")
	return cmd
}
