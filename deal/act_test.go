// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var adjudicatorKey = strings.Repeat("ad", 32)

// fullOffer is a buyer's offer to key with what the act calls read: a
// payment row and a delivery row with a remedy, a named record the seller
// writes, and an adjudicator with two forums.
func fullOffer(t *testing.T, key string) xdealv1.ProposeBody {
	t.Helper()
	raw := `{
  "role": "buyer", "to": "` + key + `",
  "terms": {"item": "lamp", "currency": "USD"},
  "pack_digest": "` + strings.Repeat("a", 64) + `",
  "agree_by": "2026-11-02T10:00:00Z",
  "terms_rows": {"rows": [
    {"id": "paid_as_agreed", "owed_by": "buyer", "owed": {"amount_minor": 6000, "currency": "USD"},
     "due": {"at": "2026-11-01T23:59:59Z"}, "window_end": {"at": "2026-11-04T23:59:59Z"}},
    {"id": "delivered", "owed_by": "seller", "owed": {"item": "lamp"},
     "due": {"at": "2026-11-03T23:59:59Z"}, "window_end": {"at": "2026-11-06T23:59:59Z"},
     "remedies": [{"id": "replace", "within_seconds": 86400}]}],
   "named": {"shipped": {"side": "seller", "latest": "2026-11-03T23:59:59Z"}}},
  "adjudicator": {"key_id": "` + adjudicatorKey + `", "rules_digest": "` + strings.Repeat("b", 64) + `", "rules_url": "https://rules.test/r"},
  "forums": ["auto:ref", "external:panel"], "witnesses": []
}`
	var body xdealv1.ProposeBody
	require.NoError(t, json.Unmarshal([]byte(raw), &body))
	return body
}

// agreedFull runs deal.open on fullOffer, with each side holding the
// other's agreement and stamp proof, so either side's first act passes the
// gate.
func agreedFull(t *testing.T) (buyer, seller *side, dealID string) {
	t.Helper()
	buyer, seller, dealID, ba := agreedFullHeld(t)
	seller.receiveAgreement(ba)
	return buyer, seller, dealID
}

// agreedFullHeld is agreedFull before the acceptor takes the offerer's
// agreement and its stamp proof.
func agreedFullHeld(t *testing.T) (buyer, seller *side, dealID string, ba Agreement) {
	t.Helper()
	return agreedOn(t, fullOffer)
}

// agreedOn is agreedFullHeld on the offer made from the seller's key.
func agreedOn(t *testing.T, offerOf func(*testing.T, string) xdealv1.ProposeBody) (buyer, seller *side, dealID string, ba Agreement) {
	t.Helper()
	ctx := t.Context()
	buyer, seller = newSide(t, "buyer"), newSide(t, "seller")
	offer, err := buyer.store.Offer(ctx, offerOf(t, seller.keyID()), "offer-1")
	require.NoError(t, err)
	require.NoError(t, buyer.store.MarkWritten(ctx, offer))
	seller.receiveSent(offer.Pair, nil)
	seller.admit(offer.DealID)
	sa, err := seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)
	buyer.receiveAgreement(sa)
	buyer.admit(offer.DealID)
	ba, err = buyer.store.Agree(ctx, buyer.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)
	return buyer, seller, offer.DealID, ba
}

// journal seals one x-deal-v1 journal record on p's shelf, as
// capsulectl-deal's step writer does, and returns its digest. dealScoped
// puts the deal id on it.
func (p *side) journal(dealID string, dealScoped bool, body xdealv1.Body, refs ...xdealv1.RelRef) string {
	p.t.Helper()
	p.tick()
	b := xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, RecordType: body.RecordType(),
		At: p.now.UTC().Format(time.RFC3339), Refs: refs}
	if dealScoped {
		b.DealID = dealID
	}
	rec := xdealv1.Record{Block: b, Body: body}
	require.NoError(p.t, xdealv1.Check(rec))
	payload, digest, err := xdealv1.Encode(rec)
	require.NoError(p.t, err)
	j, err := p.shelf.Journal(p.t.Context())
	require.NoError(p.t, err)
	_, err = j.Append(p.t.Context(), evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}})
	require.NoError(p.t, err)
	return digest
}

