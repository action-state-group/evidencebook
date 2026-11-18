// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/settlement"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// The payment check (design §6.3): the recomputed check of a payment row,
// over AAC's settlement legs (draft-mih-agent-settlement-records-01), with
// AAC's verifier, settlement.Verify, so no second join of legs exists here.
//
// The terms leg is the offer's: the offer's sent Capsule (§7) carries it as
// its top-level settlement member, sealed by the offerer, the party that set
// the terms, in the role its side has in the payment. Both agreements cite
// that offer, and each side's payment leg's terms_ref is the sent Capsule's
// id. A terms leg has one amount (the draft: "a leg answers exactly one
// terms leg"), so an offer carries one only when its terms have exactly one
// payment row, and the check runs only on that row's own payment: a refund
// or another remedy that is a payment needs its own terms leg, and the check
// is not run on it (Sherlock2 and its twin on the 4a door, 2026-11-17).

// StagePayment is a payment check's inputs read (PaymentCheck).
const StagePayment = "payment"

// SettlementMember is the Capsule member a settlement leg travels in.
const SettlementMember = settlement.Member

// The payment check's results (rowstatus's check): pass, fail, or "" for
// not run.
const (
	PaymentPass = "pass"
	PaymentFail = "fail"
)

// Why a payment check was not run, or failed (PaymentResult.Reason).
const (
	ReasonNoTermsLeg       = "no_terms_leg"       // the offer's sent Capsule carries no terms leg
	ReasonSeveralPayments  = "several_payments"   // the terms have more than one payment row
	ReasonRefundCitesOffer = "refund_cites_offer" // a refund or remedy leg cites the offer's terms leg
	ReasonNotAgreed        = "not_agreed"         // the legs are not agreed (yet): AAC's PaymentState
	ReasonPending          = "pending"            // agreed, with status pending
	ReasonMismatch         = "mismatch"           // the two sides' legs differ
	ReasonStatus           = "status"             // agreed, with status failed or reversed
	ReasonAmount           = "amount"             // agreed, with an amount other than the terms
)

// PaymentRows are the ids of the payment rows of an offer's terms, in
// order: the recomputed rows whose owed is an amount, exactly
// {"amount_minor", "currency"} (design §6.1's example, x-deal-v0's money),
// in a currency with a known assetScale (dealprofile.AssetScale).
func PaymentRows(tr TermsRows) []string {
	var out []string
	for _, row := range tr.Rows {
		if isPaymentRow(row) {
			out = append(out, row.ID)
		}
	}
	return out
}

func isPaymentRow(row Row) bool {
	_, ok := owedAmount(row)
	return row.Recomputed && ok
}

