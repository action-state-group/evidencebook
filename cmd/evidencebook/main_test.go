// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

type output struct {
	Adapter    string                   `json:"adapter"`
	Properties []adapter.PropertyResult `json:"properties"`
}

func TestAdaptX402(t *testing.T) {
	td := filepath.Join("..", "..", "adapter", "x402", "testdata")
	// The negative fixtures are evaluated under policy eoa, so their FAIL is
	// a real FAIL; under the default policy they are INCONCLUSIVE (exit 0).
	policy := filepath.Join(td, "trust-policy-eoa.json")
	cases := []struct {
		file  string
		code  int
		props int
		fail  string
	}{
		{"exchange-positive.http", 0, 13, ""},
		{"exchange-signature-bit-flip.http", 1, 13, "native_check"},
		{"leg-payer-observed.json", 0, 18, ""},
		{"leg-payer-observed-signature-bit-flip.json", 1, 18, "native_check"},
		{"payment-payload.json", 0, 13, ""},
		{"payment-payload-signature-bit-flip.json", 1, 13, "native_check"},
	}
	for _, c := range cases {
		for _, viaStdin := range []bool{false, true} {
			var out, errOut bytes.Buffer
			path := filepath.Join(td, c.file)
			arg, in := path, []byte(nil)
			if viaStdin {
				var err error
				if in, err = os.ReadFile(path); err != nil {
					t.Fatal(err)
				}
				arg = "-"
			}
			code := run([]string{"adapt", "x402", "-policy", policy, arg}, bytes.NewReader(in), &out, &errOut)
			if code != c.code {
				t.Fatalf("%s (stdin %v): exit %d, want %d (%s)", c.file, viaStdin, code, c.code, errOut.String())
			}
			var got output
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var fails []string
			for _, p := range got.Properties {
				if p.Status == "FAIL" {
					fails = append(fails, p.Property)
					if p.DetailFields["check"] != "payer_signature" || p.DetailFields["object"] != "x402.payment-payload" {
						t.Errorf("%s: FAIL detail %v", c.file, p.DetailFields)
					}
				}
			}
			if got.Adapter != "x402" || len(got.Properties) != c.props || strings.Join(fails, ",") != c.fail {
				t.Fatalf("%s: %s", c.file, out.String())
			}
		}
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"adapt", "x402", "-policy", filepath.Join(td, "trust-policy.json"), filepath.Join(td, "leg-payer-observed-signature-bit-flip.json")}, nil, &out, &errOut); code != 0 {
		t.Errorf("default policy, signature flip: exit %d, want 0 (INCONCLUSIVE)", code)
	}
	out.Reset()
	if code := run([]string{"adapt", "x402", "-explain", "-policy", policy, filepath.Join(td, "payment-payload-signature-bit-flip.json")}, nil, &out, &errOut); code != 1 || !strings.Contains(out.String(), `"explanation": "recovers to`) {
		t.Errorf("-explain: exit %d\n%s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"adapt", "x402", "-declare"}, nil, &out, &errOut); code != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("-declare: exit %d", code)
	}
	for _, args := range [][]string{nil, {"adapt"}, {"adapt", "nope", "f"}, {"adapt", "x402"}, {"adapt", "x402", "/nonexistent"}} {
		if code := run(args, nil, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestAdaptUCP(t *testing.T) {
	td := filepath.Join("..", "..", "adapter", "ucp", "testdata")
	policy := filepath.Join(td, "trust-policy.json")
	cases := []struct {
		file  string
		code  int
		props int
		fails string // check names, from detail
	}{
		{"checkout-positive.json", 0, 7, ""},
		{"checkout-member-mutated.json", 1, 7, "ucp.merchant_authorization.signature"},
		{"checkout-no-ap2.json", 0, 8, ""},
		{"order-webhook.http", 0, 7, ""},
		{"order-webhook-body-mutated.http", 1, 7, "ucp.webhook.content_digest"},
		{"order-webhook-example-coverage.http", 1, 7, "ucp.webhook.coverage"},
		{"order-body.json", 0, 8, ""},
		{"checkout-response-signed.http", 0, 8, ""},
		{"checkout-response-body-mutated.http", 1, 8, "ucp.response_signature"},
		{"order-response-signed.http", 0, 9, ""},
		{filepath.Join("aac-vectors", "positive.checkout.json"), 0, 7, ""},
		{filepath.Join("aac-vectors", "native-check-fails.checkout.json"), 1, 7, "ucp.merchant_authorization.signature"},
	}
	for _, c := range cases {
		for _, viaStdin := range []bool{false, true} {
			var out, errOut bytes.Buffer
			path := filepath.Join(td, c.file)
			arg, in := path, []byte(nil)
			if viaStdin {
				var err error
				if in, err = os.ReadFile(path); err != nil {
					t.Fatal(err)
				}
				arg = "-"
			}
			code := run([]string{"adapt", "ucp", "-policy", policy, arg}, bytes.NewReader(in), &out, &errOut)
			if code != c.code {
				t.Fatalf("%s (stdin %v): exit %d, want %d (%s)", c.file, viaStdin, code, c.code, errOut.String())
			}
			var got output
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var fails []string
			for _, p := range got.Properties {
				if p.Status == "FAIL" {
					fails = append(fails, adapter.CheckName(p))
				}
			}
			for _, p := range got.Properties {
				if (p.Property == adapter.NativeCheck || p.Property == "ucp.response_signature") && !strings.HasPrefix(p.DetailFields["object"], "ucp.") {
					t.Fatalf("%s: detail %v has no object", c.file, p.DetailFields)
				}
			}
			if got.Adapter != "ucp" || len(got.Properties) != c.props || strings.Join(fails, ",") != c.fails {
				t.Fatalf("%s: %s", c.file, out.String())
			}
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"adapt", "ucp", "-declare"}, nil, &out, &errOut); code != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("-declare: exit %d", code)
	}
	if code := run([]string{"adapt", "ucp"}, nil, &out, &errOut); code != 2 {
		t.Fatalf("no file: exit %d, want 2", code)
	}
}
