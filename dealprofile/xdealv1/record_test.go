// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dealID = "deal-0123456789abcdef"
	at     = "2026-11-01T10:00:00Z"
)

func hex(c byte) string { return strings.Repeat(string(c), 64) }

func secs(n int64) *int64 { return &n }

func ref(c byte) *Ref { r := RefTo(hex(c)); return &r }

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }

func offerBody() *ProposeBody {
	return &ProposeBody{
		Role: RoleBuyer, To: Some("seller-key"),
		Terms:      Terms{Item: "lamp", Currency: "USD"},
		PackDigest: hex('a'), StatedAt: at, AgreeBy: "2026-11-02T10:00:00Z",
		TermsRows: TermsRows{
			Rows: []Row{
				{ID: "paid_as_agreed", OwedBy: RoleBuyer, Owed: jsonRaw(`{"amount_minor":6000,"currency":"USD"}`),
					Due: Date{At: "2026-11-01T23:59:59Z"}, WindowEnd: Date{At: "2026-11-04T23:59:59Z"}, Recomputed: true},
				{ID: "delivered", OwedBy: RoleSeller, Owed: jsonRaw(`{}`),
					Due: Date{After: "paid", Seconds: secs(345600)}, WindowEnd: Date{After: "paid", Seconds: secs(604800)}},
			},
			Named: map[string]Named{"paid": {Side: RoleBuyer, Latest: "2026-11-04T23:59:59Z"}},
		},
		Adjudicator: Some(Adjudicator{KeyID: "adjudicator-key", RulesDigest: hex('b'), RulesURL: "https://rules.test/r"}),
		Witnesses:   []string{"https://witness.test"},
	}
}

func blk(t string) Block {
	return Block{Profile: Profile, Canonicalization: Canonicalization, DealID: dealID, RecordType: t, At: at}
}

// inBook is a deal-book block at seq.
func inBook(t string, seq int64) Block {
	b := blk(t)
	b.Seq, b.Seen = seq, NullOf[string]()
	if seq > 1 {
		b.Prev, b.AgreementRef = ref('c'), ref('d')
	}
	return b
}

func record(b Block, body Body) Record { return Record{Block: b, Body: body} }

func acceptorAgreement(offer Record) Record {
	d, _ := Digest(offer)
	rows, _ := RowsDigest(offer.Body.(*ProposeBody).TermsRows)
	layer := true
	return record(inBook(TypeAgree, 1), &AgreeBody{
		Kind: KindAgreement, Offer: RefTo(d), RowsDigest: rows, Side: SideAcceptor, Role: RoleSeller,
		Admit: ref('e'), Witnesses: []string{"https://witness.test"},
		Adjudicator:       Some(Adjudicator{KeyID: "adjudicator-key", RulesDigest: hex('b'), RulesURL: "https://rules.test/r"}),
		CounterpartyLayer: &layer, CounterpartyKey: Some("buyer-key"), CheckpointKey: "seller-checkpoint", ShelfLogID: "seller-shelf",
	})
}

func approval(choice string) *dealprofile.ApprovalBody {
	a := &dealprofile.ApprovalBody{Choice: choice, Proceed: choice != "hold", Approver: "user", SaidCommitment: hex('f')}
	if choice == ChoiceConfirm {
		a.Limits = &dealprofile.ApprovalBodyLimits{}
	}
	return a
}

func noDeal(t string) Block { b := blk(t); b.DealID = ""; return b }

