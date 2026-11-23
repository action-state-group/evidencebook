// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paymentDeal is a started deal between two parties' shelves: the buyer's
// offer sent with its terms leg, and both agreements sealed in their books.
type paymentDeal struct {
	buyer, seller          *party
	offer                  Record
	offerPair              []byte
	offerDigest, capsuleID string
	buyerLog, sellerLog    string
	buyerAg, sellerAg      Record
	buyerAgID, sellerAgID  string
	buyerAgPair            []byte
	sellerAgPair           []byte
	buyerLast, sellerLast  Record
	buyerSeq, sellerSeq    int64
}

func newPaymentDeal(t *testing.T, edit func(*ProposeBody), terms bool) *paymentDeal {
	t.Helper()
	d := &paymentDeal{buyer: newParty(t, "buyer"), seller: newParty(t, "seller"),
		buyerLog: "deal/" + hex.EncodeToString(seed("buyer log")[:16]), sellerLog: "deal/" + hex.EncodeToString(seed("seller log")[:16])}
	ob := fxOffer()
	ob.To = Some(d.seller.key())
	if edit != nil {
		edit(ob)
	}
	d.offer = Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: ob}
	var members map[string]json.RawMessage
	if terms {
		leg, err := TermsLeg(d.offer)
		require.NoError(t, err)
		members = map[string]json.RawMessage{SettlementMember: leg}
	}
	d.offerPair = d.buyer.sendOfferWith(d.offer, members)
	sent, err := VerifySent(d.offerPair, nil)
	require.NoError(t, err)
	d.offerDigest, d.capsuleID = sent.Digest, sent.CapsuleID

	od := RefTo(d.offerDigest)
	sa := fxAgreement(&od, ref('e'), nil, SideAcceptor, RoleSeller, d.buyer.key())
	sa.CheckpointKey, sa.ShelfLogID = d.seller.checkpointKey(), testCadenceLog
	sb := journalBlock(TypeAgree, "2026-11-01T09:45:00Z", true)
	sb.Seq, sb.Seen = 1, NullOf[string]()
	d.sellerAg = Record{Block: sb, Body: sa}
	d.sellerAgID, d.sellerAgPair = d.seller.seal(d.sellerLog, d.sellerAg)

	ba := fxAgreement(&od, ref('e'), ref('f'), SideOfferer, RoleBuyer, d.seller.key())
	ba.CheckpointKey, ba.ShelfLogID = d.buyer.checkpointKey(), testCadenceLog
	bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	bb.Seq, bb.Seen = 1, Some(d.sellerAgID)
	d.buyerAg = Record{Block: bb, Body: ba}
	d.buyerAgID, d.buyerAgPair = d.buyer.seal(d.buyerLog, d.buyerAg)
	d.buyerLast, d.sellerLast, d.buyerSeq, d.sellerSeq = d.buyerAg, d.sellerAg, 1, 1
	return d
}

// leg seals a settle record with its settlement member in the buyer's book
// (buyer true) or the seller's, and returns its book pair.
func (d *paymentDeal) leg(t *testing.T, buyer bool, body *SettleBody, member map[string]any) []byte {
	t.Helper()
	p, log, last, ag, seen, seq := d.seller, d.sellerLog, &d.sellerLast, d.sellerAg, d.buyerAgID, &d.sellerSeq
	if buyer {
		p, log, last, ag, seen, seq = d.buyer, d.buyerLog, &d.buyerLast, d.buyerAg, d.sellerAgID, &d.buyerSeq
	}
	*seq++
	b := journalBlock(TypeSettle, "2026-11-01T10:20:00Z", true)
	pr, ar := RefTo(mustDigest(t, *last)), RefTo(mustDigest(t, ag))
	b.Seq, b.Prev, b.AgreementRef, b.Seen = *seq, &pr, &ar, Some(seen)
	r := Record{Block: b, Body: body}
	require.NoError(t, Check(r))
	raw, err := json.Marshal(member)
	require.NoError(t, err)
	_, pair := p.sealWith(log, r, map[string]json.RawMessage{SettlementMember: raw})
	*last = r
	return pair
}

func usd(v string) map[string]any {
	return map[string]any{"value": v, "assetCode": "USD", "assetScale": 2}
}

var orderRef = map[string]any{"type": "acp.order_id", "value": "order-0001"}

