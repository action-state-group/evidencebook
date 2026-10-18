// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

// Each change to the held request fails exactly the declared rows.
func TestWebhookMutations(t *testing.T) {
	p := fixturePolicy(t)
	pos := string(webhookRequest(t, orderBody, fullComponents))
	sigRe := regexp.MustCompile(`Signature: sig1=:([^:]+):`)
	flipSig := func(s string) string {
		m := sigRe.FindStringSubmatch(s)
		b, _ := base64.StdEncoding.DecodeString(m[1])
		b[5] ^= 0x01
		return strings.Replace(s, m[1], base64.StdEncoding.EncodeToString(b), 1)
	}
	type want map[string]adapter.Status
	cases := []struct {
		name string
		raw  string
		want want
	}{
		{"positive", pos, want{}},
		{"LF line ends", strings.ReplaceAll(strings.Replace(pos, "\r\n\r\n", "\n\n", 1), "\r\n", "\n"), want{}},
		{"field name case", strings.Replace(pos, "Content-Type:", "content-TYPE:", 1), want{}},
		{"body octet", strings.Replace(pos, `"currency":"USD"`, `"currency":"EUR"`, 1), want{PropWebhookDigest: adapter.Fail}},
		{"covered header value", strings.Replace(pos, "Content-Type: application/json", "Content-Type: text/plain", 1), want{PropWebhookSignature: adapter.Fail}},
		{"path", strings.Replace(pos, "POST /webhooks/ucp/orders", "POST /webhooks/ucp/other", 1), want{PropWebhookSignature: adapter.Fail}},
		{"method", strings.Replace(pos, "POST /", "PUT /", 1), want{PropWebhookSignature: adapter.Fail}},
		{"authority", strings.Replace(pos, "Host: platform.example", "Host: attacker.example", 1), want{PropWebhookSignature: adapter.Fail}},
		{"signature bit", flipSig(pos), want{PropWebhookSignature: adapter.Fail}},
		{"signature params", strings.Replace(pos, "created=1760702400", "created=1760702401", 1), want{PropWebhookSignature: adapter.Fail}},
		{"query added", strings.Replace(pos, "/webhooks/ucp/orders HTTP", "/webhooks/ucp/orders?x=1 HTTP", 1), want{PropWebhookCoverage: adapter.Fail}},
		{"uncovered header added", strings.Replace(pos, "Content-Length:", "X-Extra: 1\r\nContent-Length:", 1), want{}},
		{"idempotency-key added, uncovered", strings.Replace(pos, "Content-Length:", "Idempotency-Key: k1\r\nContent-Length:", 1), want{PropWebhookCoverage: adapter.Fail}},
		{"covered header missing", strings.Replace(pos, "Content-Type: application/json\r\n", "", 1), want{PropWebhookComponents: adapter.NotPresent, PropWebhookSignature: adapter.NotChecked}},
		{"Content-Digest missing", strings.Replace(pos, regexp.MustCompile(`Content-Digest: [^\r]*\r\n`).FindString(pos), "", 1), want{PropWebhookComponents: adapter.NotPresent, PropWebhookDigest: adapter.NotPresent, PropWebhookSignature: adapter.NotChecked}},
		{"keyid changed", strings.Replace(pos, `keyid="`+testKID+`"`, `keyid="other"`, 1), want{PropWebhookKey: adapter.Fail, PropWebhookSignature: adapter.NotChecked}},
		{"UCP-Agent other origin", strings.Replace(pos, "profile=\""+fxOrigin, "profile=\"https://other.example", 1), want{PropWebhookKey: adapter.NotChecked, PropWebhookSignature: adapter.NotChecked, PropBusinessTrust: adapter.Inconclusive}},
		{"UCP-Agent not https", strings.Replace(pos, "profile=\"https://", "profile=\"http://", 1), want{PropWebhookKey: adapter.Fail, PropWebhookSignature: adapter.NotChecked, PropBusinessTrust: adapter.Inconclusive}},
		{"UCP-Agent removed", strings.Replace(pos, regexp.MustCompile(`UCP-Agent: [^\r]*\r\n`).FindString(pos), "", 1), want{PropWebhookComponents: adapter.NotPresent, PropWebhookKey: adapter.NotPresent, PropWebhookSignature: adapter.NotChecked, PropBusinessTrust: adapter.Inconclusive}},
		{"unknown tag", strings.Replace(pos, `;keyid=`, `;tag="other";keyid=`, 1), want{PropWebhookSignature: adapter.NotChecked}},
		{"web-bot-auth tag, keyid not a thumbprint", strings.Replace(pos, `;keyid=`, `;tag="web-bot-auth";keyid=`, 1), want{PropWebhookSignature: adapter.Fail}},
		{"alg parameter mismatch", strings.Replace(pos, `;keyid=`, `;alg="ed25519";keyid=`, 1), want{PropWebhookSignature: adapter.Fail}},
		{"Signature-Input malformed", strings.Replace(pos, "Signature-Input: sig1=(", "Signature-Input: sig1=((", 1), want{PropWebhookComponents: adapter.Fail, PropWebhookCoverage: adapter.Fail, PropWebhookKey: adapter.Fail, PropWebhookSignature: adapter.Fail}},
		{"Signature absent", strings.Replace(pos, sigRe.FindString(pos)+"\r\n", "", 1), want{PropWebhookComponents: adapter.NotPresent, PropWebhookCoverage: adapter.NotPresent, PropWebhookKey: adapter.NotPresent, PropWebhookSignature: adapter.NotPresent}},
		{"order body breaks schema", "", want{}},
		{"both signatures bad: the first is reported", strings.Replace(strings.Replace(flipSig(pos), "Signature-Input: sig1=", "Signature-Input: bad=(\"@method\");keyid=\"x\", sig1=", 1), "Signature: sig1=", "Signature: bad=:AAAA:, sig1=", 1), want{PropWebhookCoverage: adapter.Fail, PropWebhookKey: adapter.Fail, PropWebhookSignature: adapter.NotChecked}},
	}
	// A signed request whose body is not a valid order: only the schema row.
	badOrder := strings.Replace(orderBody, `"status":"processing"`, `"status":"lost"`, 1)
	cases[len(cases)-2].raw = string(webhookRequest(t, badOrder, fullComponents))
	cases[len(cases)-2].want = want{PropOrderSchema: adapter.Fail}
	// With a valid second signature after a bad first one, the valid one is
	// reported.
	two := strings.Replace(strings.Replace(pos, "Signature-Input: sig1=", "Signature-Input: bad=(\"@method\");keyid=\"x\", sig1=", 1), "Signature: sig1=", "Signature: bad=:AAAA:, sig1=", 1)
	cases = append(cases, struct {
		name string
		raw  string
		want want
	}{"bad first signature, valid second", two, want{}})

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name != "positive" && c.raw == pos {
				t.Fatal("mutation did not apply")
			}
			rs := check([]byte(c.raw), p)
			st := statuses(rs)
			anyFail := false
			for _, prop := range webhookRows {
				if prop == CheckOrder {
					continue
				}
				w, named := c.want[prop]
				if !named {
					w = adapter.Pass
				}
				if st[prop] != w {
					t.Errorf("%s: %s, want %s (%+v)", prop, st[prop], w, rs)
				}
				if w == adapter.Fail && prop != PropBusinessTrust {
					anyFail = true
				}
			}
			if anyFail && st[objCheck] != adapter.Fail {
				t.Errorf("native_check %s, want FAIL", st[objCheck])
			}
			if len(c.want) == 0 && st[objCheck] != adapter.Pass {
				t.Errorf("native_check %s, want PASS", st[objCheck])
			}
		})
	}
}

func TestSFRoundTrip(t *testing.T) {
	in := `("@method" "@authority" "content-digest");created=1760702400;keyid="k\"1";tag="web-bot-auth";x;y=tok;z=?0`
	d, err := parseSFDictionary("sig1=" + in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := serializeInnerList(d[0].inner, d[0].params)
	if err != nil || out != in {
		t.Fatalf("%q %v", out, err)
	}
	for _, bad := range []string{`a=(`, `a=1.5`, `A=1`, `a=:!!:`, `a="x`, `a=1,`, `a=1 b=2`} {
		if _, err := parseSFDictionary(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