// bind records a step of dealID that binds a journal record, as
// capsulectl-deal's step list does for a record that carries no deal id.
func (p *side) bind(dealID string, n int, kind, digest string) {
	p.t.Helper()
	_, err := p.db.ExecContext(p.t.Context(), `INSERT INTO deal_v1_events (deal_id, n, kind, local, record_digest) VALUES (?, ?, ?, '{}', ?)`, dealID, n, kind, digest)
	require.NoError(p.t, err)
}

func relTo(rel, digest string) xdealv1.RelRef {
	r := xdealv1.RefTo(digest)
	return xdealv1.RelRef{Rel: rel, Type: r.Type, DigestAlg: r.DigestAlg, Digest: r.Digest}
}

func nonceOf(label string) string {
	s := sha256.Sum256([]byte("act tests\x00" + label))
	return hex.EncodeToString(s[:])
}

// approvedCheck seals a check of action and its passing verdict, approved
// by approver ("user", or "standing_intent" under an intent), and returns
// the check's digest.
func (p *side) approvedCheck(dealID, action, approver, label string) string {
	p.t.Helper()
	check := p.journal(dealID, true, &xdealv1.CheckBody{CheckBody: dealprofile.CheckBody{Action: action}, Nonce: nonceOf(label + " check")})
	card := nonceOf(label + " card")
	verdict := p.journal(dealID, true, &xdealv1.VerdictBody{VerdictBody: dealprofile.VerdictBody{Result: "pass", Differences: []dealprofile.Difference{}, Options: []string{}, CardCommitment: card},
		Nonce: nonceOf(label + " verdict")}, relTo("checks", check))
	a := &xdealv1.ApprovalBody{ApprovalBody: dealprofile.ApprovalBody{Choice: "proceed", Proceed: true, Approver: approver}, Nonce: nonceOf(label + " approval")}
	if approver == "user" {
		a.SaidCommitment, a.CardCommitment = nonceOf(label+" said"), card
	}
	p.journal(dealID, true, a, relTo("approves", verdict))
	return check
}

func TestPerformSealsAnAuthorizedAct(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	check := seller.approvedCheck(dealID, "commit", "user", "perform")

	var seen *xdealv1.ActAuthorization
	in := PerformIn{Check: check, Act: "deliver", Observed: "carrier_scan", Marks: []string{"shipped"},
		Statements: []xdealv1.StatementItem{{Row: "delivered", Value: "met"}},
		Before:     func(_ xdealv1.Record, auth *xdealv1.ActAuthorization) error { seen = auth; return nil }}
	seller.tick()
	got, err := seller.store.Perform(ctx, dealID, in)
	require.NoError(t, err)
	assert.False(t, got.Again)
	assert.Equal(t, uint64(2), got.Seq)
	require.NotNil(t, got.Gate)
	assert.True(t, got.Gate.Allowed, "%+v", got.Gate)
	require.NotNil(t, got.Authorization)
	assert.True(t, got.Authorization.NeedsPersonSignature)
	assert.Same(t, seen, got.Authorization, "the caller's check saw the act's authorization")
	read, err := xdealv1.VerifyBook(got.Pair)
	require.NoError(t, err)
	assert.Equal(t, got.Digest, func() string { d, _ := xdealv1.Digest(read.Record); return d }())
	assert.Equal(t, []string{"shipped"}, read.Record.Block.Marks)

	// The same call again seals nothing and says what the act waits on.
	in.Before = func(xdealv1.Record, *xdealv1.ActAuthorization) error {
		t.Fatal("not run for a record already sealed")
		return nil
	}
	seller.tick()
	again, err := seller.store.Perform(ctx, dealID, in)
	require.NoError(t, err)
	assert.True(t, again.Again)
	assert.Equal(t, got.Digest, again.Digest)
	assert.Equal(t, got.Pair, again.Pair)
	assert.Nil(t, again.Gate)
	require.NotNil(t, again.Authorization)
	assert.Equal(t, got.Authorization.ApprovalDigest, again.Authorization.ApprovalDigest)

	// A second act on the same approval is refused: one approval, one act.
	in.Act = "redeliver"
	in.Before = nil
	_, err = seller.store.Perform(ctx, dealID, in)
	var na *NotAuthorizedError
	require.ErrorAs(t, err, &na)
	assert.Contains(t, inputReason(t, err), "the act is not authorized: ")
	assert.Contains(t, err.Error(), "already authorized")
}