func (d *paymentDeal) payerMember(status, value string) map[string]any {
	return map[string]any{"version": "0", "leg": "payer_observed", "sealer_role": "payer", "terms_ref": d.capsuleID,
		"amount": usd(value), "payment_ref": orderRef, "status": status, "observed_at": "2026-11-01T10:15:00Z"}
}

func (d *paymentDeal) payeeMember(status, value string) map[string]any {
	return map[string]any{"version": "0", "leg": "payee_observed", "sealer_role": "payee", "terms_ref": d.capsuleID,
		"received": usd(value), "receive_fee": usd("0"), "payment_ref": orderRef, "status": status, "observed_at": "2026-11-01T10:18:00Z"}
}

func payerBody() *SettleBody {
	return &SettleBody{Leg: LegPayer, Statements: statements(item("paid_as_agreed", ValueMet)), Check: ref('1')}
}

func payeeBody() *SettleBody {
	return &SettleBody{Leg: LegPayee, Statements: statements(item("paid_as_agreed", ValueMet))}
}

func (d *paymentDeal) check(t *testing.T, legs ...[]byte) PaymentResult {
	t.Helper()
	res, err := PaymentCheck(PaymentInput{Offer: d.offerPair, OfferDigest: d.offerDigest, AcceptorKey: d.seller.key(), Row: "paid_as_agreed", Legs: legs})
	require.NoError(t, err)
	return res
}

func TestTermsLeg(t *testing.T) {
	leg, err := TermsLeg(Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: fxOffer()})
	require.NoError(t, err)
	assert.Equal(t, `{"amount":{"assetCode":"USD","assetScale":2,"value":"6000"},"leg":"terms","sealer_role":"payer","version":"0"}`, string(leg),
		"the buyer offers and owes the payment: it sets the terms as payer")

	seller := fxOffer()
	seller.Role = RoleSeller
	leg, err = TermsLeg(Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: seller})
	require.NoError(t, err)
	assert.Contains(t, string(leg), `"sealer_role":"payee"`, "a seller's offer sets the terms as payee")

	for name, edit := range map[string]func(*ProposeBody){
		"two payment rows": func(o *ProposeBody) {
			o.TermsRows.Rows = append(o.TermsRows.Rows, o.TermsRows.Rows[0])
			o.TermsRows.Rows[len(o.TermsRows.Rows)-1].ID = "deposit"
		},
		"no payment row": func(o *ProposeBody) { o.TermsRows.Rows[0].Recomputed = false },
		"an unknown currency": func(o *ProposeBody) {
			o.TermsRows.Rows[0].Owed = json.RawMessage(`{"amount_minor":6000,"currency":"ZZZ"}`)
		},
		"another member": func(o *ProposeBody) {
			o.TermsRows.Rows[0].Owed = json.RawMessage(`{"amount_minor":6000,"currency":"USD","note":"x"}`)
		},
		"a negative amount": func(o *ProposeBody) {
			o.TermsRows.Rows[0].Owed = json.RawMessage(`{"amount_minor":-1,"currency":"USD"}`)
		},
		"a case alias": func(o *ProposeBody) {
			o.TermsRows.Rows[0].Owed = json.RawMessage(`{"amount_minor":6000,"currency":"USD","Currency":"BTC"}`)
		},
		"another case": func(o *ProposeBody) {
			o.TermsRows.Rows[0].Owed = json.RawMessage(`{"amount_minor":6000,"Currency":"USD"}`)
		},
		"a null amount": func(o *ProposeBody) {
			o.TermsRows.Rows[0].Owed = json.RawMessage(`{"amount_minor":null,"currency":"USD"}`)
		},
		"a null currency": func(o *ProposeBody) {
			o.TermsRows.Rows[0].Owed = json.RawMessage(`{"amount_minor":6000,"currency":null}`)
		},
		"a fractional amount": func(o *ProposeBody) {
			o.TermsRows.Rows[0].Owed = json.RawMessage(`{"amount_minor":60.5,"currency":"USD"}`)
		},
	} {
		o := fxOffer()
		edit(o)
		_, err := TermsLeg(Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: o})
		assert.Error(t, err, name)
	}
	_, err = TermsLeg(Record{Block: journalBlock(TypeAgree, "2026-11-01T09:30:00Z", true), Body: fxAgreement(ref('a'), ref('e'), nil, SideAcceptor, RoleSeller, fxBuyerKey)})
	assert.Error(t, err, "only an offer has a terms leg")
	assert.Equal(t, []string{"paid_as_agreed"}, PaymentRows(fxOffer().TermsRows))
}

