// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/action-state-group/evidencebook/adapter"
)

// Properties reported only for a settlement-records leg. They are not
// native checks: wrapped_content_check is the content check of
// draft-mih-agent-settlement-records-00, Wrapping Existing Signed Objects,
// rule 2 (its failure is that draft's wrapped_digest_mismatch), reported
// once per wrapped x402 type with the type in detail; leg_amount and
// leg_network compare the leg's own members with the wrapped payment
// payload.
const (
	PropWrappedContent = "wrapped_content_check"
	PropLegAmount      = "leg_amount"
	PropLegNetwork     = "leg_network"
)

const (
	MethodWrappedContent = "settlement-records Wrapping Existing Signed Objects rule 2 (content check): SHA-256 over the content octets (base64url without padding, decoded) equals digest"
	MethodLegAmount      = "settlement.amount.value = the wrapped payment payload's accepted.amount, as decimal strings"
	MethodLegNetwork     = "settlement.payment_ref.network (x402.transaction qualifier) = the wrapped payment payload's accepted.network"
)

const (
	wrappedPaymentPayload = "x402.payment-payload"
	wrappedSettleResponse = "x402.settle-response"
	wrappedReceipt        = "x402.receipt"
)

var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

var errNeitherForm = errors.New("a JSON object that is neither a settlement-records leg (no settlement member) nor an x402 payment payload (no x402Version)")

// checkJSON handles the two JSON input forms: a settlement-records leg (an
// object with a settlement member) and the bare payment payload.
func checkJSON(raw []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	o := &out{}
	v, err := decodeStrict(raw)
	root, ok := asObj(v)
	switch {
	case err == nil && ok && root.has("settlement"):
		checkLeg(o, root, policy)
	case err == nil && ok && !root.has("x402Version"):
		checkPayload(o, raw, errNeitherForm, policy)
		checkResponse(o, nil, nil, nil, policy)
		checkReceiptOctets(o, nil, nil, nil, policy)
	default:
		// The bare payment payload, or JSON that does not parse (reported by
		// payload_form).
		checkObjects(o, raw, nil, nil, policy)
	}
	return o.results
}

// unwrap finds the single wrapped entry of type typ, reports its content
// check, and returns the content octets (nil when absent or unusable).
func unwrap(o *out, wrapped []any, typ string) []byte {
	set := func(st adapter.Status, d string) { o.plain(PropWrappedContent, st, MethodWrappedContent, typ+": "+d) }
	var entries []obj
	for _, e := range wrapped {
		if eo, ok := asObj(e); ok {
			if t, _ := eo.str("type"); t == typ {
				entries = append(entries, eo)
			}
		}
	}
	switch len(entries) {
	case 0:
		set(adapter.NotPresent, "no wrapped entry")
		return nil
	case 1:
	default:
		set(adapter.Fail, fmt.Sprintf("%d wrapped entries of this type; which one is meant is ambiguous", len(entries)))
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
		set(adapter.NotPresent, "the entry carries the digest only: possession of a digest, nothing to check natively")
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
		set(adapter.Fail, fmt.Sprintf("wrapped_digest_mismatch: content hashes to %s, digest is %s", got, digest))
		return octets
	}
	set(adapter.Pass, fmt.Sprintf("%d octets, SHA-256 %s", len(octets), digest))
	return octets
}

func checkLeg(o *out, root obj, policy adapter.TrustPolicy) {
	settlement, ok := root.object("settlement")
	var wrapped []any
	if ok && settlement.has("wrapped") {
		wrapped, ok = settlement["wrapped"].([]any)
	}
	if !ok {
		// Nothing can be unwrapped: each content check fails, and the
		// native checks have no octets.
		for _, t := range []string{wrappedPaymentPayload, wrappedSettleResponse, wrappedReceipt} {
			o.plain(PropWrappedContent, adapter.Fail, MethodWrappedContent, t+": settlement or settlement.wrapped is not of the draft's type")
		}
		checkObjects(o, nil, nil, nil, policy)
		o.plain(PropLegAmount, adapter.NotChecked, MethodLegAmount, "settlement not readable")
		o.plain(PropLegNetwork, adapter.NotChecked, MethodLegNetwork, "settlement not readable")
		return
	}
	pay := checkPayload(o, unwrap(o, wrapped, wrappedPaymentPayload), nil, policy)
	resp := checkResponse(o, unwrap(o, wrapped, wrappedSettleResponse), nil, pay, policy)
	checkReceiptOctets(o, unwrap(o, wrapped, wrappedReceipt), resp, pay, policy)
	checkLegCross(o, settlement, pay)
}

func checkLegCross(o *out, settlement obj, pay *payment) {
	var accepted obj
	if pay != nil && pay.inScope {
		accepted = pay.accepted
	}
	cross := func(prop, method, legPath, legVal string, legOK bool, member string) {
		switch {
		case !legOK:
			o.plain(prop, adapter.NotPresent, method, "the leg has no "+legPath)
		case accepted == nil:
			o.plain(prop, adapter.NotChecked, method, "no in-scope x402 v2 payment payload to compare with")
		default:
			want, ok := accepted.str(member)
			switch {
			case !ok:
				o.plain(prop, adapter.Fail, method, "accepted."+member+" is missing")
			case want == legVal:
				o.plain(prop, adapter.Pass, method, legPath+" "+legVal+" equals accepted."+member)
			default:
				o.plain(prop, adapter.Fail, method, fmt.Sprintf("%s %s, accepted.%s %s", legPath, legVal, member, want))
			}
		}
	}
	amount, _ := settlement.object("amount")
	value, ok := amount.str("value")
	cross(PropLegAmount, MethodLegAmount, "settlement.amount.value", value, ok, "amount")

	ref, _ := settlement.object("payment_ref")
	network, ok := ref.str("network")
	if t, _ := ref.str("type"); ok && t != "x402.transaction" {
		o.plain(PropLegNetwork, adapter.NotChecked, MethodLegNetwork, fmt.Sprintf("payment_ref type %q is not x402.transaction", t))
		return
	}
	cross(PropLegNetwork, MethodLegNetwork, "settlement.payment_ref.network", network, ok, "network")
}
