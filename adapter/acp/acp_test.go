// SPDX-License-Identifier: Apache-2.0

package acp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixturePolicy(t *testing.T) adapter.TrustPolicy {
	t.Helper()
	p, err := adapter.LoadTrustPolicy(filepath.Join("testdata", "trust-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func digestPolicy(octets []byte) adapter.TrustPolicy {
	sum := sha256.Sum256(octets)
	return adapter.TrustPolicy{Pins: map[string]string{PinCarriedDigest: hex.EncodeToString(sum[:])}}
}

// The vendored schemas are ACP's files, byte for byte, with the digests that
// agent-action-capsule spec/adapters/acp-v0.md §1 records (lines 39-40, at
// 2ef8aeb4).
func TestVendoredSchemaDigests(t *testing.T) {
	for _, c := range []struct {
		data       []byte
		const_, ac string
	}{
		{schemaCheckout, SchemaCheckoutSHA256, "d0e4290617d66bf05d002b8ace388732be2b3eb9a92a1003db7a2daa1e0436f2"},
		{schemaDelegatePayment, SchemaDelegatePaymentSHA256, "307739ae400e7368eaa25c6024d30751348a4af8a516d071565fcd8aa328cf4d"},
	} {
		sum := sha256.Sum256(c.data)
		if got := hex.EncodeToString(sum[:]); got != c.const_ || got != c.ac {
			t.Errorf("vendored schema SHA-256 %s, want %s (acp-v0 §1)", got, c.ac)
		}
	}
	if _, err := schemas(); err != nil {
		t.Fatal(err)
	}
}

// The native_check methods are acp-v0 §2's (e) tables, verbatim (lines 125,
// 166, 167, 168, 204 at 2ef8aeb4).
func TestDeclaredMethodsVerbatim(t *testing.T) {
	for got, want := range map[string]string{
		MethodNativeCheckout:     "none: ACP 2026-04-17 defines no issuer signature over CheckoutSession",
		MethodNativeOrder:        "none: ACP 2026-04-17 defines no issuer signature over Order",
		MethodNativeOrderWebhook: "not run: HMAC-SHA256 under a shared secret (OAS-WH Merchant-Signature)",
		// acp-v0 §2.2 (e) at 2ef8aeb4, the row for a body without the header.
		MethodNativeOrderWebhookAbsent: "none: no Merchant-Signature header was received with the order event body",
		MethodNativeToken:              "none: ACP 2026-04-17 defines no issuer signature over DelegatePaymentResponse",
		MethodDigest:                   "SHA-256 over the octets as delivered, compared with the carried digest",
	} {
		if got != want {
			t.Errorf("method %q, declared %q", got, want)
		}
	}
	// The reported digest methods are the declared row plus the source of
	// the carried digest (ruling on em-oss question 2).
	for _, m := range []string{MethodDigestPinned, MethodDigestLeg} {
		if !strings.HasPrefix(m, MethodDigest+": ") {
			t.Errorf("digest method %q does not start with the declared row", m)
		}
	}
	decl := Adapter{}.NativeObject()
	for _, o := range decl.Objects {
		if o.NativelySigned {
			t.Errorf("%s declared natively signed; ACP 2026-04-17 signs none of its objects", o.Name)
		}
	}
	for _, p := range decl.Properties {
		if nativeChecks[p.Name] && !strings.HasPrefix(p.Supports, "nothing") {
			t.Errorf("native_check for %s supports %q; it supports nothing", p.Object, p.Supports)
		}
	}
}

var nativeChecks = map[string]bool{
	CheckCheckoutSessionSignature: true, CheckOrderSignature: true,
	CheckOrderEventHMAC: true, CheckDelegatePaymentSignature: true,
}

// want names a result by adapter.CheckName: the property, or for a
// native_check report the check name at the start of its detail.
type want struct {
	prop   string
	status adapter.Status
	method string
}

func TestFixtures(t *testing.T) {
	np, nc, pass := adapter.NotPresent, adapter.NotChecked, adapter.Pass
	cases := []struct {
		file   string
		policy bool
		want   []want
	}{
		{"checkout-session.json", true, []want{
			{PropDigest, pass, MethodDigestPinned}, {PropObjectForm, pass, MethodObjectForm},
			{CheckCheckoutSessionSignature, np, MethodNativeCheckout}}},
		{"checkout-session.json", false, []want{
			{PropDigest, np, MethodDigestPinned}, {PropObjectForm, pass, MethodObjectForm},
			{CheckCheckoutSessionSignature, np, MethodNativeCheckout}}},
		{"checkout-session-with-order.json", false, []want{
			{PropDigest, np, MethodDigestPinned}, {PropObjectForm, pass, MethodObjectForm},
			{CheckCheckoutSessionSignature, np, MethodNativeCheckout}, {CheckOrderSignature, np, MethodNativeOrder}}},
		{"delegate-payment-response.json", false, []want{
			{PropDigest, np, MethodDigestPinned}, {PropObjectForm, pass, MethodObjectForm},
			{CheckDelegatePaymentSignature, np, MethodNativeToken}}},
		{"order-event.json", false, []want{
			{PropDigest, np, MethodDigestPinned}, {PropObjectForm, pass, MethodObjectForm},
			{CheckOrderEventHMAC, np, MethodNativeOrderWebhookAbsent}}},
		{"order-event.http", false, []want{
			{PropDigest, np, MethodDigestPinned}, {PropObjectForm, pass, MethodObjectForm},
			{CheckOrderEventHMAC, nc, MethodNativeOrderWebhook}}},
		{"leg-checkout-session.json", false, []want{
			{PropDigest, pass, MethodDigestLeg}, {PropObjectForm, pass, MethodObjectForm},
			{CheckCheckoutSessionSignature, np, MethodNativeCheckout}}},
		{"mcp-checkout-session.json", false, []want{
			{PropDigest, np, MethodDigestPinned}, {PropObjectForm, pass, MethodObjectForm},
			{CheckCheckoutSessionSignature, np, MethodNativeCheckout}}},
	}
	for _, c := range cases {
		var policy adapter.TrustPolicy
		if c.policy {
			policy = fixturePolicy(t)
		}
		got := Adapter{}.Check(readFixture(t, c.file), policy)
		if len(got) != len(c.want) {
			t.Fatalf("%s: %d results, want %d: %+v", c.file, len(got), len(c.want), got)
		}
		for i, w := range c.want {
			g := got[i]
			if nativeChecks[w.prop] != (g.Property == PropNativeCheck) {
				t.Errorf("%s result %d: property %s for check %s", c.file, i, g.Property, w.prop)
			}
			if adapter.CheckName(g) != w.prop || g.Status != w.status || g.Method != w.method {
				t.Errorf("%s result %d: {%s %s %q}, want {%s %s %q} (%s)", c.file, i, adapter.CheckName(g), g.Status, g.Method, w.prop, w.status, w.method, g.Detail)
			}
		}
	}
}

// The order event's body octets are the same whether received alone or in
// the HTTP message, so both carry the same digest.
func TestHTTPBodyIsTheOctets(t *testing.T) {
	body := readFixture(t, "order-event.json")
	msg := readFixture(t, "order-event.http")
	if !bytes.HasSuffix(msg, body) {
		t.Fatal("order-event.http does not end with order-event.json's octets")
	}
	got := Adapter{}.Check(msg, digestPolicy(body))
	if got[0].Status != adapter.Pass {
		t.Fatalf("digest of the HTTP body: %+v", got[0])
	}
	if strings.Contains(got[2].Detail, "\r") {
		t.Fatal("header value kept a CR")
	}
}

// Over MCP the octets are those of result as they appear in the response,
// whatever whitespace surrounds them.
func TestMCPResultOctets(t *testing.T) {
	inner := readFixture(t, "delegate-payment-response.json")
	inner = bytes.TrimRight(inner, "\n")
	for _, tmpl := range []string{
		`{"jsonrpc":"2.0","id":1,"result":%s}`,
		`{"jsonrpc":"2.0","result" :  %s , "id":1}`,
		"{\n  \"result\":\n\t%s,\n  \"jsonrpc\": \"2.0\", \"id\": \"x\"\n}",
	} {
		raw := []byte(strings.Replace(tmpl, "%s", string(inner), 1))
		got := Adapter{}.Check(raw, digestPolicy(inner))
		if got[0].Status != adapter.Pass || got[1].Status != adapter.Pass {
			t.Errorf("%q: %+v", tmpl, got)
		}
	}
	got := Adapter{}.Check([]byte(`{"jsonrpc":"2.0","id":7,"error":{"code":-32000,"message":"Payment declined"}}`), adapter.TrustPolicy{})
	if len(got) != 2 || got[1].Status != adapter.Fail {
		t.Errorf("JSON-RPC error: %+v", got)
	}
}

func TestDigest(t *testing.T) {
	raw := readFixture(t, "checkout-session.json")
	policy := fixturePolicy(t)
	if got := (Adapter{}).Check(raw, policy)[0]; got.Status != adapter.Pass {
		t.Fatalf("fixture digest: %+v", got)
	}
	// Any change to the octets, even one that leaves the JSON value the
	// same, is a different object: the digest is over the octets as
	// delivered, never a re-serialization.
	for name, mutated := range map[string][]byte{
		"one byte":           bytes.Replace(raw, []byte(`"usd"`), []byte(`"eur"`), 1),
		"whitespace only":    bytes.Replace(raw, []byte("\n  "), []byte("\n   "), 1),
		"final newline gone": bytes.TrimRight(raw, "\n"),
	} {
		got := Adapter{}.Check(mutated, policy)
		if got[0].Status != adapter.Fail {
			t.Errorf("%s: digest %+v", name, got[0])
		}
	}
	bad := adapter.TrustPolicy{Pins: map[string]string{PinCarriedDigest: "ABC"}}
	if got := (Adapter{}).Check(raw, bad)[0]; got.Status != adapter.Inconclusive {
		t.Errorf("malformed pin: %+v", got)
	}
}

func TestForms(t *testing.T) {
	session := string(readFixture(t, "checkout-session.json"))
	event := readFixture(t, "order-event.json")
	cases := []struct {
		name   string
		raw    string
		form   adapter.Status
		native []adapter.Status
	}{
		{"empty", "", adapter.Fail, nil},
		{"not JSON", "{", adapter.Fail, nil},
		{"array", "[]", adapter.Fail, nil},
		{"unknown object", `{"hello":"world"}`, adapter.Fail, nil},
		{"bare order", `{"id":"ord_1","checkout_session_id":"cs_1","permalink_url":"https://example.com/o/1"}`, adapter.Fail, nil},
		{"duplicate member", `{"id":"vt_1","id":"vt_2","created":"2026-01-01T00:00:00Z","metadata":{}}`, adapter.Fail, nil},
		{"trailing data", `{"id":"vt_1","created":"2026-01-01T00:00:00Z","metadata":{}} {}`, adapter.Fail, nil},
		{"token missing created", `{"id":"vt_1","metadata":{}}`, adapter.Fail, []adapter.Status{adapter.NotPresent}},
		{"token extra member", `{"id":"vt_1","created":"2026-01-01T00:00:00Z","metadata":{},"allowance":{}}`, adapter.Fail, []adapter.Status{adapter.NotPresent}},
		{"session missing totals", strings.Replace(session, `"totals"`, `"totalz"`, 1), adapter.Fail, []adapter.Status{adapter.NotPresent}},
		// A member claiming a signature is neither recognized nor checked:
		// the schema rejects it, and native_check stays NOT_PRESENT.
		{"session with a signature member", strings.Replace(session, "{", `{"signature":"eyJhbGciOiJFUzI1NiJ9..c2ln",`, 1), adapter.Fail, []adapter.Status{adapter.NotPresent}},
		{"event, header malformed", "POST / HTTP/1.1\nMerchant-Signature: v1=zz\n\n" + string(event), adapter.Pass, []adapter.Status{adapter.NotChecked}},
		{"event, status line, LF", "HTTP/1.1 200 OK\nContent-Type: application/json\n\n" + string(event), adapter.Pass, []adapter.Status{adapter.NotPresent}},
		{"event, unrecognized type accepted", strings.Replace(string(event), `"order_create"`, `"order_archived"`, 1), adapter.Pass, []adapter.Status{adapter.NotPresent}},
		{"event without data.status", strings.Replace(string(event), `"status": "created"`, `"state": "created"`, 1), adapter.Fail, []adapter.Status{adapter.NotPresent}},
		{"gzip body", "POST / HTTP/1.1\nContent-Encoding: gzip\n\n" + string(event), adapter.Fail, nil},
		{"chunked body", "POST / HTTP/1.1\nTransfer-Encoding: chunked\n\n" + string(event), adapter.Fail, nil},
		{"duplicate header", "POST / HTTP/1.1\nMerchant-Signature: a\nmerchant-signature: b\n\n" + string(event), adapter.Fail, nil},
		{"no empty line", "POST / HTTP/1.1\nContent-Type: application/json", adapter.Fail, nil},
	}
	for _, c := range cases {
		got := Adapter{}.Check([]byte(c.raw), adapter.TrustPolicy{})
		form, ok := adapter.Find(got, PropObjectForm)
		if !ok || form.Status != c.form {
			t.Errorf("%s: object_form %+v, want %s", c.name, form, c.form)
			continue
		}
		var native []adapter.Status
		for _, r := range got {
			if r.Property == PropNativeCheck {
				native = append(native, r.Status)
			}
		}
		if len(native) != len(c.native) {
			t.Errorf("%s: native_check %v, want %v", c.name, native, c.native)
			continue
		}
		for i := range native {
			if native[i] != c.native[i] {
				t.Errorf("%s: native_check %v, want %v", c.name, native, c.native)
			}
		}
	}
}

// No input makes native_check PASS or FAIL: ACP defines no issuer
// signature, and the webhook HMAC is never run. A change to the adapter that
// claims a native result turns this test red.
func TestNativeCheckNeverDecides(t *testing.T) {
	var inputs [][]byte
	for _, f := range []string{"checkout-session.json", "checkout-session-with-order.json", "delegate-payment-response.json", "order-event.json", "order-event.http", "mcp-checkout-session.json", "leg-checkout-session.json"} {
		raw := readFixture(t, f)
		inputs = append(inputs, raw)
		// Flip each byte in turn of the first 256, and drop the tail.
		for i := 0; i < len(raw) && i < 256; i++ {
			m := bytes.Clone(raw)
			m[i] ^= 0x01
			inputs = append(inputs, m)
		}
		inputs = append(inputs, raw[:len(raw)/2])
	}
	policies := []adapter.TrustPolicy{{}, fixturePolicy(t), {Keys: map[string][]string{"acp.merchant": {"k1"}}, Pins: map[string]string{"acp.merchant_secret": "s3cret"}}}
	for _, raw := range inputs {
		for _, p := range policies {
			for _, r := range (Adapter{}).Check(raw, p) {
				if r.Property != PropNativeCheck {
					continue
				}
				if r.Status != adapter.NotPresent && r.Status != adapter.NotChecked {
					t.Fatalf("native_check %s for %q...", r.Status, raw[:min(len(raw), 80)])
				}
				switch r.Method {
				case MethodNativeCheckout, MethodNativeOrder, MethodNativeOrderWebhook, MethodNativeOrderWebhookAbsent, MethodNativeToken:
				default:
					t.Fatalf("native_check method %q is not a declared one", r.Method)
				}
				if !nativeChecks[adapter.CheckName(r)] {
					t.Fatalf("native_check name %q is not a declared check", adapter.CheckName(r))
				}
			}
		}
	}
}

// Ruling S1: a native_check NOT_PRESENT or NOT_CHECKED maps as profile §8.1
// rows 3-4 pending a profile row. It never counts against a requirement that
// does not rest on it, and a requirement that rests on it is not_evaluable,
// never met or not_met. Requirements rest on native checks by check name.
func TestS1Mapping(t *testing.T) {
	session := readFixture(t, "checkout-session.json")
	withPin := Adapter{}.Check(session, fixturePolicy(t))
	webhook := Adapter{}.Check(readFixture(t, "order-event.http"), adapter.TrustPolicy{})
	withOrder := Adapter{}.Check(readFixture(t, "checkout-session-with-order.json"), adapter.TrustPolicy{})
	token := Adapter{}.Check(readFixture(t, "delegate-payment-response.json"), adapter.TrustPolicy{})

	rests := func(id string, rel adapter.Relation, on ...string) adapter.Requirement {
		return adapter.Requirement{ID: id, Relation: rel, DependsOn: on}
	}
	possession := rests("the presenter holds the octets the Artifact cites", adapter.IsTheCheck, PropDigest)
	wellFormed := rests("the cited octets are an ACP checkout session", adapter.SupportedByTheCheck, PropDigest, PropObjectForm)
	const row3 = "3 (native NOT_PRESENT; mapped as profile §8.1 rows 3-4 pending a profile row)"
	const row4 = "4 (native NOT_CHECKED or INCONCLUSIVE; mapped as profile §8.1 rows 3-4 pending a profile row)"

	cases := []struct {
		name              string
		results           []adapter.PropertyResult
		req               adapter.Requirement
		row               string
		sufficiency, verd string
		basis             string
	}{
		// Not resting on a native check: the NOT_PRESENT is not counted.
		{"possession, digest pinned", withPin, possession, "7 (the requirement is the check, and it passed)", "SATISFIED", "met", PropDigest},
		{"well-formed, digest pinned", withPin, wellFormed, "7 (the contract evaluates the content)", "", "", PropDigest + "," + PropObjectForm},
		// Resting on one: INSUFFICIENT or UNKNOWN, not_evaluable.
		{"session issued by the merchant", withPin, rests("the merchant issued this checkout session", adapter.IsTheCheck, CheckCheckoutSessionSignature), row3, "INSUFFICIENT", "not_evaluable", CheckCheckoutSessionSignature},
		{"order issued by the merchant (path 1)", withOrder, rests("the merchant issued this order", adapter.IsTheCheck, CheckOrderSignature), row3, "INSUFFICIENT", "not_evaluable", CheckOrderSignature},
		{"token issued and bound to an Allowance", token, rests("the token issuer bound this token to the Allowance", adapter.IsTheCheck, CheckDelegatePaymentSignature), row3, "INSUFFICIENT", "not_evaluable", CheckDelegatePaymentSignature},
		{"order event from the merchant (path 2)", webhook, rests("the merchant sent this order event", adapter.IsTheCheck, CheckOrderEventHMAC), row4, "UNKNOWN", "not_evaluable", CheckOrderEventHMAC},
		{"paid for, resting on the session signature", withPin, rests("the order was placed and paid", adapter.SupportedByTheCheck, PropDigest, PropObjectForm, CheckCheckoutSessionSignature), row3, "INSUFFICIENT", "not_evaluable", CheckCheckoutSessionSignature},
		// A check the input does not report counts as NOT_PRESENT.
		{"session signature asked of a token", token, rests("the merchant issued this checkout session", adapter.IsTheCheck, CheckCheckoutSessionSignature), row3, "INSUFFICIENT", "not_evaluable", CheckCheckoutSessionSignature},
		// Without a carried digest, the possession requirement lacks its
		// own input, not the native check's.
		{"possession, no digest", webhook, possession, row3, "INSUFFICIENT", "not_evaluable", PropDigest},
	}
	for _, c := range cases {
		ev := adapter.Evaluate(c.results, c.req)
		if ev.Row != c.row || ev.Sufficiency != c.sufficiency || ev.Verdict != c.verd || strings.Join(ev.Basis, ",") != c.basis {
			t.Errorf("%s: %+v, want row %q, %s/%s, basis %s", c.name, ev, c.row, c.sufficiency, c.verd, c.basis)
		}
		for _, r := range c.req.DependsOn {
			if nativeChecks[r] && (ev.Verdict == "met" || ev.Verdict == "not_met") {
				t.Errorf("%s: a requirement resting on %s got %s", c.name, r, ev.Verdict)
			}
		}
	}
}

func legWith(t *testing.T, entries ...map[string]any) []byte {
	t.Helper()
	leg := map[string]any{"settlement": map[string]any{"wrapped": entries}}
	b, err := json.Marshal(leg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func entry(typ string, octets []byte) map[string]any {
	sum := sha256.Sum256(octets)
	return map[string]any{"type": typ, "digest_alg": "SHA-256", "digest": hex.EncodeToString(sum[:]), "content": base64.RawURLEncoding.EncodeToString(octets)}
}

// In a settlement-records leg the carried digest is the wrapped entry's
// digest; the relying party's pin applies only to a standalone adapt.
func TestLeg(t *testing.T) {
	session := readFixture(t, "checkout-session.json")
	token := readFixture(t, "delegate-payment-response.json")
	event := readFixture(t, "order-event.json")
	otherPin := adapter.TrustPolicy{Pins: map[string]string{PinCarriedDigest: strings.Repeat("0", 64)}}

	mismatch := entry(WrappedCheckoutSession, session)
	mismatch["digest"] = strings.Repeat("a", 64)
	digestOnly := entry(WrappedDelegatePayment, token)
	delete(digestOnly, "content")
	sha1 := entry(WrappedDelegatePayment, token)
	sha1["digest_alg"] = "SHA-1"

	cases := []struct {
		name         string
		raw          []byte
		digest, form adapter.Status
		native       []string
	}{
		{"fixture, pin for other octets ignored", readFixture(t, "leg-checkout-session.json"), adapter.Pass, adapter.Pass, []string{CheckCheckoutSessionSignature}},
		{"token", legWith(t, entry(WrappedDelegatePayment, token)), adapter.Pass, adapter.Pass, []string{CheckDelegatePaymentSignature}},
		{"order event: no header in a leg", legWith(t, entry(WrappedOrderEvent, event)), adapter.Pass, adapter.Pass, []string{CheckOrderEventHMAC}},
		{"x402 entries only", legWith(t, map[string]any{"type": "x402.payment-payload", "digest_alg": "SHA-256", "digest": strings.Repeat("0", 64)}), adapter.NotPresent, adapter.NotPresent, nil},
		{"digest mismatch, content still read", legWith(t, mismatch), adapter.Fail, adapter.Pass, []string{CheckCheckoutSessionSignature}},
		{"digest only", legWith(t, digestOnly), adapter.NotPresent, adapter.NotPresent, nil},
		{"digest_alg not SHA-256", legWith(t, sha1), adapter.NotChecked, adapter.NotPresent, nil},
		{"two ACP entries", legWith(t, entry(WrappedDelegatePayment, token), entry(WrappedCheckoutSession, session)), adapter.Fail, adapter.NotPresent, nil},
		{"type says token, content is a session", legWith(t, entry(WrappedDelegatePayment, session)), adapter.Pass, adapter.Fail, []string{CheckCheckoutSessionSignature}},
		{"no wrapped array", []byte(`{"settlement":{}}`), adapter.Fail, adapter.NotPresent, nil},
	}
	for _, c := range cases {
		got := Adapter{}.Check(c.raw, otherPin)
		var native []string
		for _, r := range got {
			if r.Property == PropNativeCheck {
				native = append(native, adapter.CheckName(r))
				if r.Status != adapter.NotPresent {
					t.Errorf("%s: %+v", c.name, r)
				}
			}
		}
		if got[0].Property != PropDigest || got[0].Status != c.digest || got[0].Method != MethodDigestLeg ||
			got[1].Property != PropObjectForm || got[1].Status != c.form ||
			strings.Join(native, ",") != strings.Join(c.native, ",") {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// Each native_check report's detail is the object {"check", "object"}; the
// explanation is not part of the report's JSON.
func TestNativeCheckDetailShape(t *testing.T) {
	objects := map[string]string{
		CheckCheckoutSessionSignature: ObjCheckoutSession,
		CheckOrderSignature:           objOrderPath1,
		CheckOrderEventHMAC:           objOrderPath2,
		CheckDelegatePaymentSignature: ObjDelegatePayment,
	}
	seen := map[string]bool{}
	for _, f := range []string{"checkout-session-with-order.json", "delegate-payment-response.json", "order-event.http"} {
		for _, r := range (Adapter{}).Check(readFixture(t, f), adapter.TrustPolicy{}) {
			if r.Property != PropNativeCheck {
				continue
			}
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Detail map[string]string `json:"detail"`
			}
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("%s: detail is not an object: %s", f, b)
			}
			check := got.Detail["check"]
			if len(got.Detail) != 2 || objects[check] == "" || got.Detail["object"] != objects[check] {
				t.Errorf("%s: detail %v", f, got.Detail)
			}
			if r.Detail == "" || bytes.Contains(b, []byte(r.Detail)) {
				t.Errorf("%s: the explanation must be kept, and kept out of the JSON: %s", f, b)
			}
			seen[check] = true
		}
	}
	if len(seen) != len(objects) {
		t.Errorf("checks seen %v", seen)
	}
}

// A CheckoutSessionWithOrder validates whole against JS-CHK, whose
// CheckoutSessionBase lists order; the adapter never uses OAS-CHK, so the
// A-4 defect (acp-v0 §6 at 2ef8aeb4) does not arise and no note is made. A
// session member or an order member that breaks JS-CHK still fails. A
// webhook order validates against OAS-WH EventDataOrder.
func TestOrderSchemas(t *testing.T) {
	completed := string(readFixture(t, "checkout-session-with-order.json"))
	event := string(readFixture(t, "order-event.json"))
	cases := []struct {
		name, raw string
		form      adapter.Status
		detail    string
	}{
		{"completed session", completed, adapter.Pass, "$defs/CheckoutSessionWithOrder), valid"},
		{"completed session, order without permalink_url", strings.Replace(completed, `"permalink_url"`, `"permalink_uri"`, 1), adapter.Fail, "at /order"},
		{"completed session, unknown session member", strings.Replace(completed, `"currency"`, `"currencyx"`, 1), adapter.Fail, "does not validate"},
		{"event", event, adapter.Pass, "EventDataOrder"},
		{"event, data.client_reference_id (JS-CHK only)", strings.Replace(event, `"type": "order",`, `"type": "order", "client_reference_id": "x",`, 1), adapter.Fail, "does not validate"},
		{"event, data.type missing", strings.Replace(event, `"type": "order",`, ``, 1), adapter.Fail, "does not validate"},
	}
	for _, c := range cases {
		form, _ := adapter.Find(Adapter{}.Check([]byte(c.raw), adapter.TrustPolicy{}), PropObjectForm)
		if form.Status != c.form || !strings.Contains(form.Detail, c.detail) {
			t.Errorf("%s: %+v", c.name, form)
		}
		if strings.Contains(form.Detail, "A-4") || strings.Contains(form.Detail, "defect") {
			t.Errorf("%s: no A-4 note is made for JS-CHK: %s", c.name, form.Detail)
		}
	}
}

// The body is read as I-JSON, as UCP's checkout is: a lone surrogate escape
// or invalid UTF-8 fails object_form instead of reading as U+FFFD.
func TestObjectFormIsIJSON(t *testing.T) {
	raw := readFixture(t, "checkout-session.json")
	for bad, why := range map[string]string{
		`"usd\udc00"`: "lone low surrogate",
		"\"usd\xff\"": "not valid UTF-8",
	} {
		mutated := bytes.Replace(raw, []byte(`"usd"`), []byte(bad), 1)
		got, ok := adapter.Find(Adapter{}.Check(mutated, fixturePolicy(t)), PropObjectForm)
		if !ok || got.Status != adapter.Fail || !strings.Contains(got.Detail, why) {
			t.Errorf("%s: %+v", why, got)
		}
	}
}
