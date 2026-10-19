// SPDX-License-Identifier: Apache-2.0

// Package acp is the evidencebook adapter for the Agentic Commerce Protocol
// (ACP), API version 2026-04-17: the checkout session, the order, and the
// delegated payment token, as declared in agent-action-capsule
// spec/adapters/acp-v0.md (cited here as acp-v0).
//
// ACP defines no signature by the issuer over any of these objects. This
// adapter therefore supports possession of exact octets and nothing more. It
// identifies which ACP object the octets are, validates them against ACP's
// published JSON Schema, and digests them as delivered. Its native_check is
// NOT_PRESENT for the checkout session, order and token, and NOT_CHECKED for
// the order webhook's shared-secret HMAC (acp-v0 §2, §3.3). It never reports
// a native_check PASS or FAIL.
//
// A native_check NOT_PRESENT or NOT_CHECKED maps as profile §8.1 rows 3-4
// pending a profile row (m3-adapters ruling S1): INSUFFICIENT or UNKNOWN,
// not_evaluable, for a requirement that rests on it, and nothing for one that
// does not.
//
// The adapter makes no network calls.
package acp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/action-state-group/evidencebook/adapter"
	"github.com/action-state-group/evidencebook/adapter/internal/ijson"
)

// Property names, in report order.
const (
	PropDigest      = "octets_digest"
	PropObjectForm  = "object_form"
	PropNativeCheck = adapter.NativeCheck
)

// Native check names. Each is reported as a native_check report with its
// name at the start of detail (adapter.NativeCheckResult; ruling S2), and a
// requirement rests on it by that name (adapter.CheckName).
const (
	CheckCheckoutSessionSignature = "checkout_session_signature"
	CheckOrderSignature           = "order_signature"
	CheckOrderEventHMAC           = "order_event_hmac"
	CheckDelegatePaymentSignature = "delegate_payment_signature"
)

// PinCarriedDigest is the trust policy pin for the digest the relying party
// expects: the SHA-256, in lowercase hex, that the Artifact carrying or citing
// the ACP object states for it. The relying party takes it from that
// Artifact after verifying the Artifact's envelope, never from the ACP octets.
const PinCarriedDigest = "acp.carried_digest"

// Methods, as reported. The native_check methods are acp-v0 §2's, verbatim.
const (
	// MethodDigest is acp-v0 §2's digest row. The method reported adds the
	// source of the carried digest (m3-adapters ruling on em-oss question 2):
	// the wrapping record's digest when the octets arrive in a
	// settlement-records leg, otherwise the relying party's pin.
	MethodDigest       = "SHA-256 over the octets as delivered, compared with the carried digest"
	MethodDigestPinned = MethodDigest + ": the relying party's pin " + PinCarriedDigest + " (standalone; no wrapping record)"
	MethodDigestLeg    = MethodDigest + ": the settlement-records leg's wrapped[] entry digest, over its content (base64url without padding, decoded) (Wrapping Existing Signed Objects rule 2)"
	MethodObjectForm   = "one JSON value, duplicate members rejected; the ACP 2026-04-17 object identified by its members and validated (JSON Schema 2020-12, formats as annotations, no external references) against ACP's published schema vendored at commit 7fdd78df: JS-CHK $defs/CheckoutSession or $defs/CheckoutSessionWithOrder (whole object; the adapter never validates against OAS-CHK, so acp-v0 A-4 does not arise), JS-DP $defs/DelegatePaymentResponse; or the OAS-WH WebhookEvent body with data as OAS-WH EventDataOrder; the octets are not authenticated"

	MethodNativeCheckout     = "none: ACP 2026-04-17 defines no issuer signature over CheckoutSession"
	MethodNativeOrder        = "none: ACP 2026-04-17 defines no issuer signature over Order"
	MethodNativeOrderWebhook = "not run: HMAC-SHA256 under a shared secret (OAS-WH Merchant-Signature)"
	// MethodNativeOrderWebhookAbsent is acp-v0 §2.2 (e) for an order event
	// body received without its Merchant-Signature header (2ef8aeb4).
	MethodNativeOrderWebhookAbsent = "none: no Merchant-Signature header was received with the order event body"
	MethodNativeToken              = "none: ACP 2026-04-17 defines no issuer signature over DelegatePaymentResponse"
)

