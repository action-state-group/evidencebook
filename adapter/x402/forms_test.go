// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook/adapter"
)

var legKeys = []string{
	PropWrappedContent + "/" + wrappedPaymentPayload,
	RowPayloadForm, RowPayerSignature, RowPayerTrust, RowAssetTrust, RowAuthorizationTerms, RowAuthorizationWindow,
	PropWrappedContent + "/" + wrappedSettleResponse,
	RowSettleResponseForm, RowSettleResponseJoin, RowSettleResponseSignature, RowSettlementOnchain,
	PropWrappedContent + "/" + wrappedReceipt,
	RowReceiptSignature, RowReceiptSignerAuthorized, RowDeliveryBinding,
	PropLegAmount, PropLegNetwork,
}

func TestFormsPropertyLists(t *testing.T) {
	f := newFixture()
	for name, c := range map[string]struct {
		raw  []byte
		want []string
	}{
		"header block": {f.headerBlock(), tableRows},
		"bare payload": {f.payloadJSON(), tableRows},
		"leg":          {f.legJSON(), legKeys},
	} {
		got := keys(Adapter{}.Check(c.raw, fixturePolicy()))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestLegPositive(t *testing.T) {
	for _, r := range (Adapter{}).Check(newFixture().legJSON(), fixturePolicy()) {
		if want := positiveStatuses[key(r)]; r.Status != want {
			t.Errorf("%s = %s (%s), want %s", key(r), r.Status, r.Detail, want)
		}
		native := r.Property == adapter.NativeCheck
		leg := r.Property == PropWrappedContent || r.Property == PropLegAmount || r.Property == PropLegNetwork
		if native == leg {
			t.Errorf("%s: property %q", key(r), r.Property)
		}
	}
}

func TestBarePayload(t *testing.T) {
	st := statuses(Adapter{}.Check(newFixture().payloadJSON(), fixturePolicy()))
	for _, row := range tableRows {
		w := positiveStatuses[row]
		if row == RowAuthorizationWindow || row == RowSettleResponseForm || row == RowSettleResponseJoin || row == RowSettlementOnchain ||
			row == RowReceiptSignature || row == RowReceiptSignerAuthorized || row == RowDeliveryBinding {
			w = adapter.NotPresent
		}
		if st[row] != w {
			t.Errorf("%s = %s, want %s", row, st[row], w)
		}
	}
}

// The demo negative: one signature byte flipped inside the leg's content,
// with the digest recomputed. Under policy eoa: the content check passes,
// payer_signature fails, nothing else moves. Under the default policy the
// same leg is INCONCLUSIVE (ruling Q5).
func TestLegSignatureFlipDigestRecomputed(t *testing.T) {
	f := newFixture()
	f.Signature = flipBit(f.Signature, 10)
	results := Adapter{}.Check(f.legJSON(), eoaPolicy())
	if got := failing(results); strings.Join(got, ",") != RowPayerSignature {
		t.Fatalf("FAIL on %v, want exactly payer_signature", got)
	}
	for k, st := range statuses(results) {
		if k != RowPayerSignature && st != positiveStatuses[k] {
			t.Errorf("%s = %s, positive %s", k, st, positiveStatuses[k])
		}
	}
	if r := find(t, Adapter{}.Check(f.legJSON(), fixturePolicy()), RowPayerSignature); r.Status != adapter.Inconclusive {
		t.Fatalf("default policy: %+v", r)
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
	return Adapter{}.Check(b, eoaPolicy())
}

func TestLegMutations(t *testing.T) {
	entry := func(s map[string]any, i int) map[string]any {
		return s["wrapped"].([]any)[i].(map[string]any)
	}
	wcPay, wcResp, wcRcpt := PropWrappedContent+"/"+wrappedPaymentPayload, PropWrappedContent+"/"+wrappedSettleResponse, PropWrappedContent+"/"+wrappedReceipt
	payloadAbsent := map[string]adapter.Status{RowPayloadForm: adapter.NotPresent, RowPayerSignature: adapter.NotPresent,
		RowPayerTrust: adapter.NotPresent, RowAssetTrust: adapter.NotPresent, RowAuthorizationTerms: adapter.NotPresent,
		RowSettleResponseJoin: adapter.NotPresent, RowAuthorizationWindow: adapter.NotPresent,
		PropLegAmount: adapter.NotChecked, PropLegNetwork: adapter.NotChecked}
	with := func(m map[string]adapter.Status, k string, v adapter.Status) map[string]adapter.Status {
		out := map[string]adapter.Status{k: v}
		for a, b := range m {
			out[a] = b
		}
		return out
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
		}, map[string]adapter.Status{wcPay: adapter.Fail, RowPayerSignature: adapter.Fail}},
		{"leg amount", func(s map[string]any) { s["amount"].(map[string]any)["value"] = "10001" },
			map[string]adapter.Status{PropLegAmount: adapter.Fail}},
		{"leg network", func(s map[string]any) { s["payment_ref"].(map[string]any)["network"] = "eip155:8453" },
			map[string]adapter.Status{PropLegNetwork: adapter.Fail}},
		{"response digest", func(s map[string]any) { entry(s, 1)["digest"] = strings.Repeat("0", 64) },
			map[string]adapter.Status{wcResp: adapter.Fail}},
		{"receipt digest", func(s map[string]any) { entry(s, 2)["digest"] = strings.Repeat("0", 64) },
			map[string]adapter.Status{wcRcpt: adapter.Fail}},
		{"digest only", func(s map[string]any) { delete(entry(s, 0), "content") }, with(payloadAbsent, wcPay, adapter.NotPresent)},
		{"padded content", func(s map[string]any) { entry(s, 0)["content"] = entry(s, 0)["content"].(string) + "=" }, with(payloadAbsent, wcPay, adapter.Fail)},
		{"no response wrapped", func(s map[string]any) { s["wrapped"] = []any{entry(s, 0), entry(s, 2)} },
			map[string]adapter.Status{wcResp: adapter.NotPresent, RowSettleResponseForm: adapter.NotPresent,
				RowSettleResponseJoin: adapter.NotPresent, RowSettlementOnchain: adapter.NotPresent, RowAuthorizationWindow: adapter.NotPresent}},
		{"no receipt wrapped", func(s map[string]any) { s["wrapped"] = s["wrapped"].([]any)[:2] },
			map[string]adapter.Status{wcRcpt: adapter.NotPresent}}, // the receipt in the response is used
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k, got := range statuses(legMutate(t, c.edit)) {
				want, ok := c.want[k]
				if !ok {
					want = positiveStatuses[k]
				}
				if got != want {
					t.Errorf("%s = %s, want %s", k, got, want)
				}
			}
		})
	}
}

func TestJSONNeitherForm(t *testing.T) {
	if r := find(t, Adapter{}.Check([]byte(`{"hello":1}`), fixturePolicy()), RowPayloadForm); r.Status != adapter.Fail {
		t.Fatalf("%+v", r)
	}
	if r := find(t, Adapter{}.Check([]byte(`{"x402Version":2,`), fixturePolicy()), RowPayloadForm); r.Status != adapter.Fail {
		t.Fatalf("truncated JSON: %+v", r)
	}
}
