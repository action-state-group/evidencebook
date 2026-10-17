// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"fmt"
	"math/big"

	"github.com/action-state-group/evidencebook/adapter"
)

// Receipt variants. Payload version 2 has two open upstream proposals with
// disjoint member names; the payload's members select one.
const (
	pr3186Head = "3dcef3c3"
	pr3140Head = "93b41d5a"
)

var (
	fields3186 = []string{"responseHash", "responseHashAlg", "responseHashEncoding"}
	fields3140 = []string{"commitmentId", "settlementUnbound", "contentHash", "contentHashAlg", "requestHash", "receiptRef"}
)

// receiptVariant is a receipt schema: its label, EIP-712 domain version and
// Receipt type string, and the optional members after transaction.
type receiptVariant struct {
	label, domainVersion, typ string
	extra                     []string // string members, in type order (settlementUnbound is a bool)
}

var (
	receiptV1     = receiptVariant{"v1", "1", typeReceiptV1, nil}
	receiptV23186 = receiptVariant{"v2 #3186 " + pr3186Head, "1", typeReceiptV2, fields3186}
	receiptV23140 = receiptVariant{"v2 #3140 " + pr3140Head, "2", typeReceiptV2PR3140, fields3140}
)

func receiptDomain(version string) []byte {
	return keccak256(
		keccak256([]byte(typeEIP712DomainNoVC)),
		wordString(receiptDomainName),
		wordString(version),
		wordUint(big.NewInt(receiptDomainChainIDInt)),
	)
}

// selectVariant picks the schema for a receipt payload as transmitted.
func selectVariant(payload obj) (receiptVariant, error) {
	vn, ok := payload.num("version")
	if !ok {
		return receiptVariant{}, fmt.Errorf("payload.version is missing or not a number")
	}
	switch vn.String() {
	case "1":
		return receiptV1, nil
	case "2":
		has := func(names []string) bool {
			for _, n := range names {
				if payload.has(n) {
					return true
				}
			}
			return false
		}
		a, b := has(fields3186), has(fields3140)
		switch {
		case a && !b:
			return receiptV23186, nil
		case b && !a:
			return receiptV23140, nil
		}
		return receiptVariant{}, errUnsupportedReceipt{"payload version 2 matching neither or both proposals (#3186, #3140)"}
	}
	return receiptVariant{}, errUnsupportedReceipt{"payload version " + vn.String()}
}

type errUnsupportedReceipt struct{ what string }

func (e errUnsupportedReceipt) Error() string { return "unsupported receipt: " + e.what }

// receiptStructHash hashes a receipt payload as transmitted under variant v.
// An absent optional string member is the empty string and an absent
// settlementUnbound is false, as the extension requires.
func receiptStructHash(payload obj, v receiptVariant) ([]byte, error) {
	vn, _ := payload.num("version")
	version, err := parseUint256(vn.String())
	if err != nil {
		return nil, fmt.Errorf("payload.version: %v", err)
	}
	words := [][]byte{keccak256([]byte(v.typ)), wordUint(version)}
	for _, k := range []string{"network", "resourceUrl", "payer"} {
		s, ok := payload.str(k)
		if !ok {
			return nil, fmt.Errorf("payload.%s is missing or not a string", k)
		}
		words = append(words, wordString(s))
	}
	in, ok := payload.num("issuedAt")
	if !ok {
		return nil, fmt.Errorf("payload.issuedAt is missing or not a number")
	}
	issuedAt, err := parseUint256(in.String())
	if err != nil {
		return nil, fmt.Errorf("payload.issuedAt: %v", err)
	}
	words = append(words, wordUint(issuedAt))
	for _, k := range append([]string{"transaction"}, v.extra...) {
		if k == "settlementUnbound" {
			b := false
			if payload.has(k) {
				if b, ok = payload.boolean(k); !ok {
					return nil, fmt.Errorf("payload.%s is not a boolean", k)
				}
			}
			n := big.NewInt(0)
			if b {
				n = big.NewInt(1)
			}
			words = append(words, wordUint(n))
			continue
		}
		s := ""
		if payload.has(k) {
			if s, ok = payload.str(k); !ok {
				return nil, fmt.Errorf("payload.%s is not a string", k)
			}
		}
		words = append(words, wordString(s))
	}
	return keccak256(words...), nil
}

// checkReceiptOctets finds the receipt (a wrapped x402.receipt's octets, or
// else the one inside the settlement response) and reports the three
// receipt rows.
func checkReceiptOctets(o *out, octets []byte, resp *response, pay *payment, policy adapter.TrustPolicy) {
	var env obj
	switch {
	case octets != nil:
		if len(octets) > 0 && octets[0] == '{' {
			v, err := decodeStrict(octets)
			e, ok := asObj(v)
			if err != nil || !ok {
				if err == nil {
					err = fmt.Errorf("not a JSON object")
				}
				o.native(RowReceiptSignature, adapter.Fail, MethodReceiptSignature, "receipt octets: "+err.Error())
				o.native(RowReceiptSignerAuthorized, adapter.NotChecked, MethodReceiptAuth, "receipt not readable")
				o.native(RowDeliveryBinding, adapter.NotChecked, MethodDeliveryBinding, "receipt not readable")
				return
			}
			env = e
		} else {
			env = obj{"format": "jws", "signature": string(octets)}
		}
	case resp != nil:
		ext, _ := resp.root.object("extensions")
		or, _ := ext.object("offer-receipt")
		info, _ := or.object("info")
		if info.has("receipt") {
			e, ok := info.object("receipt")
			if !ok {
				o.native(RowReceiptSignature, adapter.Fail, MethodReceiptSignature, "receipt is not an object")
				o.native(RowReceiptSignerAuthorized, adapter.NotChecked, MethodReceiptAuth, "receipt not readable")
				o.native(RowDeliveryBinding, adapter.NotChecked, MethodDeliveryBinding, "receipt not readable")
				return
			}
			env = e
		}
	}
	if env == nil {
		o.native(RowReceiptSignature, adapter.NotPresent, MethodReceiptSignature, "no receipt in extensions[\"offer-receipt\"].info.receipt")
		o.native(RowReceiptSignerAuthorized, adapter.NotPresent, MethodReceiptAuth, "no receipt")
		o.native(RowDeliveryBinding, adapter.NotPresent, MethodDeliveryBinding, "no receipt")
		return
	}
	checkReceipt(o, env, policy)
}

