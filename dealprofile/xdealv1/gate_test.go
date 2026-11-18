// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateBook is the buyer's (the offerer's) agreement in the handshake, naming
// the seller's, sealed in the buyer's book: its record, record id and pair.
func gateBook(t *testing.T, h *handshake, layer bool) (Record, string, []byte) {
	t.Helper()
	od, err := Digest(h.offer)
	require.NoError(t, err)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	b := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	b.Seq, b.Seen = 1, Some(h.agreementID)
	if !layer {
		f := false
		ab.CounterpartyLayer, ab.CounterpartyKey, b.Seen = &f, NullOf[string](), NullOf[string]()
	}
	r := Record{Block: b, Body: ab}
	require.NoError(t, Check(r))
	id, pair := h.buyer.seal("deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0", r)
	return r, id, pair
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
	ag, _, _ := gateBook(t, h, true)
	own := []Record{ag}
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
// so (Steven, u611).
func TestGateActWithoutALayerIsUnguarded(t *testing.T) {
	h := newHandshake(t, nil)
	ag, _, _ := gateBook(t, h, false)
	own := []Record{ag}
	g, err := h.buyer.profile.GateAct(context.Background(), ActGateInput{Own: own, Act: gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')})})
	require.NoError(t, err)
	assert.Equal(t, ActGate{Gated: true, Allowed: true, Unguarded: true, Reason: GateNoLayer}, g)
}

// A proof the gate does not accept leaves the act refused: one under
// another shelf log than the agreement names.
func TestGateActRefusesAProofOfAnotherShelf(t *testing.T) {
	h := newHandshake(t, func(a *AgreeBody) { a.ShelfLogID = "deal-cadence/ffffffffffffffff" })
	h.buyer.receive(h.agreementPair, &h.proof)
	ag, _, _ := gateBook(t, h, true)
	assert.Equal(t, ActGate{Gated: true, Reason: GateNoProof}, gate(t, h, []Record{ag}, gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')})))
}

func TestGateActRefusesAnotherBook(t *testing.T) {
	h := newHandshake(t, nil)
	ag, _, _ := gateBook(t, h, true)
	own := []Record{ag}
	pay := gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')})
	other := pay
	other.Block.DealID = "deal/" + "ffffffffffffffffffffffffffffffff"
	notAgreement := gateAct(1, &StatementBody{Statements: statements()})
	for name, in := range map[string]ActGateInput{
		"an empty book":                     {Act: pay},
		"a book not opening an agreement":   {Own: []Record{notAgreement}, Act: pay},
		"an act of another deal":            {Own: own, Act: other},
		"a record of another deal":          {Own: append(own, other), Act: pay},
		"another deal after the first act":  {Own: append(append([]Record{}, own...), pay, other), Act: pay},
		"another deal, a payee leg to seal": {Own: append(append([]Record{}, own...), other), Act: gateAct(2, &SettleBody{Leg: LegPayee, Statements: statements()})},
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
	_, buyerID, buyerPair := gateBook(t, h, true)
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

// The gate never reads the witnesses (u610: whether or not a witness is
// named), and a holder with no agreement of the deal is refused.
func TestGateActWithoutWitnessesAndWithoutAnAgreement(t *testing.T) {
	h := newHandshake(t, nil)
	pay := gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')})
	_, err := h.buyer.profile.GateAct(context.Background(), ActGateInput{Own: []Record{h.agreement}, Act: pay})
	assert.ErrorContains(t, err, "sealed no agreement", "the buyer holds none yet")

	h2 := newHandshake(t, func(a *AgreeBody) { a.Witnesses = []Witness{} })
	h2.buyer.receive(h2.agreementPair, &h2.proof)
	od, err := Digest(h2.offer)
	require.NoError(t, err)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h2.seller.key())
	ab.Witnesses, ab.CheckpointKey, ab.ShelfLogID = []Witness{}, h2.buyer.checkpointKey(), testCadenceLog
	b := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	b.Seq, b.Seen = 1, Some(h2.agreementID)
	ag := Record{Block: b, Body: ab}
	h2.buyer.seal("deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0", ag)
	g, err := h2.buyer.profile.GateAct(context.Background(), ActGateInput{Own: []Record{ag}, Act: pay, CounterpartAgreement: h2.agreementID})
	require.NoError(t, err)
	assert.Equal(t, ActGate{Gated: true, Allowed: true}, g, "an unwitnessed deal's proof opens the gate")
}

// A book opening with the counterparty's agreement is refused: its
// counterparty_layer is about the holder, so reading it would invert the
// gate. Here the seller's agreement says it runs no counterparty layer,
// which would let the buyer's first act through unguarded.
func TestGateActRefusesTheCounterpartysAgreement(t *testing.T) {
	h := newHandshake(t, nil)
	gateBook(t, h, true)
	theirs := h.agreement
	f := false
	ab := *theirs.Body.(*AgreeBody)
	ab.CounterpartyLayer, ab.CounterpartyKey = &f, NullOf[string]()
	theirs.Body = &ab
	_, err := h.buyer.profile.GateAct(context.Background(), ActGateInput{Own: []Record{theirs},
		Act: gateAct(2, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1')}), CounterpartAgreement: h.agreementID})
	assert.ErrorContains(t, err, "the holder's own agreement")
}
