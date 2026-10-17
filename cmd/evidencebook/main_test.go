// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type output struct {
	Adapter    string `json:"adapter"`
	Properties []struct {
		Property, Status, Method, Detail string
	} `json:"properties"`
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
		{"exchange-positive.http", 0, 12, ""},
		{"exchange-signature-bit-flip.http", 1, 12, "native_check"},
		{"leg-payer-observed.json", 0, 17, ""},
		{"leg-payer-observed-signature-bit-flip.json", 1, 17, "native_check"},
		{"payment-payload.json", 0, 12, ""},
		{"payment-payload-signature-bit-flip.json", 1, 12, "native_check"},
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
					if !strings.HasPrefix(p.Detail, "payer_signature: ") {
						t.Errorf("%s: FAIL detail %q", c.file, p.Detail)
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
	if code := run([]string{"adapt", "x402", "-declare"}, nil, &out, &errOut); code != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("-declare: exit %d", code)
	}
	for _, args := range [][]string{nil, {"adapt"}, {"adapt", "nope", "f"}, {"adapt", "x402"}, {"adapt", "x402", "/nonexistent"}} {
		if code := run(args, nil, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
