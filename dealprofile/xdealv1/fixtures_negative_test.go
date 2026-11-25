// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A negative fixture is one positive record of Appendix A's deal with one
// change, refused by one check at one stage. Check names the call:
//
//   - "record": Check, or CheckSealed with seal_context;
//   - "agreement": CheckAgreement(record, offer, held_acceptor_agreement);
//   - "dispute": CheckDispute(record, agreement);
//   - "ruling": CheckRuling(record, agreement).
type negative struct {
	name, description string
	base              string // the positive fixture it changes
	check             string
	stage, contains   string
	edit              func(blk, body map[string]any)
	// text replaces the record's JSON text, for a change no JSON value
	// can show (a member given twice).
	text func(raw string) string
	ctx  *SealContext
	// with is the agreement (dispute, ruling) or offer (agreement) the
	// record is read against; held the acceptor's agreement's record id an
	// offerer's agreement is read against.
	with string
	held bool
}

const (
	nOffer    = "appendix-a/buyer-journal/05-offer.json"
	nIntent   = "appendix-a/buyer-journal/01-intent.json"
	nAsk      = "appendix-a/buyer-journal/03-ask.json"
	nCheck    = "appendix-a/buyer-journal/06-check-pay.json"
	nAdmit    = "appendix-a/buyer-journal/04-admit-go.json"
	nVerdict  = "appendix-a/buyer-journal/07-verdict-pass.json"
	nApproval = "appendix-a/buyer-journal/08-approval-proceed.json"
	nSellerAg = "appendix-a/seller-book/01-agreement.json"
	nBuyerAg  = "appendix-a/buyer-book/01-agreement.json"
	nPayment  = "appendix-a/buyer-book/02-payment.json"
	nReceipt  = "appendix-a/buyer-book/03-receipt.json"
	nDelivery = "appendix-a/seller-book/03-delivery.json"
	nDispute  = "appendix-a/buyer-book/05-dispute.json"
	nNames    = "appendix-a/buyer-book/06-ruling-received.json"
	nRuling   = "appendix-a/case-book/01-ruling.json"
	nRefund   = "appendix-a/seller-book/07-refund.json"
	nDecline  = "not-started/seller-journal/01-decline.json"
	nCancel   = "not-started/buyer-journal/01-cancellation.json"
)