func checkReceipt(o *out, env obj, policy adapter.TrustPolicy) {
	format, _ := env.str("format")
	if format == "jws" {
		o.native(RowReceiptSignature, adapter.NotChecked, MethodReceiptSignature, "JWS receipts are not checked in v0")
		o.native(RowReceiptSignerAuthorized, adapter.NotChecked, MethodReceiptAuth, "no signer established (JWS not checked)")
		o.native(RowDeliveryBinding, adapter.NotChecked, MethodDeliveryBinding, "JWS receipt not checked")
		return
	}
	if format != "eip712" {
		o.native(RowReceiptSignature, adapter.Fail, MethodReceiptSignature, fmt.Sprintf("format %q is not eip712 or jws", format))
		o.native(RowReceiptSignerAuthorized, adapter.NotChecked, MethodReceiptAuth, "no signer established")
		o.native(RowDeliveryBinding, adapter.NotChecked, MethodDeliveryBinding, "receipt not readable")
		return
	}
	payload, okP := env.object("payload")
	sigHex, okS := env.str("signature")
	if !okP || !okS {
		o.native(RowReceiptSignature, adapter.Fail, MethodReceiptSignature, "an eip712 receipt needs payload and signature")
		o.native(RowReceiptSignerAuthorized, adapter.NotChecked, MethodReceiptAuth, "no signer established")
		o.native(RowDeliveryBinding, adapter.NotChecked, MethodDeliveryBinding, "receipt not readable")
		return
	}
	variant, err := selectVariant(payload)
	if err != nil {
		st := adapter.Fail
		if _, ok := err.(errUnsupportedReceipt); ok {
			st = adapter.NotChecked
		}
		o.native(RowReceiptSignature, st, MethodReceiptSignature, err.Error())
		o.native(RowReceiptSignerAuthorized, adapter.NotChecked, MethodReceiptAuth, "no signer established")
		o.native(RowDeliveryBinding, adapter.NotChecked, MethodDeliveryBinding, err.Error())
		return
	}
	vx := map[string]string{"variant": variant.label}
	signer, err := func() (address, error) {
		h, err := receiptStructHash(payload, variant)
		if err != nil {
			return address{}, err
		}
		sig, err := parseHexBytes(sigHex)
		if err != nil {
			return address{}, err
		}
		return recoverAddress(typedDataDigest(receiptDomain(variant.domainVersion), h), sig)
	}()
	if err != nil {
		o.nativeX(RowReceiptSignature, adapter.Fail, MethodReceiptSignature, err.Error(), vx)
		o.nativeX(RowReceiptSignerAuthorized, adapter.NotChecked, MethodReceiptAuth, "no signer established", vx)
	} else {
		o.nativeX(RowReceiptSignature, adapter.Pass, MethodReceiptSignature, "signer recovered: "+signer.String(), vx)
		checkReceiptSigner(o, signer, policy, vx)
	}
	checkDeliveryBinding(o, payload, variant, vx)
}

func checkReceiptSigner(o *out, signer address, policy adapter.TrustPolicy, vx map[string]string) {
	set := func(st adapter.Status, d string) { o.nativeX(RowReceiptSignerAuthorized, st, MethodReceiptAuth, d, vx) }
	switch {
	case policy.Pin(PinReceiptSignerAuthorized) == "excluded":
		set(adapter.NotChecked, "excluded by policy")
	case len(policy.Keys[KeyReceiptSigner]) == 0:
		set(adapter.Inconclusive, "the policy names no authorized receipt signer; "+signer.String()+" unresolved")
	case inList(policy.Keys[KeyReceiptSigner], signer):
		set(adapter.Pass, signer.String()+" is an authorized receipt signer")
	default:
		set(adapter.Fail, signer.String()+" is not among the authorized receipt signers")
	}
}

// checkDeliveryBinding reports delivery_binding. The adapter holds no
// response body in v0, so a v2 receipt with a hash is NOT_PRESENT (body
// octets not held).
func checkDeliveryBinding(o *out, payload obj, v receiptVariant, vx map[string]string) {
	set := func(st adapter.Status, d string) { o.nativeX(RowDeliveryBinding, st, MethodDeliveryBinding, d, vx) }
	switch v.label {
	case receiptV1.label:
		set(adapter.NotChecked, "version 1 receipt: no content hash")
	case receiptV23186.label:
		enc, _ := payload.str("responseHashEncoding")
		if h, _ := payload.str("responseHash"); h == "" {
			set(adapter.NotPresent, "no hash in the receipt")
			return
		}
		set(adapter.NotPresent, "responseHash ("+enc+") present; the response body octets are not held")
	case receiptV23140.label:
		if h, _ := payload.str("contentHash"); h == "" {
			set(adapter.NotPresent, "no hash in the receipt")
			return
		}
		set(adapter.NotPresent, "contentHash (decoded entity body) present; the response body octets are not held")
	}
}
