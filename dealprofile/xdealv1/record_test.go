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

func hx(c byte) string { return strings.Repeat(string(c), 64) }

func secs(n int64) *int64 { return &n }

func ref(c byte) *Ref { r := RefTo(hx(c)); return &r }

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }

func offerBody() *ProposeBody {
	return &ProposeBody{
		Role: RoleBuyer, To: Some("seller-key"),
		Terms:      Terms{Item: "lamp", Currency: "USD"},
		PackDigest: hx('a'), StatedAt: at, AgreeBy: "2026-11-02T10:00:00Z",
		TermsRows: TermsRows{
			Rows: []Row{
				{ID: "paid_as_agreed", OwedBy: RoleBuyer, Owed: jsonRaw(`{"amount_minor":6000,"currency":"USD"}`),
					Due: Date{At: "2026-11-01T23:59:59Z"}, WindowEnd: Date{At: "2026-11-04T23:59:59Z"}, Recomputed: true},
				{ID: "delivered", OwedBy: RoleSeller, Owed: jsonRaw(`{}`),
					Due: Date{After: "paid", Seconds: secs(345600)}, WindowEnd: Date{After: "paid", Seconds: secs(604800)}},
			},
			Named: map[string]Named{"paid": {Side: RoleBuyer, Latest: "2026-11-04T23:59:59Z"}},
		},
		Adjudicator: Some(Adjudicator{KeyID: "adjudicator-key", RulesDigest: hx('b'), RulesURL: "https://rules.test/r"}),
		Forums:      []string{ForumAutoRef},
		Witnesses:   []Witness{{KeyID: hx('4'), URL: "https://witness.test"}},
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
		Admit: ref('e'), Witnesses: []Witness{{KeyID: hx('4'), URL: "https://witness.test"}},
		Adjudicator:       Some(Adjudicator{KeyID: "adjudicator-key", RulesDigest: hx('b'), RulesURL: "https://rules.test/r"}),
		Forums:            []string{ForumAutoRef},
		CounterpartyLayer: &layer, CounterpartyKey: Some("buyer-key"), CheckpointKey: "seller-checkpoint", ShelfLogID: "seller-shelf",
	})
}

func approval(choice string) *ApprovalBody {
	a := &ApprovalBody{ApprovalBody: dealprofile.ApprovalBody{Choice: choice, Proceed: choice != "hold", Approver: "user", SaidCommitment: hx('f')}, Nonce: hx('6')}
	if choice == ChoiceConfirm {
		a.Limits = &dealprofile.ApprovalBodyLimits{}
	}
	return a
}