// Object names, as declared.
const (
	ObjCheckoutSession = "acp checkout session"
	ObjOrder           = "acp order"
	ObjDelegatePayment = "acp delegated payment token"

	// The object member of each native_check report's detail.
	objOrderPath1 = ObjOrder + " (path 1)"
	objOrderPath2 = ObjOrder + " (path 2)"
)

// Adapter implements adapter.Adapter for ACP.
type Adapter struct{}

var _ adapter.Adapter = Adapter{}

// Name implements adapter.Adapter.
func (Adapter) Name() string { return "acp" }

// NativeObject implements adapter.Adapter: the FR-19 declaration, after
// acp-v0 §2.
func (Adapter) NativeObject() adapter.NativeObject {
	return adapter.NativeObject{
		Protocol: "Agentic Commerce Protocol (ACP) API version 2026-04-17, read at agentic-commerce-protocol commit 7fdd78df677a94dce04c770644b0fbbb1401272b (agent-action-capsule spec/adapters/acp-v0.md)",
		Objects: []adapter.Object{
			{
				Name:             ObjCheckoutSession,
				Format:           "JSON CheckoutSession, or CheckoutSessionWithOrder once completed (JS-CHK $defs): the response body of createCheckoutSession, updateCheckoutSession, getCheckoutSession, completeCheckoutSession or cancelCheckoutSession, or the JSON-RPC result of the same-named MCP tool",
				Octets:           "the response body as received, after any content coding is removed; over MCP, the octets of the result member's value as it appears in the JSON-RPC response. ACP fixes no byte sequence: two retrievals are different octets",
				Issuer:           "the merchant (the server of the checkout API)",
				IssuerResolution: "none: ACP gives the object no issuer identifier or key; the only attribution is the caller's TLS connection (to an MCP proxy, when one sits between), which a third party cannot re-check from the octets",
				Method:           MethodNativeCheckout,
				NativelySigned:   false,
			},
			{
				Name:             ObjOrder,
				Format:           "JSON Order (JS-CHK $defs/Order). Path 1: the order member of the CheckoutSessionWithOrder that completeCheckoutSession returns. Path 2: the data member of an order event the merchant POSTs by postOrderEvents (OAS-WH)",
				Octets:           "path 1: the checkout session's octets, of which the order is a member; path 2: the request body as received (OAS-WH's raw_body)",
				Issuer:           "the merchant",
				IssuerResolution: "path 1: as the checkout session. Path 2: the Merchant-Signature HMAC key is a secret shared by the merchant and the receiving platform, so it identifies no single issuer to a third party, and ACP defines no way to publish or resolve it",
				Method:           MethodNativeOrder + " (path 1); " + MethodNativeOrderWebhook + " (path 2)",
				NativelySigned:   false,
			},
			{
				Name:             "settlement-records leg (optional carrier)",
				Format:           "an Agent Action Capsule (JSON) with a settlement member whose wrapped[] carries one entry {type, digest_alg, digest, content} of type acp.checkout-session, acp.order-event or acp.delegate-payment-response (proposed by acp-v0 A-3, not registered)",
				Octets:           "the entry's content, base64url without padding, decoded",
				Issuer:           "the leg's sealer; the wrapped object keeps its own issuer",
				IssuerResolution: "not resolved here: the Capsule envelope is verified by the Capsule verifier. The entry's digest is the carried digest; it proves possession of the octets only",
				Method:           MethodDigestLeg,
				NativelySigned:   false,
			},
			{
				Name:             ObjDelegatePayment,
				Format:           "JSON DelegatePaymentResponse {id, created, metadata} (JS-DP): the 201 body of delegatePayment; id is an opaque vault token identifier, and the Allowance it is limited to is in the request, not restated",
				Octets:           "the response body as received",
				Issuer:           "the server of the delegate payment API",
				IssuerResolution: "none: no issuer identifier or key; attribution is the caller's TLS connection",
				Method:           MethodNativeToken,
				NativelySigned:   false,
			},
		},
		Predicate: "Possession only: the presenting party holds octets whose SHA-256 is D, and states they are the body ACP returned from operation O at time T (for an order event, the body postOrderEvents delivered). The digest binds the octets; the operation, the time and the merchant are the presenter's statement.",
		NotSupported: []string{
			"that the merchant produced the checkout session or the order",
			"that the session's status, line_items, totals or order were the merchant's state",
			"that a delegated payment token existed, was bound to a given Allowance, or was valid at any time",
			"that the order event's Merchant-Signature is valid: the HMAC is under a shared secret and is not run; a receiver's statement that it verified it is the receiver's own",
			"that two carried objects naming the same token or session identifier are related beyond naming the same string",
			"anything about the caller's request Signature header (acp-v0 §3.1)",
		},
		Properties: []adapter.Property{
			{Name: PropDigest, Object: "any object above", Method: MethodDigest, Supports: "the octets held are the octets the carried digest names (possession only)"},
			{Name: PropObjectForm, Object: "any object above", Method: MethodObjectForm, Supports: "the octets are the named ACP object as ACP's published schema defines it; nothing about who produced them"},
			{Name: CheckCheckoutSessionSignature, Object: ObjCheckoutSession, Method: MethodNativeCheckout, Supports: "nothing: always NOT_PRESENT"},
			{Name: CheckOrderSignature, Object: objOrderPath1, Method: MethodNativeOrder, Supports: "nothing: always NOT_PRESENT"},
			{Name: CheckOrderEventHMAC, Object: objOrderPath2, Method: MethodNativeOrderWebhook, Supports: "nothing: NOT_CHECKED when the Merchant-Signature header is received"},
			{Name: CheckOrderEventHMAC, Object: objOrderPath2, Method: MethodNativeOrderWebhookAbsent, Supports: "nothing: NOT_PRESENT when the order event body arrives without the header"},
			{Name: CheckDelegatePaymentSignature, Object: ObjDelegatePayment, Method: MethodNativeToken, Supports: "nothing: always NOT_PRESENT"},
		},
	}
}

