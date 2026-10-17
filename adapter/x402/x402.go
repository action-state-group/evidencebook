// SPDX-License-Identifier: Apache-2.0

// Package x402 is the evidencebook adapter for x402 payments: the payer's
// payment payload (PAYMENT-SIGNATURE, v1 X-PAYMENT) and the settlement
// response (PAYMENT-RESPONSE, v1 X-PAYMENT-RESPONSE), scheme "exact" on EVM
// with an EIP-3009 transferWithAuthorization.
//
// The native check of the payment payload is EIP-712 signature recovery over
// the authorization's members exactly as transmitted, under a domain the
// relying party pins (chain, token contract, token name and version). The
// settlement response is not signed by the facilitator; it gets a structure
// check, and settlement itself is an on-chain lookup that this adapter never
// performs. An optional payee receipt (the offer-receipt extension) is
// checked by EIP-712 recovery when present.
//
// The adapter makes no network calls.
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

// Property names, in report order.
const (
	PropPayloadForm      = "payment_payload_form"
	PropPayerSignature   = "payer_signature"
	PropChain            = "chain"
	PropAsset            = "asset"
	PropPayeeBinding     = "payee_binding"
	PropAmountBinding    = "amount_binding"
	PropValidityWindow   = "validity_window"
	PropSettleForm       = "settle_response_form"
	PropSettlement       = "settlement"
	PropReceiptSignature = "payee_receipt_signature"
)

// Trust policy pins and key roles this adapter reads.
const (
	// PinChain is the CAIP-2 network the relying party accepts, e.g. eip155:84532.
	PinChain = "x402.chain"
	// PinAsset is the token contract address.
	PinAsset = "x402.asset"
	// PinAssetName and PinAssetVersion are the token's EIP-712 domain name and version.
	PinAssetName    = "x402.asset.name"
	PinAssetVersion = "x402.asset.version"
	// PinPayTo and PinAmount optionally fix the payee and the atomic amount.
	PinPayTo  = "x402.pay_to"
	PinAmount = "x402.amount"
	// PinPayerAccounts is "eoa" (the default) or "eoa-or-contract". Under
	// "eoa-or-contract" a signature that does not recover to the payer is
	// INCONCLUSIVE, because a contract account's signature is checked on
	// chain (EIP-1271), which this adapter does not do.
	PinPayerAccounts = "x402.payer_accounts"
	// KeyReceiptSigner lists the addresses accepted as signers of payee receipts.
	KeyReceiptSigner = "x402.receipt_signer"
)

