// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

var update = flag.Bool("update", false, "rewrite testdata fixtures")

// TEST KEY. The ES256 example key of RFC 7515 (JSON Web Signature) Appendix
// A.3, published in the RFC as an example. It is no one's key and must never
// be used for anything but tests (ruling S3). UCP publishes no test key
// (ucp-v0 §5 item 5).
const (
	testKID = "rfc7515-appendix-a3"
	testX   = "f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU"
	testY   = "x_FEzRu9m36HLN_tue659LNpXW6pCyStikYjKIWI5a0"
	testD   = "jpsQnnGQmL-YBIffH1136cspYG6-0iY7X1fCE9-E9LI"

	fxOrigin     = "https://merchant.example"
	fxRetainedAt = "2026-10-17T12:00:00Z"
)

func testKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	d, err := b64url.DecodeString(testD)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// sign returns a deterministic (RFC 6979) fixed-width r||s signature, so the
// fixtures are reproducible byte for byte.
func sign(t testing.TB, k *ecdsa.PrivateKey, h crypto.Hash, msg []byte) []byte {
	t.Helper()
	hh := h.New()
	hh.Write(msg)
	der, err := k.Sign(nil, hh.Sum(nil), h)
	if err != nil {
		t.Fatal(err)
	}
	var rs struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &rs); err != nil {
		t.Fatal(err)
	}
	size := (k.Curve.Params().BitSize + 7) / 8
	out := make([]byte, 2*size)
	rs.R.FillBytes(out[:size])
	rs.S.FillBytes(out[size:])
	return out
}

func testJWKSet() string {
	return `{"keys":[{"kid":"` + testKID + `","kty":"EC","crv":"P-256","x":"` + testX + `","y":"` + testY + `","use":"sig","alg":"ES256"}]}`
}

func testPolicyJSON() string {
	p := map[string]any{
		"pins": map[string]string{
			PinKeys:             testJWKSet(),
			PinKeysOrigin:       fxOrigin,
			PinKeysRetainedAt:   fxRetainedAt,
			adapter.PinPolicyID: "fixture-accept-merchant.example",
		},
		"keys": map[string][]string{KeyBusinessOrigin: {fxOrigin}},
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return string(b) + "\n"
}

// checkoutTemplate is a constructed (not recorded) completed checkout under
// the AP2 Mandates Extension. Its members are deliberately not in JCS order
// and it is indented: the signature covers the RFC 8785 form, not these
// octets.
const checkoutTemplate = `{
  "ucp": {
    "version": "2026-08-25",
    "capabilities": {
      "dev.ucp.shopping.checkout": [{"version": "2026-08-25"}],
      "dev.ucp.common.payment.ap2_mandate": [{"version": "2026-08-25", "extends": "dev.ucp.shopping.checkout"}]
    },
    "payment_handlers": {}
  },
  "id": "chk_abc123",
  "status": "completed",
  "currency": "USD",
  "line_items": [
    {
      "id": "li_1",
      "item": {"id": "item_123", "title": "Widget", "price": 2500},
      "quantity": 2,
      "totals": [
        {"type": "subtotal", "amount": 5000},
        {"type": "total", "amount": 5000}
      ]
    }
  ],
  "totals": [
    {"type": "subtotal", "amount": 5000},
    {"type": "tax", "amount": 400},
    {"type": "total", "amount": 5400}
  ],
  "links": [
    {"type": "terms_of_service", "url": "https://merchant.example/terms", "title": "Terms \u00e9 \u20ac"}
  ],
  "order": {
    "id": "order_abc123",
    "permalink_url": "https://merchant.example/orders/order_abc123"
  },
  "ap2": {
    "merchant_authorization": "%s"
  }
}
`

// signCheckout signs a checkout (a parseIJSON value) as AP2 §Business
// Authorization does, and returns the merchant_authorization value.
func signCheckout(t testing.TB, co map[string]any, header string) string {
	t.Helper()
	payload := map[string]any{}
	for k, v := range co {
		if k != "ap2" {
			payload[k] = v
		}
	}
	canon, err := jcs(payload)
	if err != nil {
		t.Fatal(err)
	}
	encHeader := base64.RawURLEncoding.EncodeToString([]byte(header))
	input := encHeader + "." + base64.RawURLEncoding.EncodeToString(canon)
	return encHeader + ".." + base64.RawURLEncoding.EncodeToString(sign(t, testKey(t), crypto.SHA256, []byte(input)))
}

const testHeader = `{"alg":"ES256","kid":"` + testKID + `"}`

func positiveCheckout(t testing.TB) []byte {
	t.Helper()
	v, err := parseIJSON([]byte(fmt.Sprintf(checkoutTemplate, "x..y")))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf(checkoutTemplate, signCheckout(t, v.(map[string]any), testHeader)))
}

