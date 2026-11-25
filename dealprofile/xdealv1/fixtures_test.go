// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The x-deal-v1 fixtures (design §11.1, PR 2; §11.2, Records): Appendix A's
// deal, record by record, as positive fixtures in
// dealprofile/x-deal-v1/fixtures, and one negative for each record check of
// design §3. TestFixtures builds them here, checks every one, and compares
// them with the files byte for byte; EB_WRITE_DEAL_V1_FIXTURES=1 writes them.

const fixturesDir = "../x-deal-v1/fixtures"

// label is a fixed 64-hex value derived from a name, for the keys, packs and
// commitments the fixtures name but do not open.
func label(name string) string {
	s := sha256.Sum256([]byte("x-deal-v1 fixtures\x00" + name))
	return hex.EncodeToString(s[:])
}

// recordID stands in for the record id (the Capsule id of the sealed book
// record, design §3.1) of the book record whose record digest is d. The
// fixtures seal no Capsules, so seen and a ruling's read hold these.
func recordID(d string) string { return label("record id of " + d) }

const (
	fxDealID   = "deal-6a1f0c93b27d4e58"
	fxForum    = ForumAutoRef
	fxCurrency = "USD"
)

var (
	fxBuyerKey       = label("buyer record key")
	fxSellerKey      = label("seller record key")
	fxAdjudicatorKey = label("adjudicator key")
	fxWitness        = Witness{KeyID: label("witness key"), URL: "https://witness.example"}
	fxAdjudicator    = Adjudicator{KeyID: fxAdjudicatorKey, RulesDigest: label("adjudicator rules"), RulesURL: "https://adjudicator.example/rules"}
)

// fixture is one positive fixture: a record of Appendix A's deal.
type fixture struct {
	Path   string // under fixturesDir
	Line   string // Appendix A's line
	Story  string
	Record Record
	// Context is what CheckSealed needs for a journal record.
	Context *SealContext
}

func (f fixture) digest(t *testing.T) string {
	t.Helper()
	d, err := Digest(f.Record)
	require.NoError(t, err, f.Path)
	return d
}

// appendixA is the deal of Appendix A, record by record, and the
// settled-without-ruling path, keyed by path.
type appendixA struct {
	t        *testing.T
	list     []fixture
	byPath   map[string]*fixture
	digestOf map[string]string
}

func (a *appendixA) add(f fixture) Ref {
	a.t.Helper()
	a.list = append(a.list, f)
	a.byPath[f.Path] = &a.list[len(a.list)-1]
	d := f.digest(a.t)
	a.digestOf[f.Path] = d
	return RefTo(d)
}

func (a *appendixA) ref(path string) *Ref {
	d, ok := a.digestOf[path]
	require.True(a.t, ok, path)
	r := RefTo(d)
	return &r
}

func (a *appendixA) id(path string) string { return recordID(a.ref(path).Digest) }

func (a *appendixA) record(path string) Record { return a.byPath[path].Record }

func journalBlock(t, at string, deal bool) Block {
	b := Block{Profile: Profile, Canonicalization: Canonicalization, RecordType: t, At: at}
	if deal {
		b.DealID = fxDealID
	}
	return b
}

// bookBlock is a deal-book block: seq, prev (the book's previous record),
// agreement_ref and seen (a record id, or null).
func (a *appendixA) bookBlock(book, t, at string, seq int64, seen string) Block {
	b := journalBlock(t, at, true)
	b.Seq = seq
	if seen == "" {
		b.Seen = NullOf[string]()
	} else {
		b.Seen = Some(seen)
	}
	if seq > 1 {
		b.Prev = a.ref(a.bookPath(book, seq-1))
		b.AgreementRef = a.ref(a.bookPath(book, 1))
	}
	return b
}

// bookPath is the fixture path of a book's record at seq. The settled path
// shares the main path's records before it leaves the table.
func (a *appendixA) bookPath(book string, seq int64) string {
	prefix := fmt.Sprintf("%s/%02d-", book, seq)
	for _, f := range a.list {
		if strings.HasPrefix(f.Path, prefix) {
			return f.Path
		}
	}
	if b, ok := strings.CutPrefix(book, "settled/"); ok {
		return a.bookPath("appendix-a/"+b, seq)
	}
	a.t.Fatalf("no record at %s", prefix)
	return ""
}

func statements(items ...StatementItem) []StatementItem {
	if items == nil {
		return []StatementItem{}
	}
	return items
}

func item(row, value string) StatementItem { return StatementItem{Row: row, Value: value} }

func amount(n int64) *int64 { return &n }

