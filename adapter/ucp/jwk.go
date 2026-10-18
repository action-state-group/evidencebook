// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
)

var b64url = base64.RawURLEncoding.Strict()

// jwk is one member of a business profile's keys[] (RFC 7517), as retained
// by the relying party.
type jwk struct {
	raw map[string]any
	kid string
}

func (k jwk) str(name string) string { s, _ := k.raw[name].(string); return s }

// keySet is the retained copy of keys[] the relying party supplies in its
// trust policy. The adapter never fetches a profile.
type keySet []jwk

// parseKeySet reads a JWK Set ({"keys": [...]}) or a bare keys[] array.
func parseKeySet(text string) (keySet, error) {
	v, err := parseIJSON([]byte(text))
	if err != nil {
		return nil, err
	}
	if o, ok := v.(map[string]any); ok {
		v = o["keys"]
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, errors.New(`neither a JWK Set nor a keys[] array`)
	}
	var ks keySet
	for i, e := range arr {
		o, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("keys[%d] is not an object", i)
		}
		kid, _ := o["kid"].(string)
		ks = append(ks, jwk{raw: o, kid: kid})
	}
	return ks, nil
}

// lookup finds the key for kid among the keys usable for signature
// verification. UCP's Identity Resolution Algorithm step 4 skips a key marked
// use:"enc" and a key whose key_ops is present without "verify" (RFC 7517
// §4.2, §4.3); a key with use:"sig", or with neither member, is eligible. The
// first eligible key with that kid is used.
func (ks keySet) lookup(kid string) (jwk, bool, string) {
	skipped := ""
	for _, k := range ks {
		if k.kid != kid {
			continue
		}
		if k.str("use") == "enc" {
			skipped = `use:"enc"`
			continue
		}
		if ops, present := k.raw["key_ops"]; present {
			arr, _ := ops.([]any)
			verify := false
			for _, op := range arr {
				if op == "verify" {
					verify = true
				}
			}
			if !verify {
				skipped = `key_ops without "verify"`
				continue
			}
		}
		return k, true, ""
	}
	return jwk{}, false, skipped
}

// publicKey decodes the JWK's public key: an EC key on P-256, P-384 or P-521
// (RFC 7518 §6.2), or an OKP Ed25519 key (RFC 8037). errUnsupportedKey marks
// a well-formed key of a type this verifier does not implement.
func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch kty := k.str("kty"); kty {
	case "EC":
		var curve elliptic.Curve
		var size int
		switch k.str("crv") {
		case "P-256":
			curve, size = elliptic.P256(), 32
		case "P-384":
			curve, size = elliptic.P384(), 48
		case "P-521":
			curve, size = elliptic.P521(), 66
		default:
			return nil, fmt.Errorf("%w: EC curve %q", errUnsupportedKey, k.str("crv"))
		}
		x, err := b64url.DecodeString(k.str("x"))
		if err != nil || len(x) != size {
			return nil, fmt.Errorf("EC x is not %d base64url octets", size)
		}
		y, err := b64url.DecodeString(k.str("y"))
		if err != nil || len(y) != size {
			return nil, fmt.Errorf("EC y is not %d base64url octets", size)
		}
		// crypto/ecdh rejects a point that is not on the curve.
		point := append(append([]byte{4}, x...), y...)
		var ecdhCurve ecdh.Curve
		switch size {
		case 32:
			ecdhCurve = ecdh.P256()
		case 48:
			ecdhCurve = ecdh.P384()
		default:
			ecdhCurve = ecdh.P521()
		}
		if _, err := ecdhCurve.NewPublicKey(point); err != nil {
			return nil, errors.New("EC point is not on the curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	case "OKP":
		if k.str("crv") != "Ed25519" {
			return nil, fmt.Errorf("%w: OKP curve %q", errUnsupportedKey, k.str("crv"))
		}
		x, err := b64url.DecodeString(k.str("x"))
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, errors.New("Ed25519 x is not 32 base64url octets")
		}
		return ed25519.PublicKey(x), nil
	default:
		return nil, fmt.Errorf("%w: kty %q", errUnsupportedKey, kty)
	}
}

var errUnsupportedKey = errors.New("unsupported key type")

// thumbprint is the RFC 7638 SHA-256 JWK thumbprint, base64url.
func (k jwk) thumbprint() (string, error) {
	var members map[string]any
	switch k.str("kty") {
	case "EC":
		members = map[string]any{"crv": k.str("crv"), "kty": "EC", "x": k.str("x"), "y": k.str("y")}
	case "OKP":
		members = map[string]any{"crv": k.str("crv"), "kty": "OKP", "x": k.str("x")}
	default:
		return "", fmt.Errorf("%w: kty %q", errUnsupportedKey, k.str("kty"))
	}
	c, err := jcs(members)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// verifyECDSA checks a fixed-width r||s signature (RFC 7518 §3.4, RFC 9421
// §3.3.1) over message under hash h.
func verifyECDSA(pub *ecdsa.PublicKey, h crypto.Hash, message, sig []byte) bool {
	size := (pub.Curve.Params().BitSize + 7) / 8
	if len(sig) != 2*size {
		return false
	}
	hh := h.New()
	hh.Write(message)
	r := new(big.Int).SetBytes(sig[:size])
	s := new(big.Int).SetBytes(sig[size:])
	return ecdsa.Verify(pub, hh.Sum(nil), r, s)
}
