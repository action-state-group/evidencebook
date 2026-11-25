// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decideBook is a side's book read of records, each carrying body, at seq 1..n.
func decideBook(bodies ...Body) []CaseRecord {
	out := make([]CaseRecord, len(bodies))
	for i, b := range bodies {
		seq := uint64(i + 1)
		out[i] = CaseRecord{Read: BookRead{Record: Record{Body: b}, Digest: fmt.Sprintf("sha256:%02d", seq),
			RecordID: fmt.Sprintf("rec-%02d", seq), Seq: seq}, At: "2026-11-25T18:00:00Z", TimedBy: TimedByOwnStamp}
	}
	return out
}

// decideCase is a case on row delivered that the buyer raised against the seller.
func decideCase(seller CaseSide) GatheredCase {
	return GatheredCase{Case: Case{Row: "delivered", Dispute: BookRead{KeyID: "buyer-key"},
		Keys: RulingKeys{Buyer: "buyer-key", Seller: "seller-key"}}, Seller: seller}
}

func shortDelivery(stmts ...StatementItem) *PerformBody {
	return &PerformBody{Act: "deliver", Observed: "partial:badges_150_of_200", Statements: stmts}
}

func TestDecideCaseRulesOnTheOwingSidesOwnNotMet(t *testing.T) {
	agree := &AgreeBody{Kind: "agreement"}
	g := decideCase(CaseSide{Book: decideBook(agree, shortDelivery(item("delivered", ValueNotMet)))})
	d := DecideCase(g, Null[string]{})
	require.NotNil(t, d.Decisive, d.NeedsPerson)
	assert.Equal(t, DecidingRecord{Against: RoleSeller, Value: ValueNotMet, Record: "sha256:02", RecordID: "rec-02", Seq: 2,
		At: "2026-11-25T18:00:00Z", TimedBy: TimedByOwnStamp}, *d.Decisive)
	assert.Empty(t, d.NeedsPerson)
}

func TestDecideCaseNeedsAPerson(t *testing.T) {
	notMet := shortDelivery(item("delivered", ValueNotMet))
	for name, tc := range map[string]struct {
		side CaseSide
		want string
	}{
		// the free-text observed says 150 of 200, but the seller stated nothing: nothing decides from free text
		"silent seller": {CaseSide{Book: decideBook(shortDelivery())}, "the seller stated nothing on row delivered in a record it signed"},
		"says met":      {CaseSide{Book: decideBook(shortDelivery(item("delivered", ValueMet)))}, "the seller's latest statement on row delivered, in a record it signed, says met"},
		// the latest statement decides, never the one that helps the ruling (Sherlock2's tightening 1)
		"not met, then met": {CaseSide{Book: decideBook(notMet, &StatementBody{Statements: statements(item("delivered", ValueMet))})},
			"the seller's latest statement on row delivered, in a record it signed, says met"},
		"another row only": {CaseSide{Book: decideBook(shortDelivery(item("as_described", ValueNotMet)))}, "the seller stated nothing on row delivered in a record it signed"},
		"nothing read":     {CaseSide{}, "no record the seller signed was read, so its own word on row delivered is not known"},
		"forked":           {CaseSide{Book: decideBook(notMet, notMet), ForkAt: 2}, "the seller's book has two versions after record 1"},
	} {
		t.Run(name, func(t *testing.T) {
			d := DecideCase(decideCase(tc.side), Null[string]{})
			assert.Nil(t, d.Decisive)
			assert.Equal(t, tc.want, d.NeedsPerson)
		})
	}
	stranger := decideCase(CaseSide{Book: decideBook(notMet)})
	stranger.Dispute.KeyID = "another-key"
	d := DecideCase(stranger, Null[string]{})
	assert.Nil(t, d.Decisive)
	assert.Equal(t, "the dispute is signed by neither side's key, so it names no side it is raised against", d.NeedsPerson)
	// a dispute the seller raises is against the buyer: the seller's own not_met is not the buyer's word
	bySeller := decideCase(CaseSide{Book: decideBook(notMet)})
	bySeller.Dispute.KeyID = "seller-key"
	d = DecideCase(bySeller, Null[string]{})
	assert.Equal(t, RoleBuyer, d.Against)
	assert.Nil(t, d.Decisive)
	assert.Equal(t, "no record the buyer signed was read, so its own word on row delivered is not known", d.NeedsPerson)
}

// A sealed ruling checked again reads the owing side's book only up to the
// ruling's read, so a later statement does not change the answer it was
// sealed on (Sherlock2, buzz 319f98a4).
func TestDecideCaseThroughASealedRulingsRead(t *testing.T) {
	later := &StatementBody{Statements: statements(item("delivered", ValueMet))}
	g := decideCase(CaseSide{Book: decideBook(shortDelivery(item("delivered", ValueNotMet)), later)})
	assert.Nil(t, DecideCase(g, Null[string]{}).Decisive, "read whole, the later met statement needs a person")
	d := DecideCase(g, Some("rec-01"))
	require.NotNil(t, d.Decisive, d.NeedsPerson)
	assert.Equal(t, "rec-01", d.Decisive.RecordID)

	d = DecideCase(g, Some("rec-09"))
	assert.Nil(t, d.Decisive)
	assert.Equal(t, "record rec-09 of the seller, the last one the ruling read, is not in the book read", d.NeedsPerson)
	// a ruling that read nothing of the side: nothing to decide on
	d = DecideCase(g, NullOf[string]())
	assert.Nil(t, d.Decisive)
	assert.Equal(t, "the ruling read no record the seller signed", d.NeedsPerson)
	// and not by luck of a later statement: with only the not_met record, a null read still decides nothing
	only := decideCase(CaseSide{Book: decideBook(shortDelivery(item("delivered", ValueNotMet)))})
	assert.Nil(t, DecideCase(only, NullOf[string]()).Decisive)
}
