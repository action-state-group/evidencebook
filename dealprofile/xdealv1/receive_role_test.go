// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A counterparty agreement of the holder's own role is not the other half
// of the deal: the shelf does not hold it, whether the holder holds only its
// offer or its own agreement too, so the History card never reads it as
// stamped by both (evidencebook#884). One of the other role is held.
func TestReceiveRefusesAnAgreementOfTheHoldersRole(t *testing.T) {
	// At an offerer holding only its offer (a buyer's): an acceptor's
	// agreement in the buyer's role is not held.
	h := newHandshake(t, func(a *AgreeBody) { a.Role = RoleBuyer })
	_, err := h.buyer.receiveRaw(h.agreementPair, &h.proof)
	assert.ErrorContains(t, err, "may not push this record", "offerer side")

	// At a holder of its own agreement: the same.
	h2 := newHandshake(t, func(a *AgreeBody) { a.Role = RoleBuyer })
	gateBook(t, h2, true)
	_, err = h2.buyer.receiveRaw(h2.agreementPair, &h2.proof)
	assert.ErrorContains(t, err, "may not push this record", "agreement side")

	// The other role is held, on both paths.
	ok := newHandshake(t, nil)
	assert.True(t, ok.buyer.receive(ok.agreementPair, &ok.proof).Record.Stored)
	ok2 := newHandshake(t, nil)
	gateBook(t, ok2, true)
	assert.True(t, ok2.buyer.receive(ok2.agreementPair, &ok2.proof).Record.Stored)
}