// owedAmount is a row's owed as a settlement amount: amount_minor as the
// value, the currency as the assetCode, and its assetScale.
func owedAmount(row Row) (map[string]any, bool) {
	var owed struct {
		AmountMinor *int64 `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if len(row.Owed) == 0 || strictUnmarshal(row.Owed, &owed) != nil || owed.AmountMinor == nil || *owed.AmountMinor < 0 {
		return nil, false
	}
	scale, ok := dealprofile.AssetScale(owed.Currency)
	if !ok {
		return nil, false
	}
	return map[string]any{"value": strconv.FormatInt(*owed.AmountMinor, 10), "assetCode": owed.Currency, "assetScale": json.Number(strconv.Itoa(scale))}, true
}

// TermsLeg is the terms leg an offer's sent Capsule carries as its
// settlement member, as JCS bytes: version "0", leg terms, the offerer's
// role in the payment (payer when the offer's role owes the payment row,
// payee otherwise) and the payment row's owed as its amount (owedAmount). It refuses an
// offer whose terms have no payment row or more than one.
func TermsLeg(offer Record) (json.RawMessage, error) {
	of, ok := asPropose(offer.Body)
	if !ok {
		return nil, fmt.Errorf("xdealv1: a terms leg is an offer's; this is a %s", offer.Block.RecordType)
	}
	row, err := onePaymentRow(of.TermsRows)
	if err != nil {
		return nil, err
	}
	role := settlementRole(of.Role, row.OwedBy)
	amount, _ := owedAmount(row)
	return canonical.JCS(map[string]any{"version": settlement.Version, "leg": "terms", "sealer_role": role, "amount": amount})
}

func onePaymentRow(tr TermsRows) (Row, error) {
	var found []Row
	for _, row := range tr.Rows {
		if isPaymentRow(row) {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		return Row{}, fmt.Errorf("xdealv1: a terms leg carries one payment's amount; the terms have %d payment rows", len(found))
	}
	return found[0], nil
}

// settlementRole is a party's sealer role in a payment that owedBy owes.
func settlementRole(party, owedBy string) string {
	if party == owedBy {
		return "payer"
	}
	return "payee"
}

// checkTermsMember checks the settlement member a sent Capsule carries: only
// an offer carries one, and it is exactly TermsLeg of that offer.
func checkTermsMember(member json.RawMessage, r Record) error {
	if member == nil {
		return nil
	}
	if r.Block.RecordType != TypePropose {
		return stageErr(StageSent, "a sent %s carries no settlement member; only an offer's terms leg travels", r.Block.RecordType)
	}
	want, err := TermsLeg(r)
	if err != nil {
		return stageErr(StageSent, "the settlement member: %v", err)
	}
	doc, err := decodeJSON(member)
	if err != nil {
		return stageErr(StageSent, "the settlement member: %v", err)
	}
	got, err := canonical.JCS(doc)
	if err != nil || !bytes.Equal(got, want) {
		return stageErr(StageSent, "the settlement member is not the offer's terms leg %s", want)
	}
	return nil
}

// PaymentInput is what PaymentCheck reads.
type PaymentInput struct {
	// Offer is the started deal's offer as sent: its pair {record,
	// agent_input} (§7), whose Capsule carries the terms leg.
	Offer []byte
	// OfferDigest is the record digest of the offer both agreements cite.
	OfferDigest string
	// AcceptorKey is the acceptor's key (lowercase hex): the key its
	// agreement and its deal book are signed by. An offer sent to a key
	// names it in to, and then it must be that key.
	AcceptorKey string
	// Row is the payment row checked.
	Row string
	// Legs are records of both deal books as book pairs ({record, header,
	// payload}, BookPair): the holder's own and the counted prefix of the
	// counterparty's. Records other than settle legs are passed over.
	Legs [][]byte
}

// PaymentResult is a payment check's outcome. Check is PaymentPass,
// PaymentFail, or "" when the check was not run; Reason says why it failed
// or was not run. CheckedOver are the record digests it ran over: the offer,
// then the legs AAC's verifier read, in the order given. Settlement is AAC's
// derived state of the legs that answer the offer's terms leg, when it ran.
type PaymentResult struct {
	Check       string                 `json:"check"`
	Reason      string                 `json:"reason,omitempty"`
	CheckedOver []string               `json:"checked_over"`
	Settlement  *settlement.Settlement `json:"settlement,omitempty"`
}

// PaymentCheck runs the check of a payment row (design §6.3; Settlement
// Records -01): pass when the payer's and the payee's legs answering the
// offer's terms leg are agreed, with status settled and the terms' amount;
// fail on a mismatch, and with agreed legs on a status of failed or reversed
// or another amount; not run otherwise, and when the offer carries no terms
// leg or its terms have more than one payment row. Sealer conflation (both
// observed legs under one key) cannot arise: the two parties' keys are
// distinct, and a leg sealed by the key of the other role is refused.
//
// It refuses an offer that does not verify (VerifySent) or is not the one
// both agreements cite, a row that is not a payment row, a leg that does not
// verify (VerifyBook), is of another deal, or is signed by neither party's
// key, and a leg whose side, leg or role disagree. A payer leg is the
// payment row's owing party's, a payee leg the other's, and AAC's verifier
// applies that as its key policy. A leg superseded (supersedes) by a later
// leg of its side is not read; a refund or remedy leg (reverses,
// remedy_for) that cites the offer's terms leg leaves the check not run.
func PaymentCheck(in PaymentInput) (PaymentResult, error) {
	bad := func(format string, args ...any) (PaymentResult, error) {
		return PaymentResult{}, stageErr(StagePayment, format, args...)
	}
	sent, err := VerifySent(in.Offer, nil)
	if err != nil {
		return PaymentResult{}, err
	}
	if sent.Digest != in.OfferDigest {
		return bad("the offer given is not the started deal's offer %s", in.OfferDigest)
	}
	of, ok := asPropose(sent.Record.Body)
	if !ok {
		return bad("the offer given is a %s", sent.Record.Block.RecordType)
	}
	if of.To.Present && of.To.V != nil && *of.To.V != in.AcceptorKey {
		return bad("the acceptor's key is not the key the offer was sent to")
	}
	if in.AcceptorKey == "" || in.AcceptorKey == sent.KeyID {
		return bad("the acceptor's key is empty or the offerer's")
	}
	var row *Row
	for i := range of.TermsRows.Rows {
		if of.TermsRows.Rows[i].ID == in.Row {
			row = &of.TermsRows.Rows[i]
		}
	}
	if row == nil || !isPaymentRow(*row) {
		return bad("%q is not a payment row of the offer", in.Row)
	}
	out := PaymentResult{CheckedOver: []string{sent.Digest}}
	if n := len(PaymentRows(of.TermsRows)); n != 1 {
		out.Reason = ReasonSeveralPayments
		return out, nil
	}
	var offerPair struct {
		Record map[string]json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(in.Offer, &offerPair); err != nil {
		return PaymentResult{}, err
	}
	if offerPair.Record[SettlementMember] == nil {
		out.Reason = ReasonNoTermsLeg
		return out, nil
	}

	// The parties' keys and payment roles: the payer owes the payment row.
	offerer, acceptor := of.Role, RoleBuyer
	if offerer == RoleBuyer {
		acceptor = RoleSeller
	}
	keyOf := map[string]string{offerer: sent.KeyID, acceptor: in.AcceptorKey}
	roleOf := map[string]string{} // key -> payment role
	for party, key := range keyOf {
		roleOf[key] = settlementRole(party, row.OwedBy)
	}

	type legRead struct {
		read    BookRead
		capsule map[string]json.RawMessage
		role    string
	}
	var legs []legRead
	for i, raw := range in.Legs {
		read, err := VerifyBook(raw)
		if err != nil {
			return PaymentResult{}, err
		}
		if read.Record.Block.DealID != sent.Record.Block.DealID {
			return bad("leg %d is of deal %q, not the offer's", i, read.Record.Block.DealID)
		}
		body, isSettle := bodyOf[SettleBody](read.Record.Body)
		if !isSettle || body == nil {
			continue
		}
		role, known := roleOf[read.KeyID]
		if !known {
			return bad("leg %d is signed by neither party's key", i)
		}
		var pair struct {
			Record map[string]json.RawMessage `json:"record"`
		}
		if err := json.Unmarshal(raw, &pair); err != nil {
			return PaymentResult{}, err
		}
		var member struct {
			Leg        string `json:"leg"`
			SealerRole string `json:"sealer_role"`
			TermsRef   string `json:"terms_ref"`
		}
		m := pair.Record[SettlementMember]
		if m == nil || json.Unmarshal(m, &member) != nil {
			return bad("leg %d carries no settlement member", i)
		}
		if member.TermsRef != sent.CapsuleID {
			continue // it answers another terms leg
		}
		if body.Reverses != nil || body.RemedyFor != nil {
			out.Reason = ReasonRefundCitesOffer
			return out, nil
		}
		if body.Leg != role || member.Leg != role+"_observed" || member.SealerRole != role {
			return bad("leg %d is a %s leg (its settlement member a %s leg as %s) sealed by the %s's key", i, body.Leg, member.Leg, member.SealerRole, role)
		}
		legs = append(legs, legRead{read: read, capsule: pair.Record, role: role})
	}

	// A leg superseded by a later leg of its side is not read: the book
	// keeps the replaced one, and the deal reads the outcome (§4.2, §8).
	superseded := map[string]bool{}
	for _, l := range legs {
		if b, _ := bodyOf[SettleBody](l.read.Record.Body); b != nil && b.Supersedes != nil {
			for _, t := range legs {
				if t.read.KeyID == l.read.KeyID && RefTo(t.read.Digest) == *b.Supersedes && t.read.Seq < l.read.Seq {
					superseded[t.read.Digest] = true
				}
			}
		}
	}
	capsules := []any{}
	offerCapsule, err := decodeRecord(offerPair.Record)
	if err != nil {
		return bad("the offer's Capsule: %v", err)
	}
	capsules = append(capsules, offerCapsule)
	for _, l := range legs {
		if superseded[l.read.Digest] {
			continue
		}
		c, err := decodeRecord(l.capsule)
		if err != nil {
			return bad("a leg's Capsule: %v", err)
		}
		capsules = append(capsules, c)
		out.CheckedOver = append(out.CheckedOver, l.read.Digest)
	}
	policy := map[string][]string{}
	for key, role := range roleOf {
		policy[role] = append(policy[role], key)
	}
	res := settlement.Verify(settlement.Input{Capsules: capsules, KeyPolicy: policy})
	if len(res.Failures) > 0 {
		var codes []string
		for _, f := range res.Failures {
			codes = append(codes, f.Code)
		}
		slices.Sort(codes)
		return bad("the settlement legs do not verify: %s", strings.Join(slices.Compact(codes), ", "))
	}
	for i := range res.Settlements {
		if res.Settlements[i].Terms == sent.CapsuleID {
			s := res.Settlements[i]
			out.Settlement = &s
		}
	}
	if out.Settlement == nil {
		return bad("AAC's verifier read no terms leg %s", sent.CapsuleID)
	}
	switch state := out.Settlement.PaymentState; {
	case state == nil || (*state != "agreed" && *state != "mismatch"):
		out.Reason = ReasonNotAgreed
	case *state == "mismatch":
		out.Check, out.Reason = PaymentFail, ReasonMismatch
	case out.Settlement.AgreedStatus == "failed" || out.Settlement.AgreedStatus == "reversed":
		out.Check, out.Reason = PaymentFail, ReasonStatus
	case out.Settlement.TermsAmount != "equal":
		out.Check, out.Reason = PaymentFail, ReasonAmount
	case out.Settlement.AgreedStatus == "settled":
		out.Check = PaymentPass
	default:
		out.Reason = ReasonPending
	}
	return out, nil
}

// decodeRecord is a pair's Capsule as AAC's verifier reads it, numbers kept
// as written.
func decodeRecord(members map[string]json.RawMessage) (any, error) {
	raw, err := json.Marshal(members)
	if err != nil {
		return nil, err
	}
	return decodeJSON(raw)
}