// fxOffer is the buyer's offer of line 11: the three rows of design §4.2
// with the dates computed from the pack's templates.
func fxOffer() *ProposeBody {
	return &ProposeBody{
		Role: RoleBuyer, To: Some(fxSellerKey),
		Terms:      dealprofile.Terms{Item: "ceramic lamp", Quantity: amount(1), PriceMinor: amount(6000), Currency: fxCurrency},
		PackDigest: label("seller deal terms pack"), StatedAt: "2026-11-01T09:30:00Z", AgreeBy: "2026-11-02T23:59:59Z",
		TermsRows: TermsRows{
			Rows: []Row{
				{ID: "paid_as_agreed", OwedBy: RoleBuyer, Owed: json.RawMessage(`{"amount_minor":6000,"currency":"USD"}`),
					Due: Date{At: "2026-11-01T23:59:59Z"}, WindowEnd: Date{At: "2026-11-04T23:59:59Z"}, Recomputed: true},
				{ID: "delivered", OwedBy: RoleSeller, Owed: json.RawMessage(`{"item":"ceramic lamp","quantity":1}`),
					Due: Date{After: "paid", Seconds: secs(4 * 86400)}, WindowEnd: Date{After: "paid", Seconds: secs(7 * 86400)}},
				{ID: "as_described", OwedBy: RoleSeller, Owed: json.RawMessage(`{"listing":"as published"}`),
					Due: Date{After: "received", Seconds: secs(0)}, WindowEnd: Date{After: "received", Seconds: secs(14 * 86400)}},
			},
			Named: map[string]Named{
				"paid":     {Side: RoleBuyer, Latest: "2026-11-04T23:59:59Z"},
				"received": {Side: RoleBuyer, Latest: "2026-12-01T23:59:59Z"},
			},
		},
		Adjudicator: Some(fxAdjudicator), Forums: []string{fxForum}, Witnesses: []Witness{fxWitness},
		Ask: &AskAddress{URL: "https://buyer.example/ask", Digest: label("buyer evidence commitment")},
	}
}

func fxAgreement(offer, admit, intent *Ref, side, role, counterpartyKey string) *AgreeBody {
	layer := true
	rows, _ := RowsDigest(fxOffer().TermsRows)
	return &AgreeBody{
		Kind: KindAgreement, Offer: *offer, RowsDigest: rows, Side: side, Role: role, Admit: admit, Intent: intent,
		Witnesses: []Witness{fxWitness}, Adjudicator: Some(fxAdjudicator), Forums: []string{fxForum},
		CounterpartyLayer: &layer, CounterpartyKey: Some(counterpartyKey),
		CheckpointKey: label(role + " checkpoint key"), ShelfLogID: label(role + " shelf log id"),
	}
}

// approvalOf seals a check, its passing verdict and the approval that
// proceeds on it in a journal, returning the check.
func (a *appendixA) approvedCheck(dir, n, line, action, paymentRef, approver string, at func(int) string) *Ref {
	cb := &CheckBody{CheckBody: dealprofile.CheckBody{Action: action, AmountMinor: amount(6000), Currency: fxCurrency}, PaymentRef: paymentRef, Nonce: label("check nonce " + dir + " " + n)}
	check := a.add(fixture{Path: dir + "/" + n + "-check-" + action + ".json", Line: line,
		Story:  "Its own rules check the act before it: " + action + " 60 dollars under payment reference " + paymentRef + ".",
		Record: Record{Block: journalBlock(TypeCheck, at(0), true), Body: cb}})
	card := label("card of " + check.Digest)
	vb := journalBlock(TypeVerdict, at(1), true)
	vb.Refs = []RelRef{rel("checks", check)}
	verdict := a.add(fixture{Path: dir + "/" + next(n, 1) + "-verdict-pass.json", Line: line, Story: "The verdict on that one check: pass.",
		Record: Record{Block: vb, Body: &VerdictBody{VerdictBody: dealprofile.VerdictBody{Result: "pass", Differences: []dealprofile.Difference{}, Options: []string{}, CardCommitment: card}, Nonce: label("VerdictBody nonce " + dir + " " + n)}}})
	ab := journalBlock(TypeApproval, at(2), true)
	ab.Refs = []RelRef{rel("approves", verdict)}
	approval := &ApprovalBody{ApprovalBody: dealprofile.ApprovalBody{Choice: "proceed", Proceed: true, Approver: approver}, Nonce: label("ApprovalBody nonce " + dir + " " + n)}
	story := "The owner's standing intent answers the passing verdict: proceed."
	if approver == "user" {
		approval.SaidCommitment, approval.CardCommitment = label("owner said "+check.Digest), card
		story = "The owner answers the card: proceed."
	}
	a.add(fixture{Path: dir + "/" + next(n, 2) + "-approval-proceed.json", Line: line, Story: story, Record: Record{Block: ab, Body: approval}})
	return &check
}