// The payment check's states, each from both parties' sealed legs.
func TestPaymentCheck(t *testing.T) {
	for name, c := range map[string]struct {
		payer, payee  []string // status, value; nil for no leg
		check, reason string
	}{
		"settled, the terms' amount": {[]string{"settled", "6000"}, []string{"settled", "6000"}, PaymentPass, ""},
		"pending":                    {[]string{"pending", "6000"}, []string{"pending", "6000"}, "", ReasonPending},
		"failed":                     {[]string{"failed", "6000"}, []string{"failed", "6000"}, PaymentFail, ReasonStatus},
		"reversed":                   {[]string{"reversed", "6000"}, []string{"reversed", "6000"}, PaymentFail, ReasonStatus},
		"another amount":             {[]string{"settled", "5000"}, []string{"settled", "5000"}, PaymentFail, ReasonAmount},
		"the sides differ":           {[]string{"settled", "6000"}, []string{"pending", "6000"}, PaymentFail, ReasonMismatch},
		"the payer's leg alone":      {[]string{"settled", "6000"}, nil, "", ReasonNotAgreed},
		"the payee's leg alone":      {nil, []string{"settled", "6000"}, "", ReasonNotAgreed},
		"no leg":                     {nil, nil, "", ReasonNotAgreed},
	} {
		t.Run(name, func(t *testing.T) {
			d := newPaymentDeal(t, nil, true)
			var legs [][]byte
			if c.payer != nil {
				legs = append(legs, d.leg(t, true, payerBody(), d.payerMember(c.payer[0], c.payer[1])))
			}
			if c.payee != nil {
				legs = append(legs, d.leg(t, false, payeeBody(), d.payeeMember(c.payee[0], c.payee[1])))
			}
			res := d.check(t, legs...)
			assert.Equal(t, c.check, res.Check)
			assert.Equal(t, c.reason, res.Reason)
			require.Len(t, res.CheckedOver, 1+len(legs))
			assert.Equal(t, d.offerDigest, res.CheckedOver[0])
			require.NotNil(t, res.Settlement)
			assert.Equal(t, d.capsuleID, res.Settlement.Terms)
		})
	}
}

// Records other than settle legs, and legs answering another terms leg, are
// passed over; a superseded leg is not read.
func TestPaymentCheckReadsTheLegsThatAnswer(t *testing.T) {
	d := newPaymentDeal(t, nil, true)
	_, sellerAgPair := d.seller.seal("deal/other", d.sellerAg) // a record of no settle leg
	pending := d.leg(t, true, payerBody(), d.payerMember("pending", "6000"))
	pendingRead, err := VerifyBook(pending)
	require.NoError(t, err)
	settledBody := payerBody()
	settledBody.Supersedes = ptr(RefTo(pendingRead.Digest))
	settled := d.leg(t, true, settledBody, d.payerMember("settled", "6000"))
	other := d.payeeMember("settled", "100")
	other["terms_ref"] = hex.EncodeToString(make([]byte, 32))
	elsewhere := d.leg(t, false, payeeBody(), other)
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))

	res := d.check(t, sellerAgPair, pending, settled, elsewhere, payee)
	assert.Equal(t, PaymentPass, res.Check, res.Reason)
	settledRead, err := VerifyBook(settled)
	require.NoError(t, err)
	payeeRead, err := VerifyBook(payee)
	require.NoError(t, err)
	assert.Equal(t, []string{d.offerDigest, settledRead.Digest, payeeRead.Digest}, res.CheckedOver, "the superseded leg is not read")

	// Without the supersedes, the pending and the settled leg are two heads
	// that disagree.
	d2 := newPaymentDeal(t, nil, true)
	a := d2.leg(t, true, payerBody(), d2.payerMember("pending", "6000"))
	b := d2.leg(t, true, payerBody(), d2.payerMember("settled", "6000"))
	res = d2.check(t, a, b, d2.leg(t, false, payeeBody(), d2.payeeMember("settled", "6000")))
	assert.Equal(t, PaymentFail, res.Check)
	assert.Equal(t, ReasonMismatch, res.Reason)
}

func ptr[T any](v T) *T { return &v }

