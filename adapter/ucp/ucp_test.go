// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

func fixturePolicy(t testing.TB) adapter.TrustPolicy {
	t.Helper()
	var p adapter.TrustPolicy
	if err := json.Unmarshal([]byte(testPolicyJSON()), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func statuses(rs []adapter.PropertyResult) map[string]adapter.Status {
	m := map[string]adapter.Status{}
	for _, r := range rs {
		m[adapter.CheckName(r)] = r.Status
	}
	m[objCheck] = objectNative(rs)
	return m
}

func names(rs []adapter.PropertyResult) []string {
	var out []string
	for _, r := range rs {
		out = append(out, adapter.CheckName(r))
	}
	return out
}

// objCheck is the statuses() key of the object's native check as a whole:
// PASS when every native_check report is PASS, FAIL when any is FAIL, else
// the first other status (ucp-v0 §3(e)).
const objCheck = "<object>"

func objectNative(rs []adapter.PropertyResult) adapter.Status {
	st := adapter.Pass
	for _, r := range rs {
		if r.Property != adapter.NativeCheck {
			continue
		}
		if r.Status == adapter.Fail {
			return adapter.Fail
		}
		if st == adapter.Pass && r.Status != adapter.Pass {
			st = r.Status
		}
	}
	return st
}

// failing lists the FAIL properties other than native_check, sorted.
func failing(rs []adapter.PropertyResult) string {
	var out []string
	for _, r := range rs {
		if n := adapter.CheckName(r); r.Status == adapter.Fail && n != PropBusinessTrust {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func check(raw []byte, p adapter.TrustPolicy) []adapter.PropertyResult {
	return Adapter{}.Check(raw, p)
}

var checkoutRows = []string{PropCheckoutSchema, PropMAPresent, PropMAForm, PropMAAlg, PropMAKey, PropMASignature, PropBusinessTrust}
var checkoutNoSigRows = []string{PropCheckoutSchema, PropMAPresent, PropMAForm, PropMAAlg, PropMAKey, PropMASignature, CheckCheckout, PropBusinessTrust}
var checkoutChecks = []string{PropCheckoutSchema, PropMAPresent, PropMAForm, PropMAAlg, PropMAKey, PropMASignature}
var webhookRows = []string{PropOrderSchema, PropWebhookComponents, PropWebhookCoverage, PropWebhookDigest, PropWebhookKey, PropWebhookSignature, PropBusinessTrust}
var orderBodyRows = []string{PropOrderSchema, PropWebhookComponents, PropWebhookCoverage, PropWebhookDigest, PropWebhookKey, PropWebhookSignature, CheckOrder, PropBusinessTrust}
var orderChecks = []string{PropOrderSchema, PropWebhookComponents, PropWebhookCoverage, PropWebhookDigest, PropWebhookKey, PropWebhookSignature}

// RFC 7515 Appendix A.3: the published ES256 example verifies under the
// published key, and the test key's d derives the published x and y.
func TestKnownAnswerRFC7515A3(t *testing.T) {
	k := testKey(t)
	x, _ := b64url.DecodeString(testX)
	y, _ := b64url.DecodeString(testY)
	if !slices.Equal(k.X.FillBytes(make([]byte, 32)), x) || !slices.Equal(k.Y.FillBytes(make([]byte, 32)), y) {
		t.Fatal("d does not derive the published x, y")
	}
	input := "eyJhbGciOiJFUzI1NiJ9.eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ"
	sig, err := b64url.DecodeString("DtEhU3ljbEg8L38VWAfUAqOyKAM6-Xx-F4GawxaepmXFCgfTjDxw5djxLa8ISlSApmWQxfKTUJqPP3-Kg6NU1Q")
	if err != nil {
		t.Fatal(err)
	}
	ks, _ := parseKeySet(testJWKSet())
	pub, err := ks[0].publicKey()
	if err != nil {
		t.Fatal(err)
	}
	if !verifyECDSA(pub.(*ecdsa.PublicKey), ap2Algs["ES256"].hash, []byte(input), sig) {
		t.Fatal("RFC 7515 A.3 example does not verify")
	}
	sig[0] ^= 1
	if verifyECDSA(pub.(*ecdsa.PublicKey), ap2Algs["ES256"].hash, []byte(input), sig) {
		t.Fatal("altered A.3 signature verifies")
	}
}

func TestSchemasPinned(t *testing.T) {
	files, err := verifySchemaSums()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 47 {
		t.Fatalf("%d vendored schema files, want 47", len(files))
	}
	if _, _, err := validate(false, map[string]any{}); err != nil {
		t.Fatal(err)
	}
}

// The fixtures under testdata/ give exactly the declared results.
func TestFixtures(t *testing.T) {
	p := fixturePolicy(t)
	cases := []struct {
		file   string
		rows   []string
		native adapter.Status
		fails  string
	}{
		{"checkout-positive.json", checkoutRows, adapter.Pass, ""},
		{"checkout-member-mutated.json", checkoutRows, adapter.Fail, PropMASignature},
		{"checkout-no-ap2.json", checkoutNoSigRows, adapter.NotPresent, ""},
		{"order-webhook.http", webhookRows, adapter.Pass, ""},
		{"order-webhook-body-mutated.http", webhookRows, adapter.Fail, PropWebhookDigest},
		{"order-webhook-example-coverage.http", webhookRows, adapter.Fail, PropWebhookCoverage},
		{"order-body.json", orderBodyRows, adapter.NotPresent, ""},
	}
	for _, c := range cases {
		raw, err := os.ReadFile(filepath.Join("testdata", c.file))
		if err != nil {
			t.Fatal(err)
		}
		rs := check(raw, p)
		if !slices.Equal(names(rs), c.rows) {
			t.Errorf("%s: rows %v", c.file, names(rs))
		}
		st := statuses(rs)
		if st[objCheck] != c.native || failing(rs) != c.fails {
			t.Errorf("%s: native_check %s, FAIL on %q; want %s, %q", c.file, st[objCheck], failing(rs), c.native, c.fails)
		}
		if st[PropBusinessTrust] != adapter.Pass {
			t.Errorf("%s: business_trust %s", c.file, st[PropBusinessTrust])
		}
	}
	// The no-AP2 checkout and the body alone are possession only (§5).
	for _, f := range []string{"checkout-no-ap2.json", "order-body.json"} {
		raw, _ := os.ReadFile(filepath.Join("testdata", f))
		nc, ok := adapter.FindCheck(check(raw, p), CheckCheckout)
		if !ok {
			nc, _ = adapter.FindCheck(check(raw, p), CheckOrder)
		}
		if nc.Method != methodNoSignature {
			t.Errorf("%s: native_check method %q", f, nc.Method)
		}
	}
}

// mutate parses the positive checkout, applies f, and re-serializes it.
// Serialization does not matter: the signature covers the RFC 8785 form.
func mutate(t *testing.T, f func(co map[string]any)) []byte {
	t.Helper()
	v, err := parseIJSON(positiveCheckout(t))
	if err != nil {
		t.Fatal(err)
	}
	co := v.(map[string]any)
	f(co)
	b, err := json.Marshal(co)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			v = v.(map[string]any)[k]
		case int:
			v = v.([]any)[k]
		}
	}
	return v
}

func setHeader(t *testing.T, co map[string]any, header string, resign bool) {
	t.Helper()
	ap2 := co["ap2"].(map[string]any)
	if resign {
		ap2["merchant_authorization"] = signCheckout(t, co, header)
		return
	}
	_, sig, _ := cutDetached(ap2["merchant_authorization"].(string))
	ap2["merchant_authorization"] = base64.RawURLEncoding.EncodeToString([]byte(header)) + ".." + sig
}

// One member of every signed member class changed after signing: exactly
// ucp.merchant_authorization.signature fails, and the schema still validates
// unless the mutation also breaks it.
func TestCheckoutMutations(t *testing.T) {
	p := fixturePolicy(t)
	num := func(s string) json.Number { return json.Number(s) }
	cases := []struct {
		name  string
		f     func(map[string]any)
		fails string
	}{
		{"top-level string id", func(c map[string]any) { c["id"] = "chk_abc124" }, PropMASignature},
		{"top-level enum status", func(c map[string]any) { c["status"] = "canceled" }, PropMASignature},
		{"currency", func(c map[string]any) { c["currency"] = "EUR" }, PropMASignature},
		{"ucp namespace member", func(c map[string]any) { at(c, "ucp").(map[string]any)["version"] = "2026-08-26" }, PropMASignature},
		{"ucp capability removed", func(c map[string]any) {
			delete(at(c, "ucp", "capabilities").(map[string]any), "dev.ucp.shopping.checkout")
		}, PropMASignature},
		{"nested integer quantity", func(c map[string]any) { at(c, "line_items", 0).(map[string]any)["quantity"] = num("3") }, PropMASignature},
		{"nested integer price", func(c map[string]any) { at(c, "line_items", 0, "item").(map[string]any)["price"] = num("2499") }, PropMASignature},
		{"totals amount", func(c map[string]any) { at(c, "totals", 1).(map[string]any)["amount"] = num("0") }, PropMASignature},
		{"array order", func(c map[string]any) { tt := c["totals"].([]any); tt[0], tt[2] = tt[2], tt[0] }, PropMASignature},
		{"array element removed", func(c map[string]any) { tt := c["totals"].([]any); c["totals"] = []any{tt[0], tt[2]} }, PropMASignature},
		{"link url", func(c map[string]any) { at(c, "links", 0).(map[string]any)["url"] = "https://merchant.example/terms2" }, PropMASignature},
		{"non-ASCII string", func(c map[string]any) { at(c, "links", 0).(map[string]any)["title"] = "Terms e €" }, PropMASignature},
		{"optional member removed", func(c map[string]any) { delete(at(c, "links", 0).(map[string]any), "title") }, PropMASignature},
		{"order id", func(c map[string]any) { at(c, "order").(map[string]any)["id"] = "order_other" }, PropMASignature},
		{"order permalink", func(c map[string]any) {
			at(c, "order").(map[string]any)["permalink_url"] = "https://merchant.example/orders/x"
		}, PropMASignature},
		{"order removed", func(c map[string]any) { delete(c, "order") }, PropMASignature},
		{"top-level member added", func(c map[string]any) { c["x_note"] = "added" }, PropMASignature},
		{"string to number", func(c map[string]any) { c["id"] = num("1") }, PropCheckoutSchema + "," + PropMASignature},
		{"required member removed", func(c map[string]any) { delete(c, "links") }, PropCheckoutSchema + "," + PropMASignature},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := check(mutate(t, c.f), p)
			if got := failing(rs); got != c.fails {
				t.Fatalf("FAIL on %q, want %q: %+v", got, c.fails, rs)
			}
			if statuses(rs)[objCheck] != adapter.Fail {
				t.Fatal("native_check not FAIL")
			}
		})
	}
}

// Changes that do not alter the signed content leave every row PASS.
func TestCheckoutInvariance(t *testing.T) {
	p := fixturePolicy(t)
	pos := string(positiveCheckout(t))
	cases := map[string][]byte{
		"re-serialized compact": mutate(t, func(map[string]any) {}),
		"escaped vs literal":    []byte(strings.Replace(pos, `\u00e9`, string(rune(0xe9)), 1)),
		"number spelling":       []byte(strings.Replace(pos, `"amount": 5400`, `"amount": 5.4e3`, 1)),
		"ap2 member added": mutate(t, func(c map[string]any) {
			c["ap2"].(map[string]any)["checkout_mandate"] = "aaa.bbb.ccc~ddd"
		}),
	}
	for name, raw := range cases {
		if string(raw) == pos {
			t.Fatalf("%s: no change", name)
		}
		rs := check(raw, p)
		for _, r := range rs {
			if r.Status != adapter.Pass {
				t.Errorf("%s: %s %s %s", name, adapter.CheckName(r), r.Status, r.Detail)
			}
		}
	}
}

// Changes to the authorization itself fail on the declared row.
func TestMerchantAuthorizationMutations(t *testing.T) {
	p := fixturePolicy(t)
	type want map[string]adapter.Status
	cases := []struct {
		name string
		f    func(*testing.T, map[string]any)
		want want
	}{
		{"signature bit", func(t *testing.T, c map[string]any) {
			ap2 := c["ap2"].(map[string]any)
			h, s, _ := cutDetached(ap2["merchant_authorization"].(string))
			sig, _ := b64url.DecodeString(s)
			sig[10] ^= 0x01
			ap2["merchant_authorization"] = h + ".." + base64.RawURLEncoding.EncodeToString(sig)
		}, want{PropMASignature: adapter.Fail}},
		{"signature truncated", func(t *testing.T, c map[string]any) {
			ap2 := c["ap2"].(map[string]any)
			h, s, _ := cutDetached(ap2["merchant_authorization"].(string))
			sig, _ := b64url.DecodeString(s)
			ap2["merchant_authorization"] = h + ".." + base64.RawURLEncoding.EncodeToString(sig[:63])
		}, want{PropMASignature: adapter.Fail}},
		{"compact JWS with payload", func(t *testing.T, c map[string]any) {
			ap2 := c["ap2"].(map[string]any)
			h, s, _ := cutDetached(ap2["merchant_authorization"].(string))
			ap2["merchant_authorization"] = h + ".e30." + s
		}, want{PropCheckoutSchema: adapter.Fail, PropMAForm: adapter.Fail, PropMAAlg: adapter.NotChecked, PropMAKey: adapter.NotChecked, PropMASignature: adapter.NotChecked}},
		{"not a string", func(t *testing.T, c map[string]any) { c["ap2"].(map[string]any)["merchant_authorization"] = true },
			want{PropCheckoutSchema: adapter.Fail, PropMAForm: adapter.Fail, PropMAAlg: adapter.NotChecked, PropMAKey: adapter.NotChecked, PropMASignature: adapter.NotChecked}},
		{"kid changed, not re-signed", func(t *testing.T, c map[string]any) {
			setHeader(t, c, `{"alg":"ES256","kid":"other"}`, false)
		}, want{PropMAKey: adapter.Fail, PropMASignature: adapter.NotChecked}},
		{"header member added, not re-signed", func(t *testing.T, c map[string]any) {
			setHeader(t, c, `{"alg":"ES256","kid":"`+testKID+`","typ":"JOSE"}`, false)
		}, want{PropMASignature: adapter.Fail}},
		{"alg ES384 on a P-256 key", func(t *testing.T, c map[string]any) {
			setHeader(t, c, `{"alg":"ES384","kid":"`+testKID+`"}`, false)
		}, want{PropMASignature: adapter.Fail}},
		{"alg HS256", func(t *testing.T, c map[string]any) {
			setHeader(t, c, `{"alg":"HS256","kid":"`+testKID+`"}`, false)
		}, want{PropMAAlg: adapter.Fail, PropMASignature: adapter.NotChecked}},
		{"alg EdDSA", func(t *testing.T, c map[string]any) {
			setHeader(t, c, `{"alg":"EdDSA","kid":"`+testKID+`"}`, false)
		}, want{PropMAAlg: adapter.Fail, PropMASignature: adapter.NotChecked}},
		{"crit, re-signed", func(t *testing.T, c map[string]any) {
			setHeader(t, c, `{"alg":"ES256","kid":"`+testKID+`","crit":["exp"],"exp":1}`, true)
		}, want{PropMAForm: adapter.Fail, PropMAAlg: adapter.NotChecked, PropMAKey: adapter.NotChecked, PropMASignature: adapter.NotChecked}},
		{"duplicate header member, re-signed", func(t *testing.T, c map[string]any) {
			setHeader(t, c, `{"alg":"ES256","kid":"`+testKID+`","kid":"`+testKID+`"}`, true)
		}, want{PropMAForm: adapter.Fail, PropMAAlg: adapter.NotChecked, PropMAKey: adapter.NotChecked, PropMASignature: adapter.NotChecked}},
		{"no kid, re-signed", func(t *testing.T, c map[string]any) { setHeader(t, c, `{"alg":"ES256"}`, true) },
			want{PropMAForm: adapter.Fail, PropMAAlg: adapter.NotChecked, PropMAKey: adapter.NotChecked, PropMASignature: adapter.NotChecked}},
		{"re-signed with header member added", func(t *testing.T, c map[string]any) {
			setHeader(t, c, `{"alg":"ES256","kid":"`+testKID+`","typ":"JOSE"}`, true)
		}, want{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := check(mutate(t, func(co map[string]any) { c.f(t, co) }), p)
			st := statuses(rs)
			for _, prop := range checkoutRows {
				w, named := c.want[prop]
				if !named {
					w = adapter.Pass
					if prop == objCheck {
						continue
					}
				}
				if st[prop] != w {
					t.Errorf("%s: %s, want %s (%+v)", prop, st[prop], w, rs)
				}
			}
		})
	}
}

func TestCheckoutPolicy(t *testing.T) {
	raw := positiveCheckout(t)
	with := func(f func(p *adapter.TrustPolicy)) adapter.TrustPolicy {
		p := fixturePolicy(t)
		f(&p)
		return p
	}
	jwk := func(extra string) string {
		return `{"keys":[{"kid":"` + testKID + `","kty":"EC","crv":"P-256","x":"` + testX + `","y":"` + testY + `"` + extra + `}]}`
	}
	cases := []struct {
		name               string
		p                  adapter.TrustPolicy
		key, sig, trust    adapter.Status
		native, keyMethodP string
	}{
		{"no key set (offline)", with(func(p *adapter.TrustPolicy) { delete(p.Pins, PinKeys) }), adapter.NotChecked, adapter.NotChecked, adapter.Pass, "NOT_CHECKED", methodKeyLive},
		{"key-set source named", with(func(p *adapter.TrustPolicy) { p.Pins[PinKeysSource] = "key-set.json" }), adapter.Pass, adapter.Pass, adapter.Pass, "PASS", "retained keys[] (key-set.json) as of " + fxRetainedAt},
		{"origin rejected", with(func(p *adapter.TrustPolicy) {
			p.Keys = map[string][]string{KeyBusinessOriginRejected: {fxOrigin}}
		}), adapter.Pass, adapter.Pass, adapter.Fail, "PASS", ""},
		{"origin listed as both", with(func(p *adapter.TrustPolicy) { p.Keys[KeyBusinessOriginRejected] = []string{fxOrigin} }), adapter.Pass, adapter.Pass, adapter.Fail, "PASS", ""},
		{"only a rejection list, origin not on it", with(func(p *adapter.TrustPolicy) {
			p.Keys = map[string][]string{KeyBusinessOriginRejected: {"https://other.example"}}
		}), adapter.Pass, adapter.Pass, adapter.Inconclusive, "PASS", ""},
		{"use enc", with(func(p *adapter.TrustPolicy) { p.Pins[PinKeys] = jwk(`,"use":"enc"`) }), adapter.Fail, adapter.NotChecked, adapter.Pass, "FAIL", "retained keys[] (" + fxOrigin + "/.well-known/ucp) as of " + fxRetainedAt},
		{"key_ops without verify", with(func(p *adapter.TrustPolicy) { p.Pins[PinKeys] = jwk(`,"key_ops":["sign"]`) }), adapter.Fail, adapter.NotChecked, adapter.Pass, "FAIL", ""},
		{"key_ops verify, bare array", with(func(p *adapter.TrustPolicy) {
			p.Pins[PinKeys] = strings.TrimSuffix(strings.TrimPrefix(jwk(`,"key_ops":["verify"]`), `{"keys":`), "}")
		}), adapter.Pass, adapter.Pass, adapter.Pass, "PASS", ""},
		{"kid not listed", with(func(p *adapter.TrustPolicy) { p.Pins[PinKeys] = strings.Replace(jwk(""), testKID, "k2", 1) }), adapter.Fail, adapter.NotChecked, adapter.Pass, "FAIL", ""},
		{"point not on curve", with(func(p *adapter.TrustPolicy) { p.Pins[PinKeys] = strings.Replace(jwk(""), testY, testX, 1) }), adapter.Fail, adapter.NotChecked, adapter.Pass, "FAIL", ""},
		{"P-384 key", with(func(p *adapter.TrustPolicy) {
			p.Pins[PinKeys] = `{"keys":[{"kid":"` + testKID + `","kty":"EC","crv":"P-384","x":"` + strings.Repeat("A", 64) + `","y":"` + strings.Repeat("A", 64) + `"}]}`
		}), adapter.Fail, adapter.NotChecked, adapter.Pass, "FAIL", ""},
		{"RSA key", with(func(p *adapter.TrustPolicy) {
			p.Pins[PinKeys] = `{"keys":[{"kid":"` + testKID + `","kty":"RSA","n":"AQAB","e":"AQAB"}]}`
		}), adapter.NotChecked, adapter.NotChecked, adapter.Pass, "NOT_CHECKED", ""},
		{"unreadable key set", with(func(p *adapter.TrustPolicy) { p.Pins[PinKeys] = `{"keys":` }), adapter.NotChecked, adapter.NotChecked, adapter.Pass, "NOT_CHECKED", ""},
		{"no retention time", with(func(p *adapter.TrustPolicy) { delete(p.Pins, PinKeysRetainedAt) }), adapter.Pass, adapter.Pass, adapter.Pass, "PASS", "retained keys[] (" + fxOrigin + "/.well-known/ucp)"},
		{"no origin policy", with(func(p *adapter.TrustPolicy) { p.Keys = nil }), adapter.Pass, adapter.Pass, adapter.NotChecked, "PASS", ""},
		{"origin not accepted", with(func(p *adapter.TrustPolicy) { p.Keys[KeyBusinessOrigin] = []string{"https://other.example"} }), adapter.Pass, adapter.Pass, adapter.Inconclusive, "PASS", ""},
		{"origin unknown", with(func(p *adapter.TrustPolicy) { delete(p.Pins, PinKeysOrigin) }), adapter.Pass, adapter.Pass, adapter.Inconclusive, "PASS", ""},
		{"empty policy", adapter.TrustPolicy{}, adapter.NotChecked, adapter.NotChecked, adapter.NotChecked, "NOT_CHECKED", methodKeyLive},
	}
	for _, c := range cases {
		rs := check(raw, c.p)
		st := statuses(rs)
		if st[PropMAKey] != c.key || st[PropMASignature] != c.sig || st[PropBusinessTrust] != c.trust || string(st[objCheck]) != c.native {
			t.Errorf("%s: key %s sig %s trust %s native %s", c.name, st[PropMAKey], st[PropMASignature], st[PropBusinessTrust], st[objCheck])
		}
		if k, _ := adapter.FindCheck(rs, PropMAKey); c.keyMethodP != "" && k.Method != c.keyMethodP {
			t.Errorf("%s: key method %q", c.name, k.Method)
		}
	}
}

// AP2 negotiated and the authorization missing is UCP's
// merchant_authorization_missing: NOT_PRESENT under the §3 method, not the
// §5 possession-only method.
func TestMerchantAuthorizationMissing(t *testing.T) {
	raw := mutate(t, func(c map[string]any) { delete(c, "ap2") })
	rs := check(raw, fixturePolicy(t))
	_, single := adapter.FindCheck(rs, CheckCheckout)
	pr, _ := adapter.FindCheck(rs, PropMAPresent)
	if single || objectNative(rs) != adapter.NotPresent || pr.Status != adapter.NotPresent || !strings.Contains(pr.Detail, "merchant_authorization_missing") {
		t.Fatalf("%+v", rs)
	}
	// An object with no native signature: native_check stays NOT_PRESENT
	// even when its schema row fails.
	bad := []byte(strings.Replace(string(noAP2Checkout(t)), `"currency": "USD"`, `"currency": 1`, 1))
	rs = check(bad, fixturePolicy(t))
	if st := statuses(rs); st[PropCheckoutSchema] != adapter.Fail || st[CheckCheckout] != adapter.NotPresent {
		t.Fatalf("%+v", rs)
	}
}

func TestUnreadableInput(t *testing.T) {
	for _, in := range []string{`{"id":"a","id":"b"}`, `not json`, `[1,2]`, ``, "{\"a\":\"\xff\"}"} {
		rs := check([]byte(in), fixturePolicy(t))
		if !slices.Equal(names(rs), checkoutRows) {
			t.Fatalf("%q: %v", in, names(rs))
		}
		st := statuses(rs)
		if st[PropCheckoutSchema] != adapter.Fail || st[PropMASignature] != adapter.NotChecked || st[objCheck] != adapter.Fail {
			t.Errorf("%q: %+v", in, rs)
		}
	}
}

// Ruling S1 through adapter.Evaluate: native_check FAIL decides a
// requirement that is the check (row 5) and leaves one it supports
// INSUFFICIENT (row 6); NOT_PRESENT maps as row 3, NOT_CHECKED as row 4; a
// NOT_PRESENT native_check never counts against a requirement that does not
// rest on it.
func TestS1Mapping(t *testing.T) {
	const row3 = "3 (native NOT_PRESENT; mapped as profile §8.1 rows 3-4 pending a profile row)"
	const row4 = "4 (native NOT_CHECKED or INCONCLUSIVE; mapped as profile §8.1 rows 3-4 pending a profile row)"
	p := fixturePolicy(t)
	isCheck := adapter.Requirement{ID: "checkout terms signed by the business", Relation: adapter.IsTheCheck, DependsOn: checkoutChecks}
	supports := adapter.Requirement{ID: "the order was placed", Relation: adapter.SupportedByTheCheck, DependsOn: checkoutChecks}
	orderIsCheck := adapter.Requirement{ID: "order snapshot signed by the business", Relation: adapter.IsTheCheck, DependsOn: orderChecks}
	schemaOnly := adapter.Requirement{ID: "the object is a UCP checkout", Relation: adapter.SupportedByTheCheck, DependsOn: []string{PropCheckoutSchema}}
	offline := p
	offline.Pins = map[string]string{}
	cases := []struct {
		name     string
		raw      []byte
		p        adapter.TrustPolicy
		req      adapter.Requirement
		row      string
		suf, ver string
	}{
		{"pass, is the check", positiveCheckout(t), p, isCheck, "7 (the requirement is the check, and it passed)", "SATISFIED", "met"},
		{"pass, supports", positiveCheckout(t), p, supports, "7 (the contract evaluates the content)", "", ""},
		{"fail, is the check", mutatedCheckout(t), p, isCheck, "5", "SATISFIED", "not_met"},
		{"fail, supports", mutatedCheckout(t), p, supports, "6", "INSUFFICIENT", "not_evaluable"},
		{"no AP2, is the check", noAP2Checkout(t), p, isCheck, row3, "INSUFFICIENT", "not_evaluable"},
		{"no AP2, supports", noAP2Checkout(t), p, supports, row3, "INSUFFICIENT", "not_evaluable"},
		{"no AP2, does not rest on it", noAP2Checkout(t), p, schemaOnly, "7 (the contract evaluates the content)", "", ""},
		{"offline, is the check", positiveCheckout(t), offline, isCheck, row4, "UNKNOWN", "not_evaluable"},
		{"body only, is the check", []byte(orderBody), p, orderIsCheck, row3, "INSUFFICIENT", "not_evaluable"},
		{"webhook, is the check", webhookRequest(t, orderBody, fullComponents), p, orderIsCheck, "7 (the requirement is the check, and it passed)", "SATISFIED", "met"},
	}
	for _, c := range cases {
		ev := adapter.Evaluate(check(c.raw, c.p), c.req)
		if ev.Row != c.row || ev.Sufficiency != c.suf || ev.Verdict != c.ver {
			t.Errorf("%s: %+v", c.name, ev)
		}
		if ev.Verdict == "met" && c.req.Relation != adapter.IsTheCheck {
			t.Errorf("%s: met for a requirement the check only supports", c.name)
		}
	}
}

// Every reported property is declared, in declaration order.
func TestDeclarationCoversResults(t *testing.T) {
	decl := Adapter{}.NativeObject()
	var declared []string
	for _, d := range decl.Properties {
		declared = append(declared, d.Name)
	}
	for name, raw := range fixtures(t) {
		if name == "trust-policy.json" {
			continue
		}
		last := -1
		for _, r := range check(raw, fixturePolicy(t)) {
			n := adapter.CheckName(r)
			i := slices.Index(declared, n)
			if i < 0 {
				t.Errorf("%s: %s not declared", name, n)
			}
			if i < last {
				t.Errorf("%s: %s out of declaration order", name, n)
			}
			if r.Method == "" {
				t.Errorf("%s: %s has no method", name, n)
			}
			if ownProperty := n == PropBusinessTrust || n == PropResponseSignature; (r.Property == adapter.NativeCheck) == ownProperty {
				t.Errorf("%s: %s reported as property %s", name, n, r.Property)
			}
			last = max(last, i)
		}
	}
	if b, err := json.Marshal(decl); err != nil || !json.Valid(b) {
		t.Fatal(err)
	}
}

func Example() {
	raw, _ := os.ReadFile("testdata/checkout-member-mutated.json")
	policy, _ := adapter.LoadTrustPolicy("testdata/trust-policy.json")
	for _, r := range (Adapter{}).Check(raw, policy) {
		fmt.Println(r.Property, adapter.CheckName(r), r.Status)
	}
	// Output:
	// native_check ucp.checkout.schema PASS
	// native_check ucp.merchant_authorization.present PASS
	// native_check ucp.merchant_authorization.form PASS
	// native_check ucp.merchant_authorization.alg PASS
	// native_check ucp.merchant_authorization.key PASS
	// native_check ucp.merchant_authorization.signature FAIL
	// ucp.business_trust ucp.business_trust PASS
}