func next(n string, k int) string {
	var i int
	fmt.Sscanf(n, "%d", &i)
	return fmt.Sprintf("%02d", i+k)
}

func clock(day int, hh, mm int) func(int) string {
	return func(k int) string { return fmt.Sprintf("2026-11-%02dT%02d:%02d:00Z", day, hh, mm+k) }
}

// buildAppendixA builds the deal. Paths are fixtures/<path>.
func buildAppendixA(t *testing.T) *appendixA {
	a := &appendixA{t: t, byPath: map[string]*fixture{}, digestOf: map[string]string{}}
	const (
		bj = "appendix-a/buyer-journal"
		bb = "appendix-a/buyer-book"
		sj = "appendix-a/seller-journal"
		sb = "appendix-a/seller-book"
		cb = "appendix-a/case-book"
	)
	requestDoc, answerDoc := RefTo(label("evidence request document")), RefTo(label("evidence answer document"))
	cardDigest, packDigest := label("seller card"), label("buyer admission pack")

	// 1 Nov, the buyer's side starts (lines 6 to 9g).
	intent := a.add(fixture{Path: bj + "/01-intent.json", Line: "6", Story: "The owner's words, committed; the limit of 60 dollars; the one allowed act, pay.",
		Record: Record{Block: journalBlock(TypeIntent, "2026-11-01T09:00:00Z", false),
			Body: &dealprofile.IntentBody{VerbatimCommitment: label("buy this, not over 60 dollars"), PartyRole: RoleBuyer, MaxTotalMinor: amount(6000), Allowed: []string{"pay"}}}})
	a.add(fixture{Path: bj + "/02-admit-ask.json", Line: "9", Story: "The first admission decision: ASK, for selected records of the 10 to 100 dollar band with their proofs.",
		Record: Record{Block: journalBlock(TypeAdmit, "2026-11-01T09:05:00Z", false),
			Body: &AdmitBody{Outcome: "ASK", Tier: "first_deal_10_100", Rows: []string{"selected_records_in_band"}, Asks: []Ref{requestDoc}, PackDigest: packDigest, CardDigest: cardDigest, Answers: []Ref{}, Nonce: label("seller admit ask nonce")}}})
	a.add(fixture{Path: bj + "/03-ask.json", Line: "9a", Story: "The evidence request, sealed before it is sent; an ask of an admission carries no deal_id.",
		Record:  Record{Block: journalBlock(TypeAsk, "2026-11-01T09:06:00Z", false), Body: &AskBody{Request: requestDoc, To: "counterparty"}},
		Context: &SealContext{Admission: true}})
	admitGo := a.add(fixture{Path: bj + "/04-admit-go.json", Line: "9g", Story: "The second admission decision: GO, with the answer as input.",
		Record: Record{Block: journalBlock(TypeAdmit, "2026-11-01T09:20:00Z", false),
			Body: &AdmitBody{Outcome: "GO", Tier: "first_deal_10_100", Rows: []string{"selected_records_in_band"}, Asks: []Ref{requestDoc}, PackDigest: packDigest, CardDigest: cardDigest, Answers: []Ref{answerDoc}, Nonce: label("seller admit go nonce")}}})
	offer := a.add(fixture{Path: bj + "/05-offer.json", Line: "11", Story: "The offer: the deal id, the lamp at 60 dollars, the pack digest, agree by 2 Nov, the adjudicator and witness taken from the listing, and the three rows.",
		Record: Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: fxOffer()}})

	// The seller answers the buyer's evidence request (line 9d), then
	// admits the buyer and agrees (lines 14 and 15).
	a.add(fixture{Path: sj + "/01-answer.json", Line: "9d", Story: "The seller's answer to the buyer's evidence request, with what it shared; an answer of an admission carries no deal_id.",
		Record:  Record{Block: journalBlock(TypeAnswer, "2026-11-01T09:10:00Z", false), Body: &AnswerBody{Request: requestDoc, Outcome: "evidence", Answer: answerDoc}},
		Context: &SealContext{Admission: true}})
	sellerAdmit := a.add(fixture{Path: sj + "/02-admit-go.json", Line: "14", Story: "The seller's admission decision: GO; the buyer pays first.",
		Record: Record{Block: journalBlock(TypeAdmit, "2026-11-01T09:40:00Z", false),
			Body: &AdmitBody{Outcome: "GO", Tier: "buyer_pays_first", Rows: []string{}, Asks: []Ref{}, PackDigest: label("seller admission pack"), CardDigest: label("buyer card"), Answers: []Ref{}, Nonce: label("buyer admit nonce")}}})
	a.add(fixture{Path: sb + "/01-agreement.json", Line: "15", Story: "The acceptor's agreement opens the seller's deal book; it names nothing held from the buyer.",
		Record: Record{Block: a.bookBlock(sb, TypeAgree, "2026-11-01T09:45:00Z", 1, ""), Body: fxAgreement(&offer, &sellerAdmit, nil, SideAcceptor, RoleSeller, fxBuyerKey)}})

	// The buyer's agreement (line 18), then the payment (lines 10 and 24).
	a.add(fixture{Path: bb + "/01-agreement.json", Line: "18", Story: "The offerer's agreement opens the buyer's deal book and names the seller's agreement in seen; the deal starts with it.",
		Record: Record{Block: a.bookBlock(bb, TypeAgree, "2026-11-01T10:00:00Z", 1, a.id(sb+"/01-agreement.json")),
			Body: fxAgreement(&offer, &admitGo, &intent, SideOfferer, RoleBuyer, fxSellerKey)}})
	payCheck := a.approvedCheck(bj, "06", "23", "pay", "pay-ref-7731", "standing_intent", clock(1, 10, 10))
	pay := a.bookBlock(bb, TypeSettle, "2026-11-01T10:15:00Z", 2, a.id(sb+"/01-agreement.json"))
	pay.Marks = []string{"paid"}
	a.add(fixture{Path: bb + "/02-payment.json", Line: "24", Story: "The payment record: the payer leg, row 1 met, citing its check; it is the named record paid.",
		Record: Record{Block: pay, Body: &SettleBody{Leg: LegPayer, Statements: statements(item("paid_as_agreed", ValueMet)), Check: payCheck}}})
	a.add(fixture{Path: sb + "/02-payment-received.json", Line: "27", Story: "The payment received record: the payee leg, row 1 met.",
		Record: Record{Block: a.bookBlock(sb, TypeSettle, "2026-11-01T10:20:00Z", 2, a.id(bb+"/02-payment.json")),
			Body: &SettleBody{Leg: LegPayee, Statements: statements(item("paid_as_agreed", ValueMet))}}})

	// 4 Nov, delivery (lines 37 and 40).
	a.add(fixture{Path: sb + "/03-delivery.json", Line: "37", Story: "The delivery record: delivered, the tracking number committed; rows 2 and 3 met.",
		Record: Record{Block: a.bookBlock(sb, TypePerform, "2026-11-04T10:00:00Z", 3, a.id(bb+"/02-payment.json")),
			Body: &PerformBody{Act: "deliver", Observed: "carrier_scan", DetailCommitment: label("tracking number"), Statements: statements(item("delivered", ValueMet), item("as_described", ValueMet))}}})
	receipt := a.bookBlock(bb, TypeStatement, "2026-11-04T15:00:00Z", 3, a.id(sb+"/03-delivery.json"))
	receipt.Marks = []string{"received"}
	a.add(fixture{Path: bb + "/03-receipt.json", Line: "40", Story: "The receipt record: row 2 met; it is the named record received.",
		Record: Record{Block: receipt, Body: &StatementBody{Statements: statements(item("delivered", ValueMet))}}})

	// 10 Nov, a crack (lines 43 and 47).
	a.add(fixture{Path: bb + "/04-not-as-described.json", Line: "43", Story: "Row 3 not met.",
		Record: Record{Block: a.bookBlock(bb, TypeStatement, "2026-11-10T09:00:00Z", 4, a.id(sb+"/03-delivery.json")),
			Body: &StatementBody{Statements: statements(item("as_described", ValueNotMet))}}})
	a.add(fixture{Path: sb + "/04-reply.json", Line: "47", Story: "The seller's reply: row 3 met.",
		Record: Record{Block: a.bookBlock(sb, TypeStatement, "2026-11-10T14:00:00Z", 4, a.id(bb+"/04-not-as-described.json")),
			Body: &StatementBody{Statements: statements(item("as_described", ValueMet))}}})

	// 12 Nov, the dispute (line 51).
	a.add(fixture{Path: bb + "/05-dispute.json", Line: "51", Story: "The dispute on row 3: the two statements on it, the adjudicator and the forum the agreement names.",
		Record: Record{Block: a.bookBlock(bb, TypeDispute, "2026-11-12T09:00:00Z", 5, a.id(sb+"/04-reply.json")),
			Body: &DisputeBody{Kind: KindDispute, Row: "as_described", Statements: []Ref{*a.ref(bb + "/04-not-as-described.json"), *a.ref(sb + "/04-reply.json")}, Adjudicator: fxAdjudicatorKey, Forum: fxForum}}})

	// 13 and 21 Nov, the rulings (lines 58 to 67).
	ruling := func(final bool) *RulingBody {
		return &RulingBody{Row: "as_described", Keys: RulingKeys{Buyer: fxBuyerKey, Seller: fxSellerKey}, Against: Some(RoleSeller), Value: ValueNotMet,
			Remedy: &RulingRemedy{Kind: "refund", AmountMinor: amount(6000), Currency: fxCurrency, Due: "2026-11-27T23:59:59Z"},
			Read:   RulingRead{Buyer: Some(a.id(bb + "/05-dispute.json")), Seller: Some(a.id(sb + "/04-reply.json"))},
			Forum:  fxForum, RulesDigest: fxAdjudicator.RulesDigest, Final: final}
	}
	r1b := journalBlock(TypeRuling, "2026-11-13T10:00:00Z", true)
	r1b.Seq = 1
	r1 := a.add(fixture{Path: cb + "/01-ruling.json", Line: "58", Story: "The ruling, not final: row 3 not met; the seller refunds 60 dollars by 27 Nov.", Record: Record{Block: r1b, Body: ruling(false)}})
	r2b := journalBlock(TypeRuling, "2026-11-21T10:00:00Z", true)
	r2b.Seq, r2b.Prev = 2, &r1
	r2 := a.add(fixture{Path: cb + "/02-ruling-final.json", Line: "after 67", Story: "Nobody challenged: the same ruling, sealed again, final.", Record: Record{Block: r2b, Body: ruling(true)}})
	names := func(r Ref) *StatementBody {
		return &StatementBody{Statements: statements(), Names: &StatementNames{Ruling: &r}}
	}
	a.add(fixture{Path: bb + "/06-ruling-received.json", Line: "60", Story: "The ruling received record names the ruling.",
		Record: Record{Block: a.bookBlock(bb, TypeStatement, "2026-11-13T12:00:00Z", 6, a.id(sb+"/04-reply.json")), Body: names(r1)}})
	a.add(fixture{Path: sb + "/05-ruling-received.json", Line: "63", Story: "The seller's ruling received record names the same ruling.",
		Record: Record{Block: a.bookBlock(sb, TypeStatement, "2026-11-13T14:00:00Z", 5, a.id(bb+"/06-ruling-received.json")), Body: names(r1)}})
	a.add(fixture{Path: bb + "/07-final-ruling-received.json", Line: "after 67", Story: "The ruling received record for the final ruling.",
		Record: Record{Block: a.bookBlock(bb, TypeStatement, "2026-11-21T12:00:00Z", 7, a.id(sb+"/05-ruling-received.json")), Body: names(r2)}})
	a.add(fixture{Path: sb + "/06-final-ruling-received.json", Line: "after 67", Story: "The seller's ruling received record for the final ruling.",
		Record: Record{Block: a.bookBlock(sb, TypeStatement, "2026-11-21T14:00:00Z", 6, a.id(bb+"/07-final-ruling-received.json")), Body: names(r2)}})

	// 25 Nov, the refund (lines 69 and 72).
	refundCheck := a.approvedCheck(sj, "03", "68", "cancel", "refund-ref-7731", "user", clock(25, 9, 0))
	a.add(fixture{Path: sb + "/07-refund.json", Line: "69", Story: "The refund: a payer leg that reverses the payment received record and remedies row 3 under the final ruling.",
		Record: Record{Block: a.bookBlock(sb, TypeSettle, "2026-11-25T10:00:00Z", 7, a.id(bb+"/07-final-ruling-received.json")),
			Body: &SettleBody{Leg: LegPayer, Statements: statements(), Check: refundCheck, Reverses: a.ref(sb + "/02-payment-received.json"), RemedyFor: &RemedyFor{Row: "as_described", Ruling: &r2}}}})
	a.add(fixture{Path: bb + "/08-refund-received.json", Line: "72", Story: "The refund received record: a payee leg that reverses the payment record and points at the ruling.",
		Record: Record{Block: a.bookBlock(bb, TypeSettle, "2026-11-25T15:00:00Z", 8, a.id(sb+"/07-refund.json")),
			Body: &SettleBody{Leg: LegPayee, Statements: statements(), Reverses: a.ref(bb + "/02-payment.json"), RemedyFor: &RemedyFor{Row: "as_described", Ruling: &r2}}}})

	// The same deal settled without a ruling: it leaves the table after line
	// 57, so each book continues from its record before the rulings.
	const (
		ssj = "settled/seller-journal"
		ssb = "settled/seller-book"
		sbb = "settled/buyer-book"
	)
	concession := a.approvedCheck(ssj, "03", "s1", "cancel", "refund-ref-7731", "user", clock(20, 9, 0))
	a.add(fixture{Path: ssb + "/05-refund.json", Line: "s2", Story: "The seller gives way: it refunds and states row 3 not met, a concession after the window.",
		Record: Record{Block: a.bookBlock(ssb, TypeSettle, "2026-11-20T10:00:00Z", 5, a.id(bb+"/05-dispute.json")),
			Body: &SettleBody{Leg: LegPayer, Statements: statements(item("as_described", ValueNotMet)), Check: concession, Reverses: a.ref(sb + "/02-payment-received.json"), RemedyFor: &RemedyFor{Row: "as_described"}}}})
	a.add(fixture{Path: sbb + "/06-refund-received.json", Line: "s6", Story: "The refund received record.",
		Record: Record{Block: a.bookBlock(sbb, TypeSettle, "2026-11-20T15:00:00Z", 6, a.id(ssb+"/05-refund.json")),
			Body: &SettleBody{Leg: LegPayee, Statements: statements(), Reverses: a.ref(bb + "/02-payment.json"), RemedyFor: &RemedyFor{Row: "as_described"}}}})
	a.add(fixture{Path: sbb + "/07-withdrawal.json", Line: "s7", Story: "The buyer withdraws its dispute on row 3.",
		Record: Record{Block: a.bookBlock(sbb, TypeDispute, "2026-11-20T15:05:00Z", 7, a.id(ssb+"/05-refund.json")),
			Body: &DisputeBody{Kind: KindWithdrawal, Row: "as_described"}}})

	// An offer that is declined or cancelled (the last paragraph): each is a
	// journal record sent whole, citing the offer.
	a.add(fixture{Path: "not-started/seller-journal/01-decline.json", Line: "last paragraph", Story: "The seller does not want the deal: it declines the offer, citing it; the buyer keeps no deal book.",
		Record: Record{Block: journalBlock(TypeAgree, "2026-11-01T09:40:00Z", true), Body: &AgreeBody{Kind: KindDecline, Offer: offer, Reason: ReasonOther}}})
	a.add(fixture{Path: "not-started/buyer-journal/01-cancellation.json", Line: "last paragraph", Story: "The seller agreed and the buyer changed its mind: it cancels its own offer.",
		Record: Record{Block: journalBlock(TypeAgree, "2026-11-01T09:50:00Z", true), Body: &AgreeBody{Kind: KindCancellation, Offer: offer}}})

	// Outside Appendix A: a buyer's pay check whose rules also read the
	// seller's verified History (capsule-cli#327, evidencebook#666). The
	// verdict carries the evaluated rules and binds the History by digest.
	const rh = "received-history/buyer-journal"
	rcb := &CheckBody{CheckBody: dealprofile.CheckBody{Action: "pay", AmountMinor: amount(6000), Currency: fxCurrency}, PaymentRef: "pay-ref-7732", Nonce: label("check nonce " + rh)}
	rcheck := a.add(fixture{Path: rh + "/01-check-pay.json", Line: "none (capsule-cli#327)", Story: "A pay check whose rules checker is also handed the seller's verified History.",
		Record: Record{Block: journalBlock(TypeCheck, "2026-11-01T10:10:00Z", true), Body: rcb}})
	rvb := journalBlock(TypeVerdict, "2026-11-01T10:11:00Z", true)
	rvb.Refs = []RelRef{rel("checks", rcheck)}
	a.add(fixture{Path: rh + "/02-verdict-pass.json", Line: "none (capsule-cli#327)", Story: "The verdict: pass, by rules that also read the seller's verified History, which it binds by digest, never by content.",
		Record: Record{Block: rvb, Body: &VerdictBody{VerdictBody: dealprofile.VerdictBody{Result: "pass", Differences: []dealprofile.Difference{}, Options: []string{}, CardCommitment: label("card of " + rcheck.Digest),
			Rules: &dealprofile.VerdictBodyRules{Status: "evaluated", RulesetID: "example-rules/1.0.0", DefinitionDigest: label("ruleset definition"), CheckerSHA256: label("rules checker"),
				Verdict: "allow", Tier: "recomputed", History: &dealprofile.VerdictBodyRulesHistory{Days: 30, Acts: 0, Complete: true}, Findings: []dealprofile.VerdictBodyRulesFindingsItem{}}},
			ReceivedHistory: &ReceivedHistoryDigests{HistorySHA256: []string{label("seller's History")}, InputSHA256: label("received_history input of " + rcheck.Digest)},
			Nonce:           label("VerdictBody nonce " + rh)}}})
	// The same check denied by rules that deny only given the History: the
	// verdict asks (capsule-cli#327 design §3, condition 1), its rules keep the checker's deny, and
	// received_history says the deny was the History's alone.
	rcb2 := &CheckBody{CheckBody: dealprofile.CheckBody{Action: "pay", AmountMinor: amount(6000), Currency: fxCurrency}, PaymentRef: "pay-ref-7733", Nonce: label("check nonce 2 " + rh)}
	rcheck2 := a.add(fixture{Path: rh + "/03-check-pay.json", Line: "none (capsule-cli#327)", Story: "Another pay check whose rules checker is handed the seller's verified History.",
		Record: Record{Block: journalBlock(TypeCheck, "2026-11-01T10:20:00Z", true), Body: rcb2}})
	rvb2 := journalBlock(TypeVerdict, "2026-11-01T10:21:00Z", true)
	rvb2.Refs = []RelRef{rel("checks", rcheck2)}
	check := "history/1"
	a.add(fixture{Path: rh + "/04-verdict-pause.json", Line: "none (capsule-cli#327)", Story: "The rules deny only given the seller's History, so the verdict asks: History alone never says no.",
		Record: Record{Block: rvb2, Body: &VerdictBody{VerdictBody: dealprofile.VerdictBody{Result: "pause", Differences: []dealprofile.Difference{{Question: "safety", Rule: "rules_escalate"}}, Options: []string{"hold", "proceed"}, CardCommitment: label("card of " + rcheck2.Digest),
			Rules: &dealprofile.VerdictBodyRules{Status: "evaluated", RulesetID: "example-rules/1.0.0", DefinitionDigest: label("ruleset definition"), CheckerSHA256: label("rules checker"),
				Verdict: "deny", Tier: "recomputed", History: &dealprofile.VerdictBodyRulesHistory{Days: 30, Acts: 0, Complete: true},
				Findings: []dealprofile.VerdictBodyRulesFindingsItem{{ID: "known-counterparty", Check: &check, Verdict: "fail"}}}},
			ReceivedHistory: &ReceivedHistoryDigests{HistorySHA256: []string{label("seller's History")}, InputSHA256: label("received_history input of " + rcheck2.Digest), AloneDeny: true},
			Nonce:           label("VerdictBody nonce 2 " + rh)}}})
	return a
}

