// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"fmt"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

// DerivationStandardHistory is the registered token standard_history/1
// (agent-action-capsule spec/standard-history-v0.md, cited as SH): a card,
// as of one anchor, that counts the holder's deals in the window its policy
// sets. It is recipient-free: every requester answered gets the same bytes.
const DerivationStandardHistory = standardhistory.Derivation

// StandardHistorySource is what a host supplies for Respond to answer
// standard_history/1 over the full_history subject. With it set, Respond
// answers the card to any authenticated requester, whatever the share
// policy's tier; a stranger still has only the public questions answered
// (checkpoints and history_card/1). Without it, standard_history/1 is
// derivation_unsupported.
//
// The book defines neither a disclosure policy format nor how its records
// make deals: the host reads both (SH §5.1, SH-2).
type StandardHistorySource struct {
	// HolderKind is "person" or "node": the kind of holder the book's key
	// answers for, as the holder's own controller declaration states it.
	HolderKind string
	// Inputs returns the two members of the policy in force at the anchor
	// (its digest and window, SH §5.1) and one deal entry per deal the
	// stream holds at the anchor (SH §5.2), read from records: the book's
	// servable records the anchor covers, in log order. Respond calls it
	// without the book's lock, and may call it for several requests at
	// once. Its result must depend on its arguments only, so the card does
	// not depend on who asks (ER §5).
	Inputs func(ctx context.Context, anchor Checkpoint, records []Record) (standardhistory.Policy, []standardhistory.Deal, error)
}

func (s *StandardHistorySource) validate() error {
	if s == nil {
		return nil
	}
	if !slices.Contains([]string{"person", "node"}, s.HolderKind) {
		return fmt.Errorf("%w: standard history holder kind %q is neither person nor node", ErrInvalid, s.HolderKind)
	}
	if s.Inputs == nil {
		return fmt.Errorf("%w: standard history needs its inputs", ErrInvalid)
	}
	return nil
}

// standardHistoryCard builds the card as of anchor from the host's
// inputs over leaves, the full_history subject's servable records under
// the anchor. The card is the whole artifact (SH §3) and carries no
// verification material: anchor_inclusion is NOT_CHECKED for a derivation
// (IP P-13). DeriveStandardHistory checks it against its own inputs.
func (b *Book) standardHistoryCard(ctx context.Context, view *bookView, anchor Checkpoint, leaves []uint64) (builtArtifact, error) {
	source := b.standardHistory
	records := make([]Record, len(leaves))
	for i, leaf := range leaves {
		records[i] = view.log[leaf].detached()
	}
	policy, deals, err := source.Inputs(ctx, anchor, records)
	if err != nil {
		return builtArtifact{}, err
	}
	asOf := standardhistory.AsOf{
		Anchor:              anchorDigest(anchor),
		AnchorTime:          evidencerequest.ProfileTime(anchor.IssuedAt),
		FirstCheckpointTime: evidencerequest.ProfileTime(view.checkpoints[0].IssuedAt),
	}
	card, err := DeriveStandardHistory(source.HolderKind, asOf, policy, deals)
	if err != nil {
		return builtArtifact{}, err
	}
	return builtArtifact{artifact: card}, nil
}

// DeriveStandardHistory is the standard_history/1 card (SH §4) as of asOf,
// in its JCS octets: the card a Book answers and the card a host derives
// over a shelf as of a cadence tick (Cadence.StandardHistoryAnchor). Before
// it returns, the card is checked against the very inputs it was derived
// from (SH §6), so inputs that make no standard card are ErrInvalid, never
// a card.
func DeriveStandardHistory(holderKind string, asOf standardhistory.AsOf, policy standardhistory.Policy, deals []standardhistory.Deal) ([]byte, error) {
	if deals == nil {
		deals = []standardhistory.Deal{}
	}
	controller, ok := map[string]string{"person": "person_declared", "node": "node_declared"}[holderKind]
	if !ok {
		return nil, fmt.Errorf("%w: standard history holder kind %q is neither person nor node", ErrInvalid, holderKind)
	}
	derived, err := standardhistory.Derive(holderKind, controller, asOf, policy, deals)
	if err != nil {
		return nil, fmt.Errorf("%w: standard history: %v", ErrInvalid, err)
	}
	decoded, err := evidencerequest.DecodeJSON(derived)
	if err != nil {
		return nil, err
	}
	card, err := evidencerequest.EncodeJCS(decoded)
	if err != nil {
		return nil, err
	}
	properties, err := standardhistory.Check(card, &policy, &asOf, deals)
	if err != nil {
		return nil, fmt.Errorf("%w: standard history: %v", ErrInvalid, err)
	}
	for _, property := range standardhistory.Properties {
		if properties[property] != standardhistory.Pass {
			return nil, fmt.Errorf("%w: standard history: the card fails %s against its own inputs", ErrInvalid, property)
		}
	}
	return card, nil
}
