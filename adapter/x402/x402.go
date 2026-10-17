// SPDX-License-Identifier: Apache-2.0

// Package x402 is the evidencebook adapter for x402 payments, implementing
// the x402 adapter declaration v0 (agent-action-capsule
// spec/adapters/x402-v0.md) as ruled for its first implementation.
//
// It reads the payer's payment payload (PAYMENT-SIGNATURE), the
// facilitator's settlement response (PAYMENT-RESPONSE) and the payee's
// receipt (offer-and-receipt extension), for x402 version 2, scheme "exact",
// on EVM, with the EIP-3009 transfer method. Each check of the declaration's
// §7 table is reported as a native_check report of the object it checks, with
// the check's name in detail.
//
// The adapter makes no network calls. An online check (the transaction
// lookup) reads a recording the relying party supplies in its trust policy,
// and is NOT_CHECKED without one.
package x402

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/action-state-group/evidencebook/adapter"
)

// Check names: the rows of the declaration's §7 table.
const (
	RowPayloadForm             = "payload_form"
	RowPayerSignature          = "payer_signature"
	RowPayerTrust              = "payer_trust"
	RowAssetTrust              = "asset_trust"
	RowAuthorizationTerms      = "authorization_terms"
	RowSettleResponseForm      = "settle_response_form"
	RowSettleResponseJoin      = "settle_response_join"
	RowSettleResponseSignature = "settle_response_signature"
	RowSettlementOnchain       = "settlement_onchain"
	RowReceiptSignature        = "receipt_signature"
	RowReceiptSignerAuthorized = "receipt_signer_authorized"
	RowDeliveryBinding         = "delivery_binding"
)

// Trust policy names this adapter reads.
const (
	// PinChain and PinAsset name the one (network, token contract) the
	// relying party accepts (asset_trust). PinAssetName and PinAssetVersion
	// are that token's EIP-712 domain name and version. The payer signature
	// is checked under the domain these pins give, never under transmitted
	// values.
	PinChain        = "x402.chain"
	PinAsset        = "x402.asset"
	PinAssetName    = "x402.asset.name"
	PinAssetVersion = "x402.asset.version"
	// PinPayerAccounts is "eoa-or-contract" (the default) or "eoa". Under
	// the default, a signature that does not recover to authorization.from
	// is INCONCLUSIVE, because the payer may be a contract account whose
	// signature is checked on chain (EIP-1271). Under "eoa" it is FAIL.
	PinPayerAccounts = "x402.payer_accounts"
	// PinPayerTrust, PinAssetTrust and PinReceiptSignerAuthorized set to
	// "excluded" exclude that check by policy (NOT_CHECKED).
	PinPayerTrust              = "x402.payer_trust"
	PinAssetTrust              = "x402.asset_trust"
	PinReceiptSignerAuthorized = "x402.receipt_signer_authorized"
	// KeyPayer and KeyPayerRejected list payer addresses the policy binds to
	// a party, or rejects (payer_trust).
	KeyPayer         = "x402.payer"
	KeyPayerRejected = "x402.payer_rejected"
	// KeyReceiptSigner lists the addresses authorized to sign receipts for
	// the service (receipt_signer_authorized; extension 4.5.1, external key
	// registry approach).
	KeyReceiptSigner = "x402.receipt_signer"
	// RecordedTxLookup is a recorded transaction lookup (see TxLookup);
	// RecordedCodeLookup a recorded code lookup at the payer address (see
	// CodeLookup).
	RecordedTxLookup   = "x402.tx_lookup"
	RecordedCodeLookup = "x402.code_lookup"
)