// The check runs on the paid row's own payment only (Sherlock2 and its twin
// on the 4a door): several payment rows leave it not run, and a refund or
// remedy leg citing the offer's terms leg is not checked while the payment's
// own legs are (Steven, evidencebook#722).
func TestPaymentCheckNotRun(t *testing.T) {
	two := func(o *ProposeBody) {
		o.TermsRows.Rows = append(o.TermsRows.Rows, o.TermsRows.Rows[0])
		o.TermsRows.Rows[len(o.TermsRows.Rows)-1].ID = "balance"
	}
	d := newPaymentDeal(t, two, false)
	res := d.check(t)
	assert.Equal(t, ReasonSeveralPayments, res.Reason)
	assert.Empty(t, res.Check)

	d = newPaymentDeal(t, nil, true)
	payer := d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
	payerRead, err := VerifyBook(payer)
	require.NoError(t, err)
	// The seller refunds: it pays, so its leg is a payer leg, citing the
	// offer's terms leg as if the price were the refund's.
	refund := &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('2'), Reverses: ptr(RefTo(payerRead.Digest))}
	m := d.payerMember("settled", "6000")
	refundPair := d.leg(t, false, refund, m)
	refundRead, err := VerifyBook(refundPair)
	require.NoError(t, err)
	res = d.check(t, payer, payee, refundPair)
	assert.Equal(t, PaymentPass, res.Check, "a passing payment passes beside its refund: %s", res.Reason)
	assert.Equal(t, []string{refundRead.Digest}, res.NotChecked, "the refund leg is not checked")
	assert.NotContains(t, res.CheckedOver, refundRead.Digest)

	remedy := &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('3'), RemedyFor: &RemedyFor{Row: "as_described"}}
	remedyPair := d.leg(t, false, remedy, d.payerMember("settled", "6000"))
	remedyRead, err := VerifyBook(remedyPair)
	require.NoError(t, err)
	res = d.check(t, payer, payee, remedyPair)
	assert.Equal(t, PaymentPass, res.Check)
	assert.Equal(t, []string{remedyRead.Digest}, res.NotChecked)

	// The hole #722 closes: a payer who paid less than the terms seals a
	// small refund citing the offer; the payment still fails.
	dl := newPaymentDeal(t, nil, true)
	short := dl.leg(t, true, payerBody(), dl.payerMember("settled", "5000"))
	shortPayee := dl.leg(t, false, payeeBody(), dl.payeeMember("settled", "5000"))
	shortRead, err := VerifyBook(short)
	require.NoError(t, err)
	tiny := dl.leg(t, false, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('2'), Reverses: ptr(RefTo(shortRead.Digest))}, dl.payerMember("settled", "1"))
	res = dl.check(t, short, shortPayee, tiny)
	assert.Equal(t, PaymentFail, res.Check, "a refund never hides a failing payment")
	assert.Equal(t, ReasonAmount, res.Reason)
	assert.Len(t, res.NotChecked, 1)

	// Nor by superseding the short payment leg: a refund, or a payer leg
	// of another terms leg, replaces no payment leg (review of #757).
	viaRefund := dl.leg(t, true, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('2'), Reverses: ptr(RefTo(shortRead.Digest)), Supersedes: ptr(RefTo(shortRead.Digest))}, dl.payerMember("settled", "1"))
	res = dl.check(t, short, shortPayee, viaRefund)
	assert.Equal(t, PaymentFail, res.Check, "a refund superseding the payment leg does not hide it")
	assert.Equal(t, ReasonAmount, res.Reason)
	other := dl.payerMember("settled", "6000")
	other["terms_ref"] = hex.EncodeToString(make([]byte, 32))
	viaOther := dl.leg(t, true, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('4'), Supersedes: ptr(RefTo(shortRead.Digest))}, other)
	res = dl.check(t, short, shortPayee, viaOther)
	assert.Equal(t, PaymentFail, res.Check, "a leg of another terms leg does not supersede the payment leg")
	// Nor by a later payer leg AAC's verifier refuses (a status it does
	// not know): it takes no part, so it replaces nothing.
	malformed := dl.payerMember("done", "6000")
	viaMalformed := dl.leg(t, true, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('5'), Supersedes: ptr(RefTo(shortRead.Digest))}, malformed)
	res = dl.check(t, short, shortPayee, viaMalformed)
	assert.Equal(t, PaymentFail, res.Check, "a refused leg does not supersede the payment leg")
	assert.Contains(t, res.Excluded, digestOf(t, viaMalformed))
	// A later payment leg of this terms leg still replaces it.
	fixed := dl.leg(t, true, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('1'), Supersedes: ptr(RefTo(shortRead.Digest))}, dl.payerMember("settled", "5000"))
	res = dl.check(t, short, shortPayee, fixed)
	assert.Equal(t, []string{dl.offerDigest, digestOf(t, shortPayee), digestOf(t, fixed)}, res.CheckedOver, "the payment leg it supersedes is not read (legs in the order given)")

	// A refund leg its side later superseded is not read.
	d2 := newPaymentDeal(t, nil, true)
	p2 := d2.leg(t, true, payerBody(), d2.payerMember("settled", "6000"))
	q2 := d2.leg(t, false, payeeBody(), d2.payeeMember("settled", "6000"))
	p2Read, err := VerifyBook(p2)
	require.NoError(t, err)
	stale := d2.leg(t, false, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('2'), Reverses: ptr(RefTo(p2Read.Digest))}, d2.payerMember("pending", "6000"))
	staleRead, err := VerifyBook(stale)
	require.NoError(t, err)
	own2 := d2.payerMember("failed", "6000")
	own2["terms_ref"] = hex.EncodeToString(make([]byte, 32))
	replaced := d2.leg(t, false, &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('2'), Reverses: ptr(RefTo(p2Read.Digest)), Supersedes: ptr(RefTo(staleRead.Digest))}, own2)
	res = d2.check(t, p2, q2, stale, replaced)
	assert.Equal(t, PaymentPass, res.Check, "%s", res.Reason)
	assert.Empty(t, res.NotChecked, "a superseded refund leg is not listed")

	// A refund under its own terms leg is passed over.
	own := d.payerMember("settled", "6000")
	own["terms_ref"] = hex.EncodeToString(make([]byte, 32))
	res = d.check(t, payer, payee, d.leg(t, false, refund, own))
	assert.Equal(t, PaymentPass, res.Check)
	assert.Empty(t, res.NotChecked, "it answers another terms leg")
}