func TestPerformRefusals(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	check := seller.approvedCheck(dealID, "commit", "user", "refusals")
	base := PerformIn{Check: check, Act: "deliver", Observed: "carrier_scan"}
	with := func(edit func(*PerformIn)) PerformIn { in := base; edit(&in); return in }

	for _, tc := range []struct {
		name string
		in   PerformIn
		want string
	}{
		{"an unknown row", with(func(in *PerformIn) { in.Statements = []xdealv1.StatementItem{{Row: "nope", Value: "met"}} }),
			"deal " + dealID + " has no row nope: the rows are the ones its offer names"},
		{"a value", with(func(in *PerformIn) { in.Statements = []xdealv1.StatementItem{{Row: "delivered", Value: "fine"}} }),
			"statement delivered=fine: the value is met, not_met or cannot_tell"},
		{"a remedy the row does not allow", with(func(in *PerformIn) {
			in.Statements = []xdealv1.StatementItem{{Row: "delivered", Value: "not_met", Remedy: "refund"}}
		}), "statement delivered=not_met:refund: row delivered allows no remedy refund"},
		{"a row twice", with(func(in *PerformIn) {
			in.Statements = []xdealv1.StatementItem{{Row: "delivered", Value: "met"}, {Row: "delivered", Value: "not_met"}}
		}), "statement names row delivered twice: one statement per row in a record"},
		{"an unknown mark", with(func(in *PerformIn) { in.Marks = []string{"paid"} }),
			"mark paid: the offer of deal " + dealID + " names no record paid"},
		{"a mark twice", with(func(in *PerformIn) { in.Marks = []string{"shipped", "shipped"} }), "mark names shipped twice"},
		{"an unknown remedy row", with(func(in *PerformIn) { in.RemedyRow = "nope" }),
			"deal " + dealID + " has no row nope: the rows are the ones its offer names"},
		{"a check the journal does not hold", with(func(in *PerformIn) { in.Check = strings.Repeat("0", 64) }),
			"the act is not authorized: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := seller.store.Perform(ctx, dealID, tc.in)
			assert.Contains(t, inputReason(t, err), tc.want)
		})
	}

	_, err := seller.store.Perform(ctx, "deal-0000000000000000", base)
	assert.Equal(t, "deal deal-0000000000000000 is not an x-deal-v1 deal this profile holds", inputReason(t, err))

	// The caller's own check refuses the act, and nothing is sealed.
	refused := errors.New("refused by the caller")
	_, err = seller.store.Perform(ctx, dealID, with(func(in *PerformIn) {
		in.Before = func(xdealv1.Record, *xdealv1.ActAuthorization) error { return refused }
	}))
	require.ErrorIs(t, err, refused)
	d, err := seller.store.Deal(ctx, dealID)
	require.NoError(t, err)
	book, err := seller.store.OwnBook(ctx, d)
	require.NoError(t, err)
	assert.Len(t, book, 1, "nothing was sealed")
}

func TestPerformMarkOfTheOtherSide(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedFull(t)
	_, err := buyer.store.Perform(ctx, dealID, PerformIn{Check: strings.Repeat("1", 64), Act: "pay", Observed: "bank", Marks: []string{"shipped"}})
	assert.Equal(t, "mark shipped: the offer says the seller writes it, and you are the buyer", inputReason(t, err))
}