func negatives() []negative {
	set := func(m map[string]any, k string, v any) { m[k] = v }
	del := func(m map[string]any, k string) { delete(m, k) }
	return []negative{
		// Canonicalization (§3.1, "Digest").
		{name: "neg-canonicalization-not-jcs", description: "canonicalization other than jcs fails closed", base: nPayment, check: "record",
			stage: StageCanonicalization, contains: `canonicalization must be exactly "jcs"`,
			edit: func(b, _ map[string]any) { set(b, "canonicalization", "none") }},
		{name: "neg-float", description: "a number with a fraction or exponent", base: nOffer, check: "record",
			stage: StageCanonicalization, contains: "a float at",
			text: func(raw string) string { return strings.Replace(raw, `"quantity":1`, `"quantity":1.0`, 1) }},
		{name: "neg-member-twice", description: "an object member given twice, of which a decoder keeps one", base: nPayment, check: "record",
			stage: StageCanonicalization, contains: "given twice",
			text: func(raw string) string {
				return strings.Replace(raw, `{"leg":"payer"`, `{"leg":"payee","leg":"payer"`, 1)
			}},
		// Schema (§3.3: members are closed).
		{name: "neg-unknown-block-member", description: "a block member the profile does not name", base: nPayment, check: "record",
			stage: StageSchema, contains: "",
			edit: func(b, _ map[string]any) { set(b, "amount", 6000) }},
		{name: "neg-unknown-body-member", description: "a body member the type does not name", base: nDelivery, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { set(b, "price_minor", 6000) }},
		{name: "neg-received-history-member", description: "a verdict's received_history with a member it does not name", base: nVerdict, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { b["received_history"].(map[string]any)["document"] = "their History" }},
		{name: "neg-received-history-empty", description: "a verdict's received_history that names no History", base: nVerdict, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { b["received_history"].(map[string]any)["history_sha256"] = []any{} }},
		{name: "neg-received-history-without-input", description: "a verdict's received_history without input_sha256", base: nVerdict, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { del(b["received_history"].(map[string]any), "input_sha256") }},
		{name: "neg-payer-without-check", description: "a payer leg cites the check it rests on", base: nPayment, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { del(b, "check") }},
		{name: "neg-decline-with-agreement-members", description: "a decline with an agreement's members: the body must match its kind", base: nDecline, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { set(b, "rows_digest", label("rows")) }},
		{name: "neg-decline-unknown-reason", description: "a decline's reason is one closed token", base: nDecline, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { set(b, "reason", "too_expensive") }},
		{name: "neg-cancellation-with-reason", description: "a cancellation carries no reason token", base: nCancel, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { set(b, "reason", ReasonOther) }},
		{name: "neg-decline-with-refs", description: "a decline names nothing beyond the offer it answers", base: nDecline, check: "record",
			stage: StageRecord, contains: "carries no refs, marks or counterparty",
			edit: func(b, _ map[string]any) { set(b, "marks", []any{"declined"}) }},
		{name: "neg-recomputed-false", description: "an optional member is absent rather than holding its zero value: recomputed is present only as true", base: nOffer, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) {
				b["terms_rows"].(map[string]any)["rows"].([]any)[1].(map[string]any)["recomputed"] = false
			}},
		{name: "neg-deal-id-trailing-newline", description: "a pattern's $ is the end of the text: a deal id with a final newline is not one", base: nPayment, check: "record",
			stage: StageSchema, contains: "",
			edit: func(b, _ map[string]any) { set(b, "deal_id", fxDealID+"\n") }},
		{name: "neg-v0-close-type", description: "v1 has no close: v0's close is not a v1 record type", base: nPayment, check: "record",
			stage: StageSchema, contains: "",
			edit: func(b, _ map[string]any) { set(b, "record_type", "close") }},
		{name: "neg-check-body-not-v0", description: "the check body without payment_ref is v0's body_check: its action is one v0 names", base: nCheck, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { set(b, "action", "teleport") }},
		// Schema: a journal record another record cites carries its own
		// nonce (PROFILE §5), so the citing record's digest tests no guess.
		{name: "neg-admit-without-nonce", description: "an admit, which an agreement cites, carries its nonce", base: nAdmit, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { del(b, "nonce") }},
		{name: "neg-check-without-nonce", description: "a check, which an act cites, carries its nonce", base: nCheck, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { del(b, "nonce") }},
		{name: "neg-verdict-without-nonce", description: "a verdict, which a signed approval cites, carries its nonce", base: nVerdict, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { del(b, "nonce") }},
		{name: "neg-approval-without-nonce", description: "an approval, whose digest the person signs beside the act, carries its nonce", base: nApproval, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { del(b, "nonce") }},
		{name: "neg-verdict-with-payment-ref", description: "payment_ref is a check's member, not a verdict's", base: nVerdict, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { set(b, "payment_ref", "unexpected") }},
		{name: "neg-approval-with-payment-ref", description: "payment_ref is a check's member, not an approval's", base: nApproval, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { set(b, "payment_ref", "unexpected") }},
		{name: "neg-nonce-not-256-bit-hex", description: "a nonce is 64 lowercase hex", base: nCheck, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { set(b, "nonce", strings.ToUpper(b["nonce"].(string))) }},
		{name: "neg-perform-with-refs", description: "a deal-book record cites only through its type's members, never refs", base: nDelivery, check: "record",
			stage: StageRecord, contains: "carries no refs",
			edit: func(b, _ map[string]any) {
				set(b, "refs", []any{map[string]any{"rel": "checks", "type": "deal-record", "digest_alg": "SHA-256", "digest": strings.Repeat("a", 64)}})
			}},
		{name: "neg-offer-with-refs", description: "an offer, sent whole, cites only through counter_to, never refs", base: nOffer, check: "record",
			stage: StageRecord, contains: "carries no refs",
			edit: func(b, _ map[string]any) {
				set(b, "refs", []any{map[string]any{"rel": "checks", "type": "deal-record", "digest_alg": "SHA-256", "digest": strings.Repeat("a", 64)}})
			}},
		{name: "neg-intent-without-verbatim-commitment", description: "an intent, which an agreement cites, carries its salted verbatim_commitment", base: nIntent, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) { del(b, "verbatim_commitment") }},
		// Record: placement (§3.1, §3.2).
		{name: "neg-book-record-without-seq", description: "a deal-book record carries its seq", base: nDelivery, check: "record",
			stage: StageRecord, contains: "carries its seq",
			edit: func(b, _ map[string]any) { del(b, "seq") }},
		{name: "neg-prev-on-first", description: "prev exactly when seq is past 1", base: nSellerAg, check: "record",
			stage: StageRecord, contains: "carries prev exactly when",
			edit: func(b, _ map[string]any) {
				set(b, "prev", map[string]any{"type": RecordRef, "digest_alg": DigestAlg, "digest": label("prev")})
			}},
		{name: "neg-book-record-without-seen", description: "a deal-book record carries seen", base: nDelivery, check: "record",
			stage: StageRecord, contains: "carries seen",
			edit: func(b, _ map[string]any) { del(b, "seen") }},
		{name: "neg-without-agreement-ref", description: "a deal-book record past seq 1 is bound to its agreement", base: nPayment, check: "record",
			stage: StageRecord, contains: "agreement_ref exactly when",
			edit: func(b, _ map[string]any) { del(b, "agreement_ref") }},
		{name: "neg-agreement-not-first", description: "an agreement opens its deal book", base: nSellerAg, check: "record",
			stage: StageRecord, contains: "an agreement opens its deal book",
			edit: func(b, _ map[string]any) {
				ref := map[string]any{"type": RecordRef, "digest_alg": DigestAlg, "digest": label("prev")}
				set(b, "seq", 2)
				set(b, "prev", ref)
				set(b, "agreement_ref", ref)
			}},
		{name: "neg-book-opens-with-perform", description: "a deal book opens with its agreement", base: nDelivery, check: "record",
			stage: StageRecord, contains: "opens with its agreement",
			edit: func(b, _ map[string]any) {
				set(b, "seq", 1)
				del(b, "prev")
				del(b, "agreement_ref")
			}},
		{name: "neg-journal-record-in-place", description: "a journal record shows no place in the journal", base: nOffer, check: "record",
			stage: StageRecord, contains: "a journal record carries no seq",
			edit: func(b, _ map[string]any) { set(b, "seq", 5) }},
		{name: "neg-ruling-with-seen", description: "a ruling carries no seen and no agreement_ref", base: nRuling, check: "record",
			stage: StageRecord, contains: "a ruling carries no seen",
			edit: func(b, _ map[string]any) { set(b, "seen", nil) }},
		{name: "neg-bad-date", description: "a time that names no instant", base: nReceipt, check: "record",
			stage: StageRecord, contains: "is not a UTC time",
			edit: func(b, _ map[string]any) { set(b, "at", "2026-11-31T15:00:00Z") }},
		// Record: deal_id presence (§3.1).
		{name: "neg-offer-without-deal-id", description: "an offer carries the deal id its offerer made", base: nOffer, check: "record",
			stage: StageRecord, contains: "carries deal_id",
			edit: func(b, _ map[string]any) { del(b, "deal_id") }},
		{name: "neg-intent-with-deal-id", description: "an intent spans deals: no deal_id", base: nIntent, check: "record",
			stage: StageRecord, contains: "carries no deal_id",
			edit: func(b, _ map[string]any) { set(b, "deal_id", fxDealID) }},
		{name: "neg-admission-ask-with-deal-id", description: "an ask of an admission carries no deal_id", base: nAsk, check: "record",
			ctx: &SealContext{Admission: true}, stage: StageRecord, contains: "carries deal_id only",
			edit: func(b, _ map[string]any) { set(b, "deal_id", fxDealID) }},
		// Record: bodies (§3.3).
		{name: "neg-witness-twice", description: "one key id is one witness", base: nOffer, check: "record",
			stage: StageRecord, contains: "is named twice",
			edit: func(_, b map[string]any) {
				w := b["witnesses"].([]any)[0]
				set(b, "witnesses", []any{w, map[string]any{"key_id": fxWitness.KeyID, "url": "https://moved.example"}})
			}},
		{name: "neg-forums-without-adjudicator", description: "an offer with no adjudicator names no forum", base: nOffer, check: "record",
			stage: StageRecord, contains: "no adjudicator names no forum",
			edit: func(_, b map[string]any) { set(b, "adjudicator", nil) }},
		{name: "neg-adjudicator-without-forums", description: "an offer with an adjudicator names the forums both sides accept", base: nOffer, check: "record",
			stage: StageRecord, contains: "names the forums",
			edit: func(_, b map[string]any) { set(b, "forums", []any{}) }},
		{name: "neg-row-twice", description: "a row id given twice", base: nOffer, check: "record",
			stage: StageRecord, contains: "is given twice",
			edit: func(_, b map[string]any) {
				tr := b["terms_rows"].(map[string]any)
				rows := tr["rows"].([]any)
				set(tr, "rows", append(rows, rows[0]))
			}},
		{name: "neg-row-hangs-on-unnamed", description: "a date that hangs on a record named nowhere", base: nOffer, check: "record",
			stage: StageRecord, contains: "which is not named",
			edit: func(_, b map[string]any) {
				delete(b["terms_rows"].(map[string]any)["named"].(map[string]any), "received")
			}},
		{name: "neg-layer-without-key", description: "an agreement with a counterparty that runs the layer names its record key", base: nSellerAg, check: "record",
			stage: StageRecord, contains: "names its record key",
			edit: func(_, b map[string]any) { set(b, "counterparty_key", nil) }},
		{name: "neg-no-layer-with-key", description: "an agreement with a counterparty that runs no layer has counterparty_key null", base: nSellerAg, check: "record",
			stage: StageRecord, contains: "runs no layer",
			edit: func(_, b map[string]any) { set(b, "counterparty_layer", false) }},
		{name: "neg-acceptor-seen", description: "the acceptor's agreement comes first: its seen is null", base: nSellerAg, check: "record",
			stage: StageRecord, contains: "its seen is null",
			edit: func(b, _ map[string]any) { set(b, "seen", label("buyer record")) }},
		{name: "neg-statement-names-two", description: "a statement names one arrived record, a cancellation or a ruling, not both", base: nNames, check: "record",
			stage: StageSchema, contains: "",
			edit: func(_, b map[string]any) {
				n := b["names"].(map[string]any)
				set(n, "cancellation", n["ruling"])
			}},
		// Agreement: an agreement read against its offer (§3.3, §3.4).
		{name: "neg-agreement-other-offer", description: "the agreement cites the offer's record digest", base: nSellerAg, check: "agreement", with: nOffer,
			stage: StageAgreement, contains: "does not cite this offer",
			edit: func(_, b map[string]any) { b["offer"].(map[string]any)["digest"] = label("another offer") }},
		{name: "neg-agreement-rows-digest", description: "the rows digest is the offer's", base: nSellerAg, check: "agreement", with: nOffer,
			stage: StageAgreement, contains: "rows_digest is not",
			edit: func(_, b map[string]any) { set(b, "rows_digest", label("other rows")) }},
		{name: "neg-agreement-witnesses", description: "the agreement's witnesses are the offer's", base: nSellerAg, check: "agreement", with: nOffer,
			stage: StageAgreement, contains: "witnesses are not",
			edit: func(_, b map[string]any) {
				set(b, "witnesses", []any{map[string]any{"key_id": label("other witness")}})
			}},
		{name: "neg-agreement-adjudicator", description: "the agreement's adjudicator is the offer's", base: nSellerAg, check: "agreement", with: nOffer,
			stage: StageAgreement, contains: "adjudicator is not",
			edit: func(_, b map[string]any) { b["adjudicator"].(map[string]any)["key_id"] = label("other adjudicator") }},
		{name: "neg-agreement-forums", description: "the agreement's forums are the offer's", base: nSellerAg, check: "agreement", with: nOffer,
			stage: StageAgreement, contains: "forums are not",
			edit: func(_, b map[string]any) { set(b, "forums", []any{ForumAutoTwin}) }},
		{name: "neg-acceptor-same-role", description: "the acceptor's role is the other side of the offer's", base: nSellerAg, check: "agreement", with: nOffer,
			stage: StageAgreement, contains: "acceptor's role",
			edit: func(_, b map[string]any) { set(b, "role", RoleBuyer) }},
		{name: "neg-offerer-other-role", description: "the offerer's role is the offer's", base: nBuyerAg, check: "agreement", with: nOffer, held: true,
			stage: StageAgreement, contains: "offerer's role",
			edit: func(_, b map[string]any) { set(b, "role", RoleSeller) }},
		{name: "neg-offerer-seen-not-held", description: "the offerer's agreement names the acceptor's agreement it holds", base: nBuyerAg, check: "agreement", with: nOffer, held: true,
			stage: StageAgreement, contains: "names the acceptor's agreement",
			edit: func(b, _ map[string]any) { set(b, "seen", label("another record")) }},
		// Dispute and ruling against the agreement (§3.3).
		{name: "neg-dispute-forum", description: "the dispute's forum is one the agreement accepts", base: nDispute, check: "dispute", with: nBuyerAg,
			stage: StageAgreement, contains: "not one the agreement accepts",
			edit: func(_, b map[string]any) { set(b, "forum", ForumAutoTwin) }},
		{name: "neg-dispute-adjudicator", description: "the dispute goes to the adjudicator the agreement names", base: nDispute, check: "dispute", with: nBuyerAg,
			stage: StageAgreement, contains: "another adjudicator",
			edit: func(_, b map[string]any) { set(b, "adjudicator", label("other adjudicator")) }},
		{name: "neg-dispute-other-agreement", description: "the dispute is bound to its book's agreement", base: nDispute, check: "dispute", with: nBuyerAg,
			stage: StageAgreement, contains: "bound to another agreement",
			edit: func(b, _ map[string]any) { b["agreement_ref"].(map[string]any)["digest"] = label("other agreement") }},
		{name: "neg-ruling-forum", description: "the ruling rules in a forum the agreement accepts", base: nRuling, check: "ruling", with: nBuyerAg,
			stage: StageAgreement, contains: "not one the agreement accepts",
			edit: func(_, b map[string]any) { set(b, "forum", "external:court") }},
	}
}

