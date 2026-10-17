// SPDX-License-Identifier: Apache-2.0

// Package ucp is the evidencebook adapter for the Universal Commerce Protocol
// (UCP) v2026-08-25, as declared in agent-action-capsule
// spec/adapters/ucp-v0.md.
//
// UCP has one object the business (merchant) signs as an object: the checkout
// carrying ap2.merchant_authorization, which exists only when the optional
// AP2 Mandates Extension was negotiated. Its native check is a detached JWS
// (RFC 7515 Appendix F) over the RFC 8785 form of the checkout without ap2,
// under the key the relying party retained from the business's
// /.well-known/ucp keys[]. The order object is not signed as an object; the
// Order Event Webhook that carries it is signed as an HTTP message (RFC 9421
// with an RFC 9530 Content-Digest), which a holder can check again only when
// it kept the whole request. A checkout without AP2, and an order body held
// alone, prove possession of their octets only.
//
// The adapter makes no network calls. Keys come from the trust policy.
package ucp

import (
	"bytes"

	"github.com/action-state-group/evidencebook/adapter"
)

// Adapter is the UCP adapter.
type Adapter struct{}

var _ adapter.Adapter = Adapter{}

// Name is the command-line name.
func (Adapter) Name() string { return "ucp" }

// Check detects the input's form and reports its checks in declaration
// order. Every native check is a native_check report with the check's name
// in detail (adapter.NativeCheckReport); ucp.business_trust keeps its own
// property. The forms:
//
//   - JSON with a checkout_id member: an order body held without its request
//     (§4(d) body only; §5);
//   - other JSON (or input that is neither JSON nor a request): a checkout
//     (§3, or §5 when AP2 was not negotiated);
//   - an HTTP/1.x request message: an Order Event Webhook held whole (§4).
func (Adapter) Check(raw []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if looksLikeRequest(trimmed) {
		return asReports(CheckOrder, checkWebhook(trimmed, policy))
	}
	v, err := parseIJSON(raw)
	if err == nil {
		o, isObj := v.(map[string]any)
		if !isObj {
			return asReports(CheckCheckout, checkCheckout(nil, errNotObject, policy))
		}
		if _, isOrder := o["checkout_id"]; isOrder {
			return asReports(CheckOrder, checkOrderBody(o, policy))
		}
	}
	return asReports(CheckCheckout, checkCheckout(v, err, policy))
}

var errNotObject = errorString("the JSON value is not an object")

type errorString string

func (e errorString) Error() string { return string(e) }

