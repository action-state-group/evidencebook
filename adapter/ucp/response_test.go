// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

// A signed response is reported beside the body's checks, never inside
// them: an order or a checkout without AP2 stays NOT_PRESENT whatever the
// response signature says (ucp-v0 §5 at 75009695).
func TestResponseFixtures(t *testing.T) {
	p := fixturePolicy(t)
	cases := []struct {
		file     string
		object   string
		rows     []string
		native   adapter.Status // the body's native checks as a whole
		response adapter.Status
	}{
		{"checkout-response-signed.http", CheckCheckout, append(slices.Clone(checkoutRows), PropResponseSignature), adapter.Pass, adapter.Pass},
		{"checkout-response-body-mutated.http", CheckCheckout, append(slices.Clone(checkoutRows), PropResponseSignature), adapter.Pass, adapter.Fail},
		{"checkout-response-unsigned.http", CheckCheckout, append(slices.Clone(checkoutRows), PropResponseSignature), adapter.Pass, adapter.NotPresent},
		{"order-response-signed.http", CheckOrder, append(slices.Clone(orderBodyRows), PropResponseSignature), adapter.NotPresent, adapter.Pass},
	}
	for _, c := range cases {
		raw, err := os.ReadFile(filepath.Join("testdata", c.file))
		if err != nil {
			t.Fatal(err)
		}
		rs := check(raw, p)
		if !slices.Equal(names(rs), c.rows) {
			t.Fatalf("%s: rows %v", c.file, names(rs))
		}
		st := statuses(rs)
		if st[objCheck] != c.native || st[PropResponseSignature] != c.response {
			t.Errorf("%s: native %s, response %s", c.file, st[objCheck], st[PropResponseSignature])
		}
		last := rs[len(rs)-1]
		if last.Property != PropResponseSignature || last.DetailFields["check"] != PropResponseSignature || last.DetailFields["object"] != c.object {
			t.Errorf("%s: %+v", c.file, last)
		}
		if c.response == adapter.Pass && last.Method != `RFC 9421 ecdsa-p256-sha256 over ("@status" "content-digest" "content-type")` {
			t.Errorf("%s: method %q", c.file, last.Method)
		}
	}

	// S1: a PASS response signature never satisfies a requirement that rests
	// on the order's own signature; it decides only a requirement about the
	// response.
	raw, _ := os.ReadFile(filepath.Join("testdata", "order-response-signed.http"))
	rs := check(raw, p)
	onOrder := adapter.Evaluate(rs, adapter.Requirement{ID: "order signed by the business", Relation: adapter.IsTheCheck, DependsOn: orderChecks})
	onResponse := adapter.Evaluate(rs, adapter.Requirement{ID: "the business sent these response bytes", Relation: adapter.IsTheCheck, DependsOn: []string{PropResponseSignature}})
	if onOrder.Verdict == "met" || onOrder.Sufficiency != "INSUFFICIENT" || onResponse.Verdict != "met" {
		t.Fatalf("order %+v, response %+v", onOrder, onResponse)
	}
}

func TestResponseSignatureMutations(t *testing.T) {
	p := fixturePolicy(t)
	pos := string(responseMessage(t, "200 OK", orderBody, responseComponents))
	sigRe := regexp.MustCompile(`Signature: sig1=:([^:]+):`)
	flip := func(s string) string {
		m := sigRe.FindStringSubmatch(s)
		b, _ := base64.StdEncoding.DecodeString(m[1])
		b[7] ^= 0x01
		return strings.Replace(s, m[1], base64.StdEncoding.EncodeToString(b), 1)
	}
	noKeys := fixturePolicy(t)
	delete(noKeys.KeySets, KeySetName)
	cases := []struct {
		name string
		raw  string
		p    adapter.TrustPolicy
		want adapter.Status
	}{
		{"positive", pos, p, adapter.Pass},
		{"LF line ends", strings.ReplaceAll(pos, "\r\n", "\n"), p, adapter.Pass},
		{"body octet", strings.Replace(pos, `"currency":"USD"`, `"currency":"EUR"`, 1), p, adapter.Fail},
		{"status", strings.Replace(pos, "HTTP/1.1 200 OK", "HTTP/1.1 201 Created", 1), p, adapter.Fail},
		{"covered field value", strings.Replace(pos, "Content-Type: application/json", "Content-Type: text/plain", 1), p, adapter.Fail},
		{"signature bit", flip(pos), p, adapter.Fail},
		{"@status not covered", string(responseMessage(t, "200 OK", orderBody, `"content-digest" "content-type"`)), p, adapter.Fail},
		{"keyid not listed", strings.Replace(pos, `keyid="`+testKID+`"`, `keyid="other"`, 1), p, adapter.Fail},
		{"covered field not kept", strings.Replace(pos, "Content-Type: application/json\r\n", "", 1), p, adapter.NotPresent},
		{"unsigned", string(responseMessage(t, "200 OK", orderBody, "")), p, adapter.NotPresent},
		{"no key set", pos, noKeys, adapter.NotChecked},
		{"malformed Signature-Input", strings.Replace(pos, "Signature-Input: sig1=(", "Signature-Input: sig1=((", 1), p, adapter.Fail},
	}
	for _, c := range cases {
		if c.name != "positive" && c.name != "no key set" && c.raw == pos {
			t.Fatalf("%s: mutation did not apply", c.name)
		}
		rs := check([]byte(c.raw), c.p)
		st := statuses(rs)
		if st[PropResponseSignature] != c.want {
			t.Errorf("%s: %s, want %s (%+v)", c.name, st[PropResponseSignature], c.want, rs[len(rs)-1])
		}
		// The order's own native check never moves.
		if st[CheckOrder] != adapter.NotPresent {
			t.Errorf("%s: ucp.order %s", c.name, st[CheckOrder])
		}
	}
}
