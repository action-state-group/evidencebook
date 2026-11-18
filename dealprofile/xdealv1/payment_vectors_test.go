// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const paymentDir = "../../testdata/deal-v1/payment"

// paymentVectorInput is a PaymentInput as the vectors write it.
type paymentVectorInput struct {
	Offer       json.RawMessage   `json:"offer"`
	OfferDigest string            `json:"offer_digest"`
	AcceptorKey string            `json:"acceptor_key"`
	Row         string            `json:"row"`
	Legs        []json.RawMessage `json:"legs"`
}

type paymentVectorCase struct {
	Name  string             `json:"name"`
	Input paymentVectorInput `json:"input"`
	// Result is PaymentResult's JCS when the check returns one; Stage the
	// refusal's stage ("" for an error that is no StageError).
	Result  json.RawMessage `json:"result,omitempty"`
	Refused bool            `json:"refused,omitempty"`
	Stage   string          `json:"stage,omitempty"`
}

type bookVectorCase struct {
	Name  string          `json:"name"`
	Pair  json.RawMessage `json:"pair"`
	Valid bool            `json:"valid"`
	Stage string          `json:"stage,omitempty"`
	// What a valid pair reads as.
	Digest   string `json:"digest,omitempty"`
	RecordID string `json:"record_id,omitempty"`
	KeyID    string `json:"key_id,omitempty"`
	LogID    string `json:"log_id,omitempty"`
	Seq      uint64 `json:"seq,omitempty"`
	Prev     string `json:"prev,omitempty"`
}

func paymentVectorOf(t *testing.T, name string, in PaymentInput) paymentVectorCase {
	t.Helper()
	c := paymentVectorCase{Name: name, Input: paymentVectorInput{Offer: in.Offer, OfferDigest: in.OfferDigest,
		AcceptorKey: in.AcceptorKey, Row: in.Row, Legs: []json.RawMessage{}}}
	for _, l := range in.Legs {
		c.Input.Legs = append(c.Input.Legs, json.RawMessage(l))
	}
	res, err := PaymentCheck(in)
	if err != nil {
		c.Refused = true
		var se *StageError
		if errors.As(err, &se) {
			c.Stage = se.Stage
		}
		return c
	}
	c.Result, err = jcsJSON(res)
	require.NoError(t, err)
	return c
}

func bookVectorOf(name string, pair []byte) bookVectorCase {
	c := bookVectorCase{Name: name, Pair: pair}
	b, err := VerifyBook(pair)
	if err != nil {
		var se *StageError
		if errors.As(err, &se) {
			c.Stage = se.Stage
		}
		return c
	}
	c.Valid, c.Digest, c.RecordID, c.KeyID, c.LogID, c.Seq, c.Prev = true, b.Digest, b.RecordID, b.KeyID, b.LogID, b.Seq, b.Prev
	return c
}