// Methods, as reported.
const (
	MethodPayloadForm = "base64 (RFC 4648 §4) of one JSON object, duplicate members rejected; x402 v2 PaymentPayload members (v1 noted); exact/EVM EIP-3009 authorization members as unsigned decimal, 20-byte address and 32-byte hex strings"
	MethodPayerSig    = "secp256k1 public-key recovery (EOA) over the EIP-712 digest of EIP-3009 TransferWithAuthorization, members as transmitted, domain {name, version, chainId, verifyingContract} from the trust policy pins; low-s required; recovered address compared to authorization.from"
	MethodChain       = "transmitted network (CAIP-2) compared to the trust policy pin x402.chain"
	MethodAsset       = "transmitted asset contract and EIP-712 name/version compared to the trust policy pins"
	MethodPayee       = "authorization.to compared to accepted.payTo and, when pinned, x402.pay_to"
	MethodAmount      = "authorization.value compared to accepted.amount and, when pinned, x402.amount"
	MethodWindow      = "validAfter < now < validBefore (EIP-3009), now from the trust policy clock"
	MethodSettleForm  = "structure only: x402 v2 SettlementResponse members, success true, transaction 0x and 64 hex digits, network equal to the pinned chain, payer equal to authorization.from; the response is not signed and the facilitator is not authenticated"
	MethodSettlement  = "tx lookup, not performed offline"
	MethodReceipt     = "secp256k1 public-key recovery over the EIP-712 digest of the offer-receipt Receipt (domain x402 receipt / 1 / chainId 1; payload.version 1, or 2 per the unmerged upstream delivery-binding revision), payload as transmitted; signer compared to the trust policy key role x402.receipt_signer"
)

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
		Protocol: "x402 v2 (v1 X-PAYMENT read), scheme exact, EVM, asset transfer method eip3009",
		Objects: []adapter.Object{
			{
				Name:             "x402.payment-payload",
				Format:           "PAYMENT-SIGNATURE header: base64 of a JSON PaymentPayload; payload = {signature, authorization}",
				Octets:           "the base64-decoded header value as sent",
				Issuer:           "payer: the account in authorization.from",
				IssuerResolution: "the payer is whoever holds the key the signature recovers to; the relying party resolves no identity beyond that address. Chain, token contract and token EIP-712 name/version are pinned by the trust policy, never taken from the payload",
				Method:           MethodPayerSig,
				NativelySigned:   true,
			},
			{
				Name:             "settlement-records leg (optional carrier)",
				Format:           "an Agent Action Capsule (JSON) with a settlement member whose wrapped[] carries x402.payment-payload and optionally x402.settle-response entries {type, digest_alg, digest, content}",
				Octets:           "each wrapped entry's content, base64url without padding, decoded",
				Issuer:           "the leg's sealer; the wrapped objects keep their own issuers",
				IssuerResolution: "not resolved here: the Capsule envelope (capsule_id, signature) is verified by the Capsule verifier, not by this adapter. A wrapped digest proves possession of the octets only",
				Method:           MethodWrappedDigest,
				NativelySigned:   false,
			},
			{
				Name:             "x402.settle-response",
				Format:           "PAYMENT-RESPONSE header: base64 of a JSON SettlementResponse {success, transaction, network, payer, ...}",
				Octets:           "the base64-decoded header value as received",
				Issuer:           "facilitator, relayed by the resource server",
				IssuerResolution: "none: the response carries no signature, so the facilitator is not cryptographically identified",
				Method:           MethodSettleForm + "; settlement by " + MethodSettlement,
				NativelySigned:   false,
			},
			{
				Name:             "x402 offer-receipt receipt (optional, inside the settle response)",
				Format:           "extensions[\"offer-receipt\"].info.receipt = {format: eip712, payload, signature}; format jws is read but not checked",
				Octets:           "the receipt payload members as transmitted",
				Issuer:           "payee (the resource server)",
				IssuerResolution: "the signer address must be listed under the trust policy key role x402.receipt_signer; how the relying party chose that address (payTo key, DNS TXT, registry) is outside the adapter",
				Method:           MethodReceipt,
				NativelySigned:   true,
			},
		},
		Predicate: "The holder of the key for authorization.from authorized a transfer of exactly authorization.value units of the pinned token contract on the pinned chain to authorization.to, valid strictly between validAfter and validBefore, single-use under its nonce. When a receipt check passes: the pinned payee signer stated that payer paid for resourceUrl.",
		NotSupported: []string{
			"that the transfer settled on chain (settlement is NOT_CHECKED offline)",
			"who the facilitator is, or that it reported truthfully",
			"that the resource or service was delivered",
			"the identity of the payer beyond the recovered address",
			"a payer authorization under permit2 or ERC-7710, or by a contract account (reported NOT_CHECKED; ERC-7710 has no offline-checkable payer signature and proves possession only)",
		},
		Properties: []adapter.Property{
			{Name: PropWrappedDigest, Object: "settlement-records leg", Method: MethodWrappedDigest, Supports: "the leg's wrapped x402.payment-payload content is the octets its digest names (possession only)"},
			{Name: PropPayloadForm, Object: "x402.payment-payload", Method: MethodPayloadForm, Supports: "the octets are an x402 payment payload this adapter can read"},
			{Name: PropPayerSignature, Object: "x402.payment-payload", Method: MethodPayerSig, Supports: "the payer's key signed this authorization for the pinned token and chain"},
			{Name: PropChain, Object: "x402.payment-payload", Method: MethodChain, Supports: "the payment names the chain the relying party accepts"},
			{Name: PropAsset, Object: "x402.payment-payload", Method: MethodAsset, Supports: "the payment names the token the relying party accepts"},
			{Name: PropPayeeBinding, Object: "x402.payment-payload", Method: MethodPayee, Supports: "the signed recipient is the payee the requirements named"},
			{Name: PropAmountBinding, Object: "x402.payment-payload", Method: MethodAmount, Supports: "the signed value is the amount the requirements named"},
			{Name: PropValidityWindow, Object: "x402.payment-payload", Method: MethodWindow, Supports: "the authorization was usable at the relying party's time"},
			{Name: PropSettleWrappedDigest, Object: "settlement-records leg", Method: MethodWrappedDigest, Supports: "the leg's wrapped x402.settle-response content is the octets its digest names (possession only)"},
			{Name: PropSettleForm, Object: "x402.settle-response", Method: MethodSettleForm, Supports: "a well-formed success response naming a transaction; nothing about who sent it"},
			{Name: PropSettlement, Object: "x402.settle-response", Method: MethodSettlement, Supports: "nothing offline"},
			{Name: PropReceiptSignature, Object: "x402 offer-receipt receipt", Method: MethodReceipt, Supports: "the pinned payee signer issued this receipt"},
			{Name: PropLegAmount, Object: "settlement-records leg", Method: MethodLegAmount, Supports: "the leg's amount is the amount the payer accepted"},
			{Name: PropLegNetwork, Object: "settlement-records leg", Method: MethodLegNetwork, Supports: "the leg's payment reference names the network the payer accepted"},
		},
	}
}

