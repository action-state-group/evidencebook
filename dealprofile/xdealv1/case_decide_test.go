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

// decideCase is a case on row delivered that the buyer raised against the
// seller. A seller book read is also read from the copy the buyer held, unless
// the side says otherwise.
func decideCase(seller CaseSide) GatheredCase {
	if len(seller.Book) > 0 && !seller.Held.Present {
		seller.Held = Some(seller.Book[len(seller.Book)-1].Read.RecordID)
	}
	return GatheredCase{Case: Case{Row: "delivered", Dispute: BookRead{KeyID: "buyer-key"},
		Keys: RulingKeys{Buyer: "buyer-key", Seller: "seller-key"}}, Seller: seller}
}

func shortDelivery(stmts ...StatementItem) *PerformBody {
	return &PerformBody{Act: "deliver", Observed: "partial:badges_150_of_200", Statements: stmts}
}

func TestDecideCaseReadsValueBodies(t *testing.T) {
	g := decideCase(CaseSide{Book: decideBook(PerformBody{Act: "deliver", Statements: statements(item("delivered", ValueNotMet))})})
	d := DecideCase(g, Null[string]{})
	require.NotNil(t, d.Decisive, d.NeedsPerson)
	assert.Equal(t, "rec-01", d.Decisive.RecordID)
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
		// one record stating both: item order is not time, in either order
		"met and not met at once": {CaseSide{Book: decideBook(shortDelivery(item("delivered", ValueMet), item("delivered", ValueNotMet)))},
			"the seller's latest record on row delivered states met and not_met at once"},
		"not met and met at once": {CaseSide{Book: decideBook(shortDelivery(item("delivered", ValueNotMet), item("delivered", ValueMet)))},
			"the seller's latest record on row delivered states not_met and met at once"},
		// a later met held as a value body, not a pointer, still counts
		"not met, then met as a value": {CaseSide{Book: decideBook(notMet, StatementBody{Statements: statements(item("delivered", ValueMet))})},
			"the seller's latest statement on row delivered, in a record it signed, says met"},
		"another row only": {CaseSide{Book: decideBook(shortDelivery(item("as_described", ValueNotMet)))}, "the seller stated nothing on row delivered in a record it signed"},
		"nothing read":     {CaseSide{}, "no record the seller signed was read, so its own word on row delivered is not known"},
		"forked":           {CaseSide{Book: decideBook(notMet, notMet), ForkAt: 2}, "the seller's book has two versions from record 2"},
		// a fork shown only to the buyer is found only against the copy the buyer held, so without it a person rules
		"held copy not read": {CaseSide{Book: decideBook(notMet), Held: NullOf[string]()},
			"the copy of the seller's book the buyer holds was not read, so a second version of it cannot be ruled out"},
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

// Each body that carries statements can be the deciding record, and a dispute
// the seller raises is decided against the buyer on the buyer's own word.
func TestDecideCaseOnEachBodyAndAgainstTheBuyer(t *testing.T) {
	for name, body := range map[string]Body{
		"statement": &StatementBody{Statements: statements(item("delivered", ValueNotMet))},
		"settle":    &SettleBody{Leg: "pay", Statements: statements(item("delivered", ValueNotMet))},
	} {
		d := DecideCase(decideCase(CaseSide{Book: decideBook(body)}), Null[string]{})
		require.NotNil(t, d.Decisive, "%s: %s", name, d.NeedsPerson)
		assert.Equal(t, RoleSeller, d.Decisive.Against, name)
	}

	buyerBook := decideBook(&StatementBody{Statements: statements(item("paid_as_agreed", ValueNotMet))})
	g := GatheredCase{Case: Case{Row: "paid_as_agreed", Dispute: BookRead{KeyID: "seller-key"},
		Keys: RulingKeys{Buyer: "buyer-key", Seller: "seller-key"}},
		Buyer: CaseSide{Book: buyerBook, Held: Some("rec-01")}}
	d := DecideCase(g, Null[string]{})
	require.NotNil(t, d.Decisive, d.NeedsPerson)
	assert.Equal(t, RoleBuyer, d.Decisive.Against)
	assert.Equal(t, RoleBuyer, d.Against)
}

// through bounds the read and never overrides a fork; reading only up to an
// earlier not_met decides on that earlier record.
func TestDecideCaseThroughEdges(t *testing.T) {
	notMet := shortDelivery(item("delivered", ValueNotMet))
	forked := decideCase(CaseSide{Book: decideBook(notMet, notMet), ForkAt: 2})
	d := DecideCase(forked, Some("rec-01"))
	assert.Nil(t, d.Decisive)
	assert.Equal(t, "the seller's book has two versions from record 2", d.NeedsPerson)

	twice := decideCase(CaseSide{Book: decideBook(notMet, &StatementBody{Statements: statements(item("delivered", ValueNotMet))})})
	d = DecideCase(twice, Some("rec-01"))
	require.NotNil(t, d.Decisive, d.NeedsPerson)
	assert.Equal(t, "rec-01", d.Decisive.RecordID)
	d = DecideCase(twice, Null[string]{})
	require.NotNil(t, d.Decisive, d.NeedsPerson)
	assert.Equal(t, "rec-02", d.Decisive.RecordID, "read whole, the latest not_met decides")
}
