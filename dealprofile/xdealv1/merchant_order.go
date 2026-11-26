// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// MerchantOrderType is x-deal-v1's one private payment_ref type (PROFILE §5;
// settlement-records-01: types named x- are private and never registered):
// a merchant's order number on a card checkout, whose rail has no reference
// of its own (evidencebook#771). Its value is the order id as shown,
// trimmed; its origin is the checkout's origin in normal form (NormalOrigin).
// A generic verifier reads a pair of legs under it as unjoined.
const MerchantOrderType = "x-deal-v1.merchant_order"

// NormalOrigin is a checkout origin in normal form: the scheme and the host,
// lowercased, the host in punycode, nothing else. A path, a query, a
// fragment, user info or a port other than the scheme's own is refused, so
// two spellings of one merchant are one reference. The error says why, for
// the caller to put after the origin's name.
func NormalOrigin(raw string) (string, error) {
	bad := func(why string) (string, error) {
		return "", errors.New(raw + " is not a checkout origin: " + why)
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

// CardLike reports whether a reference is a card number: 13 to 19 digits,
// spaces and hyphens set aside, that start with a card network's prefix and
// pass the card checksum (Luhn). The checksum alone passes one digit string
// in ten, so an honest long order number would too often look like a card
// (evidencebook#771). No card data goes in a payment reference.
func CardLike(ref string) bool {
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
	if len(digits) < 13 || len(digits) > 19 || !CardPrefix(string(digits)) {
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

// CardPrefix reports whether digits start with a card network's prefix:
// Visa 4; Mastercard 51-55 and 2221-2720; Maestro 50 and 56-69; American
// Express 34 and 37; Discover 6011, 644-649 and 65; JCB 3528-3589; Diners
// Club 30, 36, 38 and 39; UnionPay 62 and 81; Mir 2200-2204; RuPay 60 and
// 81-82; Troy 9792; BORICA 2205; China T-Union 31; GPN 1946; Uzcard 8600;
// Napas 9704; HUMO 9860. Every 4, 5 and 6 is one of these. UATP (1) is left
// out on purpose: it would make an order number such as 1002003004005005 a
// card again (evidencebook#817). digits holds digits only.
func CardPrefix(d string) bool {
	in := func(n, lo, hi int) bool {
		if len(d) < n {
			return false
		}
		v := 0
		for i := 0; i < n; i++ {
			v = v*10 + int(d[i]-'0')
		}
		return v >= lo && v <= hi
	}
	if d == "" {
		return false
	}
	switch d[0] {
	case '4', '5', '6':
		return true
	case '1':
		return in(4, 1946, 1946)
	case '2':
		return in(4, 2200, 2205) || in(4, 2221, 2720)
	case '3':
		return in(2, 30, 31) || in(2, 34, 34) || in(4, 3528, 3589) || in(2, 36, 39)
	case '8':
		return in(2, 81, 82) || in(4, 8600, 8600)
	case '9':
		return in(4, 9704, 9704) || in(4, 9792, 9792) || in(4, 9860, 9860)
	}
	return false
}
