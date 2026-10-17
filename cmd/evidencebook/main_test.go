// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestAdaptX402(t *testing.T) {
	td := filepath.Join("..", "..", "adapter", "x402", "testdata")
	policy := filepath.Join(td, "trust-policy.json")
	cases := []struct {
		file string
		code int
	}{
		{"exchange-positive.http", 0},
		{"exchange-signature-bit-flip.http", 1},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		code := run([]string{"adapt", "x402", "-policy", policy, filepath.Join(td, c.file)}, &out, &errOut)
		if code != c.code {
			t.Fatalf("%s: exit %d, want %d (%s)", c.file, code, c.code, errOut.String())
		}
		var got struct {
			Adapter    string `json:"adapter"`
			Properties []struct {
				Property, Status, Method string
			} `json:"properties"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Adapter != "x402" || len(got.Properties) != 10 {
			t.Fatalf("%s: %s", c.file, out.String())
		}
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"adapt", "x402", "-declare"}, &out, &errOut); code != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("-declare: exit %d", code)
	}
	for _, args := range [][]string{nil, {"adapt"}, {"adapt", "nope", "f"}, {"adapt", "x402"}, {"adapt", "x402", "/nonexistent"}} {
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