// NativeObject returns the FR-19 declaration (ucp-v0 §3-§5).
func (Adapter) NativeObject() adapter.NativeObject {
	return adapter.NativeObject{
		Protocol: "Universal Commerce Protocol " + UCPVersion + " (commit " + UCPCommit + "), AP2 Mandates Extension; declaration agent-action-capsule spec/adapters/ucp-v0.md",
		Objects: []adapter.Object{
			{
				Name:             "ucp.checkout (with ap2.merchant_authorization)",
				Format:           "UCP Checkout JSON (payment_ap2_mandate.json#/$defs/dev.ucp.shopping.checkout); ap2.merchant_authorization = <base64url-header>..<base64url-signature>, protected header {alg, kid}",
				Octets:           "the RFC 8785 form of the complete checkout as received, ap2 included (the input is read as I-JSON; any serialization of it gives the same octets)",
				Issuer:           "the business (merchant); the header names only alg and kid, so the signer is identified by context",
				IssuerResolution: "kid is looked up in a copy of the business profile's keys[] (served at <business-origin>/.well-known/ucp) that the relying party retained and supplies in its trust policy (" + PinKeys + ", " + PinKeysOrigin + ", " + PinKeysSource + ", " + PinKeysRetainedAt + "); never fetched. Whether that origin is the merchant meant is the policy's decision (" + KeyBusinessOrigin + ", " + KeyBusinessOriginRejected + ")",
				Method:           methodMANative,
				NativelySigned:   true,
			},
			{
				Name:             "ucp.checkout (AP2 not negotiated)",
				Format:           "UCP Checkout JSON without ap2",
				Octets:           "the octets as received",
				Issuer:           "the business, as the wrapping party claims",
				IssuerResolution: "none in the object; transport provenance (TLS, an RFC 9421 response signature checked on arrival) is the wrapping party's claim",
				Method:           methodNoSignature,
				NativelySigned:   false,
			},
			{
				Name:             "ucp.order, in an Order Event Webhook request held whole",
				Format:           "an HTTP/1.1 request as received: request line, field lines (Host, Content-Type, UCP-Agent, Content-Digest, Signature-Input, Signature), empty line, the order JSON body",
				Octets:           "the raw request; the body octets unchanged",
				Issuer:           "the business named by UCP-Agent: profile=\"https://<origin>/.well-known/ucp\"",
				IssuerResolution: "keyid is looked up in the retained keys[] of that origin (trust policy " + PinKeys + "); a key set retained for another origin is not used. UCP's authorization step (the order was created with this business) needs the creating checkout and is not performed",
				Method:           methodWebhookNative,
				NativelySigned:   true,
			},
			{
				Name:             "ucp.order, body only",
				Format:           "UCP Order JSON (shopping/order.json): the webhook body or a get_order response, without the request",
				Octets:           "the raw body octets as received",
				Issuer:           "the business, as the wrapping party claims",
				IssuerResolution: "none in the object",
				Method:           methodNoSignature,
				NativelySigned:   false,
			},
		},
		Predicate: "AP2 checkout, native_check PASS: the holder possesses a UCP checkout object, and every member of it except ap2 was signed, in that exact JSON content, by the key with identifier kid in the business key set the verifier used; that key set was the one served at <business-origin>/.well-known/ucp, as of the time it was retained. When status is completed and order is present, this covers the business's signed statement of the order's id and permalink_url for that checkout, and nothing else about the order. " +
			"Webhook held whole, native_check PASS: the holder possesses an HTTP request whose body is this order snapshot, signed with the covered components listed in its Signature-Input by the key keyid in the key set used for the business profile named in its UCP-Agent. " +
			"Checkout without AP2, or order body alone: the holder possesses these exact octets, and the party that wrapped them says it received them from the named business origin over the named UCP transport.",
		NotSupported: []string{
			"that the business origin is the merchant the relying party has in mind (ucp.business_trust, the policy's decision)",
			"that the key was listed in keys[] when it signed: the JWS carries no signing time, and keys leave keys[] on rotation and compromise",
			"that the buyer or user authorized the purchase (that is ap2.checkout_mandate, its own wrapped type)",
			"that payment settled, or that the order was fulfilled, shipped or not cancelled later",
			"that a checkout is the last state of its session: each response signs one state",
			"for a webhook: that the order state is current, that events arrived in the order sent, that an earlier snapshot was superseded, or that the business is the one that took the order",
		},
		Properties: []adapter.Property{
			{Name: PropCheckoutSchema, Object: "ucp.checkout", Method: "ucp-schema validate --response --op <operation>, UCP " + UCPVersion, Supports: "the object is a UCP checkout response at the pinned version (operation from the " + PinOperation + " pin, else inferred from status)"},
			{Name: PropMAPresent, Object: "ucp.checkout", Method: methodMAPresent, Supports: "the checkout carries a merchant authorization (NOT_PRESENT: merchant_authorization_missing, or AP2 not negotiated)"},
			{Name: PropMAForm, Object: "ucp.checkout", Method: methodMAForm, Supports: "the authorization is a detached JWS with alg and kid"},
			{Name: PropMAAlg, Object: "ucp.checkout", Method: methodMAAlg, Supports: "alg is ES256, ES384 or ES512 (all three verified here)"},
			{Name: PropMAKey, Object: "ucp.checkout", Method: methodKeyLive + `, or "retained keys[] (<key-set source>)", with " as of <time>" when the retention time is known`, Supports: "kid names a key usable for verification in the retained key set (NOT_CHECKED without one)"},
			{Name: PropMASignature, Object: "ucp.checkout", Method: methodMASignature(""), Supports: "the business key signed JCS(checkout − ap2) under that header (FAIL: merchant_authorization_invalid)"},
			{Name: PropOrderSchema, Object: "ucp.order", Method: methodSchema, Supports: "the body is a UCP order at the pinned version"},
			{Name: PropWebhookComponents, Object: "ucp.order webhook", Method: methodComponents, Supports: "the method, target and covered headers are held (NOT_PRESENT for a body alone)"},
			{Name: PropWebhookCoverage, Object: "ucp.order webhook", Method: methodCoverage, Supports: "the signature covers every component UCP requires (FAIL: coverage_insufficient)"},
			{Name: PropWebhookDigest, Object: "ucp.order webhook", Method: methodDigest, Supports: "Content-Digest is the body's sha-256 (FAIL: digest_mismatch)"},
			{Name: PropWebhookKey, Object: "ucp.order webhook", Method: "as " + PropMAKey, Supports: "keyid names a usable key in the key set retained for the UCP-Agent origin"},
			{Name: PropWebhookSignature, Object: "ucp.order webhook", Method: methodWebhookSignature(""), Supports: "the signature verifies over the rebuilt RFC 9421 signature base (FAIL: signature_invalid)"},
			{Name: CheckCheckout, Object: "ucp.checkout", Method: methodNoSignature, Supports: "reported only for a checkout without AP2, which has no native signature (§5): NOT_PRESENT. A checkout with AP2 has no single report; its native check passes when each ucp.checkout.schema and ucp.merchant_authorization.* report is PASS"},
			{Name: CheckOrder, Object: "ucp.order", Method: methodNoSignature, Supports: "reported only for an order body held without its request (§4(d), §5): NOT_PRESENT. A webhook held whole has no single report; its native check passes when each ucp.order.schema and ucp.webhook.* report is PASS"},
			{Name: PropBusinessTrust, Object: "each object", Method: "relying party policy <" + adapter.PinPolicyID + ">", Supports: "the relying party's policy accepts the business origin (PASS), rejects it (FAIL), does not list it or cannot decide (INCONCLUSIVE), or was not supplied (NOT_CHECKED); reported beside the native checks, never inside them"},
		},
	}
}