// fixtureFile is a positive fixture's file.
type fixtureFile struct {
	Fixture        string          `json:"fixture"`
	Expect         string          `json:"expect"`
	Line           string          `json:"appendix_a_line"`
	Story          string          `json:"story"`
	Placement      Placement       `json:"placement"`
	SealContext    *SealContext    `json:"seal_context,omitempty"`
	Record         json.RawMessage `json:"record"`
	ExpectedDigest string          `json:"expected_digest"`
	RecordID       string          `json:"record_id,omitempty"`
}

func indent(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return append(b, '\n')
}

func (a *appendixA) files(t *testing.T) map[string][]byte {
	out := map[string][]byte{}
	var digests []string
	for _, f := range a.list {
		raw, err := json.Marshal(f.Record)
		require.NoError(t, err)
		d := f.digest(t)
		ff := fixtureFile{Fixture: f.Path, Expect: "valid", Line: f.Line, Story: f.Story, Placement: PlacementOf(f.Record),
			SealContext: f.Context, Record: raw, ExpectedDigest: d}
		if ff.Placement != PlacementJournal {
			ff.RecordID = recordID(d)
		}
		out[f.Path] = indent(t, ff)
		digests = append(digests, d+"  "+f.Path)
	}
	sort.Slice(digests, func(i, j int) bool { return digests[i][66:] < digests[j][66:] })
	out["expected-digests.txt"] = []byte("# SHA-256 over the RFC 8785 (JCS) bytes of each positive record (the `record` member only)\n" + strings.Join(digests, "\n") + "\n")
	return out
}

