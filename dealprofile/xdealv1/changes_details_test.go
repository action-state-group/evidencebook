// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
)

// ChangesDetails is the test the authorization applies to each journal record
// between a check and its act: the five kinds match, and others do not.
func TestChangesDetailsMatchesTheFiveKinds(t *testing.T) {
	rec := func(recordType string, body Body, cp bool) Record {
		b := Block{RecordType: recordType}
		if cp {
			b.Counterparty = &dealprofile.Counterparty{}
		}
		return Record{Block: b, Body: body}
	}
	for name, c := range map[string]struct {
		r    Record
		want bool
	}{
		"detail_change":                    {rec(TypeDetailChange, nil, false), true},
		"message naming the counterparty":  {rec(TypeMessage, nil, true), true},
		"message":                          {rec(TypeMessage, nil, false), false},
		"evidence with facts":              {rec(TypeEvidence, &dealprofile.EvidenceBody{CounterpartyFacts: &dealprofile.CounterpartyFacts{}}, false), true},
		"evidence":                         {rec(TypeEvidence, &dealprofile.EvidenceBody{Source: "rail"}, false), false},
		"evidence naming the counterparty": {rec(TypeEvidence, &dealprofile.EvidenceBody{Source: "rail"}, true), true},
		"intent starting a leg":            {rec(TypeIntent, &IntentBody{IntentBody: dealprofile.IntentBody{}}, true), true},
		"intent picking the item":          {rec(TypeIntent, &IntentBody{IntentBody: dealprofile.IntentBody{Move: "pick", Asked: &dealprofile.Terms{Item: "lamp"}}}, false), true},
		"intent":                           {rec(TypeIntent, &IntentBody{IntentBody: dealprofile.IntentBody{}}, false), false},
		"intent with an item, no move":     {rec(TypeIntent, &IntentBody{IntentBody: dealprofile.IntentBody{Asked: &dealprofile.Terms{Item: "lamp"}}}, false), false},
		"chose":                            {rec(TypeAgentMove, &dealprofile.AgentMoveBody{Move: "chose"}, false), true},
		"agent_move with no body":          {rec(TypeAgentMove, nil, false), false},
		"searched":                         {rec(TypeAgentMove, &dealprofile.AgentMoveBody{Move: "searched"}, false), false},
		"check":                            {rec(TypeCheck, nil, false), false},
	} {
		assert.Equal(t, c.want, ChangesDetails(c.r), name)
	}
}
