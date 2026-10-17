// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"crypto"
	"crypto/ecdsa"
	_ "crypto/sha256" // SHA-256 for ES256
	_ "crypto/sha512" // SHA-384 and SHA-512 for ES384 and ES512
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"

	"github.com/action-state-group/evidencebook/adapter"
)

// The §3(e) properties of the AP2-signed checkout, in declaration order.
const (
	PropCheckoutSchema = "ucp.checkout.schema"
	PropMAPresent      = "ucp.merchant_authorization.present"
	PropMAForm         = "ucp.merchant_authorization.form"
	PropMAAlg          = "ucp.merchant_authorization.alg"
	PropMAKey          = "ucp.merchant_authorization.key"
	PropMASignature    = "ucp.merchant_authorization.signature"
	PropBusinessTrust  = "ucp.business_trust"
	// CheckCheckout is the object name of the checkout, and the check name
	// of the one native_check reported for a checkout with no native
	// signature (§5): NOT_PRESENT.
	CheckCheckout = "ucp.checkout"
)

const (
	methodSchema      = "JSON Schema 2020-12, UCP " + UCPVersion // the order (§4(e))
	methodMAPresent   = "UCP AP2 Mandates §Business Authorization"
	methodMAForm      = "RFC 7515 Appendix F detached JWS"
	methodMAAlg       = "AP2 v0.2 algorithm rule"
	methodKeyLive     = "UCP profile keys[] lookup by kid, live"
	methodMANative    = "UCP AP2 Mandates §Step 1 verify_merchant_authorization: detached JWS over JCS(checkout − ap2), key by kid from the business keys[]"
	methodNoSignature = "none: UCP defines no object signature for this object"

	ap2Capability = "dev.ucp.common.payment.ap2_mandate"
)

// detachedJWS is the schema pattern of ap2.merchant_authorization.
var detachedJWS = regexp.MustCompile(`^[A-Za-z0-9_-]+\.\.[A-Za-z0-9_-]+$`)

// ap2Algs are the algorithms AP2 v0.2 accepts, with the curve and hash each
// uses. The verifier supports all three, so the alg row's NOT_CHECKED case
// (accepted by AP2, unsupported here) does not arise.
var ap2Algs = map[string]struct {
	crv  string
	hash crypto.Hash
}{
	"ES256": {"P-256", crypto.SHA256},
	"ES384": {"P-384", crypto.SHA384},
	"ES512": {"P-521", crypto.SHA512},
}

func methodMASignature(alg string) string {
	if _, ok := ap2Algs[alg]; !ok {
		alg = "ES256/ES384/ES512"
	}
	return alg + " over JWS signing input of JCS(checkout − ap2)"
}

// checkCheckout reports §3(e) for a parsed checkout object.
func checkCheckout(v any, parseErr error, policy adapter.TrustPolicy) []adapter.PropertyResult {
	r := newRows()
	if parseErr != nil {
		r.add(PropCheckoutSchema, adapter.Fail, checkoutSchemaMethod(nil, policy), "not I-JSON: "+parseErr.Error())
		for _, p := range []string{PropMAPresent, PropMAForm, PropMAAlg, PropMAKey, PropMASignature} {
			r.add(p, adapter.NotChecked, methodFor(p, ""), "the object could not be parsed")
		}
		r.businessTrust(policy, policy.Pin(PinKeysOrigin))
		return r.out
	}
	co := v.(map[string]any)
	methodSchema := checkoutSchemaMethod(co, policy)

	// ucp.checkout.schema
	if ok, why, err := validate(false, co); err != nil {
		r.add(PropCheckoutSchema, adapter.NotChecked, methodSchema, "schemas unavailable: "+err.Error())
	} else if ok {
		r.add(PropCheckoutSchema, adapter.Pass, methodSchema, "")
	} else {
		r.add(PropCheckoutSchema, adapter.Fail, methodSchema, why)
	}

	// ucp.merchant_authorization.present
	ap2, _ := co["ap2"].(map[string]any)
	maVal, present := ap2["merchant_authorization"]
	if !present {
		native, detail := methodMANative, "merchant_authorization_missing: AP2 was negotiated and the checkout omits ap2.merchant_authorization"
		if !ap2Negotiated(co) {
			native, detail = methodNoSignature, "the AP2 Mandates Extension is not in the checkout's ucp.capabilities, so the object carries no object signature; holding it proves possession of its octets only"
		}
		r.add(PropMAPresent, adapter.NotPresent, methodMAPresent, detail)
		for _, p := range []string{PropMAForm, PropMAAlg, PropMAKey, PropMASignature} {
			r.add(p, adapter.NotPresent, methodFor(p, ""), "no ap2.merchant_authorization")
		}
		if native == methodNoSignature {
			// §5: an object with no native signature reports one
			// native_check NOT_PRESENT under the possession-only method.
			r.add(CheckCheckout, adapter.NotPresent, native, detail)
		}
		r.businessTrust(policy, policy.Pin(PinKeysOrigin))
		return r.out
	}
	r.add(PropMAPresent, adapter.Pass, methodMAPresent, "")

	// ucp.merchant_authorization.form
	hdr, encHeader, sig, formErr := parseDetachedJWS(maVal)
	if formErr != nil {
		r.add(PropMAForm, adapter.Fail, methodMAForm, formErr.Error())
		for _, p := range []string{PropMAAlg, PropMAKey, PropMASignature} {
			r.add(p, adapter.NotChecked, methodFor(p, ""), "ap2.merchant_authorization is not a readable detached JWS")
		}
		r.businessTrust(policy, policy.Pin(PinKeysOrigin))
		return r.out
	}
	r.add(PropMAForm, adapter.Pass, methodMAForm, "")
	alg, kid := hdr["alg"].(string), hdr["kid"].(string)

	// ucp.merchant_authorization.alg
	algSpec, algOK := ap2Algs[alg]
	if algOK {
		r.add(PropMAAlg, adapter.Pass, methodMAAlg, alg)
	} else {
		r.add(PropMAAlg, adapter.Fail, methodMAAlg, fmt.Sprintf("alg %q is not one AP2 accepts (ES256, ES384, ES512)", alg))
	}

	// ucp.merchant_authorization.key
	key, keyRow := resolveKey(policy, kid, "", PropMAKey)
	r.out = append(r.out, keyRow)

	// ucp.merchant_authorization.signature
	sigMethod := methodMASignature(alg)
	switch {
	case !algOK:
		r.add(PropMASignature, adapter.NotChecked, sigMethod, "alg not accepted")
	case keyRow.Status != adapter.Pass:
		r.add(PropMASignature, adapter.NotChecked, sigMethod, "no usable key for kid "+quote(kid))
	default:
		payload := map[string]any{}
		for k, val := range co {
			if k != "ap2" {
				payload[k] = val
			}
		}
		canon, err := jcs(payload)
		switch {
		case err != nil:
			r.add(PropMASignature, adapter.NotChecked, sigMethod, "RFC 8785: "+err.Error())
		default:
			input := encHeader + "." + base64.RawURLEncoding.EncodeToString(canon)
			pub, isEC := key.(*ecdsa.PublicKey)
			switch {
			case !isEC || pub.Curve.Params().Name != algSpec.crv:
				r.add(PropMASignature, adapter.Fail, sigMethod, fmt.Sprintf("the key for kid %s is not a %s key, as %s requires", quote(kid), algSpec.crv, alg))
			case !verifyECDSA(pub, algSpec.hash, []byte(input), sig):
				r.add(PropMASignature, adapter.Fail, sigMethod, "merchant_authorization_invalid: the signature does not verify")
			default:
				r.add(PropMASignature, adapter.Pass, sigMethod, "")
			}
		}
	}
	r.businessTrust(policy, policy.Pin(PinKeysOrigin))
	return r.out
}