func TestPaymentCheckRefuses(t *testing.T) {
	d := newPaymentDeal(t, nil, true)
	payer := d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
	base := PaymentInput{Offer: d.offerPair, OfferDigest: d.offerDigest, AcceptorKey: d.seller.key(), Row: "paid_as_agreed", Legs: [][]byte{payer, payee}}

	stranger := newParty(t, "stranger")
	strangerLeg := func() []byte {
		stranger.seal("deal/stranger", d.sellerAg)
		b := journalBlock(TypeSettle, "2026-11-01T10:20:00Z", true)
		pr, ar := RefTo(mustDigest(t, d.sellerAg)), RefTo(mustDigest(t, d.sellerAg))
		b.Seq, b.Prev, b.AgreementRef, b.Seen = 2, &pr, &ar, Some(d.buyerAgID)
		_, pair := stranger.sealWith("deal/stranger", Record{Block: b, Body: payeeBody()},
			map[string]json.RawMessage{SettlementMember: mustJSON(t, d.payeeMember("settled", "6000"))})
		return pair
	}()
	_, err := VerifyBook(strangerLeg)
	require.NoError(t, err, "the stranger's leg verifies on its own")
	payerAsSeller := d.leg(t, false, payerBody(), d.payerMember("settled", "6000"))
	wrongMember := d.payerMember("settled", "6000")
	wrongMember["leg"] = "payee_observed"
	wrongMember["sealer_role"] = "payee"
	delete(wrongMember, "amount")
	wrongMember["received"], wrongMember["receive_fee"] = usd("6000"), usd("0")
	payerWithPayeeMember := d.leg(t, true, payerBody(), wrongMember)
	noMember := func() []byte {
		_, pair := d.buyer.seal(d.buyerLog, func() Record {
			d.buyerSeq++
			b := journalBlock(TypeSettle, "2026-11-01T10:20:00Z", true)
			pr, ar := RefTo(mustDigest(t, d.buyerLast)), RefTo(mustDigest(t, d.buyerAg))
			b.Seq, b.Prev, b.AgreementRef, b.Seen = d.buyerSeq, &pr, &ar, Some(d.sellerAgID)
			r := Record{Block: b, Body: payerBody()}
			d.buyerLast = r
			return r
		}())
		return pair
	}()

	for name, edit := range map[string]func(*PaymentInput){
		"another offer digest":     func(in *PaymentInput) { in.OfferDigest = hex.EncodeToString(make([]byte, 32)) },
		"not a sent pair":          func(in *PaymentInput) { in.Offer = payer },
		"another acceptor":         func(in *PaymentInput) { in.AcceptorKey = stranger.key() },
		"the offerer as acceptor":  func(in *PaymentInput) { in.AcceptorKey = d.buyer.key() },
		"not a payment row":        func(in *PaymentInput) { in.Row = "delivered" },
		"no such row":              func(in *PaymentInput) { in.Row = "nothing" },
		"a leg that is not a pair": func(in *PaymentInput) { in.Legs = append(in.Legs, []byte(`{}`)) },
		"a stranger's leg":         func(in *PaymentInput) { in.Legs = append(in.Legs, strangerLeg) },
	} {
		in := base
		in.Legs = append([][]byte{}, base.Legs...)
		edit(&in)
		_, err := PaymentCheck(in)
		var se *StageError
		assert.Error(t, err, name)
		if name != "not a sent pair" && name != "a leg that is not a pair" {
			require.True(t, errors.As(err, &se), "%s: %v", name, err)
			assert.Equal(t, StagePayment, se.Stage, name)
		}
	}
	_, err = PaymentCheck(base)
	require.NoError(t, err, "the base input runs")

	// A leg's own content is the counterparty's to get wrong: such a leg
	// takes no part, and the check reads the others.
	malformed := d.payeeMember("settled", "6000")
	malformed["status"] = "done"
	badMember := d.leg(t, false, payeeBody(), malformed)
	for name, leg := range map[string][]byte{
		"a payer leg by the payee":    payerAsSeller,
		"a payer leg as payee":        payerWithPayeeMember,
		"a settle with no settlement": noMember,
		"a malformed member":          badMember,
	} {
		in := base
		in.Legs = append([][]byte{}, base.Legs...)
		in.Legs = append(in.Legs, leg)
		res, err := PaymentCheck(in)
		require.NoError(t, err, name)
		assert.Equal(t, PaymentPass, res.Check, name)
		read, err := VerifyBook(leg)
		require.NoError(t, err)
		assert.Equal(t, []string{read.Digest}, res.Excluded, name)
		assert.NotContains(t, res.CheckedOver, read.Digest, name)
	}
	// With the payee's only leg malformed, the check is not run.
	res, err := PaymentCheck(PaymentInput{Offer: d.offerPair, OfferDigest: d.offerDigest, AcceptorKey: d.seller.key(), Row: "paid_as_agreed", Legs: [][]byte{payer, badMember}})
	require.NoError(t, err)
	assert.Empty(t, res.Check)
	assert.Equal(t, ReasonNotAgreed, res.Reason)
}