// checkAppendixA runs every check a record of the deal must pass.
func checkAppendixA(t *testing.T, a *appendixA) {
	for _, f := range a.list {
		if f.Context != nil {
			require.NoError(t, CheckSealed(f.Record, *f.Context), f.Path)
		} else {
			require.NoError(t, Check(f.Record), f.Path)
		}
	}
	offer := a.record("appendix-a/buyer-journal/05-offer.json")
	sellerAg, buyerAg := a.record("appendix-a/seller-book/01-agreement.json"), a.record("appendix-a/buyer-book/01-agreement.json")
	require.NoError(t, CheckAgreement(sellerAg, offer, ""))
	require.NoError(t, CheckAgreement(buyerAg, offer, a.id("appendix-a/seller-book/01-agreement.json")))
	require.NoError(t, CheckDispute(a.record("appendix-a/buyer-book/05-dispute.json"), buyerAg))
	for _, p := range []string{"appendix-a/case-book/01-ruling.json", "appendix-a/case-book/02-ruling-final.json"} {
		for _, ag := range []Record{buyerAg, sellerAg} {
			require.NoError(t, CheckRuling(a.record(p), ag), p)
		}
	}
	// Each book: seq from 1 with no gaps, prev the previous record's digest,
	// agreement_ref the agreement's, one deal id; each act authorized by its
	// side's journal.
	books := map[string][]fixture{}
	for _, f := range a.list {
		if PlacementOf(f.Record) != PlacementJournal {
			dir := filepath.Dir(f.Path)
			books[dir] = append(books[dir], f)
		}
	}
	for _, dir := range []string{"settled/seller-book", "settled/buyer-book"} {
		main := "appendix-a/" + filepath.Base(dir)
		last := books[dir][0].Record.Block.Seq - 1
		books[dir] = append(append([]fixture(nil), books[main][:last]...), books[dir]...)
	}
	for dir, book := range books {
		for i, f := range book {
			b := f.Record.Block
			require.Equal(t, int64(i+1), b.Seq, f.Path)
			require.Equal(t, fxDealID, b.DealID, f.Path)
			if i > 0 {
				require.Equal(t, book[i-1].digest(t), b.Prev.Digest, f.Path)
				if b.AgreementRef != nil {
					require.Equal(t, book[0].digest(t), b.AgreementRef.Digest, f.Path)
				}
			}
		}
		if dir == "appendix-a/case-book" {
			continue
		}
		// A side's journal, in order: on the settled path, the main path's
		// records sealed before the path leaves it, then its own.
		side := strings.Split(filepath.Base(dir), "-")[0]
		var j, acts []Record
		settled := strings.HasPrefix(dir, "settled/")
		leaves := "2026-11-13T00:00:00Z"
		for _, f := range a.list {
			switch filepath.Dir(f.Path) {
			case "appendix-a/" + side + "-journal":
				if !settled || f.Record.Block.At < leaves {
					j = append(j, f.Record)
				}
			case "settled/" + side + "-journal":
				if settled {
					j = append(j, f.Record)
				}
			}
		}
		for _, f := range book {
			if _, _, ok := actCheck(f.Record); ok {
				acts = append(acts, f.Record)
			}
		}
		_, err := CheckAuthorizations(j, acts)
		require.NoError(t, err, dir)
	}
}

