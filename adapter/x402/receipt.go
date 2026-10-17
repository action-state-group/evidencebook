// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/action-state-group/evidencebook/adapter"
)

// receiptDomainSeparator is the offer-receipt extension's fixed receipt
// domain: name "x402 receipt", version "1", chainId 1, no verifyingContract.
func receiptDomainSeparator() []byte {
	return keccak256(
		keccak256([]byte(typeEIP712DomainNoVC)),
		wordString(receiptDomainName),
		wordString(receiptDomainVersion),
		wordUint(big.NewInt(receiptDomainChainIDInt)),
	)
}

// receiptStructHash hashes a receipt payload as transmitted. Version 1 is
// the merged upstream schema; version 2 adds the three delivery-binding
// strings of the open upstream revision. An absent optional string member is
// the empty string, as the extension requires.
func receiptStructHash(payload obj) ([]byte, int, error) {
	vn, ok := payload.num("version")
	if !ok {
		return nil, 0, fmt.Errorf("payload.version is missing or not a number")
	}
	version, err := parseUint256(vn.String())
	if err != nil {
		return nil, 0, fmt.Errorf("payload.version: %v", err)
	}
	var typ string
	var optional []string
	switch version.String() {
	case "1":
		typ = typeReceiptV1
	case "2":
		typ = typeReceiptV2
		optional = []string{"responseHash", "responseHashAlg", "responseHashEncoding"}
	default:
		return nil, 0, errUnsupportedReceiptVersion{version.String()}
	}
	words := [][]byte{keccak256([]byte(typ)), wordUint(version)}
	for _, k := range []string{"network", "resourceUrl", "payer"} {
		s, ok := payload.str(k)
		if !ok {
			return nil, 0, fmt.Errorf("payload.%s is missing or not a string", k)
		}
		words = append(words, wordString(s))
	}
	in, ok := payload.num("issuedAt")
	if !ok {
		return nil, 0, fmt.Errorf("payload.issuedAt is missing or not a number")
	}
	issuedAt, err := parseUint256(in.String())
	if err != nil {
		return nil, 0, fmt.Errorf("payload.issuedAt: %v", err)
	}
	words = append(words, wordUint(issuedAt))
	for _, k := range append([]string{"transaction"}, optional...) {
		s := ""
		if payload.has(k) {
			if s, ok = payload.str(k); !ok {
				return nil, 0, fmt.Errorf("payload.%s is not a string", k)
			}
		}
		words = append(words, wordString(s))
	}
	return keccak256(words...), int(version.Int64()), nil
}

type errUnsupportedReceiptVersion struct{ v string }

func (e errUnsupportedReceiptVersion) Error() string {
	return "receipt payload.version " + e.v + " is not 1 or 2"
}

func checkReceipt(r report, resp obj, policy adapter.TrustPolicy) {
	set := func(st adapter.Status, detail string) { r.set(PropReceiptSignature, st, MethodReceipt, detail) }
	ext, _ := resp.object("extensions")
	or, _ := ext.object("offer-receipt")
	info, _ := or.object("info")
	if !info.has("receipt") {
		set(adapter.NotPresent, "no extensions[\"offer-receipt\"].info.receipt in the settlement response")
		return
	}
	rc, ok := info.object("receipt")
	if !ok {
		set(adapter.Fail, "receipt is not an object")
		return
	}
	format, _ := rc.str("format")
	switch format {
	case "eip712":
	case "jws":
		set(adapter.NotChecked, "JWS receipt format is not implemented in this revision")
		return
	default:
		set(adapter.Fail, fmt.Sprintf("receipt format %q is not eip712 or jws", format))
		return
	}
	payload, ok := rc.object("payload")
	if !ok {
		set(adapter.Fail, "receipt.payload is missing or not an object")
		return
	}
	sigHex, ok := rc.str("signature")
	if !ok {
		set(adapter.Fail, "receipt.signature is missing or not a string")
		return
	}
	structHash, version, err := receiptStructHash(payload)
	if err != nil {
		if _, unsupported := err.(errUnsupportedReceiptVersion); unsupported {
			set(adapter.NotChecked, err.Error())
		} else {
			set(adapter.Fail, err.Error())
		}
		return
	}
	sig, err := parseHexBytes(sigHex)
	if err != nil {
		set(adapter.Fail, err.Error())
		return
	}
	signer, err := recoverAddress(typedDataDigest(receiptDomainSeparator(), structHash), sig)
	if err != nil {
		set(adapter.Fail, err.Error())
		return
	}
	note := fmt.Sprintf("receipt v%d recovers to %s", version, signer)
	if version == 2 {
		note += " (v2 schema from the unmerged upstream delivery-binding revision)"
	}
	accepted := policy.Keys[KeyReceiptSigner]
	if len(accepted) == 0 {
		set(adapter.Inconclusive, note+"; the trust policy names no receipt signer ("+KeyReceiptSigner+")")
		return
	}
	for _, k := range accepted {
		if a, err := parseAddress(k); err == nil && a == signer {
			set(adapter.Pass, note+", a pinned receipt signer")
			return
		}
	}
	set(adapter.Fail, note+", not among the pinned receipt signers "+strings.Join(accepted, ", "))
}