// validRecords is one valid record of each type and kind.
func validRecords(t *testing.T) map[string]Record {
	t.Helper()
	offer := record(blk(TypePropose), offerBody())
	payer := &SettleBody{Leg: LegPayer, Statements: []StatementItem{{Row: "paid_as_agreed", Value: ValueMet}}, Check: ref('1')}
	rulingBlock := blk(TypeRuling)
	rulingBlock.Seq = 1
	journalAsk := blk(TypeAsk)
	checkBody := &CheckBody{CheckBody: dealprofile.CheckBody{Action: "pay"}, PaymentRef: "invoice-17"}
	return map[string]Record{
		"propose":              offer,
		"agree/agreement":      acceptorAgreement(offer),
		"agree/decline":        record(blk(TypeAgree), &AgreeBody{Kind: KindDecline, Offer: *ref('2'), Reason: ReasonNotSel}),
		"agree/cancellation":   record(blk(TypeAgree), &AgreeBody{Kind: KindCancellation, Offer: *ref('2')}),
		"perform":              record(inBook(TypePerform, 3), &PerformBody{Act: "deliver", Observed: "carrier_scan", Statements: []StatementItem{{Row: "delivered", Value: ValueMet}}}),
		"settle/payer":         record(inBook(TypeSettle, 2), payer),
		"settle/payee":         record(inBook(TypeSettle, 2), &SettleBody{Leg: LegPayee, Statements: []StatementItem{}}),
		"statement":            record(inBook(TypeStatement, 4), &StatementBody{Statements: []StatementItem{{Row: "delivered", Value: ValueNotMet}}}),
		"statement/names":      record(inBook(TypeStatement, 4), &StatementBody{Statements: []StatementItem{}, Names: &StatementNames{Ruling: ref('3')}}),
		"dispute/dispute":      record(inBook(TypeDispute, 5), &DisputeBody{Kind: KindDispute, Row: "delivered", Statements: []Ref{*ref('4')}, Adjudicator: "adjudicator-key"}),
		"dispute/withdrawal":   record(inBook(TypeDispute, 6), &DisputeBody{Kind: KindWithdrawal, Row: "delivered"}),
		"dispute/challenge":    record(inBook(TypeDispute, 6), &DisputeBody{Kind: KindChallenge, Ruling: ref('3'), GroundsCommitment: hex('5')}),
		"ruling":               record(rulingBlock, &RulingBody{Row: "delivered", Keys: RulingKeys{Buyer: "buyer-key", Seller: "seller-key"}, Against: Some(RoleSeller), Value: ValueNotMet, Read: RulingRead{Buyer: Some(hex('6')), Seller: NullOf[string]()}, RulesDigest: hex('b'), Final: true}),
		"ask/journal":          record(journalAsk, &AskBody{Request: *ref('7'), To: "counterparty"}),
		"ask/book":             record(inBook(TypeAsk, 7), &AskBody{Request: *ref('7'), To: "adjudicator"}),
		"answer/journal":       record(noDeal(TypeAnswer), &AnswerBody{Request: *ref('7'), Outcome: "absence", Answer: *ref('8')}),
		"admit":                record(noDeal(TypeAdmit), &AdmitBody{Outcome: "GO", Tier: "t1", Rows: []string{}, Asks: []Ref{}, PackDigest: hex('a'), CardDigest: hex('9'), Answers: []Ref{}}),
		"intent":               record(noDeal(TypeIntent), &dealprofile.IntentBody{VerbatimCommitment: hex('a')}),
		"check":                record(blk(TypeCheck), checkBody),
		"verdict":              record(blk(TypeVerdict), &dealprofile.VerdictBody{Result: "pass", Differences: []dealprofile.Difference{}, Options: []string{}}),
		"approval/proceed":     record(blk(TypeApproval), approval("proceed")),
		"approval/confirm":     record(noDeal(TypeApproval), approval(ChoiceConfirm)),
		"rules_verdict":        record(inBook(TypeRulesVerdict, 8), &dealprofile.RulesVerdictBody{Word: "met", PackDigest: hex('a')}),
		"message/before-offer": record(noDeal(TypeMessage), &dealprofile.MessageBody{From: "agent", ContentCommitment: hex('a')}),
		"message/after-offer":  record(blk(TypeMessage), &dealprofile.MessageBody{From: "agent", ContentCommitment: hex('a')}),
		"thread":               record(noDeal(TypeThread), &dealprofile.ThreadBody{ThreadRefCommitment: hex('a')}),
		"counterparty_profile": record(noDeal(TypeCounterpartyProfile), &dealprofile.CounterpartyProfileBody{}),
		"sale_unpost":          record(noDeal(TypeSaleUnpost), &dealprofile.SaleUnpostBody{Platform: "market.test"}),
		"thread_opened":        record(noDeal(TypeThreadOpened), &dealprofile.ThreadOpenedBody{TaskAuthorityCommitment: hex('a')}),
	}
}

func TestEveryValidRecordPassesAndRoundTrips(t *testing.T) {
	for name, r := range validRecords(t) {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(r)
			require.NoError(t, err)
			payload, digest, err := Encode(raw)
			require.NoError(t, err)
			got, err := Decode(payload)
			require.NoError(t, err)
			again, err := json.Marshal(got)
			require.NoError(t, err)
			assert.JSONEq(t, string(payload), string(again))
			d, err := Digest(got)
			require.NoError(t, err)
			assert.Equal(t, digest, d)
			v, err := dealprofile.Version(payload)
			require.NoError(t, err)
			assert.Equal(t, Profile, v)
		})
	}
}