// The payment check and book pair vectors (design §6.3, §8.3): the cases of
// TestPaymentCheck, TestPaymentCheckReadsTheLegsThatAnswer,
// TestPaymentCheckNotRun, TestPaymentCheckRefuses, TestPaymentCheckUnjoined
// and TestPaymentCheckRefusesAChainedLeg, with their inputs as sent, and
// VerifyBook on every leg and on the refusals of TestBookPairVerifies and
// TestBookPairWithTwoPayloads. Written to testdata/deal-v1/payment
// (EB_WRITE_DEAL_V1_VECTORS=1), and replayed by the Rust deal::v1 tests.
func TestPaymentVectors(t *testing.T) {
	var cases []paymentVectorCase
	var books []bookVectorCase
	add := func(name string, d *paymentDeal, legs ...[]byte) {
		t.Helper()
		in := PaymentInput{Offer: d.offerPair, OfferDigest: d.offerDigest, AcceptorKey: d.seller.key(), Row: "paid_as_agreed", Legs: legs}
		cases = append(cases, paymentVectorOf(t, name, in))
		for i, l := range legs {
			books = append(books, bookVectorOf(name+"/leg-"+string(rune('0'+i)), l))
		}
	}

	for _, c := range []struct {
		name         string
		payer, payee []string
	}{
		{"settled-the-terms-amount", []string{"settled", "6000"}, []string{"settled", "6000"}},
		{"pending", []string{"pending", "6000"}, []string{"pending", "6000"}},
		{"failed", []string{"failed", "6000"}, []string{"failed", "6000"}},
		{"reversed", []string{"reversed", "6000"}, []string{"reversed", "6000"}},
		{"another-amount", []string{"settled", "5000"}, []string{"settled", "5000"}},
		{"the-sides-differ", []string{"settled", "6000"}, []string{"pending", "6000"}},
		{"the-payers-leg-alone", []string{"settled", "6000"}, nil},
		{"the-payees-leg-alone", nil, []string{"settled", "6000"}},
		{"no-leg", nil, nil},
	} {
		d := newPaymentDeal(t, nil, true)
		var legs [][]byte
		if c.payer != nil {
			legs = append(legs, d.leg(t, true, payerBody(), d.payerMember(c.payer[0], c.payer[1])))
		}
		if c.payee != nil {
			legs = append(legs, d.leg(t, false, payeeBody(), d.payeeMember(c.payee[0], c.payee[1])))
		}
		add(c.name, d, legs...)
	}

	// The legs that answer: a record of no settle leg, a superseded leg, and
	// a leg answering another terms leg are passed over.
	{
		d := newPaymentDeal(t, nil, true)
		_, sellerAgPair := d.seller.seal("deal/other", d.sellerAg)
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
		add("superseded-and-other-terms-passed-over", d, sellerAgPair, pending, settled, elsewhere, payee)

		d2 := newPaymentDeal(t, nil, true)
		a := d2.leg(t, true, payerBody(), d2.payerMember("pending", "6000"))
		b := d2.leg(t, true, payerBody(), d2.payerMember("settled", "6000"))
		add("two-payer-heads-mismatch", d2, a, b, d2.leg(t, false, payeeBody(), d2.payeeMember("settled", "6000")))
	}

	// Not run: several payment rows; a refund or a remedy citing the offer.
	{
		two := func(o *ProposeBody) {
			o.TermsRows.Rows = append(o.TermsRows.Rows, o.TermsRows.Rows[0])
			o.TermsRows.Rows[len(o.TermsRows.Rows)-1].ID = "balance"
		}
		add("several-payments", newPaymentDeal(t, two, false))

		d := newPaymentDeal(t, nil, true)
		payer := d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
		payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
		payerRead, err := VerifyBook(payer)
		require.NoError(t, err)
		refund := &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('2'), Reverses: ptr(RefTo(payerRead.Digest))}
		add("refund-cites-offer", d, payer, payee, d.leg(t, false, refund, d.payerMember("settled", "6000")))
		remedy := &SettleBody{Leg: LegPayer, Statements: statements(), Check: ref('3'), RemedyFor: &RemedyFor{Row: "as_described"}}
		add("remedy-cites-offer", d, payer, payee, d.leg(t, false, remedy, d.payerMember("settled", "6000")))
		own := d.payerMember("settled", "6000")
		own["terms_ref"] = hex.EncodeToString(make([]byte, 32))
		add("refund-under-its-own-terms", d, payer, payee, d.leg(t, false, refund, own))

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
		add("superseded-refund-not-read", d2, p2, q2, stale, replaced)
	}

	// Refusals, and legs that take no part.
	{
		d := newPaymentDeal(t, nil, true)
		payer := d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
		payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
		base := PaymentInput{Offer: d.offerPair, OfferDigest: d.offerDigest, AcceptorKey: d.seller.key(), Row: "paid_as_agreed", Legs: [][]byte{payer, payee}}
		stranger := newParty(t, "stranger")
		stranger.seal("deal/stranger", d.sellerAg)
		sb := journalBlock(TypeSettle, "2026-11-01T10:20:00Z", true)
		pr, ar := RefTo(mustDigest(t, d.sellerAg)), RefTo(mustDigest(t, d.sellerAg))
		sb.Seq, sb.Prev, sb.AgreementRef, sb.Seen = 2, &pr, &ar, Some(d.buyerAgID)
		_, strangerLeg := stranger.sealWith("deal/stranger", Record{Block: sb, Body: payeeBody()},
			map[string]json.RawMessage{SettlementMember: mustJSON(t, d.payeeMember("settled", "6000"))})
		for _, c := range []struct {
			name string
			edit func(*PaymentInput)
		}{
			{"refused/another-offer-digest", func(in *PaymentInput) { in.OfferDigest = hex.EncodeToString(make([]byte, 32)) }},
			{"refused/not-a-sent-pair", func(in *PaymentInput) { in.Offer = payer }},
			{"refused/another-acceptor", func(in *PaymentInput) { in.AcceptorKey = stranger.key() }},
			{"refused/the-offerer-as-acceptor", func(in *PaymentInput) { in.AcceptorKey = d.buyer.key() }},
			{"refused/not-a-payment-row", func(in *PaymentInput) { in.Row = "delivered" }},
			{"refused/no-such-row", func(in *PaymentInput) { in.Row = "nothing" }},
			{"refused/a-leg-that-is-not-a-pair", func(in *PaymentInput) { in.Legs = append(in.Legs, []byte(`{}`)) }},
			{"refused/a-strangers-leg", func(in *PaymentInput) { in.Legs = append(in.Legs, strangerLeg) }},
		} {
			in := base
			in.Legs = append([][]byte{}, base.Legs...)
			c.edit(&in)
			cases = append(cases, paymentVectorOf(t, c.name, in))
		}
		cases = append(cases, paymentVectorOf(t, "base", base))

		payerAsSeller := d.leg(t, false, payerBody(), d.payerMember("settled", "6000"))
		wrongMember := d.payerMember("settled", "6000")
		wrongMember["leg"], wrongMember["sealer_role"] = "payee_observed", "payee"
		delete(wrongMember, "amount")
		wrongMember["received"], wrongMember["receive_fee"] = usd("6000"), usd("0")
		payerWithPayeeMember := d.leg(t, true, payerBody(), wrongMember)
		d.buyerSeq++
		nb := journalBlock(TypeSettle, "2026-11-01T10:20:00Z", true)
		npr, nar := RefTo(mustDigest(t, d.buyerLast)), RefTo(mustDigest(t, d.buyerAg))
		nb.Seq, nb.Prev, nb.AgreementRef, nb.Seen = d.buyerSeq, &npr, &nar, Some(d.sellerAgID)
		nr := Record{Block: nb, Body: payerBody()}
		d.buyerLast = nr
		_, noMember := d.buyer.seal(d.buyerLog, nr)
		malformed := d.payeeMember("settled", "6000")
		malformed["status"] = "done"
		badMember := d.leg(t, false, payeeBody(), malformed)
		for _, c := range []struct {
			name string
			leg  []byte
		}{
			{"excluded/a-payer-leg-by-the-payee", payerAsSeller},
			{"excluded/a-payer-leg-as-payee", payerWithPayeeMember},
			{"excluded/a-settle-with-no-settlement", noMember},
			{"excluded/a-malformed-member", badMember},
		} {
			add(c.name, d, payer, payee, c.leg)
		}
		add("excluded/the-payees-only-leg-malformed", d, payer, badMember)
		books = append(books, bookVectorOf("a-strangers-leg", strangerLeg), bookVectorOf("a-sent-pair", d.offerPair))
	}

	{
		d := newPaymentDeal(t, nil, true)
		payee := d.payeeMember("settled", "6000")
		payee["payment_ref"] = map[string]any{"type": "ucp.order_id", "value": "order-0001"}
		add("unjoined", d, d.leg(t, true, payerBody(), d.payerMember("settled", "6000")), d.leg(t, false, payeeBody(), payee))
	}

	{
		d := newPaymentDeal(t, nil, true)
		pending := d.leg(t, true, payerBody(), d.payerMember("pending", "6000"))
		pendingRead, err := VerifyBook(pending)
		require.NoError(t, err)
		d.buyerSeq++
		sb := journalBlock(TypeStatement, "2026-11-01T10:19:00Z", true)
		pr, ar := RefTo(mustDigest(t, d.buyerLast)), RefTo(mustDigest(t, d.buyerAg))
		sb.Seq, sb.Prev, sb.AgreementRef, sb.Seen = d.buyerSeq, &pr, &ar, Some(d.sellerAgID)
		between := Record{Block: sb, Body: &StatementBody{Statements: statements(item("delivered", ValueMet))}}
		require.NoError(t, Check(between))
		d.buyer.seal(d.buyerLog, between)
		d.buyerLast = between
		settled := chained(t, "buyer", d.leg(t, true, payerBody(), d.payerMember("settled", "6000")), pendingRead.RecordID)
		payee := d.leg(t, false, payeeBody(), d.payeeMember("settled", "6000"))
		add("refused/a-chained-leg", d, pending, settled, payee)
	}

	// Book pairs refused on their own.
	{
		h := newHandshake(t, nil)
		books = append(books, bookVectorOf("agreement", h.agreementPair))
		tamper := func(f func(m map[string]json.RawMessage)) []byte {
			var m map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(h.agreementPair, &m))
			f(m)
			out, err := json.Marshal(m)
			require.NoError(t, err)
			return out
		}
		books = append(books,
			bookVectorOf("another-payload", tamper(func(m map[string]json.RawMessage) { m["payload"] = jcsOf(t, h.offer) })),
			bookVectorOf("another-header", tamper(func(m map[string]json.RawMessage) {
				var hd map[string]any
				require.NoError(t, json.Unmarshal(m["header"], &hd))
				hd["seq"] = 2
				m["header"], _ = json.Marshal(hd)
			})),
			bookVectorOf("no-signature", tamper(func(m map[string]json.RawMessage) {
				var rec map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(m["record"], &rec))
				delete(rec, "signature")
				m["record"], _ = json.Marshal(rec)
			})),
			bookVectorOf("a-further-member", tamper(func(m map[string]json.RawMessage) { m["extra"] = json.RawMessage(`1`) })),
		)
		seller := newParty(t, "seller")
		book, err := seller.shelf.Open(t.Context(), "deal/"+hex.EncodeToString(seed("two payloads")[:16]))
		require.NoError(t, err)
		e := entryOf(t, h.agreement)
		e.Payloads = append(e.Payloads, jcsOf(t, h.offer))
		rec, err := book.Append(t.Context(), e)
		require.NoError(t, err)
		sealed, err := book.Sealed(t.Context(), rec.RecordID)
		require.NoError(t, err)
		pair, err := BookPair(sealed, jcsOf(t, h.agreement))
		require.NoError(t, err)
		books = append(books, bookVectorOf("two-payloads", pair))
	}

	got, err := json.MarshalIndent(map[string]any{
		"description": "x-deal-v1 payment check and book pair vectors (evidencebook#461, docs/design/461-deal-profile.md §6.3, §8.3). payment: PaymentCheck(input) returns result (PaymentResult's JCS) or refuses (refused, at stage when it is a stage error); an input's offer is a sent pair, each leg a book pair {record, header, payload}. book: VerifyBook(pair) reads a valid pair as the digest, record_id, key_id, log_id, seq and prev given, and refuses the others at the stage given. Written by dealprofile/xdealv1's TestPaymentVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"payment":     cases,
		"book":        books,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(paymentDir, "vectors.json")
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(paymentDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "payment/vectors.json differs from this build's; regenerate only for an intended change")
}
