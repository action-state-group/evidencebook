package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// merchantOrderType is x-deal-v1's one private payment-ref type
// (xdealv1.MerchantOrderType, PROFILE §5): a merchant's order number on a
// card checkout, whose rail has no reference of its own (evidencebook#771).
const merchantOrderType = xdealv1.MerchantOrderType

// normalOrigin is a checkout origin in normal form (xdealv1.NormalOrigin),
// refused in the flag's terms.
func normalOrigin(raw string) (string, error) {
	origin, err := xdealv1.NormalOrigin(raw)
	if err != nil {
		return "", cli.InputError("--payment-ref-origin " + err.Error() + " (give the scheme and host only, such as https://shop.example)")
	}
	return origin, nil
}

// cardLike reports whether a reference is a card number (xdealv1.CardLike).
func cardLike(ref string) bool { return xdealv1.CardLike(ref) }

// refuseCardData refuses a payment reference that is a card number.
func refuseCardData(what, ref string) error {
	if cardLike(ref) {
		return cli.InputError(what + " looks like a card number (a card network's prefix and the card checksum): no card data goes in a payment reference. Give the rail's own reference; or, when the merchant's order number is what looks like a card, record the payment with no reference: the pay check says \"payment_ref_after_payment\": true, and deal settle --status settled takes --no-payment-ref")
	}
	return nil
}

// The row notes a card checkout's payment row carries (Sherlock2,
// evidencebook#771): plugin output beside the report, never in it.
const (
	noteRefAfterPayment = "payment reference recorded after payment: the checkout showed no reference before paying, so the merchant's order number was recorded once paid, and the payment was not bound to it beforehand"
	noteOneSided        = "the other side keeps no signed records of its own, so this record is one-sided: it holds your side only"
	notePrivateType     = "the payment's reference is the merchant's order number, in a type private to x-deal-v1 (" + merchantOrderType + "), which a generic verifier does not join, so the two sides' legs are not matched against each other"
	noteNoRef           = "no payment reference recorded: the merchant's order number was left out, so nothing in the record ties the payment to the order"
)

// v1PaymentRowNotes are the notes for a deal's payment rows, by row id:
// one-sided when the other side runs no layer; recorded after payment when a
// settled payment under the merchant order type rested on a pay check with
// no payment_ref, or no reference when it settled with none; the private type when the payment check read its legs
// unjoined under that type. It reads the deal's own sends and events and
// writes nothing.
func (s *dealSession) v1PaymentRowNotes(ctx context.Context, d *v1Deal, rows []string, payments map[string]xdealv1.PaymentResult) (map[string][]string, error) {
	var notes []string
	noLayer, err := s.v1NoLayer(ctx, d)
	if err != nil {
		return nil, err
	}
	if noLayer {
		notes = append(notes, noteOneSided)
	}
	q, err := s.db.QueryContext(ctx, `SELECT s.ref_type, s.ref_value, e.local FROM deal_v1_sends s JOIN deal_v1_events e ON e.deal_id=s.deal_id AND e.n=s.event_n WHERE s.deal_id=? AND s.leg_digest<>'' AND s.reverses=''`, d.DealID)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			q = nil
		} else {
			return nil, err
		}
	}
	merchant, after := false, false
	if q != nil {
		defer q.Close()
		for q.Next() {
			var refType, ref, local string
			if err := q.Scan(&refType, &ref, &local); err != nil {
				return nil, err
			}
			if refType != merchantOrderType {
				continue
			}
			merchant = true
			var ev dealEvent
			if err := json.Unmarshal([]byte(local), &ev); err != nil {
				return nil, fmt.Errorf("%w: deal %s's send event: %v", evidencebook.ErrCorrupt, d.DealID, err)
			}
			// A send whose leg settled keeps its amount (closeSend clears a
			// failed one's); one with no reference at send had none before
			// paying, and --status settled required the order number.
			if ref == "" && ev.Act != nil && ev.Act.AmountMinor != nil {
				after = true
			}
		}
		if err := q.Err(); err != nil {
			return nil, err
		}
	}
	// Either side's own legs say whether the payment is under the merchant
	// order: a payee writes no send, so its payee leg is read (#772 review).
	// A settled leg under it with no value was recorded with --no-payment-ref.
	noRef := false
	book, err := s.ownBookV1(ctx, d)
	if err != nil {
		return nil, err
	}
	for i, r := range book {
		if _, ok := settleOf(r); !ok {
			continue
		}
		raw, err := s.ownSettleMember(ctx, d, i+1)
		if err != nil {
			return nil, err
		}
		var m struct {
			Status     string `json:"status"`
			PaymentRef struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"payment_ref"`
		}
		if json.Unmarshal(raw, &m) == nil && m.PaymentRef.Type == merchantOrderType {
			merchant = true
			if m.Status == legSettled && m.PaymentRef.Value == "" {
				noRef = true
			}
		}
	}
	switch {
	case noRef:
		notes = append(notes, noteNoRef)
	case after:
		notes = append(notes, noteRefAfterPayment)
	}
	out := map[string][]string{}
	for _, id := range rows {
		row := append([]string(nil), notes...)
		if res, ok := payments[id]; ok && merchant && res.Reason == xdealv1.ReasonUnjoined {
			row = append(row, notePrivateType)
		}
		if len(row) > 0 {
			out[id] = row
		}
	}
	return out, nil
}

// scrubPaymentRefs is the copy the share gate scans: data with the value and
// the origin of every payment_ref object blanked, by structure (evidencebook
// #771). A leg is sealed and sent whole, so its reference cannot be withheld
// from a shared copy; blanking it in the scanned text removes exactly the
// leg's own reference and nothing else, where cutting the value out as text
// would also cut it out of a private value elsewhere ("1234" out of a phone).
// A value that is a card number is left for the gate's own check to refuse.
// Data that is not JSON, or holds no payment_ref, is returned as it is;
// numbers keep their text and nothing is HTML-escaped.
func scrubPaymentRefs(data []byte) []byte {
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return data
	}
	changed := false
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if ref, ok := t["payment_ref"].(map[string]any); ok {
				if s, ok := ref["value"].(string); ok && !cardLike(s) {
					ref["value"], changed = "", true
				}
				if _, ok := ref["origin"].(string); ok {
					ref["origin"], changed = "", true
				}
			}
			for _, v := range t {
				walk(v)
			}
		case []any:
			for _, v := range t {
				walk(v)
			}
		}
	}
	walk(v)
	if !changed {
		return data
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return data
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}
