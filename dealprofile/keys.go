// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Counterparty identifiers never enter a sealed record raw (PROFILE.md §4).
// A record carries a per-deal HMAC fingerprint of each normalized identifier,
// and free text that may contain identifiers is carried as a salted
// commitment. Raw values, the per-deal key and the commitment nonces stay in
// the producer's local store.

// ErrNotNormalizable is returned for an identifier that cannot be
// normalized. Such a value is not fingerprinted and not recorded.
var ErrNotNormalizable = errors.New("identifier cannot be normalized")

// Kinds are the counterparty identifier kinds a record may carry, a closed
// set (PROFILE.md §2).
func Kinds() []string { return append([]string(nil), kinds...) }

var kinds = []string{"payee", "name", "domain", "phone", "email", "relay_address", "profile_id"}

var legalForms = map[string]bool{"llc": true, "inc": true, "ltd": true, "corp": true, "co": true, "company": true, "gmbh": true, "llp": true, "plc": true}

var (
	phoneExtension = regexp.MustCompile(`(?i)\s*(?:ext\.?|x|#)\s*\d+\s*$`)
	nonDigit       = regexp.MustCompile(`\D`)
	payeeIsPhone   = regexp.MustCompile(`^\s*\+?[\d\s().-]{7,}\s*$`)
	urlScheme      = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://`)
	domainPort     = regexp.MustCompile(`:\d+$`)
)

func normPhone(v string) (string, error) {
	v = strings.TrimSpace(norm.NFKC.String(v))
	if loc := phoneExtension.FindStringIndex(v); loc != nil {
		v = v[:loc[0]]
	}
	plus := strings.HasPrefix(v, "+")
	digits := nonDigit.ReplaceAllString(v, "")
	if !plus {
		switch {
		case len(digits) == 10: // v0 default region: NANP
			digits = "1" + digits
		case len(digits) == 11 && strings.HasPrefix(digits, "1"):
		default:
			return "", ErrNotNormalizable
		}
	}
	if len(digits) < 8 || len(digits) > 15 {
		return "", ErrNotNormalizable
	}
	return "+" + digits, nil
}

func normEmail(v string) (string, error) {
	v = strings.ToLower(norm.NFC.String(strings.TrimSpace(v)))
	if strings.Count(v, "@") != 1 || strings.HasPrefix(v, "@") || strings.HasSuffix(v, "@") {
		return "", ErrNotNormalizable
	}
	return v, nil
}

func normDomain(v string) (string, error) {
	v = strings.ToLower(norm.NFC.String(strings.TrimSpace(v)))
	v = urlScheme.ReplaceAllString(v, "")
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	if i := strings.LastIndex(v, "@"); i >= 0 {
		v = v[i+1:]
	}
	v = strings.TrimRight(domainPort.ReplaceAllString(v, ""), ".")
	ascii, err := idna.Lookup.ToASCII(v)
	if err != nil {
		return "", ErrNotNormalizable
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", ErrNotNormalizable
	}
	for _, l := range labels {
		if l == "" {
			return "", ErrNotNormalizable
		}
	}
	registrable, err := publicsuffix.EffectiveTLDPlusOne(ascii)
	if err != nil {
		return "", ErrNotNormalizable
	}
	out, err := idna.Lookup.ToUnicode(registrable)
	if err != nil {
		return "", ErrNotNormalizable
	}
	return out, nil
}

var caseFolder = cases.Fold()

// foldCase is full case folding as Unicode's CaseFolding.txt defines it.
// x/text's cases.Fold swaps Cherokee case instead (the capitals U+13A0..U+13F5
// fold to the small forms U+AB70..U+ABBF and U+13F8..U+13FD, and the small
// forms to the capitals), so the two cases of one Cherokee name would differ;
// CaseFolding.txt folds the small forms to the capitals, and so does this.
type foldCaser struct{}

var foldCase foldCaser

func (foldCaser) String(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0xAB70 && r <= 0xABBF:
			return r - 0xAB70 + 0x13A0
		case r >= 0x13F8 && r <= 0x13FD:
			return r - 0x13F8 + 0x13F0
		}
		return r
	}, caseFolder.String(s))
}

func normName(v string) (string, error) {
	v = foldCase.String(norm.NFC.String(v))
	v = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			return ' '
		}
		return r
	}, v)
	toks := strings.Fields(v)
	if len(toks) > 0 && toks[0] == "the" {
		toks = toks[1:]
	}
	for len(toks) > 0 && legalForms[toks[len(toks)-1]] {
		toks = toks[:len(toks)-1]
	}
	if len(toks) == 0 {
		return "", ErrNotNormalizable
	}
	return strings.Join(toks, " "), nil
}

// Normalize is one raw identifier of kind in the form its fingerprint is
// taken over (PROFILE.md §4), or ErrNotNormalizable.
func Normalize(kind, v string) (string, error) {
	switch kind {
	case "phone":
		return normPhone(v)
	case "email":
		return normEmail(v)
	case "domain":
		return normDomain(v)
	case "name":
		return normName(v)
	case "payee":
		switch {
		case strings.Contains(v, "@"):
			return normEmail(v)
		case payeeIsPhone.MatchString(v):
			return normPhone(v)
		default:
			return normName(v)
		}
	case "relay_address":
		return strings.ToLower(norm.NFC.String(strings.TrimSpace(v))), nil
	case "profile_id":
		out := norm.NFC.String(strings.TrimSpace(v))
		if out == "" {
			return "", ErrNotNormalizable
		}
		return out, nil
	default:
		return "", ErrNotNormalizable
	}
}

func hmacSHA256(key []byte, parts ...string) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write([]byte(p))
	}
	return m.Sum(nil)
}

// DealKey is a deal's key, derived from the store secret and the deal id. A
// producer stores it with the deal at open, so a later rotation of the store
// secret cannot change a deal in progress.
func DealKey(storeSecret []byte, dealID string) []byte {
	return hmacSHA256(storeSecret, "x-deal-v0/deal-key\x00", dealID)
}

// ProfileKey is the key of a profile's own fingerprints (ProfileFPAlg):
// one merchant has one such fingerprint across the profile's deals. Like the
// store secret, it never leaves the device.
func ProfileKey(storeSecret []byte) []byte {
	return hmacSHA256(storeSecret, "x-deal-v0/profile-key\x00")
}

// Fingerprint is the fingerprint of one raw identifier of kind under key (a
// deal key, or a profile key), or ErrNotNormalizable, in which case the
// value is not recorded.
func Fingerprint(key []byte, kind, raw string) (string, error) {
	n, err := Normalize(kind, raw)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hmacSHA256(key, "x-deal-v0/fp\x00", kind, "\x00", n)), nil
}

// CommitText is the salted commitment CommitAlg names: SHA-256 over the JCS
// bytes of {"nonce", "text"}, with nonceHex a fresh random 256-bit value (64
// lowercase hex) for each commitment. Whoever holds its opening (nonce and
// text) recomputes it, and nobody else can test a guess against it.
func CommitText(nonceHex, text string) (string, error) {
	return canonical.JSONDigest(map[string]interface{}{"nonce": nonceHex, "text": text})
}
