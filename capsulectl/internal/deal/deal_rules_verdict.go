package deal

import (
	"context"
	"slices"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// The party's own overall rules verdict (evidencebook-deal#191, Steven's
// ruling, Buzz 8b6aaecf): one word, sealed as a rules_verdict record in the
// party's own book after the deal's final close, bound to the deal id (the
// record's header) and the digest of the pack the party's rules ran under.
//
// This repository derives nothing: the word is the party's own, given by the
// host that ran the party's rules over the finished deal. It is never re-run,
// counted or combined here, and it is never read onto a History card.
//
// A counterparty copy carries the record only when the holder's share policy
// says so (a node shares, a person does not, by default); otherwise the record
// is withheld (listed, contents withheld), and a reader that finds no readable verdict says "not shared".

// dealRulesVerdict is a rules_verdict step: the word and the pack's
// definition digest. The pack is named by its digest alone, always: a name
// (even one shaped like a published pack's) could be a private pack's, and
// naming a published pack waits for a verifiable signal, such as the digest
// matching a published pack index (Steven's condition 2, coordinator decision
// 2026-11-13).
type dealRulesVerdict struct {
	Word       string `json:"word"`
	PackDigest string `json:"pack_digest"`
}

// rulesVerdictWords are the words a rules verdict may be, the profile's
// rules_verdict.word set.
var rulesVerdictWords = []string{"met", "not_met", "unknown", "out_of_scope"}

// rulesVerdictPack is the pack a rules verdict is bound to: the definition
// digest the party's own checks ran under. With no digest given, the newest
// evaluated check's; a digest given must be one a check ran under.
func rulesVerdictPack(events []sealedEvent, digest string) (dealRulesVerdict, error) {
	var found dealRulesVerdict
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i].Event
		if e.Kind != "check" || e.Check == nil || e.Check.Rules == nil {
			continue
		}
		r := e.Check.Rules
		if r.Status != "evaluated" || r.DefinitionDigest == "" {
			continue
		}
		if digest == "" || r.DefinitionDigest == digest {
			found = dealRulesVerdict{PackDigest: r.DefinitionDigest}
			break
		}
	}
	if found.PackDigest == "" {
		if digest != "" {
			return found, cli.InputError("--pack-digest is not the definition digest of any check of this deal: a rules verdict is bound to a pack the deal's own checks ran under")
		}
		return found, cli.InputError("this deal has no check that ran a rules pack, so a rules verdict has no pack digest to be bound to")
	}
	return found, nil
}

func dealRulesVerdictCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "rules-verdict", Short: "The party's own overall rules verdict for a finished deal: seal it, and choose whether a counterparty copy carries it", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	cmd.AddCommand(rulesVerdictSealCommand(), rulesVerdictPolicyCommand())
	return cmd
}

func rulesVerdictSealCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "seal", Short: "Seal the party's own overall rules word for a closed deal, bound to the pack digest its rules ran under", Long: `Seal one rules_verdict record in your own book, after the deal's final close:
the one overall word (met, not_met, unknown or out_of_scope) your own rules
gave for the finished deal, bound to the deal and to the digest of the pack
those rules ran under (--pack-digest, default the newest check's). The pack is
named by its digest alone, never by a name.

The word is yours: nothing here runs, counts or combines rules. A deal takes
one. Whether a counterparty copy carries it is the holder's share policy
(` + "`deal rules-verdict policy`" + `).`, Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		word, _ := c.Flags().GetString("word")
		digest, _ := c.Flags().GetString("pack-digest")
		if !slices.Contains(rulesVerdictWords, word) {
			return cli.InputError("--word is one of met, not_met, unknown or out_of_scope")
		}
		return runDeal(c, true, func(ctx context.Context, s *dealSession, dealID string, events []sealedEvent) error {
			closeAt := finalClose(events)
			if closeAt < 0 {
				return cli.InputError("a rules verdict is for a finished deal: close it first (`deal close`)")
			}
			for _, se := range events {
				if se.Event.Kind == "rules_verdict" {
					return cli.InputError("this deal already has its rules verdict: a deal takes one, and it is never re-run")
				}
			}
			rv, err := rulesVerdictPack(events, digest)
			if err != nil {
				return err
			}
			rv.Word = word
			se, err := s.seal(ctx, dealID, events, dealEvent{Kind: "rules_verdict", RulesVerdict: &rv, Confirms: events[closeAt].CapsuleID})
			if err != nil {
				return err
			}
			out := stepOutput(dealID, se)
			out["word"], out["pack_digest"] = rv.Word, rv.PackDigest
			return cli.Output(c, out)
		})
	}}
	cmd.Flags().String("deal", "", "The deal id")
	cmd.Flags().String("word", "", "The overall word: met, not_met, unknown or out_of_scope (required)")
	cmd.Flags().String("pack-digest", "", "The definition digest of the pack your rules ran under (default: the newest check's)")
	return cmd
}

func rulesVerdictPolicyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "policy", Short: "Choose whether a counterparty copy carries your rules verdict; with no flag, show the choice", Long: `A node's default is to share its rules verdict in a counterparty copy
(` + "`deal report --share counterparty`, `deal respond`" + `) and a person's is not to, as for the
History card; a profile that has not declared its holder kind
(` + "`deal standard-history declare`" + `) shares none. When it is not shared the copy
lists the record as withheld, contents withheld, and says nothing of what it is: the other side's receipt
has no verdict to read and says "not shared".`, Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		share, _ := c.Flags().GetBool("share")
		noShare, _ := c.Flags().GetBool("no-share")
		if share && noShare {
			return cli.InputError("--share and --no-share are opposites")
		}
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		s, declared, err := readSHSettings(p)
		if err != nil {
			return err
		}
		if (share || noShare) && !declared {
			return errNoHolderKind()
		}
		switch {
		case share:
			s.ShareRulesVerdict = new(bool)
			*s.ShareRulesVerdict = true
		case noShare:
			s.ShareRulesVerdict = new(bool)
		}
		if share || noShare {
			if err = writeSHSettings(p, s); err != nil {
				return err
			}
		}
		out := map[string]any{"holder_kind": s.HolderKind, "shares": declared && s.sharesRulesVerdict()}
		if !declared {
			out["holder_kind"] = ""
		}
		return cli.Output(c, out)
	}}
	cmd.Flags().Bool("share", false, "Carry the rules verdict in a counterparty copy (a person's default is not to)")
	cmd.Flags().Bool("no-share", false, "Do not carry it (a node's default is to)")
	return cmd
}

// sharesRulesVerdict is whether this profile's counterparty copy carries its
// rules verdict now.
func (s *dealSession) sharesRulesVerdict() (bool, error) {
	settings, declared, err := readSHSettings(s.p)
	if err != nil {
		return false, err
	}
	return declared && settings.sharesRulesVerdict(), nil
}