func TestPerformBeforeAgreeing(t *testing.T) {
	ctx := t.Context()
	buyer := newSide(t, "buyer")
	sk := strings.Repeat("5", 64)
	offer, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)
	_, err = buyer.store.Perform(ctx, offer.DealID, PerformIn{Check: strings.Repeat("1", 64), Act: "deliver", Observed: "scan"})
	assertCoded(t, err, CodeNoAgreement, offer.DealID)
	_, err = buyer.store.Statement(ctx, offer.DealID, StatementIn{Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "met"}}})
	assertCoded(t, err, CodeNoAgreement, offer.DealID)
	_, err = buyer.store.Status(ctx, offer.DealID)
	assertCoded(t, err, CodeNoAgreement, offer.DealID)
}

// The first act waits on the other side's stamp proof over the holder's
// agreement: the acceptor holds none until it takes the offerer's
// agreement, which comes with it.
func TestPerformFirstActGate(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID, ba := agreedFullHeld(t)
	check := seller.approvedCheck(dealID, "commit", "user", "gate")
	in := PerformIn{Check: check, Act: "deliver", Observed: "scan"}
	_, err := seller.store.Perform(ctx, dealID, in)
	assert.Contains(t, inputReason(t, err), "your first act on deal "+dealID+" waits on the other side's stamp proof: ")

	seller.receiveAgreement(ba)
	got, err := seller.store.Perform(ctx, dealID, in)
	require.NoError(t, err)
	assert.True(t, got.Gate.Gated)
	assert.True(t, got.Gate.Allowed)
}

// An act under a standing intent rests on an intent that carries no deal
// id: the deal's steps bind it, and another deal's intent is not read.
func TestPerformUnderABoundStandingIntent(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	intent := seller.journal(dealID, false, &dealprofile.IntentBody{VerbatimCommitment: nonceOf("verbatim"), Allowed: []string{"commit"}})
	check := seller.approvedCheck(dealID, "commit", "standing_intent", "standing")
	in := PerformIn{Check: check, Act: "deliver", Observed: "scan"}

	// Unbound, the intent is not the deal's: no approval rests on it.
	_, err := seller.store.Perform(ctx, dealID, in)
	require.Error(t, err)
	var na *NotAuthorizedError
	require.ErrorAs(t, err, &na)

	// Another deal's step binding it changes nothing.
	seller.bind("deal-ffffffffffffffff", 1, "intent", intent)
	_, err = seller.store.Perform(ctx, dealID, in)
	require.ErrorAs(t, err, &na)

	seller.bind(dealID, 1, "intent", intent)
	got, err := seller.store.Perform(ctx, dealID, in)
	require.NoError(t, err)
	require.NotNil(t, got.Authorization)
	assert.Equal(t, intent, got.Authorization.Intent)
	assert.False(t, got.Authorization.NeedsPersonSignature)

	journal, err := seller.store.DealJournal(ctx, dealID)
	require.NoError(t, err)
	assert.Len(t, journal, 4, "the bound intent and the deal's check, verdict and approval")
}

func TestStatement(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedFull(t)
	met := StatementIn{Statements: []xdealv1.StatementItem{{Row: "delivered", Value: "met"}}}
	first, err := buyer.store.Statement(ctx, dealID, met)
	require.NoError(t, err)
	assert.False(t, first.Again)
	assert.Nil(t, first.Gate, "a statement is never gated")

	again, err := buyer.store.Statement(ctx, dealID, met)
	require.NoError(t, err)
	assert.True(t, again.Again)
	assert.Equal(t, first.Digest, again.Digest)

	// After another statement on the row, the same one is new.
	_, err = buyer.store.Statement(ctx, dealID, StatementIn{Statements: []xdealv1.StatementItem{{Row: "delivered", Value: "not_met", Remedy: "replace"}}})
	require.NoError(t, err)
	restated, err := buyer.store.Statement(ctx, dealID, met)
	require.NoError(t, err)
	assert.False(t, restated.Again)
	assert.Equal(t, uint64(4), restated.Seq)

	_, err = buyer.store.Statement(ctx, dealID, StatementIn{})
	assert.Equal(t, "statement is required at least once: a statement says how a row went", inputReason(t, err))
}

