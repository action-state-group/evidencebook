// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

// The agent-action-capsule vectors for this adapter, vectors/adapters/x402/,
// vendored byte for byte under testdata/aac-vectors/.
//
// Pinned to agent-action-capsule main 3c778daa (#299). To move the pin:
// re-copy the directory from the new commit, set aacX402Commit, and update
// aacX402SumsSHA256 (the SHA-256 of SHA256SUMS).
const (
	aacX402Commit     = "3c778daa6bc3991fce9f2838ffa7a128a2ab13c2"
	aacX402SumsSHA256 = "f0c7308ef0166477740b0e45fe5c589d9bd71d85cb1a15f23f0c9ff8de5c66d5"
)

type vectorCase struct {
	Name   string `json:"name"`
	Inputs struct {
		Wrapped []struct {
			Type      string `json:"type"`
			DigestAlg string `json:"digest_alg"`
			Digest    string `json:"digest"`
			Octets    string `json:"octets"`
		} `json:"wrapped"`
		Informative map[string]string `json:"informative"`
		Lookup      string            `json:"lookup"`
		Online      bool              `json:"online"`
	} `json:"inputs"`
	Policy struct {
		ID      string `json:"id"`
		Signer  string `json:"signer"`
		Domains []struct {
			Network           string `json:"network"`
			Asset             string `json:"asset"`
			Name              string `json:"name"`
			Version           string `json:"version"`
			ChainID           int64  `json:"chainId"`
			VerifyingContract string `json:"verifyingContract"`
		} `json:"domains"`
		Payers []struct {
			Address string `json:"address"`
			Party   string `json:"party"`
		} `json:"payers"`
		ReceiptSigners []string `json:"receipt_signers"`
		ChainSource    string   `json:"chain_source"`
	} `json:"policy"`
	Expect struct {
		Reports      []json.RawMessage `json:"reports"`
		Requirements []struct {
			Requirement string `json:"requirement"`
			Relation    string `json:"relation"`
			Row         int    `json:"row"`
			Status      string `json:"status"`
			Sufficiency string `json:"sufficiency"`
			Verdict     string `json:"verdict"`
		} `json:"requirements"`
	} `json:"expect"`
}

// vectorRequirementChecks says which checks each requirement named in the
// vectors rests on. The vectors name the requirement and its relation to
// the check, not the checks themselves; this is the adapter's reading of
// the declaration's §8 (X1 rests on payload_form and payer_signature; "the
// resource was paid for" is supported by the payer's signed authorization).
var vectorRequirementChecks = map[string][]string{
	"the payer signed this transfer authorization": {RowPayloadForm, RowPayerSignature},
	"the resource was paid for":                    {RowPayloadForm, RowPayerSignature},
}

// policyFromVector translates a case's policy into a TrustPolicy. It refuses
// a policy this adapter cannot express (more than one domain, or a domain
// whose chainId or verifyingContract is not the one its network and asset
// imply).
func policyFromVector(t *testing.T, c vectorCase, lookup []byte) adapter.TrustPolicy {
	t.Helper()
	pol := c.Policy
	if len(pol.Domains) != 1 {
		t.Fatalf("%s: %d policy domains; the adapter's policy holds one", c.Name, len(pol.Domains))
	}
	d := pol.Domains[0]
	if want := fmt.Sprintf("eip155:%d", d.ChainID); want != d.Network {
		t.Fatalf("%s: domain chainId %d does not match network %s", c.Name, d.ChainID, d.Network)
	}
	if !strings.EqualFold(d.VerifyingContract, d.Asset) {
		t.Fatalf("%s: domain verifyingContract %s is not the asset %s", c.Name, d.VerifyingContract, d.Asset)
	}
	p := adapter.TrustPolicy{
		Pins: map[string]string{
			adapter.PinPolicyID: pol.ID, PinPayerAccounts: pol.Signer,
			PinChain: d.Network, PinAsset: d.Asset, PinAssetName: d.Name, PinAssetVersion: d.Version,
		},
		Keys: map[string][]string{},
	}
	for _, py := range pol.Payers {
		p.Keys[KeyPayer] = append(p.Keys[KeyPayer], py.Address)
	}
	if len(pol.ReceiptSigners) > 0 {
		p.Keys[KeyReceiptSigner] = pol.ReceiptSigners
	}
	if c.Inputs.Online && lookup != nil {
		p.Recorded = map[string]json.RawMessage{RecordedLookup: lookup}
	}
	return p
}