// Legs AAC cannot join (another payment_ref type) are not run, as unjoined.
func TestPaymentCheckUnjoined(t *testing.T) {
	d := newPaymentDeal(t, nil, true)
	payee := d.payeeMember("settled", "6000")
	payee["payment_ref"] = map[string]any{"type": "ucp.order_id", "value": "order-0001"}
	res := d.check(t, d.leg(t, true, payerBody(), d.payerMember("settled", "6000")), d.leg(t, false, payeeBody(), payee))
	assert.Empty(t, res.Check)
	assert.Equal(t, ReasonUnjoined, res.Reason)
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

// A sent Capsule's settlement member is the offer's terms leg exactly, and
// only an offer carries one.
func TestVerifySentChecksTheTermsLeg(t *testing.T) {
	d := newPaymentDeal(t, nil, true)
	_, err := VerifySent(d.offerPair, nil)
	require.NoError(t, err)

	leg, err := TermsLeg(d.offer)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(leg, &m))
	for name, edit := range map[string]func(map[string]any){
		"another amount":   func(m map[string]any) { m["amount"] = usd("5000") },
		"another role":     func(m map[string]any) { m["sealer_role"] = "payee" },
		"another leg":      func(m map[string]any) { m["leg"] = "payer_observed" },
		"a further member": func(m map[string]any) { m["valid_until"] = "2026-11-02T00:00:00Z" },
	} {
		c := map[string]any{}
		for k, v := range m {
			c[k] = v
		}
		edit(c)
		pair := d.buyer.sendOfferWith(d.offer, map[string]json.RawMessage{SettlementMember: mustJSON(t, c)})
		_, err := VerifySent(pair, nil)
		assert.Error(t, err, name)
	}

	// A decline carries none.
	decline := Record{Block: journalBlock(TypeAgree, "2026-11-01T09:40:00Z", true),
		Body: &AgreeBody{Kind: KindDecline, Offer: RefTo(d.offerDigest)}}
	require.NoError(t, Check(decline))
	pair := d.seller.sendOfferWith(decline, map[string]json.RawMessage{SettlementMember: leg})
	_, err = VerifySent(pair, nil)
	assert.ErrorContains(t, err, "carries no settlement member", "a decline carries no settlement member")
}