// kind is the ACP object the octets were identified as.
type kind int

const (
	kindUnknown kind = iota
	kindCheckoutSession
	kindCheckoutSessionWithOrder
	kindDelegatePayment
	kindOrderEvent
)

func (k kind) String() string {
	switch k {
	case kindCheckoutSession:
		return "CheckoutSession (JS-CHK $defs/CheckoutSession)"
	case kindCheckoutSessionWithOrder:
		return "CheckoutSessionWithOrder (JS-CHK $defs/CheckoutSessionWithOrder)"
	case kindDelegatePayment:
		return "DelegatePaymentResponse (JS-DP $defs/DelegatePaymentResponse)"
	case kindOrderEvent:
		return "order event body (OAS-WH WebhookEvent, data as OAS-WH EventDataOrder)"
	}
	return "unidentified"
}

// input is the octets located in what was received, and how they arrived.
type input struct {
	octets []byte
	// carrier names the input form, for details.
	carrier string
	// signature is the Merchant-Signature header value, when a header block
	// was received and carried one.
	signature    string
	hasSignature bool
	// entryType is the wrapped entry's type, for a leg.
	entryType string
}

// Check implements adapter.Adapter. raw is one of four input forms,
// detected from its content:
//
//   - a settlement-records leg (a JSON object with a settlement member) whose
//     settlement.wrapped[] holds one entry of a type acp-v0 A-3 proposes
//     (acp.checkout-session, acp.order-event, acp.delegate-payment-response):
//     the octets are its content, and the carried digest is its digest;
//   - a JSON body: the ACP object's octets exactly as received;
//   - a JSON-RPC response (an object with a jsonrpc member) from ACP's MCP
//     binding: the octets are those of the result member's value as they
//     appear in raw;
//   - an HTTP message: an optional request or status line, header fields,
//     an empty line, then the body as received. This is how an order event
//     arrives with its Merchant-Signature header.
//
// It reports octets_digest, object_form, and one native_check for each
// declared object the octets are (two for a CheckoutSessionWithOrder: the
// session and its order).
func (Adapter) Check(raw []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	var out []adapter.PropertyResult
	set := func(prop string, st adapter.Status, method, detail string) {
		out = append(out, adapter.PropertyResult{Property: prop, Status: st, Method: method, Detail: detail})
	}

	var in input
	var wantKind []kind
	if root, ok := legRoot(raw); ok {
		var st adapter.Status
		var detail string
		in, wantKind, st, detail = unwrapLeg(root)
		set(PropDigest, st, MethodDigestLeg, detail)
		if in.octets == nil {
			// No content to read: nothing is identified, and no native
			// check applies.
			set(PropObjectForm, adapter.NotPresent, MethodObjectForm, "no wrapped ACP content to read: "+detail)
			return out
		}
	} else {
		var err error
		if in, err = locate(raw); err != nil {
			set(PropDigest, adapter.NotChecked, MethodDigestPinned, "the object's octets were not located: "+err.Error())
			set(PropObjectForm, adapter.Fail, MethodObjectForm, err.Error())
			return out
		}
		st, detail := checkDigest(in.octets, policy)
		set(PropDigest, st, MethodDigestPinned, detail)
	}

	k, err := identify(in.octets)
	if err == nil && wantKind != nil && !slices.Contains(wantKind, k) {
		err = fmt.Errorf("the wrapped entry's type is %s, but the content is a %s", in.entryType, k)
	}
	switch {
	case k == kindUnknown:
		set(PropObjectForm, adapter.Fail, MethodObjectForm, in.carrier+": "+err.Error())
		return out
	case err != nil:
		set(PropObjectForm, adapter.Fail, MethodObjectForm, fmt.Sprintf("%s: %s does not validate: %v", in.carrier, k, err))
	default:
		set(PropObjectForm, adapter.Pass, MethodObjectForm, fmt.Sprintf("%s: %s, valid; unsigned", in.carrier, k))
	}

	// Each native_check report's detail is {"check", "object"}; the
	// explanation is kept out of the report's JSON (adapter.NativeCheckReport)
	// and shown by the CLI's -explain.
	native := func(check, object string, st adapter.Status, method, explanation string) {
		out = append(out, adapter.NativeCheckReport(check, object, st, method, explanation, nil))
	}
	const possession = "no issuer signature exists to check; the octets prove possession only"
	switch k {
	case kindCheckoutSession:
		native(CheckCheckoutSessionSignature, ObjCheckoutSession, adapter.NotPresent, MethodNativeCheckout, "acp-v0 §2.1: "+possession)
	case kindCheckoutSessionWithOrder:
		native(CheckCheckoutSessionSignature, ObjCheckoutSession, adapter.NotPresent, MethodNativeCheckout, "acp-v0 §2.1: "+possession)
		native(CheckOrderSignature, objOrderPath1, adapter.NotPresent, MethodNativeOrder, "acp-v0 §2.2, the order member of the completed session: "+possession)
	case kindDelegatePayment:
		native(CheckDelegatePaymentSignature, ObjDelegatePayment, adapter.NotPresent, MethodNativeToken, "acp-v0 §2.3: "+possession+"; the token's Allowance is not in these octets")
	case kindOrderEvent:
		const why = "the HMAC key is a secret shared by the merchant and the receiving platform: a verifier that is neither party cannot run it, and a PASS by either holder would not attribute the order to the merchant alone (acp-v0 §3.3)"
		if in.hasSignature {
			form := "matches"
			if !merchantSigRE.MatchString(in.signature) {
				form = "does not match"
			}
			native(CheckOrderEventHMAC, objOrderPath2, adapter.NotChecked, MethodNativeOrderWebhook,
				fmt.Sprintf("acp-v0 §2.2, postOrderEvents: Merchant-Signature %q is present and %s OAS-WH's t=<unix_seconds>,v1=<64_hex>; not run: %s", in.signature, form, why))
		} else {
			native(CheckOrderEventHMAC, objOrderPath2, adapter.NotPresent, MethodNativeOrderWebhookAbsent,
				"acp-v0 §2.2, postOrderEvents: no Merchant-Signature header was received with the body (supply the HTTP message to report it); it would not be run if it were: "+why)
		}
	}
	return out
}

