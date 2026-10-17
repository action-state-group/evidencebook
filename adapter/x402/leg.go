// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/action-state-group/evidencebook/adapter"
)

// Properties reported only for a settlement-records leg.
const (
	PropWrappedDigest       = "wrapped_digest"
	PropSettleWrappedDigest = "settle_response_wrapped_digest"
	PropLegAmount           = "leg_amount"
	PropLegNetwork          = "leg_network"
)

const (
	MethodWrappedDigest = "SHA-256 over the exact octets of the wrapped entry's content (base64url, no padding, decoded), compared to its digest (draft-mih-agent-settlement-records-00, Wrapping Existing Signed Objects, rule 2)"
	MethodLegAmount     = "settlement.amount.value compared to the wrapped payment payload's accepted.amount, as decimal strings"
	MethodLegNetwork    = "settlement.payment_ref.network (x402.transaction qualifier) compared to the wrapped payment payload's accepted.network"
)

var legOnly = map[string]bool{
	PropWrappedDigest: true, PropSettleWrappedDigest: true, PropLegAmount: true, PropLegNetwork: true,
}

const (
	wrappedPaymentPayload = "x402.payment-payload"
	wrappedSettleResponse = "x402.settle-response"
)

var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// checkJSON handles the two JSON input forms: a settlement-records leg (an
// object with a settlement member) and the bare payment payload.
func checkJSON(raw []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	r := report{}
	v, err := decodeStrict(raw)
	root, ok := asObj(v)
	if err == nil && ok && root.has("settlement") {
		checkLeg(r, root, policy)
		return r.ordered()
	}
	if err == nil && ok && !root.has("x402Version") {
		checkAll(r, nil, nil, policy)
		r.set(PropPayloadForm, adapter.Fail, MethodPayloadForm, "a JSON object that is neither a settlement-records leg (no settlement member) nor an x402 payment payload (no x402Version)")
		for _, p := range []struct{ name, method string }{
			{PropPayerSignature, MethodPayerSig}, {PropChain, MethodChain}, {PropAsset, MethodAsset},
			{PropPayeeBinding, MethodPayee}, {PropAmountBinding, MethodAmount}, {PropValidityWindow, MethodWindow},
		} {
			r.set(p.name, adapter.NotChecked, p.method, "payment payload not readable")
		}
		return r.ordered()
	}
	// The bare payment payload, or JSON that does not parse (reported by the
	// payload form check).
	checkAll(r, raw, nil, policy)
	return r.ordered()
}

// unwrap finds the single wrapped entry of type typ, checks its content
// against its digest under prop, and returns the content octets (nil when
// absent or unusable).
func unwrap(r report, wrapped []any, typ, prop string) []byte {
	set := func(st adapter.Status, d string) { r.set(prop, st, MethodWrappedDigest, d) }
	var entries []obj
	for _, e := range wrapped {
		if o, ok := asObj(e); ok {
			if t, _ := o.str("type"); t == typ {
				entries = append(entries, o)
			}
		}
	}
	switch len(entries) {
	case 0:
		set(adapter.NotPresent, "no wrapped "+typ+" entry")
		return nil
	case 1:
	default:
		set(adapter.Fail, fmt.Sprintf("%d wrapped %s entries; which one is meant is ambiguous", len(entries), typ))
		return nil
	}
	e := entries[0]
	if alg, _ := e.str("digest_alg"); alg != "SHA-256" {
		set(adapter.NotChecked, fmt.Sprintf("digest_alg %q is not SHA-256", alg))
		return nil
	}
	digest, _ := e.str("digest")
	if !digestRE.MatchString(digest) {
		set(adapter.Fail, fmt.Sprintf("digest %q is not 64 lowercase hex digits", digest))
		return nil
	}
	if !e.has("content") {
		set(adapter.NotPresent, "the entry carries the digest only, not the content: possession of a digest, nothing to check natively")
		return nil
	}
	content, ok := e.str("content")
	if !ok {
		set(adapter.Fail, "content is not a string")
		return nil
	}
	octets, err := base64.RawURLEncoding.Strict().DecodeString(content)
	if err != nil {
		set(adapter.Fail, "content is not base64url without padding: "+err.Error())
		return nil
	}
	sum := sha256.Sum256(octets)
	if got := hex.EncodeToString(sum[:]); got != digest {
		// The octets held are not the ones the leg commits to; they are
		// still checked natively, and both results are reported.
		set(adapter.Fail, fmt.Sprintf("content hashes to %s, digest is %s", got, digest))
		return octets
	}
	set(adapter.Pass, fmt.Sprintf("%d octets, SHA-256 %s", len(octets), digest))
	return octets
}

func checkLeg(r report, root obj, policy adapter.TrustPolicy) {
	settlement, ok := root.object("settlement")
	if !ok {
		for _, p := range []struct{ name, method string }{
			{PropWrappedDigest, MethodWrappedDigest}, {PropSettleWrappedDigest, MethodWrappedDigest},
			{PropLegAmount, MethodLegAmount}, {PropLegNetwork, MethodLegNetwork},
		} {
			r.set(p.name, adapter.Fail, p.method, "settlement is not an object")
		}
		checkAll(r, nil, nil, policy)
		return
	}
	var wrapped []any
	if settlement.has("wrapped") {
		if wrapped, ok = settlement["wrapped"].([]any); !ok {
			r.set(PropWrappedDigest, adapter.Fail, MethodWrappedDigest, "settlement.wrapped is not an array")
			r.set(PropSettleWrappedDigest, adapter.Fail, MethodWrappedDigest, "settlement.wrapped is not an array")
		}
	}
	var payloadOctets, responseOctets []byte
	if ok {
		payloadOctets = unwrap(r, wrapped, wrappedPaymentPayload, PropWrappedDigest)
		responseOctets = unwrap(r, wrapped, wrappedSettleResponse, PropSettleWrappedDigest)
	}
	pay := checkAll(r, payloadOctets, responseOctets, policy)
	checkLegCross(r, settlement, pay)
}

func checkLegCross(r report, settlement obj, pay *payment) {
	var accepted obj
	if pay != nil {
		accepted = pay.accepted
	}
	cross := func(prop, method, legPath string, legVal string, legOK bool, member string) {
		switch {
		case !legOK:
			r.set(prop, adapter.NotPresent, method, "the leg has no "+legPath)
		case accepted == nil:
			r.set(prop, adapter.NotChecked, method, "no readable x402 v2 payment payload to compare with")
		default:
			want, ok := accepted.str(member)
			switch {
			case !ok:
				r.set(prop, adapter.Fail, method, "accepted."+member+" is missing")
			case want == legVal:
				r.set(prop, adapter.Pass, method, legPath+" "+legVal+" equals accepted."+member)
			default:
				r.set(prop, adapter.Fail, method, fmt.Sprintf("%s %s, accepted.%s %s", legPath, legVal, member, want))
			}
		}
	}
	amount, _ := settlement.object("amount")
	value, ok := amount.str("value")
	cross(PropLegAmount, MethodLegAmount, "settlement.amount.value", value, ok, "amount")

	ref, _ := settlement.object("payment_ref")
	network, ok := ref.str("network")
	if t, _ := ref.str("type"); ok && t != "x402.transaction" {
		r.set(PropLegNetwork, adapter.NotChecked, MethodLegNetwork, fmt.Sprintf("payment_ref type %q is not x402.transaction", t))
		return
	}
	cross(PropLegNetwork, MethodLegNetwork, "settlement.payment_ref.network", network, ok, "network")
}