// exchange is the as-received octets of one x402 exchange.
type exchange struct {
	payload, response             []byte
	payloadHeader, responseHeader string
}

// parseExchange reads an HTTP header block: one "Name: value" per line
// (CRLF or LF), names case-insensitive, other headers ignored. It returns
// the base64-decoded payment payload and settlement response, either of
// which may be absent.
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
		value = strings.Trim(value, " \t")
		if _, dup := seen[name]; dup {
			return ex, fmt.Errorf("header %s appears twice", name)
		}
		seen[name] = value
	}
	pick := func(names []string) (string, string, error) {
		var got, gotName string
		for _, n := range names {
			if v, ok := seen[n]; ok {
				if gotName != "" {
					return "", "", fmt.Errorf("both %s and %s are present", gotName, n)
				}
				got, gotName = v, n
			}
		}
		return got, gotName, nil
	}
	pv, pn, err := pick(payloadHeaders)
	if err != nil {
		return ex, err
	}
	rv, rn, err := pick(responseHeaders)
	if err != nil {
		return ex, err
	}
	ex.payloadHeader, ex.responseHeader = pn, rn
	if pn != "" {
		if ex.payload, err = base64.StdEncoding.DecodeString(pv); err != nil {
			return ex, fmt.Errorf("%s is not base64: %v", pn, err)
		}
	}
	if rn != "" {
		if ex.response, err = base64.StdEncoding.DecodeString(rv); err != nil {
			return ex, fmt.Errorf("%s is not base64: %v", rn, err)
		}
	}
	return ex, nil
}

// payment is a parsed payment payload.
type payment struct {
	version  int
	scheme   string
	network  string // as transmitted (v1: a name such as base-sepolia)
	caip2    string // network as CAIP-2, "" when a v1 name is unknown
	accepted obj    // nil for v1
	method   string // eip3009, permit2, erc7710
	sig      string
	auth     authorization
	authOK   bool
}

var v1Networks = map[string]string{"base": "eip155:8453", "base-sepolia": "eip155:84532"}