var merchantSigRE = regexp.MustCompile(`^t=\d+,v1=[a-fA-F0-9]{64}$`)

var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func checkDigest(octets []byte, policy adapter.TrustPolicy) (adapter.Status, string) {
	sum := sha256.Sum256(octets)
	got := hex.EncodeToString(sum[:])
	want := policy.Pin(PinCarriedDigest)
	switch {
	case want == "":
		return adapter.NotPresent, fmt.Sprintf("no carried digest was supplied (trust policy pin %s); the %d octets' SHA-256 is %s", PinCarriedDigest, len(octets), got)
	case !digestRE.MatchString(want):
		return adapter.Inconclusive, fmt.Sprintf("pin %s %q is not 64 lowercase hex digits", PinCarriedDigest, want)
	case got == want:
		return adapter.Pass, fmt.Sprintf("%d octets, SHA-256 %s", len(octets), got)
	default:
		return adapter.Fail, fmt.Sprintf("the octets hash to %s, the carried digest is %s", got, want)
	}
}

// locate finds the ACP object's octets in raw.
func locate(raw []byte) (input, error) {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) == 0 {
		return input{}, errors.New("empty input")
	}
	if trimmed[0] != '{' {
		return parseHTTP(raw)
	}
	v, err := ijson.Parse(raw)
	if err != nil {
		// Reported by object_form; the digest is still over raw.
		return input{octets: raw, carrier: "JSON body"}, nil
	}
	if root, ok := ijson.AsObj(v); ok && root.Has("jsonrpc") {
		if root.Has("error") {
			return input{}, errors.New("a JSON-RPC error response carries no ACP object")
		}
		result, err := memberOctets(raw, "result")
		if err != nil {
			return input{}, fmt.Errorf("JSON-RPC response: %w", err)
		}
		return input{octets: result, carrier: "MCP JSON-RPC result"}, nil
	}
	return input{octets: raw, carrier: "JSON body"}, nil
}