// Methods, as reported. Those of the declaration's §7 table are quoted from
// it; the rulings add the v normalization and the domain source to
// payer_signature, and the validity window to settlement_onchain.
const (
	MethodPayloadForm     = "x402 v2 PaymentPayload: base64 (RFC 4648 §4) and JSON parse, no duplicate members"
	MethodPayerSignature  = "EIP-712 TransferWithAuthorization (EIP-3009), secp256k1 recovery, fields from the as-transmitted payload; domain from the relying party's policy for accepted.network and accepted.asset; v 0/1 normalized to 27/28, low s"
	MethodPayerTrust      = "relying party's policy: trust policy key roles x402.payer (bound) and x402.payer_rejected"
	MethodAssetTrust      = "relying party's policy: trust policy pins x402.chain and x402.asset"
	MethodAuthTerms       = "x402 exact: authorization.to = accepted.payTo, authorization.value = accepted.amount, validAfter < validBefore"
	MethodSettleForm      = "x402 v2 SettlementResponse: base64 and JSON parse, 5.3.2 member types"
	MethodSettleJoin      = "x402 v2: network = accepted.network, payer = authorization.from"
	MethodSettleSignature = "none: x402 v2 SettlementResponse is unsigned"
	MethodReceiptAuth     = "extension 4.5.1, external key registry: relying party's policy key role x402.receipt_signer"
)

func methodSettlementOnchain(network, source string) string {
	if network == "" {
		network = "<network>"
	}
	if source == "" {
		source = "<source named by policy>"
	}
	return fmt.Sprintf("transaction lookup on %s via %s: status, Transfer, AuthorizationUsed, validAfter < block timestamp < validBefore", network, source)
}

// Header names: v2, then v1.
var (
	payloadHeaders  = []string{"PAYMENT-SIGNATURE", "X-PAYMENT"}
	responseHeaders = []string{"PAYMENT-RESPONSE", "X-PAYMENT-RESPONSE"}
)

// Adapter implements adapter.Adapter for x402.
type Adapter struct{}

var _ adapter.Adapter = Adapter{}

// Name implements adapter.Adapter.
func (Adapter) Name() string { return "x402" }