// checkoutSchemaMethod is the schema check's method (ucp-v0 §3(e)):
// "ucp-schema validate --response --op <operation>, UCP v2026-08-25". The
// operation is the policy's ucp.operation pin when set; otherwise it is
// inferred from status (completed: complete; canceled: cancel; any other:
// read). The UCP schemas annotate only requests (ucp_request), so for a
// response the operation does not change what is validated; it names the
// response the object is taken to be.
func checkoutSchemaMethod(co map[string]any, policy adapter.TrustPolicy) string {
	op := policy.Pin(PinOperation)
	if op == "" {
		switch co["status"] {
		case "completed":
			op = "complete"
		case "canceled":
			op = "cancel"
		default:
			op = "read"
		}
	}
	return "ucp-schema validate --response --op " + op + ", UCP " + UCPVersion
}

func methodFor(p, alg string) string {
	switch p {
	case PropMAForm:
		return methodMAForm
	case PropMAAlg:
		return methodMAAlg
	case PropMAKey:
		return methodKeyLive
	case PropMASignature:
		return methodMASignature(alg)
	}
	return ""
}

func ap2Negotiated(co map[string]any) bool {
	u, _ := co["ucp"].(map[string]any)
	caps, _ := u["capabilities"].(map[string]any)
	_, ok := caps[ap2Capability]
	return ok
}

// parseDetachedJWS checks §3(c) steps 1 and 2: the schema pattern, a
// protected header that decodes to an I-JSON object with string alg and kid,
// and no crit (this verifier understands no JWS extension, RFC 7515
// §4.1.11).
func parseDetachedJWS(v any) (hdr map[string]any, encHeader string, sig []byte, err error) {
	s, ok := v.(string)
	if !ok {
		return nil, "", nil, errors.New("ap2.merchant_authorization is not a string")
	}
	if !detachedJWS.MatchString(s) {
		return nil, "", nil, errors.New(`not of the form <base64url-header>..<base64url-signature>`)
	}
	encHeader, encSig, _ := cutDetached(s)
	hb, err := b64url.DecodeString(encHeader)
	if err != nil {
		return nil, "", nil, errors.New("protected header is not base64url without padding")
	}
	hv, err := parseIJSON(hb)
	if err != nil {
		return nil, "", nil, fmt.Errorf("protected header is not I-JSON: %v", err)
	}
	hdr, ok = hv.(map[string]any)
	if !ok {
		return nil, "", nil, errors.New("protected header is not a JSON object")
	}
	if _, ok := hdr["alg"].(string); !ok {
		return nil, "", nil, errors.New("protected header has no string alg")
	}
	if kid, ok := hdr["kid"].(string); !ok || kid == "" {
		return nil, "", nil, errors.New("protected header has no kid")
	}
	if _, ok := hdr["crit"]; ok {
		return nil, "", nil, errors.New("protected header has crit, and no extension is understood")
	}
	sig, err = b64url.DecodeString(encSig)
	if err != nil {
		return nil, "", nil, errors.New("signature is not base64url without padding")
	}
	return hdr, encHeader, sig, nil
}

func cutDetached(s string) (string, string, bool) {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return s[:i], s[i+2:], true
		}
	}
	return s, "", false
}

func quote(s string) string { return fmt.Sprintf("%q", s) }
