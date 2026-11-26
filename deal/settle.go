// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/settlement"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// The statuses an observed leg takes (AAC's statuses, less reversed: a
// refund is its own leg).
const (
	LegPending = "pending"
	LegSettled = "settled"
	LegFailed  = "failed"
)

// settleOf is a settle record's body.
func settleOf(r xdealv1.Record) (*xdealv1.SettleBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.SettleBody:
		return b, true
	case xdealv1.SettleBody:
		return &b, true
	}
	return nil, false
}

// owedCurrency is the currency of a row whose owed carries an amount.
func owedCurrency(r *xdealv1.Row) (string, bool) {
	var owed struct {
		AmountMinor *int64 `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if json.Unmarshal(r.Owed, &owed) != nil || owed.AmountMinor == nil || owed.Currency == "" {
		return "", false
	}
	return owed.Currency, true
}

// paymentRow is the row a leg settles: the one named, or the offer's one
// payment row (a row whose owed carries an amount).
func (a *actDeal) paymentRow(id string) (*xdealv1.Row, string, error) {
	if id != "" {
		r, err := a.row(id)
		if err != nil {
			return nil, "", err
		}
		cur, ok := owedCurrency(r)
		if !ok {
			return nil, "", inputError("row " + id + " owes no amount: a leg settles a payment row")
		}
		return r, cur, nil
	}
	var found *xdealv1.Row
	var cur string
	for i := range a.offer.TermsRows.Rows {
		r := &a.offer.TermsRows.Rows[i]
		if c, ok := owedCurrency(r); ok {
			if found != nil {
				return nil, "", coded(CodeSettleWhichRow, "", "the offer has more than one payment row: name it in the call's Row")
			}
			found, cur = r, c
		}
	}
	if found == nil {
		return nil, "", inputError("the offer of deal " + a.d.DealID + " has no payment row")
	}
	return found, cur, nil
}

// legFor is the leg the holder seals (§13 item 3): payer when its party owes
// the row; with reverses, the mirror of the leg it reverses, which must be a
// settle record of the holder's own book that did not fail.
func (s *Store) legFor(ctx context.Context, a *actDeal, row *xdealv1.Row, reverses string) (string, error) {
	if reverses == "" {
		if row.OwedBy == a.d.Role {
			return xdealv1.LegPayer, nil
		}
		return xdealv1.LegPayee, nil
	}
	var target *xdealv1.SettleBody
	for _, r := range a.book {
		b, ok := settleOf(r)
		if !ok {
			continue
		}
		d, err := xdealv1.Digest(r)
		if err != nil {
			return "", err
		}
		if d == reverses {
			target = b
		}
	}
	if target == nil {
		return "", fieldError("reverses", " must name a settle record of your own deal book: "+reverses+" is not one")
	}
	// A payee leg may be superseded by a later observation of the same
	// payment: the latest ones say whether money moved.
	failed, err := s.chainFailed(ctx, a.d, a.book, reverses)
	if err != nil {
		return "", err
	}
	if failed {
		return "", inputError("leg " + reverses + " failed: no money moved, so there is nothing to refund")
	}
	if target.Leg == xdealv1.LegPayer {
		return xdealv1.LegPayee, nil
	}
	return xdealv1.LegPayer, nil
}

// legChain is the set of digests of the settle records linked to digest by
// supersedes, in either direction: one payment's observations.
func legChain(book []xdealv1.Record, digest string) map[string]bool {
	links := map[string]string{} // record -> the record it supersedes
	for _, r := range book {
		if b, ok := settleOf(r); ok && b.Supersedes != nil {
			if d, err := xdealv1.Digest(r); err == nil {
				links[d] = b.Supersedes.Digest
			}
		}
	}
	chain := map[string]bool{digest: true}
	for changed := true; changed; {
		changed = false
		for from, to := range links {
			if chain[from] != chain[to] {
				chain[from], chain[to], changed = true, true, true
			}
		}
	}
	return chain
}

// chainTips are the places (seq) of the settle records of digest's chain
// that no record supersedes: its latest observations. A chain normally has
// one; a fork (two records superseding one) has several, each still
// standing.
func chainTips(book []xdealv1.Record, digest string) []int {
	chain := legChain(book, digest)
	superseded := map[string]bool{}
	for _, r := range book {
		if b, ok := settleOf(r); ok && b.Supersedes != nil {
			superseded[b.Supersedes.Digest] = true
		}
	}
	var tips []int
	for i, r := range book {
		if d, err := xdealv1.Digest(r); err == nil && chain[d] && !superseded[d] {
			tips = append(tips, i+1)
		}
	}
	return tips
}

// chainFailed says every latest observation of digest's chain failed.
func (s *Store) chainFailed(ctx context.Context, d *Deal, book []xdealv1.Record, digest string) (bool, error) {
	tips := chainTips(book, digest)
	for _, seq := range tips {
		st, err := s.legStatusAt(ctx, d, seq)
		if err != nil {
			return false, err
		}
		if st != LegFailed {
			return false, nil
		}
	}
	return len(tips) > 0, nil
}

// legStatusAt is the status of the settle record at seq of the holder's
// book, from its Capsule's settlement member.
func (s *Store) legStatusAt(ctx context.Context, d *Deal, seq int) (string, error) {
	member, err := s.SettlementAt(ctx, d, seq)
	if err != nil {
		return "", err
	}
	var m struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(member, &m); err != nil {
		return "", errors.Join(evidencebook.ErrCorrupt, errors.New("the settlement member of record "+strconv.Itoa(seq)+": "+err.Error()))
	}
	return m.Status, nil
}

// SettlementAt is the settlement member of the record at place seq of the
// holder's deal book of d: the Capsule carries it, the record does not. Nil
// for a record that carries none.
func (s *Store) SettlementAt(ctx context.Context, d *Deal, seq int) (json.RawMessage, error) {
	book, err := s.cfg.Shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return nil, err
	}
	rows, err := book.Query(ctx, evidencebook.Filter{FromSeq: uint64(seq), ToSeq: uint64(seq)})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, conflictf("the deal book has no record %d", seq)
	}
	stored, err := book.Sealed(ctx, rows[0].RecordID)
	if err != nil {
		return nil, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(stored.Capsule, &members); err != nil {
		return nil, err
	}
	return members["settlement"], nil
}

// termsRef is the Capsule id of the deal's offer as sent, which carries the
// terms leg every observed leg names (§13 item 3).
func (s *Store) termsRef(ctx context.Context, d *Deal) (string, error) {
	pair, err := s.OfferPair(ctx, d.OfferDigest)
	if err != nil {
		return "", err
	}
	if len(pair) == 0 {
		return "", inputError("this profile does not hold the offer of deal " + d.DealID + " as sent, which a leg names as its terms")
	}
	sent, err := xdealv1.VerifySent(pair, nil)
	if err != nil {
		return "", err
	}
	return sent.CapsuleID, nil
}

// legAmount is minor units of a currency as an AAC amount.
func legAmount(minor int64, currency string) (map[string]any, error) {
	scale, ok := dealprofile.AssetScale(currency)
	if !ok {
		return nil, inputError("currency " + currency + " has no known minor units")
	}
	if minor < 0 {
		return nil, coded(CodeSettleNegative, "", "a leg's amounts are in minor units and are never negative")
	}
	return map[string]any{"value": strconv.FormatInt(minor, 10), "assetCode": currency, "assetScale": scale}, nil
}

// settlementMember builds a leg's settlement member and refuses one AAC's
// structure check fails.
func settlementMember(m map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	if f := settlement.StructureFailures(generic); len(f) > 0 {
		return nil, inputError("the settlement leg fails AAC's structure check: " + strings.Join(f, ", "))
	}
	return raw, nil
}

// sameRecordInput says a book record has the body and marks given.
func sameRecordInput(r xdealv1.Record, body xdealv1.Body, marks []string) (bool, error) {
	want, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	got, err := json.Marshal(r.Body)
	if err != nil {
		return false, err
	}
	return bytes.Equal(got, want) && slices.Equal(r.Block.Marks, marks), nil
}

// sameButObservedAt says two settlement members are equal but for their
// observed time.
func sameButObservedAt(a, b json.RawMessage) bool {
	var ma, mb map[string]any
	if json.Unmarshal(a, &ma) != nil || json.Unmarshal(b, &mb) != nil {
		return false
	}
	delete(ma, "observed_at")
	delete(mb, "observed_at")
	ja, _ := json.Marshal(ma)
	jb, _ := json.Marshal(mb)
	return bytes.Equal(ja, jb)
}

// checkPaymentRef holds a payment_ref object to settlement-records-01's
// flat form: a registered type with its value and exactly the qualifiers the
// type names, or x-deal-v1's own merchant order (xdealv1.MerchantOrderType)
// with its order number and the checkout origin in normal form. No value may
// be a card number (evidencebook#771).
func checkPaymentRef(raw json.RawMessage) (map[string]any, error) {
	bad := func(why string) (map[string]any, error) { return nil, fieldError("payment_ref", " "+why) }
	var ref map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&ref); err != nil || ref == nil {
		return bad("is not a JSON object")
	}
	text := func(name string) (string, bool) {
		v, ok := ref[name].(string)
		return v, ok
	}
	typ, _ := text("type")
	value, ok := text("value")
	if !ok || strings.TrimSpace(value) == "" {
		return bad("has no value: the payment's reference")
	}
	if xdealv1.CardLike(value) {
		return bad("value looks like a card number: no card data goes in a payment reference")
	}
	allowed := map[string]bool{"type": true, "value": true}
	if typ == xdealv1.MerchantOrderType {
		origin, _ := text("origin")
		normal, err := xdealv1.NormalOrigin(origin)
		if err != nil {
			return bad("origin " + err.Error())
		}
		if origin != normal {
			return bad("origin " + origin + " is not in normal form: " + normal)
		}
		allowed["origin"] = true
	} else {
		t, ok := settlement.PaymentRefTypes[typ]
		if !ok {
			return bad("type " + typ + " is neither a registered payment_ref type nor " + xdealv1.MerchantOrderType)
		}
		for _, q := range t.Qualifiers {
			if v, ok := text(q); !ok || v == "" {
				return bad("needs " + q + " for " + typ)
			}
			allowed[q] = true
		}
	}
	for name := range ref {
		if !allowed[name] {
			return bad("member " + name + " does not go with " + typ)
		}
	}
	return ref, nil
}

// SettleIn is a payee leg's input: what the holder received on a payment
// row (Row, or the offer's only payment row), net of any receive fee, and
// the fee taken (both in minor units), the leg's Status (LegPending,
// LegSettled or LegFailed) and its PaymentRef, AAC's flat payment_ref
// object (settlement-records-01). Reverses names a leg of the holder's own
// book this leg refunds; Supersedes an earlier observation of the same
// payment this one replaces. Statements, Marks and RemedyRow are as for
// [PerformIn].
type SettleIn struct {
	Row, Reverses, Supersedes string
	Status                    string
	Received, ReceiveFee      int64
	PaymentRef                json.RawMessage
	Statements                []xdealv1.StatementItem
	Marks                     []string
	RemedyRow                 string
	// Before is the caller's own check of the leg (nil: none).
	Before BeforeSeal
}

// Settle seals a payee leg in the holder's deal book of dealID: what it
// received, as a settle record whose Capsule carries the leg as its
// settlement member (draft-mih-agent-settlement-records-01), built from the
// deal and the input: the terms it names are the deal's offer as sent. A
// payee leg rests on no check and is not gated. Called again with the same input, it seals nothing and returns the
// leg already sealed. A leg the holder owes (a payment or a refund it
// makes) is a payer leg, which this call does not seal.
func (s *Store) Settle(ctx context.Context, dealID string, in SettleIn) (Acted, error) {
	a, err := s.loadActDeal(ctx, dealID)
	if err != nil {
		return Acted{}, err
	}
	row, currency, err := a.paymentRow(in.Row)
	if err != nil {
		return Acted{}, err
	}
	leg, err := s.legFor(ctx, a, row, in.Reverses)
	if err != nil {
		return Acted{}, err
	}
	stmts, err := a.checkStatements(in.Statements)
	if err != nil {
		return Acted{}, err
	}
	marks, err := a.checkMarks(in.Marks)
	if err != nil {
		return Acted{}, err
	}
	body := xdealv1.SettleBody{Leg: leg, Statements: stmts}
	if in.Reverses != "" {
		r := xdealv1.RefTo(in.Reverses)
		body.Reverses = &r
	}
	if body.RemedyFor, err = a.remedyFor(in.RemedyRow); err != nil {
		return Acted{}, err
	}
	termsRef, err := s.termsRef(ctx, a.d)
	if err != nil {
		return Acted{}, err
	}
	if leg != xdealv1.LegPayee {
		what := "payment"
		if in.Reverses != "" {
			what = "refund"
		}
		return Acted{}, coded(CodeSettlePayer, "", "you owe this "+what+": it is a payer leg, which rests on your check, and this call seals only what you received", what)
	}
	switch in.Status {
	case LegPending, LegSettled, LegFailed:
	default:
		return Acted{}, inputError("a payee leg's status is pending, settled or failed")
	}
	if in.Supersedes != "" {
		ok := false
		for _, r := range a.book {
			if b, isSettle := settleOf(r); isSettle && b.Leg == xdealv1.LegPayee {
				if d, err := xdealv1.Digest(r); err == nil && d == in.Supersedes {
					ok = (b.Reverses == nil) == (body.Reverses == nil) && (b.Reverses == nil || b.Reverses.Digest == body.Reverses.Digest)
				}
			}
		}
		if !ok {
			return Acted{}, fieldError("supersedes", " must name an earlier leg of what you received in your own book, of the same payment")
		}
		r := xdealv1.RefTo(in.Supersedes)
		body.Supersedes = &r
	}
	ref, err := checkPaymentRef(in.PaymentRef)
	if err != nil {
		return Acted{}, err
	}
	received, err := legAmount(in.Received, currency)
	if err != nil {
		return Acted{}, err
	}
	fee, err := legAmount(in.ReceiveFee, currency)
	if err != nil {
		return Acted{}, err
	}
	member, err := settlementMember(map[string]any{"version": settlement.Version, "leg": "payee_observed", "sealer_role": "payee", "terms_ref": termsRef,
		"received": received, "receive_fee": fee, "payment_ref": ref, "status": in.Status, "observed_at": s.nowText()})
	if err != nil {
		return Acted{}, err
	}
	if seq, err := s.sealedPayeeLike(ctx, a, body, marks, member); err != nil {
		return Acted{}, err
	} else if seq > 0 {
		return s.again(ctx, a.d, seq, a.book[seq-1])
	}
	if in.Reverses != "" && in.Supersedes == "" {
		if err := s.payeeRefundTaken(ctx, a, in.Reverses); err != nil {
			return Acted{}, err
		}
	}
	// A new observation names the latest one of its payment: a leg already
	// superseded is not superseded again (asked only after a re-run of the
	// same observation has been found).
	if in.Supersedes != "" {
		for _, r := range a.book {
			if b, isSettle := settleOf(r); isSettle && b.Supersedes != nil && b.Supersedes.Digest == in.Supersedes {
				return Acted{}, inputError("leg " + in.Supersedes + " is already superseded: a later observation names the latest one of its payment")
			}
		}
	}
	// What the holder received rests on no check: no gate and no
	// authorization hold it.
	return s.sealRecord(ctx, dealID, a.book, BookRecord{Body: body, Marks: marks, Members: map[string]json.RawMessage{"settlement": member}}, in.Before)
}

// sealedPayeeLike is the place of a payee leg an earlier call already
// sealed: the same body and marks, a member equal but for its observed
// time, and no later record stating the same rows. Every candidate is
// compared, not only the first with the same body.
func (s *Store) sealedPayeeLike(ctx context.Context, a *actDeal, body xdealv1.SettleBody, marks []string, member json.RawMessage) (int, error) {
	for i, r := range a.book {
		if r.Block.RecordType != xdealv1.TypeSettle {
			continue
		}
		same, err := sameRecordInput(r, body, marks)
		if err != nil {
			return 0, err
		}
		if !same || statedAfter(a.book, i, body.Statements) {
			continue
		}
		held, err := s.SettlementAt(ctx, a.d, i+1)
		if err != nil {
			return 0, err
		}
		if sameButObservedAt(held, member) {
			return i + 1, nil
		}
	}
	return 0, nil
}

// payeeRefundTaken refuses a refund received of a leg that is already being
// refunded: a refund received of the leg's chain (its observations) that, at
// its latest observation, did not fail. A later observation of that refund
// names it with Supersedes instead.
func (s *Store) payeeRefundTaken(ctx context.Context, a *actDeal, target string) error {
	chain := legChain(a.book, target)
	for _, r := range a.book {
		b, ok := settleOf(r)
		if !ok || b.Leg != xdealv1.LegPayee || b.Reverses == nil || !chain[b.Reverses.Digest] {
			continue
		}
		d, err := xdealv1.Digest(r)
		if err != nil {
			return err
		}
		if failed, err := s.chainFailed(ctx, a.d, a.book, d); err != nil {
			return err
		} else if !failed {
			return coded(CodeSettleRefunded, "", "leg "+target+" is already refunded: a later observation of that refund names it in Supersedes, and a failed refund leaves the leg free to refund again", target)
		}
	}
	return nil
}
