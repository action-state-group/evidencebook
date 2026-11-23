package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"golang.org/x/net/idna"
)

// merchantOrderType is x-deal-v1's one private payment-ref type (PROFILE §5;
// Settlement Records -01 §: types named x- are private and never registered):
// a merchant's order number on a card checkout, whose rail has no reference
// of its own (Sherlock2, evidencebook#771). Its value is the order id as
// shown, trimmed, or "" when a checkout failed and left none; its origin is
// the checkout's origin in normal form. A generic verifier reads a pair of
// legs under it as unjoined.
const merchantOrderType = "x-deal-v1.merchant_order"

// normalOrigin is a checkout origin in normal form: the scheme and the host,
// lowercased, the host in punycode, nothing else. A path, a query, a
// fragment, user info or a port other than the scheme's own is refused, so
// two spellings of one merchant are one reference.
func normalOrigin(raw string) (string, error) {
	bad := func(why string) (string, error) {
		return "", cli.InputError("--payment-ref-origin " + raw + " is not a checkout origin: " + why + " (give the scheme and host only, such as https://shop.example)")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return bad("it does not parse as a URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return bad("its scheme is not https or http")
	}
	switch {
	case u.User != nil:
		return bad("it carries user info")
	case u.RawQuery != "" || u.ForceQuery:
		return bad("it carries a query")
	case u.Fragment != "":
		return bad("it carries a fragment")
	case u.Path != "" && u.Path != "/":
		return bad("it carries a path")
	case u.Opaque != "":
		return bad("it is not a host")
	}
	host := u.Hostname()
	if host == "" {
		return bad("it names no host")
	}
	if port := u.Port(); port != "" && !(scheme == "https" && port == "443") && !(scheme == "http" && port == "80") {
		return bad("it names a port other than the scheme's own")
	}
	ascii, err := idna.Lookup.ToASCII(strings.ToLower(host))
	if err != nil {
		return bad("its host is not a valid name")
	}
	return scheme + "://" + strings.ToLower(ascii), nil
}

// cardLike reports whether a reference is a card number: 13 to 19 digits,
// spaces and hyphens set aside, that start with a card network's prefix and
// pass the card checksum (Luhn). The checksum alone passes one digit string
// in ten, so an honest long order number would too often look like a card
// (Sherlock2, evidencebook#771 text review).
func cardLike(ref string) bool {
	var digits []byte
	for i := 0; i < len(ref); i++ {
		switch c := ref[i]; {
		case c >= '0' && c <= '9':
			digits = append(digits, c)
		case c == ' ' || c == '-':
		default:
			return false
		}
	}
	if len(digits) < 13 || len(digits) > 19 || !cardPrefix(string(digits)) {
		return false
	}
	sum := 0
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if (len(digits)-1-i)%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// cardPrefix reports whether digits start with a card network's prefix:
// Visa 4; Mastercard 51-55 and 2221-2720; Maestro 50 and 56-69; American
// Express 34 and 37; Discover 6011, 644-649 and 65; JCB 3528-3589; Diners
// Club 300-305, 3095, 36, 38 and 39; UnionPay 62 and 81; Mir 2200-2204;
// RuPay 60 and 81-82. Every 4, 5 and 6 is one of these.
func cardPrefix(d string) bool {
	in := func(n, lo, hi int) bool {
		v := 0
		for i := 0; i < n; i++ {
			v = v*10 + int(d[i]-'0')
		}
		return v >= lo && v <= hi
	}
	switch d[0] {
	case '4', '5', '6':
		return true
	case '2':
		return in(4, 2200, 2204) || in(4, 2221, 2720)
	case '3':
		return in(3, 300, 305) || in(4, 3095, 3095) || in(2, 34, 34) || in(4, 3528, 3589) || in(2, 36, 39)
	case '8':
		return in(2, 81, 82)
	}
	return false
}

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