// negativeFile is a negative fixture's file.
type negativeFile struct {
	Fixture     string          `json:"fixture"`
	Expect      string          `json:"expect"`
	Description string          `json:"description"`
	Base        string          `json:"base"`
	Check       string          `json:"check"`
	With        string          `json:"with,omitempty"`
	Held        string          `json:"held_acceptor_agreement,omitempty"`
	SealContext *SealContext    `json:"seal_context,omitempty"`
	Stage       string          `json:"expected_stage"`
	Contains    string          `json:"expected_error_contains"`
	Record      json.RawMessage `json:"record,omitempty"`
	RecordText  string          `json:"record_text,omitempty"`
}

// runNegative runs the negative's check on raw and returns its error.
func runNegative(a *appendixA, n negative, raw []byte, ctx *SealContext, held string) error {
	switch n.check {
	case "record":
		if ctx != nil {
			return CheckSealed(raw, *ctx)
		}
		return Check(raw)
	}
	r, err := Decode(raw)
	if err != nil {
		return err
	}
	with := a.record(n.with)
	switch n.check {
	case "agreement":
		return CheckAgreement(r, with, held)
	case "dispute":
		return CheckDispute(r, with)
	case "ruling":
		return CheckRuling(r, with)
	}
	panic(n.check)
}