// memberOctets returns the octets of the top-level member name's value
// exactly as they appear in raw, which ijson.Parse has already accepted.
func memberOctets(raw []byte, name string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil { // '{'
		return nil, err
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		// The value starts after optional whitespace and the colon.
		start := int(dec.InputOffset())
		for start < len(raw) && (raw[start] == ' ' || raw[start] == '\t' || raw[start] == '\r' || raw[start] == '\n' || raw[start] == ':') {
			start++
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
		if kt == name {
			return raw[start:dec.InputOffset()], nil
		}
	}
	return nil, fmt.Errorf("no %s member", name)
}

// parseHTTP reads an HTTP message: an optional start line, header fields
// (CRLF or LF), an empty line, and the body, which is returned unchanged.
func parseHTTP(raw []byte) (input, error) {
	in := input{carrier: "HTTP message body"}
	rest := raw
	seen := map[string]string{}
	first := true
	for {
		line, after, found := bytes.Cut(rest, []byte("\n"))
		if !found {
			return in, errors.New("HTTP message: no empty line ends the header fields")
		}
		rest = after
		text := strings.TrimSuffix(string(line), "\r")
		if text == "" {
			break
		}
		if first && (strings.HasPrefix(text, "HTTP/") || strings.Contains(text, " HTTP/")) {
			first = false
			continue
		}
		first = false
		name, value, ok := strings.Cut(text, ":")
		if !ok {
			return in, fmt.Errorf("HTTP message: line %q is not a header field", text)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if _, dup := seen[name]; dup {
			return in, fmt.Errorf("HTTP message: header %s appears twice", name)
		}
		seen[name] = strings.Trim(value, " \t")
	}
	if te, ok := seen["transfer-encoding"]; ok {
		return in, fmt.Errorf("HTTP message: Transfer-Encoding %q; supply the body after transfer decoding", te)
	}
	if ce, ok := seen["content-encoding"]; ok && !strings.EqualFold(ce, "identity") {
		return in, fmt.Errorf("HTTP message: Content-Encoding %q; the declared octets are the content after the coding is removed (acp-v0 §2.1 (a))", ce)
	}
	in.octets = rest
	in.signature, in.hasSignature = seen["merchant-signature"]
	return in, nil
}

// identify names the ACP object the octets are and validates them against
// its schema. It returns kindUnknown with the reason when no declared object
// matches, and the kind with a validation error when one matches by its
// members but does not validate.
func identify(octets []byte) (kind, error) {
	v, err := ijson.Parse(octets)
	if err != nil {
		return kindUnknown, fmt.Errorf("not one JSON value: %w", err)
	}
	o, ok := ijson.AsObj(v)
	if !ok {
		return kindUnknown, errors.New("not a JSON object")
	}
	var k kind
	switch {
	case o.Has("type") && o.Has("data"):
		k = kindOrderEvent
	case o.Has("checkout_session_id") || o.Has("permalink_url"):
		return kindUnknown, errors.New("an ACP Order on its own: ACP 2026-04-17 delivers an order only inside a completed CheckoutSessionWithOrder or as an order event's data (acp-v0 §2.2 (a)); supply those octets")
	case o.Has("order"):
		k = kindCheckoutSessionWithOrder
	case o.Has("status") || o.Has("line_items") || o.Has("totals") || o.Has("currency"):
		k = kindCheckoutSession
	case o.Has("created") || o.Has("metadata"):
		k = kindDelegatePayment
	default:
		return kindUnknown, errors.New("a JSON object that is none of CheckoutSession, CheckoutSessionWithOrder, DelegatePaymentResponse or an order event body")
	}
	return k, validate(k, v)
}
