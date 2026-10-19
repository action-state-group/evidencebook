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
	"github.com/action-state-group/evidencebook/adapter/internal/ijson"
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
	v, err := ijson.Parse(raw)
	root, ok := ijson.AsObj(v)
	switch {
	case err == nil && ok && root.Has("settlement"):
		return checkLeg(root, policy)
	case err == nil && ok && !root.Has("x402Version"):
		return checkSections(objects{payload: raw, payloadErr: errNeitherForm}, policy).all()
	}
	// The bare payment payload, or JSON that does not parse (reported by
	// payload_form).
	return checkSections(objects{payload: raw}, policy).all()
}

// unwrap finds the single wrapped entry of type typ, reports its content
// check, and returns the content octets (nil when absent or unusable).
func unwrap(o *out, wrapped []any, typ string) []byte {
	set := func(st adapter.Status, d string) { o.plain(PropWrappedContent, st, MethodWrappedContent, typ+": "+d) }
	var entries []ijson.Obj
	for _, e := range wrapped {
		if eo, ok := ijson.AsObj(e); ok {
			if t, _ := eo.Str("type"); t == typ {
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
	if alg, _ := e.Str("digest_alg"); alg != "SHA-256" {
		set(adapter.NotChecked, fmt.Sprintf("digest_alg %q is not SHA-256", alg))
		return nil
	}
	digest, _ := e.Str("digest")
	if !digestRE.MatchString(digest) {
		set(adapter.Fail, fmt.Sprintf("digest %q is not 64 lowercase hex digits", digest))
		return nil
	}
	if !e.Has("content") {
		set(adapter.NotPresent, "the entry carries the digest only: possession of a digest, nothing to check natively")
		return nil
	}
	content, ok := e.Str("content")
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

func checkLeg(root ijson.Obj, policy adapter.TrustPolicy) []adapter.PropertyResult {
	settlement, ok := root.Object("settlement")
	var wrapped []any
	if ok && settlement.Has("wrapped") {
		wrapped, ok = settlement["wrapped"].([]any)
	}
	var wPay, wResp, wRcpt out
	var in objects
	if ok {
		in.payload = unwrap(&wPay, wrapped, wrappedPaymentPayload)
		in.response = unwrap(&wResp, wrapped, wrappedSettleResponse)
		in.receipt = unwrap(&wRcpt, wrapped, wrappedReceipt)
	} else {
		for t, w := range map[string]*out{wrappedPaymentPayload: &wPay, wrappedSettleResponse: &wResp, wrappedReceipt: &wRcpt} {
			w.plain(PropWrappedContent, adapter.Fail, MethodWrappedContent, t+": settlement or settlement.wrapped is not of the draft's type")
		}
	}
	s := checkSections(in, policy)
	var legRows out
	if ok {
		checkLegCross(&legRows, settlement, s.pay)
	} else {
		legRows.plain(PropLegAmount, adapter.NotChecked, MethodLegAmount, "settlement not readable")
		legRows.plain(PropLegNetwork, adapter.NotChecked, MethodLegNetwork, "settlement not readable")
	}
	var res []adapter.PropertyResult
	for _, part := range [][]adapter.PropertyResult{wPay.results, s.payment.results, wResp.results, s.response.results,
		wRcpt.results, s.receipt.results, legRows.results} {
		res = append(res, part...)
	}
	return res
}

func checkLegCross(o *out, settlement ijson.Obj, pay *payment) {
	var accepted ijson.Obj
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
			want, ok := accepted.Str(member)
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
	amount, _ := settlement.Object("amount")
	value, ok := amount.Str("value")
	cross(PropLegAmount, MethodLegAmount, "settlement.amount.value", value, ok, "amount")

	ref, _ := settlement.Object("payment_ref")
	network, ok := ref.Str("network")
	if t, _ := ref.Str("type"); ok && t != "x402.transaction" {
		o.plain(PropLegNetwork, adapter.NotChecked, MethodLegNetwork, fmt.Sprintf("payment_ref type %q is not x402.transaction", t))
		return
	}
	cross(PropLegNetwork, MethodLegNetwork, "settlement.payment_ref.network", network, ok, "network")
}