func TestFixtures(t *testing.T) {
	a := buildAppendixA(t)
	checkAppendixA(t, a)
	files := a.files(t)
	for path, b := range negativeFiles(t, a) {
		files[path] = b
	}
	if os.Getenv("EB_WRITE_DEAL_V1_FIXTURES") == "1" {
		// Everything but the README is TestFixtures's to write.
		entries, err := os.ReadDir(fixturesDir)
		if err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		for _, e := range entries {
			if e.Name() != "README.md" {
				require.NoError(t, os.RemoveAll(filepath.Join(fixturesDir, e.Name())))
			}
		}
		for path, b := range files {
			p := filepath.Join(fixturesDir, path)
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, b, 0o644))
		}
	}
	var onDisk []string
	require.NoError(t, filepath.Walk(fixturesDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(p) != "README.md" {
			rel, _ := filepath.Rel(fixturesDir, p)
			onDisk = append(onDisk, filepath.ToSlash(rel))
		}
		return err
	}))
	var want []string
	for path, b := range files {
		want = append(want, path)
		got, err := os.ReadFile(filepath.Join(fixturesDir, path))
		require.NoError(t, err, "%s (EB_WRITE_DEAL_V1_FIXTURES=1 writes it)", path)
		assert.True(t, bytes.Equal(b, got), "%s differs from what TestFixtures builds", path)
	}
	sort.Strings(want)
	sort.Strings(onDisk)
	assert.Equal(t, want, onDisk, "every fixture file is one TestFixtures builds")
}

// Every positive fixture decodes, passes its checks and has its digest, read
// from the files alone, as another implementation reads them.
func TestPositiveFixturesFromFiles(t *testing.T) {
	digests := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(fixturesDir, "expected-digests.txt"))
	require.NoError(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if d, p, ok := strings.Cut(line, "  "); ok && !strings.HasPrefix(line, "#") {
			digests[p] = d
		}
	}
	require.NotEmpty(t, digests)
	for path, want := range digests {
		var ff fixtureFile
		b, err := os.ReadFile(filepath.Join(fixturesDir, path))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &ff), path)
		r, err := Decode(ff.Record)
		require.NoError(t, err, path)
		if ff.SealContext != nil {
			assert.NoError(t, CheckSealed(ff.Record, *ff.SealContext), path)
		} else {
			assert.NoError(t, Check(ff.Record), path)
		}
		d, err := Digest(ff.Record)
		require.NoError(t, err)
		assert.Equal(t, want, d, path)
		assert.Equal(t, want, ff.ExpectedDigest, path)
		assert.Equal(t, ff.Placement, PlacementOf(r), path)
		v, err := dealprofile.Version(ff.Record)
		require.NoError(t, err, path)
		assert.Equal(t, Profile, v, path)
	}
}