// NativeObject implements adapter.Adapter: the FR-19 declaration.
func (Adapter) NativeObject() adapter.NativeObject {
	return adapter.NativeObject{
		Protocol: "x402 v2, scheme exact, EVM (eip155), asset transfer method eip3009; declaration agent-action-capsule spec/adapters/x402-v0.md",
		Objects: []adapter.Object{
			{
				Name:             "x402.payment-payload",
				Format:           "PaymentPayload JSON; payload = {signature, authorization}",
				Octets:           "the base64-decoded PAYMENT-SIGNATURE header value as sent",
				Issuer:           "payer: authorization.from",
				IssuerResolution: "the signer address is recovered; the relying party's policy binds it to a party or not (payer_trust). Chain, token contract and EIP-712 domain come from the policy (asset_trust), never from the payload",
				Method:           MethodPayerSignature,
				NativelySigned:   true,
			},
			{
				Name:             "x402.settle-response",
				Format:           "SettlementResponse JSON {success, errorReason, transaction, network, payer, amount, extensions}",
				Octets:           "the base64-decoded PAYMENT-RESPONSE header value as received",
				Issuer:           "facilitator, delivered by the resource server",
				IssuerResolution: "none: unsigned, carries no key identifier; possession and transport-level provenance only. Settlement rests on a transaction lookup on the chain it names",
				Method:           MethodSettleForm + "; " + MethodSettleJoin + "; " + methodSettlementOnchain("", ""),
				NativelySigned:   false,
			},
			{
				Name:             "x402.receipt",
				Format:           "offer-and-receipt extension receipt {format, payload, signature}, at extensions[\"offer-receipt\"].info.receipt of the settlement response, or wrapped on its own",
				Octets:           "EIP-712: the RFC 8785 form of the envelope object; JWS: the ASCII compact serialization",
				Issuer:           "payee (the resource server)",
				IssuerResolution: "EIP-712: the signer address is recovered; the relying party's policy decides whether it is authorized for payload.resourceUrl (receipt_signer_authorized)",
				Method:           "x402 offer-receipt v1, or v2 under #3186 or #3140 selected by member names, EIP-712 secp256k1 recovery; JWS not checked in v0",
				NativelySigned:   true,
			},
			{
				Name:             "settlement-records leg (carrier)",
				Format:           "Agent Action Capsule JSON with a settlement member; settlement.wrapped[] entries {type, digest_alg, digest, content}",
				Octets:           "each wrapped entry's content, base64url without padding, decoded",
				Issuer:           "the leg's sealer; each wrapped object keeps its own issuer",
				IssuerResolution: "not resolved here: the Capsule envelope is a Capsule verifier's check. A wrapped digest proves possession of the octets only",
				Method:           MethodWrappedContent,
				NativelySigned:   false,
			},
		},
		Predicate: "X1: the holder of the key for authorization.from signed an EIP-3009 authorization to transfer value atomic units of the token at accepted.asset on accepted.network to authorization.to, valid between validAfter and validBefore, with its nonce (payload_form, payer_signature). X2: X1, matching the accepted payTo and amount (authorization_terms). X3: that authorization was executed on chain in the named transaction (X1, settle_response_form, settlement_onchain). X4: the payee, through an authorized key, states it received payment from payer for resourceUrl (receipt_signature, receipt_signer_authorized). X5: X4, and the payee signed a digest of the response body the verifier holds (delivery_binding).",
		NotSupported: []string{
			"that the facilitator issued or agrees with the settlement response: it is unsigned (possession only, FR-20)",
			"that the resource was delivered or that the payer received any content",
			"that the amount matched a signed offer (x402.offer is out of scope)",
			"anything about a party beyond what the relying party's policy binds to an address or key",
			"x402 v1, other schemes, non-eip155 networks, permit2 and erc7710 (erc7710 carries no offline-checkable payer signature: possession only), contract-account signatures, JWS receipts: NOT_CHECKED",
		},
		Properties: []adapter.Property{
			{Name: RowPayloadForm, Object: "x402.payment-payload", Method: MethodPayloadForm, Supports: "X1"},
			{Name: RowPayerSignature, Object: "x402.payment-payload", Method: MethodPayerSignature, Supports: "X1"},
			{Name: RowPayerTrust, Object: "x402.payment-payload", Method: MethodPayerTrust, Supports: "a named payer"},
			{Name: RowAssetTrust, Object: "x402.payment-payload", Method: MethodAssetTrust, Supports: "an accepted asset"},
			{Name: RowAuthorizationTerms, Object: "x402.payment-payload", Method: MethodAuthTerms, Supports: "X2"},
			{Name: RowSettleResponseForm, Object: "x402.settle-response", Method: MethodSettleForm, Supports: "X3"},
			{Name: RowSettleResponseJoin, Object: "x402.settle-response", Method: MethodSettleJoin, Supports: "consistency only, no attribution"},
			{Name: RowSettleResponseSignature, Object: "x402.settle-response", Method: MethodSettleSignature, Supports: "nothing: always NOT_PRESENT"},
			{Name: RowSettlementOnchain, Object: "x402.settle-response", Method: methodSettlementOnchain("", ""), Supports: "X3"},
			{Name: RowReceiptSignature, Object: "x402.receipt", Method: "x402 offer-receipt <payload version>[ <variant and PR head>], <EIP-712 or JWS>", Supports: "X4"},
			{Name: RowReceiptSignerAuthorized, Object: "x402.receipt", Method: MethodReceiptAuth, Supports: "X4"},
			{Name: RowDeliveryBinding, Object: "x402.receipt", Method: "#3186 responseHash (raw or jcs) or #3140 contentHash (decoded entity body), SHA-256", Supports: "X5"},
			{Name: PropWrappedContent, Object: "settlement-records leg", Method: MethodWrappedContent, Supports: "possession of the wrapped octets (not a native check)"},
			{Name: PropLegAmount, Object: "settlement-records leg", Method: MethodLegAmount, Supports: "the leg's amount is the accepted amount (not a native check)"},
			{Name: PropLegNetwork, Object: "settlement-records leg", Method: MethodLegNetwork, Supports: "the leg's payment reference names the accepted network (not a native check)"},
		},
	}
}

// out accumulates results in report order.
type out struct{ results []adapter.PropertyResult }

func (o *out) native(row string, st adapter.Status, method, explanation string) {
	o.results = append(o.results, adapter.NativeCheckResult(row, st, method, explanation))
}

func (o *out) plain(prop string, st adapter.Status, method, detail string) {
	o.results = append(o.results, adapter.PropertyResult{Property: prop, Status: st, Method: method, Detail: detail})
}

// exchange is the as-received octets of one x402 exchange, and the error
// decoding each header value, if any.
type exchange struct {
	payload, response       []byte
	payloadErr, responseErr error
}

