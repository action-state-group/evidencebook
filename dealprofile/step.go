// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"fmt"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
)

// Each step of a deal is sealed as one Capsule (PROFILE.md §1) whose payload
// is the step's record: action_id "<deal_id>/<seq>", chained to the step
// before it, with the deal id as its top-level deal_id member. On a book it
// also carries an evidence record header of type StepRecordType whose
// subject_ref is the action_id, whose one link cites the parent, and whose
// field commitments are the step's local event and the deal's closed money
// terms. The producer decides what the step says; StepCapsule only lays it
// out.

// Relations a step's chain may name. A step follows the one before it
// (ordering only); a record sealed after a terminal close confirms that
// close.
const (
	RelationFollows  emit.ChainRelation = "follows"
	RelationConfirms emit.ChainRelation = "confirms"
)

// Decide marks a step as a decide Capsule: an action taken under a sealed
// approval whose action has an effect type. The producer decides that, and
// how the action was disposed and how recoverable it is; the effect is
// always the agent's own report that it acted, with no response in hand.
type Decide struct {
	Disposition     emit.Disposition
	EffectType      string
	Irreversibility emit.IrreversibilityClass
}

// StepInput is what a step's Capsule is made from.
type StepInput struct {
	DealID   string
	Seq      int64
	Operator string
	// Developer is the producer's own developer name (the capsulectl deal
	// plugin names "capsulectl-deal").
	Developer string
	// At is the step's seal time, the record's block "at"; it is taken to
	// the second, as the block states it.
	At time.Time
	// Parent is the Capsule the step chains to: the step before it
	// (RelationFollows), or the close it confirms (RelationConfirms); nil
	// for the first step.
	Parent *emit.Chain
	// Settlement is a settlement leg the step carries, as built and stored
	// with the step; it rides as the Capsule's top-level settlement member.
	Settlement json.RawMessage
	// Decide is set for a decide step, nil for fyi.
	Decide *Decide
	// Local, Amount and Currency are the values the header's field
	// commitments commit: the producer's local event (opaque here), and the
	// deal's closed amount and currency ("" when none).
	Local, Amount, Currency string
}

// EpistemicProducerClaim is the epistemic type of every step and report
// record: the producer's own claim.
const EpistemicProducerClaim = "producer_claim"

// StepHeader is the evidence record header fields a step carries on a book:
// all but what the book assigns. A step names no correlation.
type StepHeader struct {
	RecordType    string
	EpistemicType string
	// CommittedAt is the step's seal time, the record's committed_at.
	CommittedAt time.Time
	SubjectRef  string
	// Cites is the parent Capsule, which the header's one cites link
	// targets, when the step has one.
	Cites  []string
	Fields map[string]string
}

// StepCapsule is a step's Capsule members and its record header fields.
// The producer seals the Capsule with the record's payload as JSON
// (json.RawMessage) and its own key.
func StepCapsule(in StepInput) (emit.Input, StepHeader, error) {
	if in.DealID == "" || in.Seq < 1 {
		return emit.Input{}, StepHeader{}, fmt.Errorf("dealprofile: a step needs a deal id and a seq >= 1")
	}
	if in.Developer == "" {
		return emit.Input{}, StepHeader{}, fmt.Errorf("dealprofile: a step needs the producer's developer name")
	}
	if p := in.Parent; p != nil && p.Relation != RelationFollows && p.Relation != RelationConfirms {
		return emit.Input{}, StepHeader{}, fmt.Errorf("dealprofile: chain relation %q is not follows or confirms", p.Relation)
	}
	at := in.At.UTC().Truncate(time.Second)
	c := emit.Input{
		ActionID: fmt.Sprintf("%s/%d", in.DealID, in.Seq), ActionType: emit.ActionTypeFYI,
		Operator: in.Operator, Developer: in.Developer, Timestamp: at,
	}
	if len(in.Settlement) > 0 {
		c.Extensions = []emit.Extension{{Name: "settlement", Value: in.Settlement}}
	}
	id, err := json.Marshal(in.DealID)
	if err != nil {
		return emit.Input{}, StepHeader{}, err
	}
	c.Extensions = append(c.Extensions, emit.Extension{Name: DealIDMember, Value: id})
	if d := in.Decide; d != nil {
		disposition := d.Disposition
		c.ActionType = emit.ActionTypeDecide
		c.Disposition = &disposition
		c.Effect = &emit.Effect{
			Type: d.EffectType, Status: emit.EffectDispatched, IrreversibilityClass: d.Irreversibility,
			EffectAttestation: emit.AttestationRuntimeClaimed,
		}
	}
	h := StepHeader{
		RecordType: StepRecordType, EpistemicType: EpistemicProducerClaim, CommittedAt: at, SubjectRef: c.ActionID,
		Fields: map[string]string{LocalField: in.Local, AmountField: in.Amount, CurrencyField: in.Currency},
	}
	if p := in.Parent; p != nil {
		chain := *p
		c.Chain = &chain
		h.Cites = []string{p.ParentCapsuleID}
	}
	return c, h, nil
}