func TestDisputeWithdrawAndChallenge(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedFull(t)
	_, err := buyer.store.Statement(ctx, dealID, StatementIn{Statements: []xdealv1.StatementItem{{Row: "delivered", Value: "not_met"}}})
	require.NoError(t, err)

	_, err = buyer.store.Dispute(ctx, dealID, DisputeIn{Row: "delivered"})
	assert.Equal(t, "forum is required: the agreement accepts auto:ref, external:panel", inputReason(t, err))
	_, err = buyer.store.Dispute(ctx, dealID, DisputeIn{Row: "nope", Forum: "auto:ref"})
	assert.Equal(t, "deal "+dealID+" has no row nope: the rows are the ones its offer names", inputReason(t, err))
	_, err = buyer.store.WithdrawDispute(ctx, dealID, WithdrawIn{Row: "delivered"})
	assert.Equal(t, "you have no open dispute on row delivered of deal "+dealID+" to take back", inputReason(t, err))

	opened, err := buyer.store.Dispute(ctx, dealID, DisputeIn{Row: "delivered", Forum: "auto:ref"})
	require.NoError(t, err)
	b, ok := disputeOf(opened.Record)
	require.True(t, ok)
	assert.Equal(t, adjudicatorKey, b.Adjudicator)
	assert.Len(t, b.Statements, 1, "the dispute cites the statement on the row")

	again, err := buyer.store.Dispute(ctx, dealID, DisputeIn{Row: "delivered"})
	require.NoError(t, err)
	assert.True(t, again.Again)
	assert.Equal(t, opened.Digest, again.Digest)
	_, err = buyer.store.Dispute(ctx, dealID, DisputeIn{Row: "delivered", Forum: "external:panel"})
	assertCoded(t, err, CodeDisputeOpen, "delivered", "auto:ref")

	withdrawn, err := buyer.store.WithdrawDispute(ctx, dealID, WithdrawIn{Row: "delivered"})
	require.NoError(t, err)
	assert.False(t, withdrawn.Again)
	again, err = buyer.store.WithdrawDispute(ctx, dealID, WithdrawIn{Row: "delivered"})
	require.NoError(t, err)
	assert.True(t, again.Again)
	assert.Equal(t, withdrawn.Digest, again.Digest)

	// A ruling the holder does not hold cannot be challenged.
	ruling := strings.Repeat("7", 64)
	_, err = buyer.store.Challenge(ctx, dealID, ChallengeIn{Ruling: ruling, GroundsCommitment: strings.Repeat("8", 64)})
	assertCoded(t, err, CodeChallengeUnknownRuling, ruling, dealID)
}

func TestDisputeWithNoAdjudicator(t *testing.T) {
	ctx := t.Context()
	buyer, _, offer, _, _ := handshake(t)
	_, err := buyer.store.Dispute(ctx, offer.DealID, DisputeIn{Row: "paid_as_agreed"})
	assert.Equal(t, "the agreement of deal "+offer.DealID+" names no adjudicator and no forum, so no dispute is possible", inputReason(t, err))
	_, err = buyer.store.Challenge(ctx, offer.DealID, ChallengeIn{Ruling: strings.Repeat("7", 64)})
	assert.Equal(t, "the agreement of deal "+offer.DealID+" names no adjudicator, so it has no ruling to challenge", inputReason(t, err))
}

func payeeIn(status string) SettleIn {
	return SettleIn{Status: status, Received: 5900, ReceiveFee: 100,
		PaymentRef: json.RawMessage(`{"type":"ln.payment_hash","value":"` + strings.Repeat("c", 64) + `"}`)}
}