func negativeFiles(t *testing.T, a *appendixA) map[string][]byte {
	out := map[string][]byte{}
	for _, n := range negatives() {
		base := a.byPath[n.base]
		require.NotNil(t, base, n.name)
		var raw []byte
		if n.text != nil {
			b, err := json.Marshal(base.Record)
			require.NoError(t, err)
			raw = []byte(n.text(string(b)))
			require.NotEqual(t, string(b), string(raw), n.name)
		} else {
			raw = edit(t, base.Record, n.edit)
		}
		var held string
		if n.held {
			held = a.id(nSellerAg)
		}
		ctx := n.ctx
		if ctx == nil {
			ctx = base.Context
		}
		err := runNegative(a, n, raw, ctx, held)
		require.Error(t, err, n.name)
		t.Logf("%s: %v", n.name, err)
		require.Equal(t, n.stage, stageOf(err), "%s: %v", n.name, err)
		require.Contains(t, err.Error(), n.contains, n.name)
		nf := negativeFile{Fixture: n.name, Expect: "invalid", Description: n.description, Base: n.base, Check: n.check, With: n.with,
			Held: held, SealContext: ctx, Stage: n.stage, Contains: n.contains}
		if n.text != nil {
			nf.RecordText = string(raw)
		} else {
			nf.Record = raw
		}
		out["negative/"+n.name+".json"] = indent(t, nf)
	}
	return out
}