// v0Body reads a v0 body from its JSON.
func v0Body[T any](t *testing.T, raw string) *T {
	t.Helper()
	var b T
	require.NoError(t, json.Unmarshal([]byte(raw), &b))
	return &b
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
	checkBody := &CheckBody{CheckBody: dealprofile.CheckBody{Action: "pay"}, PaymentRef: "invoice-17", Nonce: hx('7')}
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
		"dispute/dispute":      record(inBook(TypeDispute, 5), &DisputeBody{Kind: KindDispute, Row: "delivered", Statements: []Ref{*ref('4')}, Adjudicator: "adjudicator-key", Forum: ForumAutoRef}),
		"dispute/withdrawal":   record(inBook(TypeDispute, 6), &DisputeBody{Kind: KindWithdrawal, Row: "delivered"}),
		"dispute/challenge":    record(inBook(TypeDispute, 6), &DisputeBody{Kind: KindChallenge, Ruling: ref('3'), GroundsCommitment: hx('5')}),
		"ruling":               record(rulingBlock, &RulingBody{Row: "delivered", Keys: RulingKeys{Buyer: "buyer-key", Seller: "seller-key"}, Against: Some(RoleSeller), Value: ValueNotMet, Read: RulingRead{Buyer: Some(hx('6')), Seller: NullOf[string]()}, RulesDigest: hx('b'), Forum: ForumAutoRef, Final: true}),
		"ask/journal":          record(journalAsk, &AskBody{Request: *ref('7'), To: "counterparty"}),
		"ask/book":             record(inBook(TypeAsk, 7), &AskBody{Request: *ref('7'), To: "adjudicator"}),
		"answer/journal":       record(noDeal(TypeAnswer), &AnswerBody{Request: *ref('7'), Outcome: "absence", Answer: *ref('8')}),
		"admit":                record(noDeal(TypeAdmit), &AdmitBody{Outcome: "GO", Tier: "t1", Rows: []string{}, Asks: []Ref{}, PackDigest: hx('a'), CardDigest: hx('9'), Answers: []Ref{}, Nonce: hx('8')}),
		"intent":               record(noDeal(TypeIntent), &dealprofile.IntentBody{VerbatimCommitment: hx('a')}),
		"check":                record(blk(TypeCheck), checkBody),
		"verdict":              record(blk(TypeVerdict), &VerdictBody{VerdictBody: dealprofile.VerdictBody{Result: "pass", Differences: []dealprofile.Difference{}, Options: []string{}}, Nonce: hx('5')}),
		"approval/proceed":     record(blk(TypeApproval), approval("proceed")),
		"approval/confirm":     record(noDeal(TypeApproval), approval(ChoiceConfirm)),
		"rules_verdict":        record(inBook(TypeRulesVerdict, 8), &dealprofile.RulesVerdictBody{Word: "met", PackDigest: hx('a')}),
		"message/before-offer": record(noDeal(TypeMessage), &dealprofile.MessageBody{From: "agent", ContentCommitment: hx('a')}),
		"message/after-offer":  record(blk(TypeMessage), &dealprofile.MessageBody{From: "agent", ContentCommitment: hx('a')}),
		"thread":               record(noDeal(TypeThread), &dealprofile.ThreadBody{ThreadRefCommitment: hx('a')}),
		"counterparty_profile": record(noDeal(TypeCounterpartyProfile), &dealprofile.CounterpartyProfileBody{}),
		"sale_unpost":          record(noDeal(TypeSaleUnpost), &dealprofile.SaleUnpostBody{Platform: "market.test"}),
		"thread_opened":        record(noDeal(TypeThreadOpened), &dealprofile.ThreadOpenedBody{TaskAuthorityCommitment: hx('a')}),
		"sale":                 record(noDeal(TypeSale), v0Body[dealprofile.SaleBody](t, `{"deal_type": "purchase", "intent": {"verbatim_commitment": "`+hx('d')+`", "party_role": "seller", "allowed": ["offer", "commit"]}, "terms": {"item": "lamp", "quantity": 1, "price_minor": 6000, "currency": "USD"}, "recourse": {"rail": "card", "refundable": false}, "item_ref_commitment": "`+hx('e')+`"}`)),
		"disclosure":           record(noDeal(TypeDisclosure), v0Body[dealprofile.DisclosureBody](t, `{"to": "counterparty", "authority": "none", "rule": "answer_was_not_proceed", "fields": [{"class": "phone", "value_commitment": "`+hx('9')+`"}]}`)),
		"claim":                record(blk(TypeClaim), v0Body[dealprofile.Claim](t, `{"text_commitment": "`+hx('8')+`", "source_kind": "merchant"}`)),
		"evidence":             record(blk(TypeEvidence), v0Body[dealprofile.EvidenceBody](t, `{"source": "listing_photo", "verified": false}`)),
		"detail_change":        record(blk(TypeDetailChange), v0Body[dealprofile.DetailChangeBody](t, `{"source": "counterparty", "changed": ["rail"], "recourse": {"rail": "zelle", "refundable": false}}`)),
		"agent_move":           record(blk(TypeAgentMove), v0Body[dealprofile.AgentMoveBody](t, `{"move": "couldnt", "reason": "other", "detail_commitment": "`+hx('7')+`"}`)),
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