func TestSettlePayeeLeg(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)

	pending, err := seller.store.Settle(ctx, dealID, payeeIn(LegPending))
	require.NoError(t, err)
	assert.False(t, pending.Again)
	b, ok := settleOf(pending.Record)
	require.True(t, ok)
	assert.Equal(t, xdealv1.LegPayee, b.Leg)
	d, err := seller.store.Deal(ctx, dealID)
	require.NoError(t, err)
	member, err := seller.store.SettlementAt(ctx, d, int(pending.Seq))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(member, &m))
	assert.Equal(t, "payee_observed", m["leg"])
	assert.Equal(t, "pending", m["status"])
	terms, err := seller.store.termsRef(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, terms, m["terms_ref"], "the leg names the deal's offer as sent")

	// The same observation later is the same leg: nothing new is sealed.
	seller.tick()
	again, err := seller.store.Settle(ctx, dealID, payeeIn(LegPending))
	require.NoError(t, err)
	assert.True(t, again.Again)
	assert.Equal(t, pending.Digest, again.Digest)

	// A later observation supersedes it, once.
	settled := payeeIn(LegSettled)
	settled.Supersedes = pending.Digest
	next, err := seller.store.Settle(ctx, dealID, settled)
	require.NoError(t, err)
	assert.False(t, next.Again)
	failed := payeeIn(LegFailed)
	failed.Supersedes = pending.Digest
	_, err = seller.store.Settle(ctx, dealID, failed)
	assert.Equal(t, "leg "+pending.Digest+" is already superseded: a later observation names the latest one of its payment", inputReason(t, err))

	failed.Supersedes = strings.Repeat("9", 64)
	_, err = seller.store.Settle(ctx, dealID, failed)
	assert.Equal(t, "supersedes must name an earlier leg of what you received in your own book, of the same payment", inputReason(t, err))

	neg := payeeIn(LegSettled)
	neg.Received = -1
	_, err = seller.store.Settle(ctx, dealID, neg)
	assertCoded(t, err, CodeSettleNegative)

	_, err = seller.store.Settle(ctx, dealID, payeeIn("lost"))
	assert.Equal(t, "a payee leg's status is pending, settled or failed", inputReason(t, err))
	_, err = seller.store.Settle(ctx, dealID, SettleIn{Row: "delivered", Status: LegSettled})
	assert.Equal(t, "row delivered owes no amount: a leg settles a payment row", inputReason(t, err))
}

func TestSettleRefusesAPayerLeg(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedFull(t)
	_, err := buyer.store.Settle(ctx, dealID, payeeIn(LegSettled))
	assertCoded(t, err, CodeSettlePayer, "payment")
	in := payeeIn(LegSettled)
	in.Reverses = strings.Repeat("9", 64)
	_, err = buyer.store.Settle(ctx, dealID, in)
	assert.Equal(t, "reverses must name a settle record of your own deal book: "+in.Reverses+" is not one", inputReason(t, err))
}

// The holder's refund of a payment it received is a payer leg.
func TestSettleRefusesAPayerRefund(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	paid, err := seller.store.Settle(ctx, dealID, payeeIn(LegSettled))
	require.NoError(t, err)
	refund := payeeIn(LegSettled)
	refund.Reverses = paid.Digest
	_, err = seller.store.Settle(ctx, dealID, refund)
	assertCoded(t, err, CodeSettlePayer, "refund")
}