func TestAACVectors(t *testing.T) {
	dir := filepath.Join("testdata", "aac-vectors")
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if s := sha256.Sum256(sums); hex.EncodeToString(s[:]) != aacX402SumsSHA256 {
		t.Fatalf("testdata/aac-vectors/SHA256SUMS is not the one pinned at %s", aacX402Commit)
	}
	// Every file the sums name is byte-identical, and nothing else is there.
	listed := map[string]bool{"SHA256SUMS": true}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			t.Fatalf("SHA256SUMS line %q", sc.Text())
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if s := sha256.Sum256(b); hex.EncodeToString(s[:]) != sum {
			t.Fatalf("%s differs from the vectors at %s", name, aacX402Commit)
		}
		listed[name] = true
	}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			if !listed[filepath.ToSlash(rel)] {
				t.Errorf("%s is not in SHA256SUMS", rel)
			}
		}
		return nil
	})

	data, err := os.ReadFile(filepath.Join(dir, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Count int          `json:"count"`
		Cases []vectorCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.Count != 2 || len(corpus.Cases) != 2 {
		t.Fatalf("%d cases", len(corpus.Cases))
	}
	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			octets := map[string][]byte{}
			for _, w := range c.Inputs.Wrapped {
				b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(w.Octets)))
				if err != nil {
					t.Fatal(err)
				}
				if s := sha256.Sum256(b); w.DigestAlg != "SHA-256" || hex.EncodeToString(s[:]) != w.Digest {
					t.Fatalf("%s: octets do not hash to the wrapped digest", w.Type)
				}
				octets[w.Type] = b
			}
			// The informative headers decode to the same octets (the
			// transport step, checked once per file).
			for header, path := range c.Inputs.Informative {
				h, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
				if err != nil {
					t.Fatal(err)
				}
				dec, err := base64.StdEncoding.DecodeString(string(h))
				if err != nil {
					t.Fatalf("%s: %v", header, err)
				}
				typ := map[string]string{"PAYMENT-SIGNATURE": wrappedPaymentPayload, "PAYMENT-RESPONSE": wrappedSettleResponse}[header]
				if !bytes.Equal(dec, octets[typ]) {
					t.Fatalf("%s does not decode to the %s octets", header, typ)
				}
			}
			var lookup []byte
			if c.Inputs.Lookup != "" {
				if lookup, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Inputs.Lookup))); err != nil {
					t.Fatal(err)
				}
			}
			policy := policyFromVector(t, c, lookup)
			got := CheckObjects(octets[wrappedPaymentPayload], octets[wrappedSettleResponse], octets[wrappedReceipt], policy)

			// Each report must equal the expected one exactly, as JSON values.
			if len(got) != len(c.Expect.Reports) {
				t.Fatalf("%d reports, expected %d", len(got), len(c.Expect.Reports))
			}
			for i, want := range c.Expect.Reports {
				gotJSON, err := json.Marshal(got[i])
				if err != nil {
					t.Fatal(err)
				}
				var g, w any
				_ = json.Unmarshal(gotJSON, &g)
				_ = json.Unmarshal(want, &w)
				if gs, ws := canonicalJSON(g), canonicalJSON(w); gs != ws {
					t.Errorf("report %d:\n got  %s\n want %s\n (explanation: %s)", i, gs, ws, got[i].Detail)
				}
			}
			// The same through the header block form.
			block := fmt.Sprintf("PAYMENT-SIGNATURE: %s\r\nPAYMENT-RESPONSE: %s\r\n",
				base64.StdEncoding.EncodeToString(octets[wrappedPaymentPayload]), base64.StdEncoding.EncodeToString(octets[wrappedSettleResponse]))
			viaHeaders := Adapter{}.Check([]byte(block), policy)
			for i := range got {
				a, _ := json.Marshal(got[i])
				b, _ := json.Marshal(viaHeaders[i])
				if !bytes.Equal(a, b) {
					t.Errorf("header form report %d differs: %s vs %s", i, b, a)
				}
			}

			for _, req := range c.Expect.Requirements {
				rel := map[string]adapter.Relation{"is the check": adapter.IsTheCheck, "supported by the check": adapter.SupportedByTheCheck}[req.Relation]
				checks, ok := vectorRequirementChecks[req.Requirement]
				if rel == 0 || !ok {
					t.Fatalf("requirement %q (%s) not understood", req.Requirement, req.Relation)
				}
				ev := adapter.Evaluate(got, adapter.Requirement{ID: req.Requirement, Relation: rel, DependsOn: checks})
				if !strings.HasPrefix(ev.Row, fmt.Sprint(req.Row)) || ev.Status != req.Status || ev.Sufficiency != req.Sufficiency || ev.Verdict != req.Verdict {
					t.Errorf("%q: row %s %s/%s/%s, expected row %d %s/%s/%s", req.Requirement,
						ev.Row, ev.Status, ev.Sufficiency, ev.Verdict, req.Row, req.Status, req.Sufficiency, req.Verdict)
				}
			}
		})
	}
}

func canonicalJSON(v any) string {
	b, _ := json.Marshal(v) // encoding/json sorts map keys
	return string(b)
}
