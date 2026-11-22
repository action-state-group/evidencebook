package deal

import (
	"context"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// The seller's verdict on its agent's claim is its own record, sealed right
// after the claim, so a copy can disclose the claim's words (a buyer checks
// what it was told against them) and withhold the verdict apart from them
// (evidencebook#607, PROFILE.md rule 12; evidencebook-deal#244). A claim
// sealed before this carries its verdict on itself and re-derives so, unchanged.
const dealClaimShapeVersion = "1"

// dealClaimAuthority is a claim_authority step: which claim it judges (the
// claim's capsule id) and the verdict the claim's own step already holds,
// copied so the step encodes from itself.
type dealClaimAuthority struct {
	Claim        string `json:"claim"`
	AuthorizedBy string `json:"authorized_by,omitempty"`
	Unchecked    bool   `json:"unchecked,omitempty"`
	Rule         string `json:"rule,omitempty"`
}

// line is the step in plain words, for the user's own trail.
func (a *dealClaimAuthority) line() string {
	if a.Unchecked {
		return "your agent's claim was judged: nothing you approved covered it"
	}
	return "your agent's claim was judged: covered by what you approved"
}

// claimNeedsAuthority says whether a claim step is in the new shape and has
// no claim_authority step after it yet: its verdict is pending, so it
// authorizes nothing until the pair is whole.
func claimNeedsAuthority(events []sealedEvent) (sealedEvent, bool) {
	if len(events) == 0 {
		return sealedEvent{}, false
	}
	last := events[len(events)-1]
	if last.Event.Kind == "claim" && last.Event.ClaimShape != "" {
		return last, true
	}
	return sealedEvent{}, false
}

// sealClaimAuthority seals the claim_authority step of the claim step claim,
// encoded from the verdict the claim's own step holds.
func (s *dealSession) sealClaimAuthority(ctx context.Context, dealID string, events []sealedEvent, claim sealedEvent) (sealedEvent, error) {
	cl := claim.Event.Claim
	return s.seal(ctx, dealID, events, dealEvent{Kind: "claim_authority", ClaimAuthority: &dealClaimAuthority{
		Claim: claim.CapsuleID, AuthorizedBy: cl.AuthorizedBy, Unchecked: cl.Unchecked, Rule: cl.Rule,
	}})
}

// finishClaimPair seals the missing claim_authority of a trailing new-shape
// claim, before any other step is sealed, and returns the steps with it. A
// producer that stopped between the two seals thereby leaves nothing a later
// step can depend on: the verdict is the one the claim was noted with, read
// from its own step, never recomputed.
func (s *dealSession) finishClaimPair(ctx context.Context, dealID string, events []sealedEvent) ([]sealedEvent, error) {
	claim, ok := claimNeedsAuthority(events)
	if !ok {
		return events, nil
	}
	se, err := s.sealClaimAuthority(ctx, dealID, events, claim)
	if err != nil {
		return nil, err
	}
	return append(events, se), nil
}

// claimAuthorityBody is a claim_authority record's body and refs: exactly one
// of an authority basis with the evaluation it rests on, or unchecked with the
// rule that failed, and an about ref to the claim.
func claimAuthorityBody(ev dealEvent, events []sealedEvent, block map[string]interface{}, digestOf func(string) string) (map[string]interface{}, error) {
	a := ev.ClaimAuthority
	body := map[string]interface{}{}
	refs := []interface{}{relRef("about", digestOf(a.Claim))}
	if a.AuthorizedBy != "" {
		if err := addAuthority(body, events, a.AuthorizedBy, nil); err != nil {
			return nil, err
		}
		refs = append(refs, relRef("authorized_by", digestOf(a.AuthorizedBy)))
	} else if a.Unchecked {
		body["unchecked"] = true
		body["rule"] = a.Rule
	} else {
		return nil, cli.InputError("a claim's verdict is authorized or unchecked")
	}
	block["refs"] = refs
	return body, nil
}

// finishPendingClaim runs finishClaimPair for the commands that seal a step
// or compute a rule input from the deal (note, check, close and the
// `outcome-note seal` and `rules-verdict seal` steps; `deal tick` finishes it before it
// seals a deferred email): a command that only reads leaves the pending
// claim as it is, pending.
func (s *dealSession) finishPendingClaim(ctx context.Context, c *cobra.Command, dealID string, events []sealedEvent) ([]sealedEvent, error) {
	if _, pending := claimNeedsAuthority(events); !pending {
		return events, nil
	}
	switch c.Name() {
	case "note", "check", "close", "seal":
	default:
		return events, nil
	}
	return s.finishWritable(ctx, dealID, events)
}

// finishWritable is finishClaimPair for a session that can seal: a deal opened
// read-only keeps the claim pending.
func (s *dealSession) finishWritable(ctx context.Context, dealID string, events []sealedEvent) ([]sealedEvent, error) {
	if s.onBook && s.book == nil {
		return events, nil
	}
	return s.finishClaimPair(ctx, dealID, events)
}