func parsePayment(octets []byte) (*payment, error) {
	v, err := decodeStrict(octets)
	if err != nil {
		return nil, err
	}
	root, ok := asObj(v)
	if !ok {
		return nil, fmt.Errorf("not a JSON object")
	}
	ver, ok := root.num("x402Version")
	if !ok {
		return nil, fmt.Errorf("x402Version is missing or not a number")
	}
	p := &payment{}
	body, ok := root.object("payload")
	if !ok {
		return nil, fmt.Errorf("payload is missing or not an object")
	}
	switch ver.String() {
	case "2":
		p.version = 2
		if p.accepted, ok = root.object("accepted"); !ok {
			return nil, fmt.Errorf("accepted is missing or not an object")
		}
		if p.scheme, ok = p.accepted.str("scheme"); !ok {
			return nil, fmt.Errorf("accepted.scheme is missing")
		}
		if p.network, ok = p.accepted.str("network"); !ok {
			return nil, fmt.Errorf("accepted.network is missing")
		}
		p.caip2 = p.network
		p.method = "eip3009"
		if extra, ok := p.accepted.object("extra"); ok && extra.has("assetTransferMethod") {
			if p.method, ok = extra.str("assetTransferMethod"); !ok {
				return nil, fmt.Errorf("accepted.extra.assetTransferMethod is not a string")
			}
		} else if body.has("permit2Authorization") {
			p.method = "permit2"
		} else if body.has("permissionContext") {
			p.method = "erc7710"
		}
	case "1":
		p.version = 1
		if p.scheme, ok = root.str("scheme"); !ok {
			return nil, fmt.Errorf("scheme is missing")
		}
		if p.network, ok = root.str("network"); !ok {
			return nil, fmt.Errorf("network is missing")
		}
		p.caip2 = v1Networks[p.network]
		p.method = "eip3009"
	default:
		return nil, fmt.Errorf("x402Version %s is not 1 or 2", ver)
	}
	if native, _ := p.checksNatively(); !native {
		return p, nil // read, but not a payload this adapter checks natively
	}
	if p.sig, ok = body.str("signature"); !ok {
		return nil, fmt.Errorf("payload.signature is missing or not a string")
	}
	a, ok := body.object("authorization")
	if !ok {
		return nil, fmt.Errorf("payload.authorization is missing or not an object")
	}
	field := func(k string) (string, error) {
		s, ok := a.str(k)
		if !ok {
			return "", fmt.Errorf("authorization.%s is missing or not a string", k)
		}
		return s, nil
	}
	var s string
	if s, err = field("from"); err == nil {
		p.auth.From, err = parseAddress(s)
	}
	if err == nil {
		if s, err = field("to"); err == nil {
			p.auth.To, err = parseAddress(s)
		}
	}
	for _, f := range []struct {
		k   string
		dst **big.Int
	}{{"value", &p.auth.Value}, {"validAfter", &p.auth.ValidAfter}, {"validBefore", &p.auth.ValidBefore}} {
		if err != nil {
			break
		}
		if s, err = field(f.k); err == nil {
			*f.dst, err = parseUint256(s)
		}
	}
	if err == nil {
		if s, err = field("nonce"); err == nil {
			p.auth.Nonce, err = parseBytes32(s)
		}
	}
	if err != nil {
		return nil, err
	}
	p.authOK = true
	return p, nil
}

// checksNatively reports whether the payment is one whose payer
// authorization this adapter verifies, and if not, why.
func (p *payment) checksNatively() (bool, string) {
	switch {
	case p.scheme != "exact":
		return false, fmt.Sprintf("scheme %q is not implemented by this adapter", p.scheme)
	case p.method == "erc7710":
		return false, "ERC-7710 delegation: x402 carries no payer signature a verifier can check offline (the facilitator verifies by on-chain simulation); possession of the octets only"
	case p.method == "permit2":
		return false, "permit2 transfer method is not implemented in this revision"
	case p.method != "eip3009":
		return false, fmt.Sprintf("asset transfer method %q is not implemented", p.method)
	case p.caip2 == "":
		return false, fmt.Sprintf("x402 v1 network %q has no CAIP-2 mapping in this adapter", p.network)
	case !strings.HasPrefix(p.caip2, "eip155:"):
		return false, fmt.Sprintf("network %q is not an EVM (eip155) chain", p.caip2)
	}
	return true, ""
}