// parseExchange reads an HTTP header block: one "Name: value" per line
// (CRLF or LF), names case-insensitive, other headers ignored. The error is
// about the block itself; a header value that is not base64 is reported in
// payloadErr or responseErr.
func parseExchange(raw []byte) (exchange, error) {
	var ex exchange
	seen := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return ex, fmt.Errorf("line %q is not a header field", line)
		}
		name = strings.ToUpper(strings.TrimSpace(name))
		if _, dup := seen[name]; dup {
			return ex, fmt.Errorf("header %s appears twice", name)
		}
		seen[name] = strings.Trim(value, " \t")
	}
	pick := func(names []string) ([]byte, error, error) {
		var got []byte
		var gotName string
		var decErr error
		for _, n := range names {
			v, ok := seen[n]
			if !ok {
				continue
			}
			if gotName != "" {
				return nil, nil, fmt.Errorf("both %s and %s are present", gotName, n)
			}
			gotName = n
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				decErr = fmt.Errorf("%s is not base64 (RFC 4648 §4): %v", n, err)
				b = []byte{}
			}
			got = b
		}
		return got, decErr, nil
	}
	var err error
	if ex.payload, ex.payloadErr, err = pick(payloadHeaders); err != nil {
		return ex, err
	}
	if ex.response, ex.responseErr, err = pick(responseHeaders); err != nil {
		return ex, err
	}
	return ex, nil
}

