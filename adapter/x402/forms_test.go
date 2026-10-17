// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

func props(results []adapter.PropertyResult) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.Property)
	}
	return out
}

var (
	headerProps = []string{PropPayloadForm, PropPayerSignature, PropChain, PropAsset, PropPayeeBinding,
		PropAmountBinding, PropValidityWindow, PropSettleForm, PropSettlement, PropReceiptSignature}
	legProps = []string{PropWrappedDigest, PropPayloadForm, PropPayerSignature, PropChain, PropAsset, PropPayeeBinding,
		PropAmountBinding, PropValidityWindow, PropSettleWrappedDigest, PropSettleForm, PropSettlement,
		PropReceiptSignature, PropLegAmount, PropLegNetwork}
)

func TestFormsPropertyLists(t *testing.T) {
	f := newFixture()
	for name, c := range map[string]struct {
		raw  []byte
		want []string
	}{
		"header block": {f.headerBlock(), headerProps},
		"bare payload": {f.payloadJSON(), headerProps},
		"leg":          {f.legJSON(), legProps},
	} {
		got := props(Adapter{}.Check(c.raw, fixturePolicy()))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestLegPositive(t *testing.T) {
	results := Adapter{}.Check(newFixture().legJSON(), fixturePolicy())
	for _, r := range results {
		want, ok := positiveStatuses[r.Property]
		if legOnly[r.Property] {
			want, ok = adapter.Pass, true
		}
		if !ok || r.Status != want {
			t.Errorf("%s = %s (%s), want %s", r.Property, r.Status, r.Detail, want)
		}
	}
}

func TestBarePayload(t *testing.T) {
	st := statuses(Adapter{}.Check(newFixture().payloadJSON(), fixturePolicy()))
	for p, w := range positiveStatuses {
		switch p {
		case PropSettleForm, PropSettlement, PropReceiptSignature:
			w = adapter.NotPresent
		}
		if st[p] != w {
			t.Errorf("%s = %s, want %s", p, st[p], w)
		}
	}
}

// The demo negative: one signature byte flipped inside the leg's content,
// with the digest recomputed so the leg still binds the content. The
// wrapped digest passes and the native check fails, nothing else moves.
func TestLegSignatureFlipDigestRecomputed(t *testing.T) {
	f := newFixture()
	f.Signature = flipBit(f.Signature, 10)
	results := Adapter{}.Check(f.legJSON(), fixturePolicy())
	if got := failing(results); strings.Join(got, ",") != PropPayerSignature {
		t.Fatalf("FAIL on %v, want exactly payer_signature", got)
	}
	positive := statuses(Adapter{}.Check(newFixture().legJSON(), fixturePolicy()))
	for p, st := range statuses(results) {
		if p != PropPayerSignature && st != positive[p] {
			t.Errorf("%s = %s, positive %s", p, st, positive[p])
		}
	}
	if r, _ := adapter.Find(results, PropWrappedDigest); r.Status != adapter.Pass {
		t.Fatalf("wrapped_digest %+v", r)
	}
}

func legMutate(t *testing.T, edit func(settlement map[string]any)) []adapter.PropertyResult {
	t.Helper()
	var leg map[string]any
	if err := json.Unmarshal(newFixture().legJSON(), &leg); err != nil {
		t.Fatal(err)
	}
	edit(leg["settlement"].(map[string]any))
	b, err := json.Marshal(leg)
	if err != nil {
		t.Fatal(err)
	}
	return Adapter{}.Check(b, fixturePolicy())
}

func TestLegMutations(t *testing.T) {
	entry := func(s map[string]any, i int) map[string]any {
		return s["wrapped"].([]any)[i].(map[string]any)
	}
	cases := []struct {
		name string
		edit func(s map[string]any)
		want map[string]adapter.Status
	}{
		{"content flipped, digest kept", func(s map[string]any) {
			f := newFixture()
			f.Signature = flipBit(f.Signature, 10)
			entry(s, 0)["content"] = legWrappedEntry(wrappedPaymentPayload, f.payloadJSON())["content"]
		}, map[string]adapter.Status{PropWrappedDigest: adapter.Fail, PropPayerSignature: adapter.Fail}},
		{"leg amount", func(s map[string]any) { s["amount"].(map[string]any)["value"] = "10001" },
			map[string]adapter.Status{PropLegAmount: adapter.Fail}},
		{"leg network", func(s map[string]any) { s["payment_ref"].(map[string]any)["network"] = "eip155:8453" },
			map[string]adapter.Status{PropLegNetwork: adapter.Fail}},
		{"response digest", func(s map[string]any) { entry(s, 1)["digest"] = strings.Repeat("0", 64) },
			map[string]adapter.Status{PropSettleWrappedDigest: adapter.Fail}},
		{"digest only", func(s map[string]any) { delete(entry(s, 0), "content") },
			map[string]adapter.Status{PropWrappedDigest: adapter.NotPresent, PropPayloadForm: adapter.NotPresent,
				PropPayerSignature: adapter.NotPresent, PropLegAmount: adapter.NotChecked, PropLegNetwork: adapter.NotChecked,
				PropChain: adapter.NotPresent, PropAsset: adapter.NotPresent, PropPayeeBinding: adapter.NotPresent,
				PropAmountBinding: adapter.NotPresent, PropValidityWindow: adapter.NotPresent}},
		{"padded content", func(s map[string]any) { entry(s, 0)["content"] = entry(s, 0)["content"].(string) + "=" },
			map[string]adapter.Status{PropWrappedDigest: adapter.Fail, PropPayloadForm: adapter.NotPresent,
				PropPayerSignature: adapter.NotPresent, PropLegAmount: adapter.NotChecked, PropLegNetwork: adapter.NotChecked,
				PropChain: adapter.NotPresent, PropAsset: adapter.NotPresent, PropPayeeBinding: adapter.NotPresent,
				PropAmountBinding: adapter.NotPresent, PropValidityWindow: adapter.NotPresent}},
		{"no response wrapped", func(s map[string]any) { s["wrapped"] = s["wrapped"].([]any)[:1] },
			map[string]adapter.Status{PropSettleWrappedDigest: adapter.NotPresent, PropSettleForm: adapter.NotPresent,
				PropSettlement: adapter.NotPresent, PropReceiptSignature: adapter.NotPresent}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := statuses(legMutate(t, c.edit))
			for p, got := range st {
				want, ok := c.want[p]
				if !ok {
					want = positiveStatuses[p]
					if legOnly[p] {
						want = adapter.Pass
					}
				}
				if got != want {
					t.Errorf("%s = %s, want %s", p, got, want)
				}
			}
		})
	}
}

func TestCommittedFormFixtures(t *testing.T) {
	p, err := adapter.LoadTrustPolicy(filepath.Join("testdata", "trust-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	for file, wantFail := range map[string]string{
		"leg-payer-observed.json":                    "",
		"leg-payer-observed-signature-bit-flip.json": PropPayerSignature,
		"payment-payload.json":                       "",
		"payment-payload-signature-bit-flip.json":    PropPayerSignature,
	} {
		raw, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(failing(Adapter{}.Check(raw, p)), ","); got != wantFail {
			t.Errorf("%s: FAIL on %q, want %q", file, got, wantFail)
		}
	}
}

func TestJSONNeitherForm(t *testing.T) {
	r, _ := adapter.Find(Adapter{}.Check([]byte(`{"hello":1}`), fixturePolicy()), PropPayloadForm)
	if r.Status != adapter.Fail {
		t.Fatalf("%+v", r)
	}
	r, _ = adapter.Find(Adapter{}.Check([]byte(`{"x402Version":2,`), fixturePolicy()), PropPayloadForm)
	if r.Status != adapter.Fail {
		t.Fatalf("truncated JSON: %+v", r)
	}
}