// chainID reads the decimal reference of an eip155 CAIP-2 identifier.
func chainID(caip2 string) (*big.Int, error) {
	ref, ok := strings.CutPrefix(caip2, "eip155:")
	if !ok {
		return nil, fmt.Errorf("%q is not an eip155 network", caip2)
	}
	return parseUint256(ref)
}

var txHashRE = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

// report collects one result per property.
type report map[string]adapter.PropertyResult

func (r report) set(prop string, st adapter.Status, method, detail string) {
	r[prop] = adapter.PropertyResult{Property: prop, Status: st, Method: method, Detail: detail}
}

// Check implements adapter.Adapter. raw is one of three input forms,
// detected from its content:
//
//   - a settlement-records leg: a JSON Capsule with a settlement member whose
//     wrapped[] carries x402.payment-payload (and optionally
//     x402.settle-response) content;
//   - the bare payment payload: the base64-decoded PAYMENT-SIGNATURE octets
//     (a JSON object with x402Version), with no settlement response;
//   - an HTTP header block carrying PAYMENT-SIGNATURE (or v1 X-PAYMENT) and,
//     optionally, PAYMENT-RESPONSE (or v1 X-PAYMENT-RESPONSE), as received.
//
// Only the properties that apply to the form are reported: the wrapped_*
// and leg_* properties for a leg only.
func (a Adapter) Check(raw []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	if trimmed := bytes.TrimLeft(raw, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '{' {
		return checkJSON(raw, policy)
	}
	ex, err := parseExchange(raw)
	if err != nil {
		r := report{}
		for _, prop := range a.NativeObject().Properties {
			if !legOnly[prop.Name] {
				r.set(prop.Name, adapter.NotChecked, prop.Method, "header block not read: "+err.Error())
			}
		}
		r.set(PropPayloadForm, adapter.Fail, MethodPayloadForm, err.Error())
		return r.ordered()
	}
	return CheckObjects(ex.payload, ex.response, policy)
}

// CheckObjects checks a payment payload and a settlement response given as
// their base64-decoded octets (the Settlement Records octets for
// x402.payment-payload and x402.settle-response). Either may be nil, meaning
// absent.
func CheckObjects(payloadOctets, responseOctets []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	r := report{}
	checkAll(r, payloadOctets, responseOctets, policy)
	return r.ordered()
}

func checkAll(r report, payloadOctets, responseOctets []byte, policy adapter.TrustPolicy) *payment {
	pay := checkPayment(r, payloadOctets, policy)
	checkResponse(r, responseOctets, pay, policy)
	return pay
}

func (r report) ordered() []adapter.PropertyResult {
	var out []adapter.PropertyResult
	for _, prop := range (Adapter{}).NativeObject().Properties {
		if res, ok := r[prop.Name]; ok {
			out = append(out, res)
		}
	}
	return out
}

func checkPayment(r report, octets []byte, policy adapter.TrustPolicy) *payment {
	payloadProps := []struct{ name, method string }{
		{PropPayerSignature, MethodPayerSig}, {PropChain, MethodChain}, {PropAsset, MethodAsset},
		{PropPayeeBinding, MethodPayee}, {PropAmountBinding, MethodAmount}, {PropValidityWindow, MethodWindow},
	}
	if octets == nil {
		r.set(PropPayloadForm, adapter.NotPresent, MethodPayloadForm, "no payment payload (PAYMENT-SIGNATURE, X-PAYMENT, or a wrapped x402.payment-payload with content)")
		for _, p := range payloadProps {
			r.set(p.name, adapter.NotPresent, p.method, "no payment payload")
		}
		return nil
	}
	pay, err := parsePayment(octets)
	if err != nil {
		r.set(PropPayloadForm, adapter.Fail, MethodPayloadForm, err.Error())
		for _, p := range payloadProps {
			r.set(p.name, adapter.NotChecked, p.method, "payment payload not readable")
		}
		return nil
	}
	r.set(PropPayloadForm, adapter.Pass, MethodPayloadForm, fmt.Sprintf("x402 v%d, scheme %s, network %s, method %s", pay.version, pay.scheme, pay.network, pay.method))

	native, why := pay.checksNatively()
	if !native {
		for _, p := range payloadProps {
			r.set(p.name, adapter.NotChecked, p.method, why)
		}
		return pay
	}
	checkChain(r, pay, policy)
	checkAsset(r, pay, policy)
	checkPayerSignature(r, pay, policy)
	checkBinding(r, PropPayeeBinding, MethodPayee, pay, policy.Pin(PinPayTo), "payTo", func(s string) (bool, error) {
		want, err := parseAddress(s)
		return want == pay.auth.To, err
	}, pay.auth.To.String())
	checkBinding(r, PropAmountBinding, MethodAmount, pay, policy.Pin(PinAmount), "amount", func(s string) (bool, error) {
		want, err := parseUint256(s)
		return err == nil && want.Cmp(pay.auth.Value) == 0, err
	}, pay.auth.Value.String())
	checkWindow(r, pay, policy)
	return pay
}

func checkChain(r report, pay *payment, policy adapter.TrustPolicy) {
	pin := policy.Pin(PinChain)
	switch {
	case pin == "":
		r.set(PropChain, adapter.NotChecked, MethodChain, "the trust policy pins no chain ("+PinChain+")")
	case pay.caip2 == pin:
		r.set(PropChain, adapter.Pass, MethodChain, pay.caip2)
	default:
		r.set(PropChain, adapter.Fail, MethodChain, fmt.Sprintf("transmitted %s, pinned %s", pay.network, pin))
	}
}

func checkAsset(r report, pay *payment, policy adapter.TrustPolicy) {
	if pay.version == 1 {
		r.set(PropAsset, adapter.NotPresent, MethodAsset, "an x402 v1 payment payload carries no asset")
		return
	}
	pin := policy.Pin(PinAsset)
	if pin == "" {
		r.set(PropAsset, adapter.NotChecked, MethodAsset, "the trust policy pins no asset ("+PinAsset+")")
		return
	}
	want, err := parseAddress(pin)
	if err != nil {
		r.set(PropAsset, adapter.Inconclusive, MethodAsset, "pinned asset unreadable: "+err.Error())
		return
	}
	s, _ := pay.accepted.str("asset")
	got, err := parseAddress(s)
	if err != nil {
		r.set(PropAsset, adapter.Fail, MethodAsset, "accepted.asset: "+err.Error())
		return
	}
	if got != want {
		r.set(PropAsset, adapter.Fail, MethodAsset, fmt.Sprintf("transmitted %s, pinned %s", got, want))
		return
	}
	extra, _ := pay.accepted.object("extra")
	for _, f := range []struct{ member, pin string }{{"name", PinAssetName}, {"version", PinAssetVersion}} {
		want := policy.Pin(f.pin)
		if want == "" {
			continue
		}
		if got, _ := extra.str(f.member); got != want {
			r.set(PropAsset, adapter.Fail, MethodAsset, fmt.Sprintf("accepted.extra.%s %q, pinned %q", f.member, got, want))
			return
		}
	}
	r.set(PropAsset, adapter.Pass, MethodAsset, want.String())
}

// ERC-6492 wraps a counterfactual contract wallet's signature and ends with
// this 32-byte magic value.
var erc6492Magic = strings.Repeat("6492", 16)

func checkPayerSignature(r report, pay *payment, policy adapter.TrustPolicy) {
	set := func(st adapter.Status, detail string) { r.set(PropPayerSignature, st, MethodPayerSig, detail) }
	name, version := policy.Pin(PinAssetName), policy.Pin(PinAssetVersion)
	pinChain, pinAsset := policy.Pin(PinChain), policy.Pin(PinAsset)
	if name == "" || version == "" || pinChain == "" || pinAsset == "" {
		set(adapter.Inconclusive, "the trust policy does not pin the EIP-712 domain ("+strings.Join([]string{PinChain, PinAsset, PinAssetName, PinAssetVersion}, ", ")+")")
		return
	}
	cid, err := chainID(pinChain)
	if err != nil {
		set(adapter.Inconclusive, "pinned chain: "+err.Error())
		return
	}
	contract, err := parseAddress(pinAsset)
	if err != nil {
		set(adapter.Inconclusive, "pinned asset: "+err.Error())
		return
	}
	contractAccounts := policy.Pin(PinPayerAccounts) == "eoa-or-contract"
	sig, err := parseHexBytes(pay.sig)
	if err != nil {
		set(adapter.Fail, err.Error())
		return
	}
	if len(sig) != 65 {
		kind := "a contract-account (EIP-1271) signature"
		if strings.HasSuffix(strings.ToLower(pay.sig), erc6492Magic) {
			kind = "an ERC-6492 counterfactual contract-account signature"
		}
		if contractAccounts {
			set(adapter.NotChecked, fmt.Sprintf("%d-byte signature: %s, verified by an on-chain call, not performed offline", len(sig), kind))
		} else {
			set(adapter.Fail, fmt.Sprintf("%d-byte signature; the trust policy accepts EOA payers only, whose signatures are 65 bytes", len(sig)))
		}
		return
	}
	digest := typedDataDigest(tokenDomainSeparator(name, version, cid, contract), pay.auth.structHash())
	got, err := recoverAddress(digest, sig)
	if err != nil {
		set(adapter.Fail, err.Error())
		return
	}
	switch {
	case got == pay.auth.From:
		set(adapter.Pass, "recovers to "+got.String())
	case contractAccounts:
		set(adapter.Inconclusive, fmt.Sprintf("recovers to %s, not authorization.from %s; a contract account's EIP-1271 check is on chain, not performed offline", got, pay.auth.From))
	default:
		set(adapter.Fail, fmt.Sprintf("recovers to %s, not authorization.from %s", got, pay.auth.From))
	}
}

// checkBinding compares one authorization member to the accepted
// requirement member (v2) and to an optional pin.
func checkBinding(r report, prop, method string, pay *payment, pin, member string, matches func(string) (bool, error), signed string) {
	var against []string
	if pay.accepted != nil {
		s, ok := pay.accepted.str(member)
		if !ok {
			r.set(prop, adapter.Fail, method, "accepted."+member+" is missing")
			return
		}
		ok, err := matches(s)
		if err != nil {
			r.set(prop, adapter.Fail, method, "accepted."+member+": "+err.Error())
			return
		}
		if !ok {
			r.set(prop, adapter.Fail, method, fmt.Sprintf("signed %s, accepted.%s %s", signed, member, s))
			return
		}
		against = append(against, "accepted."+member)
	}
	if pin != "" {
		ok, err := matches(pin)
		if err != nil {
			r.set(prop, adapter.Inconclusive, method, "pin unreadable: "+err.Error())
			return
		}
		if !ok {
			r.set(prop, adapter.Fail, method, fmt.Sprintf("signed %s, pinned %s", signed, pin))
			return
		}
		against = append(against, "pin")
	}
	if len(against) == 0 {
		r.set(prop, adapter.NotPresent, method, "an x402 v1 payload carries no "+member+" and the trust policy pins none")
		return
	}
	r.set(prop, adapter.Pass, method, signed+" equals "+strings.Join(against, " and "))
}

func checkWindow(r report, pay *payment, policy adapter.TrustPolicy) {
	if policy.Now.IsZero() {
		r.set(PropValidityWindow, adapter.NotChecked, MethodWindow, "the trust policy supplies no clock")
		return
	}
	now := big.NewInt(policy.Now.Unix())
	detail := fmt.Sprintf("validAfter %s, now %s, validBefore %s", pay.auth.ValidAfter, now, pay.auth.ValidBefore)
	if pay.auth.ValidAfter.Cmp(now) < 0 && now.Cmp(pay.auth.ValidBefore) < 0 {
		r.set(PropValidityWindow, adapter.Pass, MethodWindow, detail)
	} else {
		r.set(PropValidityWindow, adapter.Fail, MethodWindow, detail)
	}
}

func checkResponse(r report, octets []byte, pay *payment, policy adapter.TrustPolicy) {
	if octets == nil {
		r.set(PropSettleForm, adapter.NotPresent, MethodSettleForm, "no settlement response (PAYMENT-RESPONSE, X-PAYMENT-RESPONSE, or a wrapped x402.settle-response with content)")
		r.set(PropSettlement, adapter.NotPresent, MethodSettlement, "no settlement response")
		r.set(PropReceiptSignature, adapter.NotPresent, MethodReceipt, "no settlement response")
		return
	}
	v, err := decodeStrict(octets)
	resp, ok := asObj(v)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("not a JSON object")
		}
		r.set(PropSettleForm, adapter.Fail, MethodSettleForm, err.Error())
		r.set(PropSettlement, adapter.NotChecked, MethodSettlement, "settlement response not readable")
		r.set(PropReceiptSignature, adapter.NotChecked, MethodReceipt, "settlement response not readable")
		return
	}
	tx, _ := resp.str("transaction")
	if tx == "" {
		r.set(PropSettlement, adapter.NotPresent, MethodSettlement, "no transaction hash")
	} else {
		r.set(PropSettlement, adapter.NotChecked, MethodSettlement, "transaction "+tx)
	}
	r.set(settleForm(resp, pay, policy))
	checkReceipt(r, resp, policy)
}