// mutatedCheckout is the positive checkout with one signed member changed
// after signing: the total goes from 5400 to 5401. It still validates.
func mutatedCheckout(t testing.TB) []byte {
	t.Helper()
	pos := positiveCheckout(t)
	out := bytes.Replace(pos, []byte(`{"type": "total", "amount": 5400}`), []byte(`{"type": "total", "amount": 5401}`), 1)
	if bytes.Equal(out, pos) {
		t.Fatal("mutation did not apply")
	}
	return out
}

func noAP2Checkout(t testing.TB) []byte {
	t.Helper()
	s := fmt.Sprintf(checkoutTemplate, "x..y")
	s = strings.Replace(s, `,
      "dev.ucp.common.payment.ap2_mandate": [{"version": "2026-08-25", "extends": "dev.ucp.shopping.checkout"}]`, "", 1)
	s = strings.Replace(s, `,
  "ap2": {
    "merchant_authorization": "x..y"
  }`, "", 1)
	if strings.Contains(s, "ap2") {
		t.Fatal("template changed: ap2 still present")
	}
	return []byte(s)
}

// orderBody is a constructed order snapshot, the webhook body.
const orderBody = `{"event_id":"evt_123","created_time":"2026-10-17T12:00:00Z","ucp":{"version":"2026-08-25","capabilities":{"dev.ucp.shopping.order":[{"version":"2026-08-25"}]}},"id":"order_abc123","checkout_id":"chk_abc123","permalink_url":"https://merchant.example/orders/order_abc123","line_items":[{"id":"li_1","item":{"id":"item_123","title":"Widget","price":2500},"quantity":{"total":2,"fulfilled":0},"totals":[{"type":"subtotal","amount":5000},{"type":"total","amount":5000}],"status":"processing"}],"fulfillment":{},"currency":"USD","totals":[{"type":"subtotal","amount":5000},{"type":"tax","amount":400},{"type":"total","amount":5400}]}`

// webhookRequest builds a signed Order Event Webhook request (UCP Message
// Signatures §REST Request Signing), CRLF line ends, as received.
func webhookRequest(t testing.TB, body string, components string) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	digest := "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"
	params := `(` + components + `);created=1760702400;keyid="` + testKID + `"`
	fields := map[string]string{
		"@method":        "POST",
		"@authority":     "platform.example",
		"@path":          "/webhooks/ucp/orders",
		"content-digest": digest,
		"content-type":   "application/json",
		"ucp-agent":      `profile="` + fxOrigin + `/.well-known/ucp"`,
	}
	var base strings.Builder
	for _, c := range strings.Fields(components) {
		name := strings.Trim(c, `"`)
		base.WriteString(`"` + name + `": ` + fields[name] + "\n")
	}
	base.WriteString(`"@signature-params": ` + params)
	sig := sign(t, testKey(t), crypto.SHA256, []byte(base.String()))
	lines := []string{
		"POST /webhooks/ucp/orders HTTP/1.1",
		"Host: platform.example",
		"Content-Type: application/json",
		"UCP-Agent: " + fields["ucp-agent"],
		"Content-Digest: " + digest,
		"Signature-Input: sig1=" + params,
		"Signature: sig1=:" + base64.StdEncoding.EncodeToString(sig) + ":",
		"Content-Length: " + fmt.Sprint(len(body)),
		"",
		body,
	}
	return []byte(strings.Join(lines, "\r\n"))
}

const fullComponents = `"@method" "@authority" "@path" "content-digest" "content-type" "ucp-agent"`

func fixtures(t testing.TB) map[string][]byte {
	pos := webhookRequest(t, orderBody, fullComponents)
	// The body changed after signing: one octet of the order id.
	bodyMut := bytes.Replace(pos, []byte(`"id":"order_abc123"`), []byte(`"id":"order_abc124"`), 1)
	return map[string][]byte{
		"trust-policy.json":                   []byte(testPolicyJSON()),
		"checkout-positive.json":              positiveCheckout(t),
		"checkout-member-mutated.json":        mutatedCheckout(t),
		"checkout-no-ap2.json":                noAP2Checkout(t),
		"order-webhook.http":                  pos,
		"order-webhook-body-mutated.http":     bodyMut,
		"order-webhook-example-coverage.http": webhookRequest(t, orderBody, `"@method" "@authority" "@path" "content-digest" "content-type"`),
		"order-body.json":                     []byte(orderBody),
	}
}

// TestFixturesUpToDate requires testdata/ to be byte-identical to the
// generator's output; -update rewrites it.
func TestFixturesUpToDate(t *testing.T) {
	for name, want := range fixtures(t) {
		path := filepath.Join("testdata", name)
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale: run go test ./adapter/ucp -run TestFixturesUpToDate -update", path)
		}
	}
}