func TestStatus(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedFull(t)
	_, err := buyer.store.Statement(ctx, dealID, StatementIn{Statements: []xdealv1.StatementItem{{Row: "delivered", Value: "met"}}})
	require.NoError(t, err)
	v, err := buyer.store.Status(ctx, dealID)
	require.NoError(t, err)
	assert.Equal(t, xdealv1.RoleBuyer, v.Role)
	assert.Equal(t, xdealv1.SideOfferer, v.Side)
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"delivered"`)

	_, err = buyer.store.Status(ctx, "deal-0000000000000000")
	assert.Equal(t, "deal deal-0000000000000000 is not an x-deal-v1 deal this profile holds", inputReason(t, err))
}

// Perform needs the receive profile for its gate.
func TestActNeedsTheReceiveProfile(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	check := seller.approvedCheck(dealID, "commit", "user", "profile")
	cfg := seller.store.cfg
	cfg.ReceiveProfile = nil
	st, err := Open(ctx, cfg)
	require.NoError(t, err)
	_, err = st.Perform(context.Background(), dealID, PerformIn{Check: check, Act: "deliver", Observed: "scan"})
	assert.ErrorContains(t, err, "Config.ReceiveProfile")
}

// A call decides from the book it read; a record another write appends
// before its own is refused, and the same call made again decides again.
func TestACallWhoseBookMovedIsMadeAgain(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	check := seller.approvedCheck(dealID, "commit", "user", "moved")
	other := xdealv1.StatementBody{Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "cannot_tell"}}}
	interleave := func() {
		done := false
		bookAppendHook = func() {
			if done {
				return
			}
			done = true
			_, err := seller.store.SealInBook(ctx, dealID, BookRecord{Body: other})
			require.NoError(t, err)
		}
		t.Cleanup(func() { bookAppendHook = func() {} })
	}

	in := PerformIn{Check: check, Act: "deliver", Observed: "scan"}
	interleave()
	_, err := seller.store.Perform(ctx, dealID, in)
	assertCoded(t, err, CodeRecordRetry)
	bookAppendHook = func() {}
	got, err := seller.store.Perform(ctx, dealID, in)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), got.Seq, "after the statement that took its place")

	// Two of the same statement at once: the second is refused, and made
	// again it finds the first.
	met := StatementIn{Statements: []xdealv1.StatementItem{{Row: "delivered", Value: "met"}}}
	done := false
	bookAppendHook = func() {
		if done {
			return
		}
		done = true
		bookAppendHook = func() {}
		_, err := seller.store.Statement(ctx, dealID, met)
		require.NoError(t, err)
	}
	_, err = seller.store.Statement(ctx, dealID, met)
	assertCoded(t, err, CodeRecordRetry)
	again, err := seller.store.Statement(ctx, dealID, met)
	require.NoError(t, err)
	assert.True(t, again.Again)
}

func TestSettleChecksThePaymentRef(t *testing.T) {
	ctx := t.Context()
	_, seller, dealID := agreedFull(t)
	with := func(ref string) SettleIn { in := payeeIn(LegSettled); in.PaymentRef = json.RawMessage(ref); return in }
	for _, tc := range []struct{ ref, want string }{
		{`[1]`, "payment_ref is not a JSON object"},
		{`{"type":"ln.payment_hash"}`, "payment_ref has no value: the payment's reference"},
		{`{"type":"made.up","value":"x"}`, "payment_ref type made.up is neither a registered payment_ref type nor x-deal-v1.merchant_order"},
		{`{"type":"ln.payment_hash","value":"x","network":"main"}`, "payment_ref member network does not go with ln.payment_hash"},
		{`{"type":"x402.transaction","value":"0xabc"}`, "payment_ref needs network for x402.transaction"},
		{`{"type":"x-deal-v1.merchant_order","value":"A-17","origin":"https://Shop.Example/"}`, "payment_ref origin https://Shop.Example/ is not in normal form: https://shop.example"},
		{`{"type":"x-deal-v1.merchant_order","value":"A-17","origin":"ftp://shop.example"}`, "payment_ref origin ftp://shop.example is not a checkout origin: its scheme is not https or http"},
		{`{"type":"x-deal-v1.merchant_order","value":"4111 1111 1111 1111","origin":"https://shop.example"}`, "payment_ref value looks like a card number"},
	} {
		_, err := seller.store.Settle(ctx, dealID, with(tc.ref))
		assert.Contains(t, inputReason(t, err), tc.want, tc.ref)
	}
	got, err := seller.store.Settle(ctx, dealID, with(`{"type":"x-deal-v1.merchant_order","value":"A-17","origin":"https://shop.example"}`))
	require.NoError(t, err)
	assert.False(t, got.Again)
}

// sealPayerLeg seals a payer leg in p's book directly, as a payment the
// holder made (phase 3 seals one through the store), with status.
func (p *side) sealPayerLeg(dealID, status string) string {
	p.t.Helper()
	member := json.RawMessage(`{"status":"` + status + `"}`)
	ref := xdealv1.RefTo(strings.Repeat("e", 64))
	sealed, err := p.store.SealInBook(p.t.Context(), dealID, BookRecord{Body: xdealv1.SettleBody{Leg: xdealv1.LegPayer, Statements: []xdealv1.StatementItem{}, Check: &ref},
		Members: map[string]json.RawMessage{"settlement": member}})
	require.NoError(p.t, err)
	return sealed.Digest
}

// A refund received names a payer leg of the holder's own book; a second
// refund of the same leg is refused while the first did not fail, and a
// failed payment has nothing to refund.
func TestSettleRefundReceived(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedFull(t)
	paid := buyer.sealPayerLeg(dealID, LegSettled)
	refund := payeeIn(LegSettled)
	refund.Reverses = paid
	got, err := buyer.store.Settle(ctx, dealID, refund)
	require.NoError(t, err)
	b, _ := settleOf(got.Record)
	assert.Equal(t, xdealv1.LegPayee, b.Leg)
	require.NotNil(t, b.Reverses)
	assert.Equal(t, paid, b.Reverses.Digest)

	second := refund
	second.Received = 1000
	_, err = buyer.store.Settle(ctx, dealID, second)
	assertCoded(t, err, CodeSettleRefunded, paid)

	failed := buyer.sealPayerLeg(dealID, LegFailed)
	refund.Reverses = failed
	_, err = buyer.store.Settle(ctx, dealID, refund)
	assert.Equal(t, "leg "+failed+" failed: no money moved, so there is nothing to refund", inputReason(t, err))
}

func TestSettleOnAnOfferOfTwoPaymentRows(t *testing.T) {
	ctx := t.Context()
	twoRows := func(t *testing.T, key string) xdealv1.ProposeBody {
		o := fullOffer(t, key)
		o.TermsRows.Rows[1].Owed = json.RawMessage(`{"amount_minor": 500, "currency": "USD"}`)
		return o
	}
	_, seller, dealID, ba := agreedOn(t, twoRows)
	seller.receiveAgreement(ba)
	_, err := seller.store.Settle(ctx, dealID, payeeIn(LegSettled))
	assertCoded(t, err, CodeSettleWhichRow)
	in := payeeIn(LegSettled)
	in.Row = "paid_as_agreed"
	_, err = seller.store.Settle(ctx, dealID, in)
	require.NoError(t, err)
}

func TestStatusProviderStatus(t *testing.T) {
	ctx := t.Context()
	buyer, _, dealID := agreedFull(t)
	d, err := buyer.store.Deal(ctx, dealID)
	require.NoError(t, err)
	p := &xdealv1.StatusProvider{Shelf: buyer.shelf, OfferPair: buyer.store.OfferPair}
	v, err := p.Status(ctx, d.OwnLogID)
	require.NoError(t, err)
	got, err := buyer.store.Status(ctx, dealID)
	require.NoError(t, err)
	assert.Equal(t, v, got)

	_, err = (&xdealv1.StatusProvider{}).Status(ctx, d.OwnLogID)
	assert.ErrorContains(t, err, "has no shelf")
	_, err = p.Status(ctx, "deal/00000000000000000000000000000000")
	assert.ErrorContains(t, err, "is not an x-deal-v1 deal book")
}