func settleForm(resp obj, pay *payment, policy adapter.TrustPolicy) (string, adapter.Status, string, string) {
	fail := func(d string) (string, adapter.Status, string, string) {
		return PropSettleForm, adapter.Fail, MethodSettleForm, d
	}
	success, ok := resp.boolean("success")
	if !ok {
		return fail("success is missing or not a boolean")
	}
	tx, ok := resp.str("transaction")
	if !ok {
		return fail("transaction is missing or not a string")
	}
	network, ok := resp.str("network")
	if !ok {
		return fail("network is missing or not a string")
	}
	if !success {
		reason, _ := resp.str("errorReason")
		return fail("success is false; errorReason " + fmt.Sprintf("%q", reason))
	}
	if !txHashRE.MatchString(tx) {
		return fail(fmt.Sprintf("transaction %q is not 0x and 64 hex digits", tx))
	}
	caip2 := network
	if m, ok := v1Networks[network]; ok {
		caip2 = m
	}
	want := policy.Pin(PinChain)
	if want == "" && pay != nil {
		want = pay.caip2
	}
	if want != "" && caip2 != want {
		return fail(fmt.Sprintf("network %s, expected %s", network, want))
	}
	if resp.has("payer") {
		s, ok := resp.str("payer")
		if !ok {
			return fail("payer is not a string")
		}
		if pay != nil && pay.authOK {
			got, err := parseAddress(s)
			if err != nil {
				return fail("payer: " + err.Error())
			}
			if got != pay.auth.From {
				return fail(fmt.Sprintf("payer %s, authorization.from %s", got, pay.auth.From))
			}
		}
	}
	return PropSettleForm, adapter.Pass, MethodSettleForm, "transaction " + tx + " on " + network + " (unsigned facilitator statement)"
}
