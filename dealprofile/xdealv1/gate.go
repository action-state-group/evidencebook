// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"fmt"
)

// The first-act gate (Steven, u610; design §7.2): a holder's first perform or
// payer settle of a deal is refused until the counterparty's stamp proof
// covers the counterparty's agreement (CanAct), on both sides, whether or not
// the agreement names a witness. A payee leg and a statement are never gated,
// nor is any act after the first. A deal whose counterparty runs no layer has
// no counterparty agreement or proof to wait for: its first act runs
// unguarded, and the holder records that it did, never passing it silently
// (Sherlock2's second condition on u610). The holder may refuse it instead.

// Why GateAct refused or marked an act (ActGate.Reason).
const (
	GateNoProof = "no_counterparty_proof" // no held stamp proof covers the counterparty's agreement
	GateNoLayer = "counterparty_no_layer" // the counterparty runs no layer: the act runs unguarded
)

// ActGateInput is what GateAct reads. Own is the holder's deal book before
// the act, in book order, opening with the holder's agreement. Act is the act
// to seal. CounterpartAgreement and CommitmentShelfLog are CanAct's.
type ActGateInput struct {
	Own                  []Record
	Act                  Record
	CounterpartAgreement string
	CommitmentShelfLog   string
}

// ActGate is GateAct's answer. Gated says the act is a first perform or payer
// settle. Allowed says it may be sealed. Unguarded says it is allowed only
// because the counterparty runs no layer, which the holder records. Reason
// names why an act was refused or is unguarded.
type ActGate struct {
	Gated     bool   `json:"gated"`
	Allowed   bool   `json:"allowed"`
	Unguarded bool   `json:"unguarded,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// GateAct applies the first-act gate (u610) to an act the holder is about to
// seal. It refuses input that is not the holder's book of the act's deal:
// an empty book, one that does not open with an agreement, or an act or
// record of another deal.
func (p *ReceiveProfile) GateAct(ctx context.Context, in ActGateInput) (ActGate, error) {
	if len(in.Own) == 0 {
		return ActGate{}, fmt.Errorf("xdealv1: gate: the holder's book is empty")
	}
	ag, ok := asAgree(in.Own[0].Body)
	if !ok || ag.Kind != KindAgreement {
		return ActGate{}, fmt.Errorf("xdealv1: gate: the holder's book does not open with its agreement")
	}
	deal := in.Own[0].Block.DealID
	if in.Act.Block.DealID != deal {
		return ActGate{}, fmt.Errorf("xdealv1: gate: the act is of deal %q, the book of %q", in.Act.Block.DealID, deal)
	}
	acted := false
	for _, r := range in.Own[1:] {
		if r.Block.DealID != deal {
			return ActGate{}, fmt.Errorf("xdealv1: gate: a record of deal %q in the book of %q", r.Block.DealID, deal)
		}
		acted = acted || gatedAct(r)
	}
	if !gatedAct(in.Act) || acted {
		return ActGate{Allowed: true}, nil // not a first act
	}
	if ag.CounterpartyLayer == nil || !*ag.CounterpartyLayer {
		return ActGate{Gated: true, Allowed: true, Unguarded: true, Reason: GateNoLayer}, nil
	}
	covered, err := p.CanAct(ctx, CanActInput{DealID: deal, CounterpartAgreement: in.CounterpartAgreement, CommitmentShelfLog: in.CommitmentShelfLog})
	if err != nil {
		return ActGate{}, err
	}
	if !covered {
		return ActGate{Gated: true, Reason: GateNoProof}, nil
	}
	return ActGate{Gated: true, Allowed: true}, nil
}

// gatedAct reports whether r is an act the gate holds: a perform, or a
// payer leg.
func gatedAct(r Record) bool {
	if _, ok := bodyOf[PerformBody](r.Body); ok {
		return true
	}
	s, ok := bodyOf[SettleBody](r.Body)
	return ok && s.Leg == LegPayer
}
