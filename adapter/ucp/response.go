// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"

	"github.com/action-state-group/evidencebook/adapter"
)

// PropResponseSignature is the RFC 9421 signature on an operation response
// (ucp-v0 §5 at 75009695, "A signed response is not the order's
// signature"). It is message-level provenance, reported as a property of its
// own beside the object's checks and outside them: a PASS never makes the
// object's native check PASS.
const PropResponseSignature = "ucp.response_signature"

// checkResponse reports an operation response held whole: the body's object
// checks, as for the body alone, then ucp.response_signature.
func checkResponse(raw []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	h, err := parseRequest(raw)
	if err != nil {
		out := asReports(CheckCheckout, checkCheckout(nil, err, policy))
		return append(out, responseSignatureResult(CheckCheckout, adapter.Fail, "RFC 9421 over the response", "the response could not be read: "+err.Error()))
	}
	object := CheckCheckout
	var out []adapter.PropertyResult
	v, perr := parseIJSON(h.body)
	if o, ok := v.(map[string]any); perr == nil && ok && o["checkout_id"] != nil {
		object = CheckOrder
		out = asReports(object, checkOrderBody(o, policy))
	} else {
		if perr == nil && !ok {
			perr = errNotObject
		}
		out = asReports(object, checkCheckout(v, perr, policy))
	}
	return append(out, responseSignature(h, object, policy))
}

func responseSignatureResult(object string, st adapter.Status, method, explanation string) adapter.PropertyResult {
	return adapter.PropertyResult{
		Property:     PropResponseSignature,
		Status:       st,
		Method:       method,
		Detail:       explanation,
		DetailFields: map[string]string{"check": PropResponseSignature, "object": object},
	}
}

// responseSignature checks the response's RFC 9421 signature (UCP Message
// Signatures §REST Response Verification): the covered set includes
// @status, and content-digest and content-type when there is a body;
// Content-Digest is the SHA-256 of the raw body; the signature verifies over
// the rebuilt signature base under the key that keyid names in the retained
// keys[]. With several signatures, the first that passes is reported, else
// the first.
func responseSignature(h *httpRequest, object string, policy adapter.TrustPolicy) adapter.PropertyResult {
	labels, inputs, sigs, err := signatureHeaders(h)
	if err != nil {
		st := adapter.NotPresent
		if strings.HasPrefix(err.Error(), "malformed") {
			st = adapter.Fail
		}
		return responseSignatureResult(object, st, "RFC 9421 over the response", err.Error())
	}
	var first adapter.PropertyResult
	for i, label := range labels {
		r := responseLabel(h, label, inputs[label], sigs, object, policy)
		if r.Status == adapter.Pass {
			return r
		}
		if i == 0 {
			first = r
		}
	}
	return first
}

func responseLabel(h *httpRequest, label string, in sfMember, sigs map[string]sfMember, object string, policy adapter.TrustPolicy) adapter.PropertyResult {
	at := "signature " + label + ": "
	if !in.isList {
		return responseSignatureResult(object, adapter.Fail, "RFC 9421 over the response", at+"Signature-Input member is not an inner list")
	}
	covered, _ := serializeInnerList(in.inner, nil)
	res := func(st adapter.Status, alg, explanation string) adapter.PropertyResult {
		if alg == "" {
			alg = "<alg>"
		}
		return responseSignatureResult(object, st, "RFC 9421 "+alg+" over "+covered, at+explanation)
	}

	var names, missing []string
	values := map[string]string{}
	var unsupported error
	dup := false
	for _, c := range in.inner {
		name, ok := c.val.(string)
		if !ok {
			unsupported = errors.New("a component identifier is not a string")
			continue
		}
		dup = dup || slices.Contains(names, name)
		names = append(names, name)
		v, err := h.componentValue(c)
		switch {
		case errors.Is(err, errNotHeld):
			missing = append(missing, name)
		case err != nil:
			unsupported = err
		default:
			values[name] = v
		}
	}
	if len(missing) > 0 {
		return res(adapter.NotPresent, "", "covered but not kept: "+strings.Join(missing, ", "))
	}
	sm, ok := sigs[label]
	var sig []byte
	if ok && !sm.isList {
		sig, _ = sm.item.val.([]byte)
	}
	if sig == nil {
		return res(adapter.NotPresent, "", "no Signature byte sequence for this label")
	}
	required := []string{"@status"}
	if len(h.body) > 0 {
		required = append(required, "content-digest", "content-type")
	}
	for _, c := range required {
		if !slices.Contains(names, c) {
			return res(adapter.Fail, "", "signature_invalid: "+c+" is not covered")
		}
	}
	if tagV, _ := in.param("tag"); tagV != nil && tagV != "web-bot-auth" {
		return res(adapter.NotChecked, "", "the tag is not one UCP defines or accepts")
	}
	if unsupported != nil {
		return res(adapter.NotChecked, "", unsupported.Error())
	}
	if dup {
		return res(adapter.Fail, "", "a component is covered twice (RFC 9421 §2.5)")
	}
	kidV, _ := in.param("keyid")
	kid, _ := kidV.(string)
	if kid == "" {
		return res(adapter.Fail, "", "Signature-Input has no keyid")
	}
	pub, keyRow := resolveKey(policy, kid, "", PropResponseSignature)
	switch keyRow.Status {
	case adapter.Pass:
	case adapter.Fail:
		return res(adapter.Fail, "", "key_not_found: "+keyRow.Detail)
	default:
		return res(keyRow.Status, "", keyRow.Detail)
	}
	alg, hash := "", crypto.Hash(0)
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		switch k.Curve.Params().Name {
		case "P-256":
			alg, hash = "ecdsa-p256-sha256", crypto.SHA256
		case "P-384":
			alg, hash = "ecdsa-p384-sha384", crypto.SHA384
		}
	case ed25519.PublicKey:
		alg = "ed25519"
	}
	if alg == "" {
		return res(adapter.NotChecked, "", "the key's curve is not one this verifier implements for RFC 9421")
	}
	if d := digestRow(h); d.Status != adapter.Pass {
		return res(adapter.Fail, alg, d.Detail)
	}
	if a, ok := in.param("alg"); ok && a != alg {
		return res(adapter.Fail, alg, "the alg parameter does not match the key")
	}
	base, err := signatureBase(in, values)
	if err != nil {
		return res(adapter.Fail, alg, err.Error())
	}
	var good bool
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		good = verifyECDSA(k, hash, []byte(base), sig)
	case ed25519.PublicKey:
		good = ed25519.Verify(k, []byte(base), sig)
	}
	if !good {
		return res(adapter.Fail, alg, "signature_invalid: the signature does not verify over the rebuilt signature base")
	}
	return res(adapter.Pass, alg, "kid "+quote(kid))
}