// chained reseals a leg's header as a Capsule with a supersedes chain to
// parent, signed by the same key: a record a deal book never seals.
func chained(t *testing.T, name string, pair []byte, parent string) []byte {
	t.Helper()
	var p struct {
		Record  map[string]json.RawMessage `json:"record"`
		Header  json.RawMessage            `json:"header"`
		Payload json.RawMessage            `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(pair, &p))
	var c struct {
		ActionID   string `json:"action_id"`
		Operator   string `json:"operator"`
		Developer  string `json:"developer"`
		Timestamp  string `json:"timestamp"`
		References []struct {
			Type      string `json:"type"`
			DigestAlg string `json:"digest_alg"`
			Digest    string `json:"digest"`
		} `json:"references"`
	}
	raw, err := json.Marshal(p.Record)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &c))
	at, err := time.Parse(time.RFC3339Nano, c.Timestamp)
	require.NoError(t, err)
	var refs []emit.Reference
	for _, r := range c.References {
		refs = append(refs, emit.Reference{Type: r.Type, DigestAlg: r.DigestAlg, Digest: r.Digest})
	}
	id, err := emit.NewEd25519SigningIdentity(seed(name + " record key"))
	require.NoError(t, err)
	sealed, err := emit.Seal(emit.SealInput{Capsule: emit.Input{ActionID: c.ActionID, ActionType: emit.ActionTypeFYI,
		Operator: c.Operator, Developer: c.Developer, Timestamp: at, References: refs,
		Chain:      &emit.Chain{ParentCapsuleID: parent, Relation: emit.ChainSupersedes},
		Extensions: []emit.Extension{{Name: SettlementMember, Value: p.Record[SettlementMember]}}},
		Payload: p.Header, Identity: id})
	require.NoError(t, err)
	signed, err := signedCapsule(sealed.Payload, sealed.Envelope)
	require.NoError(t, err)
	out, err := json.Marshal(map[string]json.RawMessage{"record": signed, "header": p.Header, "payload": p.Payload})
	require.NoError(t, err)
	return out
}

// A leg whose Capsule carries a chain is refused: AAC's verifier would drop
// the leg its supersedes names, outside the deal's own supersedes rule, and
// turn two disagreeing payer legs into a pass.
func TestPaymentCheckRefusesAChainedLeg(t *testing.T) {
	d := newPaymentDeal(t, nil, true)
	pending := d.leg(t, true, payerBody(), d.payerMember("pending", "6000"))
	pendingRead, err := VerifyBook(pending)
	require.NoError(t, err)
	// A record between, so the chain's parent is not also the record cited.
	d.buyerSeq++
	sb := journalBlock(TypeStatement, "2026-11-01T10:19:00Z", true)
	pr, ar := RefTo(mustDigest(t, d.buyerLast)), RefTo(mustDigest(t, d.buyerAg))
	sb.Seq, sb.Prev, sb.AgreementRef, sb.Seen = d.buyerSeq, &pr, &ar, Some(d.sellerAgID)
	between := Record{Block: sb, Body: &StatementBody{Statements: statements(item("delivered", ValueMet))}}
	require.NoError(t, Check(between))
	d.buyer.seal(d.buyerLog, between)
	d.buyerLast = between
	settled := chained(t, "buyer", d.leg(t, true, payerBody(), d.payerMember("settled", "6000")), pendingRead.RecordID)
	_, err = VerifyBook(settled)
	require.NoError(t, err, "the chained leg verifies on its own")
	payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
	_, err = PaymentCheck(PaymentInput{Offer: d.offerPair, OfferDigest: d.offerDigest, AcceptorKey: d.seller.key(), Row: "paid_as_agreed",
		Legs: [][]byte{pending, settled, payee}})
	assert.ErrorContains(t, err, "carries a chain")
}

func digestOf(t *testing.T, pair []byte) string {
	t.Helper()
	r, err := VerifyBook(pair)
	require.NoError(t, err)
	return r.Digest
}
