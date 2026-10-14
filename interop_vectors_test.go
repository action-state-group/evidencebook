// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The request-draft conformance vectors, vendored byte for byte from
// agent-action-capsule vectors/evidence-request/ (last changed at ee83efa,
// read at main 6cae247). They are spec-derived: every expected result was
// written from draft-mih-agent-evidence-request-00, never from an
// implementation. testdata/evidence-request/SHA256SUMS is the upstream
// SHA256SUMS lines for the four files, so a drifted copy fails here.
const erVectors = "testdata/evidence-request"

func TestVendoredRequestVectorsMatchUpstreamSums(t *testing.T) {
	file, err := os.Open(filepath.Join(erVectors, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	checked := 0
	for scanner.Scan() {
		sum, name, ok := strings.Cut(scanner.Text(), "  ")
		if !ok {
			t.Fatalf("SHA256SUMS line %q", scanner.Text())
		}
		data, err := os.ReadFile(filepath.Join(erVectors, name))
		if err != nil {
			t.Fatal(err)
		}
		if digestBytes(data) != sum {
			t.Fatalf("%s does not match its upstream SHA-256", name)
		}
		checked++
	}
	if checked != 4 {
		t.Fatalf("checked %d files, want 4", checked)
	}
}

type requestVector struct {
	ID                string `json:"id"`
	RequestJSON       string `json:"request_json"`
	RequestJSONDigest string `json:"request_json_digest"`
	RequestCBORHex    string `json:"request_cbor_hex"`
	RequestCBORDigest string `json:"request_cbor_digest"`
	Expect            struct {
		WellFormed bool   `json:"well_formed"`
		Reason     string `json:"reason"`
	} `json:"expect"`
}

func loadCases[T any](t *testing.T, name string) []T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(erVectors, name))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []T `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	return corpus.Cases
}

// TestRequestVectors runs every request.json case through the responder's
// request reader, in both bindings: each must be well formed, or refused
// with exactly the stated reason. The digest of the octets fed is the
// stated digest (the digest is over the octets as received).
func TestRequestVectors(t *testing.T) {
	cases := loadCases[requestVector](t, "request.json")
	if len(cases) != 33 {
		t.Fatalf("request.json has %d cases, want 33", len(cases))
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			bindings := []Message{{Octets: []byte(c.RequestJSON), MediaType: MediaTypeJSON}}
			digests := []string{c.RequestJSONDigest}
			if c.RequestCBORHex != "" {
				octets, err := hex.DecodeString(c.RequestCBORHex)
				if err != nil {
					t.Fatal(err)
				}
				bindings, digests = append(bindings, Message{Octets: octets, MediaType: MediaTypeCBOR}), append(digests, c.RequestCBORDigest)
			}
			for i, m := range bindings {
				if digests[i] != "" && digestBytes(m.Octets) != digests[i] {
					t.Fatalf("%s: digest of the octets is not the stated digest", m.MediaType)
				}
				got := readRequest(m)
				if c.Expect.WellFormed && got.reason != "" {
					t.Fatalf("%s: refused %s (%s), want well formed", m.MediaType, got.reason, got.why)
				}
				if !c.Expect.WellFormed && got.reason != c.Expect.Reason {
					t.Fatalf("%s: got %q, want refusal %s", m.MediaType, got.reason, c.Expect.Reason)
				}
			}
		})
	}
}

type refusalVector struct {
	ID      string          `json:"id"`
	Refusal json.RawMessage `json:"refusal"`
	File    string          `json:"file"`
	Expect  struct {
		Signature        string `json:"signature"`
		Conformant       bool   `json:"conformant"`
		ReasonRegistered bool   `json:"reason_registered"`
	} `json:"expect"`
}

// TestRefusalVectors: every signed refusal.json case read as a reply and
// checked by Refusal.Verify (the signature alone) and Refusal.Conforming
// (the registry and member forms), kept apart as the corpus keeps them
// (ERV A13). A refusal whose members are not text strings (two reasons, a
// missing issued_at) is not a refusal this package can read, so it is
// non-conforming whatever its signature.
func TestRefusalVectors(t *testing.T) {
	for _, c := range loadCases[refusalVector](t, "refusal.json") {
		t.Run(c.ID, func(t *testing.T) {
			raw := c.Refusal
			if c.File != "" {
				data, err := os.ReadFile(filepath.Join(erVectors, c.File))
				if err != nil {
					t.Skipf("refers to %s, which is not vendored (testdata/refusal-interop/refusal.json is the same object)", c.File)
				}
				raw = data
			}
			reply, err := ParseReply(Message{Octets: raw, MediaType: MediaTypeJSON})
			if err != nil || reply.Refusal == nil {
				if c.Expect.Conformant {
					t.Fatalf("a conforming refusal was not read: %v", err)
				}
				return
			}
			r := *reply.Refusal
			verified := r.Verify() == nil
			if verified != (c.Expect.Signature == "valid") {
				t.Fatalf("signature verified=%v, want %s", verified, c.Expect.Signature)
			}
			if conformant := verified && r.Conforming(); conformant != c.Expect.Conformant {
				t.Fatalf("conformant=%v, want %v", conformant, c.Expect.Conformant)
			}
		})
	}
}