// Check implements adapter.Adapter. raw is one of three input forms,
// detected from its content:
//
//   - a settlement-records leg: a JSON Capsule with a settlement member
//     whose wrapped[] carries x402.payment-payload, and optionally
//     x402.settle-response and x402.receipt, content;
//   - the bare payment payload: the base64-decoded PAYMENT-SIGNATURE octets
//     (a JSON object with x402Version), with no settlement response;
//   - an HTTP header block carrying PAYMENT-SIGNATURE and, optionally,
//     PAYMENT-RESPONSE, as received.
//
// Every row of the declaration's §7 table is reported as a native_check
// report, in table order; a leg adds its wrapped content checks and leg
// cross-checks, which are not native checks.
func (a Adapter) Check(raw []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	if trimmed := bytes.TrimLeft(raw, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '{' {
		return checkJSON(raw, policy)
	}
	o := &out{}
	ex, err := parseExchange(raw)
	if err != nil {
		ex = exchange{payload: []byte{}, payloadErr: err, response: []byte{}, responseErr: err}
	}
	pay := checkPayload(o, ex.payload, ex.payloadErr, policy)
	resp := checkResponse(o, ex.response, ex.responseErr, pay, policy)
	checkReceiptOctets(o, nil, resp, pay, policy)
	return o.results
}

// notCheckedAfterForm reports the response rows after a response that
// could not be read.
func notCheckedAfterForm(o *out, policy adapter.TrustPolicy) {
	o.native(RowSettleResponseJoin, adapter.NotChecked, MethodSettleJoin, "settlement response not readable")
	o.native(RowSettleResponseSignature, adapter.NotPresent, MethodSettleSignature, "there is no signature to check")
	o.native(RowSettlementOnchain, adapter.NotChecked, methodSettlementOnchain("", lookupSource(policy)), "settlement response not readable")
}

// CheckObjects checks the decoded octets of a payment payload, a settlement
// response and a receipt (the registered octets of x402.payment-payload,
// x402.settle-response and x402.receipt). Any may be nil, meaning not held.
// A receipt inside the settlement response is used when receipt is nil.
func CheckObjects(payload, response, receipt []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	o := &out{}
	checkObjects(o, payload, response, receipt, policy)
	return o.results
}

// checkObjects is CheckObjects appending to o, and returns the payment
// read (nil when none is usable).
func checkObjects(o *out, payload, response, receipt []byte, policy adapter.TrustPolicy) *payment {
	pay := checkPayload(o, payload, nil, policy)
	resp := checkResponse(o, response, nil, pay, policy)
	checkReceiptOctets(o, receipt, resp, pay, policy)
	return pay
}

// payment is the payment payload as parsed.
type payment struct {
	root     obj
	accepted obj
	body     obj
	auth     obj
	// inScope is false when §3 puts the payload out of scope.
	inScope bool
	why     string
}

var v1Networks = map[string]string{"base": "eip155:8453", "base-sepolia": "eip155:84532"}

func checkPayload(o *out, octets []byte, decodeErr error, policy adapter.TrustPolicy) *payment {
	rows := []struct{ name, method string }{
		{RowPayerSignature, MethodPayerSignature}, {RowPayerTrust, MethodPayerTrust},
		{RowAssetTrust, MethodAssetTrust}, {RowAuthorizationTerms, MethodAuthTerms},
	}
	if octets == nil {
		o.native(RowPayloadForm, adapter.NotPresent, MethodPayloadForm, "no PAYMENT-SIGNATURE octets held")
		for _, r := range rows {
			o.native(r.name, adapter.NotPresent, r.method, "no payment payload held")
		}
		return nil
	}
	var v any
	err := decodeErr
	if err == nil {
		v, err = decodeStrict(octets)
	}
	root, isObj := asObj(v)
	if err == nil && !isObj {
		err = fmt.Errorf("not a JSON object")
	}
	if err != nil {
		o.native(RowPayloadForm, adapter.Fail, MethodPayloadForm, err.Error())
		for _, r := range rows {
			o.native(r.name, adapter.NotChecked, r.method, "payment payload not readable")
		}
		return nil
	}
	o.native(RowPayloadForm, adapter.Pass, MethodPayloadForm, fmt.Sprintf("%d octets", len(octets)))

	pay := &payment{root: root}
	pay.accepted, _ = root.object("accepted")
	pay.body, _ = root.object("payload")
	pay.auth, _ = pay.body.object("authorization")
	pay.inScope, pay.why = pay.scope()
	if !pay.inScope {
		for _, r := range rows {
			o.native(r.name, adapter.NotChecked, r.method, "out of scope: "+pay.why)
		}
		return pay
	}
	checkPayerSignature(o, pay, policy)
	checkPayerTrust(o, pay, policy)
	checkAssetTrust(o, pay, policy)
	checkAuthorizationTerms(o, pay)
	return pay
}

// scope applies the declaration's §3.
func (p *payment) scope() (bool, string) {
	ver, _ := p.root.num("x402Version")
	switch {
	case ver.String() == "1":
		return false, "x402 version 1"
	case ver.String() != "2":
		return false, fmt.Sprintf("x402Version %q", ver.String())
	case p.accepted == nil:
		return false, "accepted is missing or not an object"
	}
	scheme, _ := p.accepted.str("scheme")
	network, _ := p.accepted.str("network")
	method := "eip3009"
	if extra, ok := p.accepted.object("extra"); ok && extra.has("assetTransferMethod") {
		method, _ = extra.str("assetTransferMethod")
	} else if p.body.has("permit2Authorization") {
		method = "permit2"
	} else if p.body.has("permissionContext") {
		method = "erc7710"
	}
	switch {
	case scheme != "exact":
		return false, fmt.Sprintf("scheme %q", scheme)
	case !strings.HasPrefix(network, "eip155:"):
		return false, fmt.Sprintf("network %q is not eip155", network)
	case method == "erc7710":
		return false, "transfer method erc7710: x402 carries no payer signature a verifier can check offline (the facilitator verifies by on-chain simulation); possession of the octets only"
	case method != "eip3009":
		return false, fmt.Sprintf("transfer method %q", method)
	}
	if sigHex, ok := p.body.str("signature"); ok {
		if sig, err := parseHexBytes(sigHex); err == nil && len(sig) != 65 {
			kind := "a contract-account signature (verified by an on-chain call)"
			if strings.HasSuffix(strings.ToLower(sigHex), erc6492Magic) {
				kind = "an ERC-6492 counterfactual contract-account signature (verified by an on-chain call)"
			}
			return false, fmt.Sprintf("%d-byte signature, %s", len(sig), kind)
		}
	}
	return true, ""
}

func (p *payment) str(o obj, k string) (string, bool) {
	if o == nil {
		return "", false
	}
	return o.str(k)
}

// from returns authorization.from as an address when it is present and well formed.
func (p *payment) from() (address, bool) {
	if p == nil || !p.inScope {
		return address{}, false
	}
	s, ok := p.str(p.auth, "from")
	if !ok {
		return address{}, false
	}
	a, err := parseAddress(s)
	return a, err == nil
}

// authorization reads the six authorization members. missing names the
// first absent member; err reports a malformed one.
func (p *payment) authorization() (m authorization, missing string, err error) {
	get := func(k string) string {
		if missing != "" || err != nil {
			return ""
		}
		s, ok := p.str(p.auth, k)
		if !ok {
			missing = "authorization." + k
		}
		return s
	}
	from, to := get("from"), get("to")
	value, va, vb, nonce := get("value"), get("validAfter"), get("validBefore"), get("nonce")
	if missing != "" {
		return m, missing, nil
	}
	if m.From, err = parseAddress(from); err != nil {
		return m, "", fmt.Errorf("authorization.from: %v", err)
	}
	if m.To, err = parseAddress(to); err != nil {
		return m, "", fmt.Errorf("authorization.to: %v", err)
	}
	for _, f := range []struct {
		k, s string
		dst  **big.Int
	}{{"value", value, &m.Value}, {"validAfter", va, &m.ValidAfter}, {"validBefore", vb, &m.ValidBefore}} {
		if *f.dst, err = parseUint256(f.s); err != nil {
			return m, "", fmt.Errorf("authorization.%s: %v", f.k, err)
		}
	}
	if m.Nonce, err = parseBytes32(nonce); err != nil {
		return m, "", fmt.Errorf("authorization.nonce: %v", err)
	}
	return m, "", nil
}

// chainID reads the decimal reference of an eip155 CAIP-2 identifier.
func chainID(caip2 string) (*big.Int, error) {
	ref, ok := strings.CutPrefix(caip2, "eip155:")
	if !ok {
		return nil, fmt.Errorf("%q is not an eip155 network", caip2)
	}
	return parseUint256(ref)
}

// ERC-6492 wraps a counterfactual contract wallet's signature and ends with
// this 32-byte magic value.
var erc6492Magic = strings.Repeat("6492", 16)

func checkPayerSignature(o *out, pay *payment, policy adapter.TrustPolicy) {
	set := func(st adapter.Status, d string) { o.native(RowPayerSignature, st, MethodPayerSignature, d) }
	sigHex, ok := pay.str(pay.body, "signature")
	if !ok {
		set(adapter.NotPresent, "payload.signature is absent")
		return
	}
	auth, missing, err := pay.authorization()
	if missing != "" {
		set(adapter.NotPresent, missing+" is absent")
		return
	}
	sig, serr := parseHexBytes(sigHex)
	if serr != nil {
		set(adapter.Fail, serr.Error())
		return
	}
	if err != nil {
		set(adapter.Fail, err.Error())
		return
	}
	name, version := policy.Pin(PinAssetName), policy.Pin(PinAssetVersion)
	pinChain, pinAsset := policy.Pin(PinChain), policy.Pin(PinAsset)
	if name == "" || version == "" || pinChain == "" || pinAsset == "" {
		set(adapter.NotChecked, "the policy gives no EIP-712 domain ("+strings.Join([]string{PinChain, PinAsset, PinAssetName, PinAssetVersion}, ", ")+")")
		return
	}
	cid, err := chainID(pinChain)
	if err != nil {
		set(adapter.NotChecked, "policy chain: "+err.Error())
		return
	}
	contract, err := parseAddress(pinAsset)
	if err != nil {
		set(adapter.NotChecked, "policy asset: "+err.Error())
		return
	}
	digest := typedDataDigest(tokenDomainSeparator(name, version, cid, contract), auth.structHash())
	got, err := recoverAddress(digest, sig)
	if err != nil {
		set(adapter.Fail, err.Error())
		return
	}
	if got == auth.From {
		set(adapter.Pass, "recovers to authorization.from "+got.String())
		return
	}
	if policy.Pin(PinPayerAccounts) == "eoa" {
		set(adapter.Fail, fmt.Sprintf("recovers to %s, not authorization.from %s (policy: eoa)", got, auth.From))
		return
	}
	if noCode, src := recordedNoCode(policy, auth.From); noCode {
		set(adapter.Fail, fmt.Sprintf("recovers to %s, not authorization.from %s; recorded code lookup via %s shows no code at %s", got, auth.From, src, auth.From))
		return
	}
	set(adapter.Inconclusive, fmt.Sprintf("recovered address differs; `from` may be a contract account; EIP-1271 not checked (recovered %s, from %s)", got, auth.From))
}

func checkPayerTrust(o *out, pay *payment, policy adapter.TrustPolicy) {
	set := func(st adapter.Status, d string) { o.native(RowPayerTrust, st, MethodPayerTrust, d) }
	if policy.Pin(PinPayerTrust) == "excluded" {
		set(adapter.NotChecked, "excluded by policy")
		return
	}
	s, ok := pay.str(pay.auth, "from")
	if !ok {
		set(adapter.NotPresent, "authorization.from is absent")
		return
	}
	from, err := parseAddress(s)
	if err != nil {
		set(adapter.Fail, "authorization.from: "+err.Error())
		return
	}
	if inList(policy.Keys[KeyPayerRejected], from) {
		set(adapter.Fail, "the policy rejects "+from.String())
		return
	}
	if inList(policy.Keys[KeyPayer], from) {
		set(adapter.Pass, "the policy binds "+from.String())
		return
	}
	set(adapter.Inconclusive, "the policy holds no binding for "+from.String())
}

func inList(list []string, a address) bool {
	for _, k := range list {
		if b, err := parseAddress(k); err == nil && b == a {
			return true
		}
	}
	return false
}

func checkAssetTrust(o *out, pay *payment, policy adapter.TrustPolicy) {
	set := func(st adapter.Status, d string) { o.native(RowAssetTrust, st, MethodAssetTrust, d) }
	if policy.Pin(PinAssetTrust) == "excluded" {
		set(adapter.NotChecked, "excluded by policy")
		return
	}
	network, _ := pay.accepted.str("network")
	assetStr, ok := pay.accepted.str("asset")
	if !ok {
		set(adapter.Fail, "accepted.asset is absent")
		return
	}
	pinChain, pinAsset := policy.Pin(PinChain), policy.Pin(PinAsset)
	if pinChain == "" || pinAsset == "" {
		set(adapter.Inconclusive, "the policy has no entry for an asset ("+PinChain+", "+PinAsset+")")
		return
	}
	want, err := parseAddress(pinAsset)
	if err != nil {
		set(adapter.Inconclusive, "policy asset unreadable: "+err.Error())
		return
	}
	got, err := parseAddress(assetStr)
	if err != nil {
		set(adapter.Fail, "accepted.asset: "+err.Error())
		return
	}
	if network != pinChain || got != want {
		set(adapter.Fail, fmt.Sprintf("accepted %s on %s; the policy accepts %s on %s", got, network, want, pinChain))
		return
	}
	set(adapter.Pass, fmt.Sprintf("%s on %s", got, network))
}

func checkAuthorizationTerms(o *out, pay *payment) {
	set := func(st adapter.Status, d string) { o.native(RowAuthorizationTerms, st, MethodAuthTerms, d) }
	auth, missing, err := pay.authorization()
	payTo, okP := pay.accepted.str("payTo")
	amount, okA := pay.accepted.str("amount")
	switch {
	case missing != "":
		set(adapter.NotPresent, missing+" is absent")
		return
	case !okP:
		set(adapter.NotPresent, "accepted.payTo is absent")
		return
	case !okA:
		set(adapter.NotPresent, "accepted.amount is absent")
		return
	case err != nil:
		set(adapter.Fail, err.Error())
		return
	}
	var fails []string
	if p, err := parseAddress(payTo); err != nil || p != auth.To {
		fails = append(fails, fmt.Sprintf("authorization.to %s, accepted.payTo %s", auth.To, payTo))
	}
	if a, err := parseUint256(amount); err != nil || a.Cmp(auth.Value) != 0 {
		fails = append(fails, fmt.Sprintf("authorization.value %s, accepted.amount %s", auth.Value, amount))
	}
	if auth.ValidAfter.Cmp(auth.ValidBefore) >= 0 {
		fails = append(fails, fmt.Sprintf("validAfter %s is not before validBefore %s", auth.ValidAfter, auth.ValidBefore))
	}
	if len(fails) > 0 {
		set(adapter.Fail, strings.Join(fails, "; "))
		return
	}
	set(adapter.Pass, fmt.Sprintf("to %s, value %s, window (%s, %s)", auth.To, auth.Value, auth.ValidAfter, auth.ValidBefore))
}

var txHashRE = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

// response is a settlement response that passed its form check.
type response struct {
	root               obj
	network, tx, payer string
	hasPayer           bool
}

func checkResponse(o *out, octets []byte, decodeErr error, pay *payment, policy adapter.TrustPolicy) *response {
	src := lookupSource(policy)
	if octets == nil {
		o.native(RowSettleResponseForm, adapter.NotPresent, MethodSettleForm, "no PAYMENT-RESPONSE octets held")
		o.native(RowSettleResponseJoin, adapter.NotPresent, MethodSettleJoin, "no settlement response held")
		o.native(RowSettleResponseSignature, adapter.NotPresent, MethodSettleSignature, "there is no signature to check")
		o.native(RowSettlementOnchain, adapter.NotPresent, methodSettlementOnchain("", src), "no settlement response held")
		return nil
	}
	resp, err := parseResponse(octets)
	if decodeErr != nil {
		err = decodeErr
	}
	if err != nil {
		o.native(RowSettleResponseForm, adapter.Fail, MethodSettleForm, err.Error())
		notCheckedAfterForm(o, policy)
		return nil
	}
	success, _ := resp.root.boolean("success")
	formDetail := "well-formed"
	if !success {
		reason, _ := resp.root.str("errorReason")
		formDetail = fmt.Sprintf("well-formed; states success false, errorReason %q", reason)
	}
	o.native(RowSettleResponseForm, adapter.Pass, MethodSettleForm, formDetail)
	checkSettleJoin(o, resp, pay)
	o.native(RowSettleResponseSignature, adapter.NotPresent, MethodSettleSignature, "there is no signature to check")
	checkSettlementOnchain(o, resp, pay, policy)
	return resp
}

// parseResponse applies the 5.3.2 member types.
func parseResponse(octets []byte) (*response, error) {
	v, err := decodeStrict(octets)
	if err != nil {
		return nil, err
	}
	root, ok := asObj(v)
	if !ok {
		return nil, fmt.Errorf("not a JSON object")
	}
	r := &response{root: root}
	if _, ok := root.boolean("success"); !ok {
		return nil, fmt.Errorf("success is missing or not a boolean")
	}
	if r.tx, ok = root.str("transaction"); !ok {
		return nil, fmt.Errorf("transaction is missing or not a string")
	}
	if r.tx != "" && !txHashRE.MatchString(r.tx) {
		return nil, fmt.Errorf("transaction %q is neither empty nor 0x and 64 hex digits", r.tx)
	}
	if r.network, ok = root.str("network"); !ok {
		return nil, fmt.Errorf("network is missing or not a string")
	}
	if ns, ref, ok := strings.Cut(r.network, ":"); !ok || ns == "" || ref == "" {
		return nil, fmt.Errorf("network %q is not a CAIP-2 identifier", r.network)
	}
	for _, k := range []string{"errorReason", "payer", "amount"} {
		if root.has(k) {
			if _, ok := root.str(k); !ok {
				return nil, fmt.Errorf("%s is not a string", k)
			}
		}
	}
	if root.has("extensions") {
		if _, ok := root.object("extensions"); !ok {
			return nil, fmt.Errorf("extensions is not an object")
		}
	}
	r.payer, r.hasPayer = root.str("payer")
	return r, nil
}

func checkSettleJoin(o *out, resp *response, pay *payment) {
	set := func(st adapter.Status, d string) { o.native(RowSettleResponseJoin, st, MethodSettleJoin, d) }
	if pay == nil || pay.accepted == nil {
		set(adapter.NotPresent, "the payment payload is not held")
		return
	}
	var fails []string
	accNet, _ := pay.accepted.str("network")
	if resp.network != accNet {
		fails = append(fails, fmt.Sprintf("network %s, accepted.network %s", resp.network, accNet))
	}
	if resp.hasPayer {
		fromStr, _ := pay.str(pay.auth, "from")
		a, errA := parseAddress(resp.payer)
		b, errB := parseAddress(fromStr)
		if errA != nil || errB != nil || a != b {
			fails = append(fails, fmt.Sprintf("payer %s, authorization.from %s", resp.payer, fromStr))
		}
	}
	if len(fails) > 0 {
		set(adapter.Fail, strings.Join(fails, "; "))
		return
	}
	if resp.hasPayer {
		set(adapter.Pass, "network and payer agree")
	} else {
		set(adapter.Pass, "network agrees; payer absent")
	}
}
