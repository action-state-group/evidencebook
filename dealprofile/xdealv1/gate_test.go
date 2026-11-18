// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateBook is the buyer's (the offerer's) book in the handshake: its
// agreement, naming the seller's.
func gateBook(t *testing.T, h *handshake, layer bool) Record {
	t.Helper()
	od, err := Digest(h.offer)
	require.NoError(t, err)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	if !layer {
		f := false
		ab.CounterpartyLayer, ab.CounterpartyKey = &f, NullOf[string]()
	}
	b := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	b.Seq, b.Seen = 1, Some(h.agreementID)
	return Record{Block: b, Body: ab}
}

func gateAct(seq int64, body Body) Record {
	b := journalBlock(TypeSettle, "2026-11-01T10:15:00Z", true)
	b.Seq = seq
	if _, ok := bodyOf[PerformBody](body); ok {
		b.RecordType = TypePerform
	}
	if _, ok := bodyOf[StatementBody](body); ok {
		b.RecordType = TypeStatement
	}
	return Record{Block: b, Body: body}
}

func gate(t *testing.T, h *handshake, own []Record, act Record) ActGate {
	t.Helper()
	g, err := h.buyer.profile.GateAct(context.Background(), ActGateInput{Own: own, Act: act, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	return g
}

// u610: the first perform or payer settle waits for the counterparty's stamp
// proof; a payee leg, a statement and any later act never wait.
func TestGateActHoldsTheFirstAct(t *testing.T) {
	h := newHandshake(t, nil)
	h.buyer.receive(h.agreementPair, nil) // the agreement, no proof yet
	own := []Record{gateBook(t, h, true)}
	pay := gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')})
	perform := gateAct(2, &PerformBody{Act: "ship", Statements: statements(), Check: ref('1')})
	payee := gateAct(2, &SettleBody{Leg: LegPayee, Statements: statements()})
	stmt := gateAct(2, &StatementBody{Statements: statements(item("delivered", ValueMet))})

	assert.Equal(t, ActGate{Gated: true, Reason: GateNoProof}, gate(t, h, own, pay), "a first payer leg waits")
	assert.Equal(t, ActGate{Gated: true, Reason: GateNoProof}, gate(t, h, own, perform), "a first perform waits")
	assert.Equal(t, ActGate{Allowed: true}, gate(t, h, own, payee), "a payee leg never waits")
	assert.Equal(t, ActGate{Allowed: true}, gate(t, h, own, stmt), "a statement never waits")
	assert.Equal(t, ActGate{Allowed: true}, gate(t, h, append(own, payee, stmt, pay), gateAct(5, perform.Body)), "only the first act waits")
	assert.Equal(t, ActGate{Gated: true, Reason: GateNoProof}, gate(t, h, append(own, payee, stmt), pay), "payee legs and statements before it are not acts")

	// The proof arrives: the first act may be sealed.
	h.buyer.receive(h.agreementPair, &h.proof)
	assert.Equal(t, ActGate{Gated: true, Allowed: true}, gate(t, h, own, pay))
	h.buyer.reopen()
	assert.Equal(t, ActGate{Gated: true, Allowed: true}, gate(t, h, own, perform), "after a restart")
}

// A counterparty without the layer: the first act runs unguarded, and says
// so (u610, Sherlock2's second condition).
func TestGateActWithoutALayerIsUnguarded(t *testing.T) {
	h := newHandshake(t, nil)
	own := []Record{gateBook(t, h, false)}
	g, err := h.buyer.profile.GateAct(context.Background(), ActGateInput{Own: own, Act: gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')})})
	require.NoError(t, err)
	assert.Equal(t, ActGate{Gated: true, Allowed: true, Unguarded: true, Reason: GateNoLayer}, g)
}

// A proof the gate does not accept leaves the act refused: one under
// another shelf log than the agreement names.
func TestGateActRefusesAProofOfAnotherShelf(t *testing.T) {
	h := newHandshake(t, func(a *AgreeBody) { a.ShelfLogID = "deal-cadence/ffffffffffffffff" })
	h.buyer.receive(h.agreementPair, &h.proof)
	assert.Equal(t, ActGate{Gated: true, Reason: GateNoProof}, gate(t, h, []Record{gateBook(t, h, true)}, gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')})))
}

func TestGateActRefusesAnotherBook(t *testing.T) {
	h := newHandshake(t, nil)
	own := []Record{gateBook(t, h, true)}
	pay := gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')})
	other := pay
	other.Block.DealID = "deal/" + "ffffffffffffffffffffffffffffffff"
	notAgreement := gateAct(1, &StatementBody{Statements: statements()})
	for name, in := range map[string]ActGateInput{
		"an empty book":                   {Act: pay},
		"a book not opening an agreement": {Own: []Record{notAgreement}, Act: pay},
		"an act of another deal":          {Own: own, Act: other},
		"a record of another deal":        {Own: append(own, other), Act: pay},
	} {
		_, err := h.buyer.profile.GateAct(context.Background(), in)
		assert.Error(t, err, name)
	}
}

// The gate holds on both sides (u610): the acceptor's first perform waits
// for the offerer's stamp proof covering the offerer's agreement.
func TestGateActHoldsTheAcceptor(t *testing.T) {
	h := newHandshake(t, nil)
	h.buyer.receive(h.agreementPair, &h.proof)
	buyerAg := gateBook(t, h, true)
	buyerAg.Body.(*AgreeBody).CheckpointKey, buyerAg.Body.(*AgreeBody).ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	buyerLog := "deal/" + "b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0"
	buyerID, buyerPair := h.buyer.seal(buyerLog, buyerAg)
	perform := gateAct(2, &PerformBody{Act: "ship", Statements: statements(), Check: ref('1')})
	in := ActGateInput{Own: []Record{h.agreement}, Act: perform, CounterpartAgreement: buyerID}

	h.seller.receive(buyerPair, nil)
	g, err := h.seller.profile.GateAct(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, ActGate{Gated: true, Reason: GateNoProof}, g, "no offerer proof yet")

	proof := h.buyer.stamp(buyerID)
	h.seller.receive(buyerPair, &proof)
	g, err = h.seller.profile.GateAct(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, ActGate{Gated: true, Allowed: true}, g)
}
