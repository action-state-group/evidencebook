// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/action-state-group/evidencebook/adapter"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Known answers, independent of this package: Keccak-256 of the empty
// string, the EIP-3009 TRANSFER_WITH_AUTHORIZATION_TYPEHASH constant of the
// FiatToken (USDC) contracts, and the worked example of EIP-712 itself (the
// "Ether Mail" example: domain separator, struct hash, digest, and the
// signature by the key keccak256("cow")).
func TestKnownAnswers(t *testing.T) {
	if got := hex.EncodeToString(keccak256(nil)); got != "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470" {
		t.Fatalf("keccak256(\"\") = %s", got)
	}
	if got := hex.EncodeToString(keccak256([]byte(typeTransferWithAuth))); got != "7c7c6cdb67a18743f49ec6fa9b35f50d52ed05cbed4cc592e13b44501c1a2267" {
		t.Fatalf("TransferWithAuthorization typehash = %s", got)
	}

	vc, _ := parseAddress("0xCcCCccccCCCCcCCCCCCcCcCccCcCCCcCcccccccC")
	domain := tokenDomainSeparator("Ether Mail", "1", big.NewInt(1), vc)
	if got := hex.EncodeToString(domain); got != "f2cee375fa42b42143804025fc449deafd50cc031ca257e0b194a650a912090f" {
		t.Fatalf("EIP-712 example domain separator = %s", got)
	}
	cow, _ := parseAddress("0xCD2a3d9F938E13CD947Ec05AbC7FE734Df8DD826")
	bob, _ := parseAddress("0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB")
	personType := keccak256([]byte("Person(string name,address wallet)"))
	person := func(name string, w address) []byte {
		return keccak256(personType, wordString(name), wordAddress(w))
	}
	mail := keccak256(keccak256([]byte("Mail(Person from,Person to,string contents)Person(string name,address wallet)")),
		person("Cow", cow), person("Bob", bob), wordString("Hello, Bob!"))
	if got := hex.EncodeToString(mail); got != "c52c0ee5d84264471806290a3f2c4cecfc5490626bf912d01f240d7a274b371e" {
		t.Fatalf("EIP-712 example struct hash = %s", got)
	}
	digest := typedDataDigest(domain, mail)
	if got := hex.EncodeToString(digest); got != "be609aee343fb3c4b28e1df9e632fca64fcfaede20f02e86244efddf30957bd2" {
		t.Fatalf("EIP-712 example digest = %s", got)
	}
	sig := append(mustHex(t, "4355c47d63924e8a72e509b65029052eb6c299d53a04e167c5775fd466751c9d07299936d304c153f6443dfa05f40ff007d72911b6f72307f996231605b91562"), 28)
	got, err := recoverAddress(digest, sig)
	if err != nil || got != cow {
		t.Fatalf("EIP-712 example recovers to %s, %v", got, err)
	}
	if k := secp256k1.PrivKeyFromBytes(keccak256([]byte("cow"))); addressOf(k) != cow {
		t.Fatalf("keccak256(cow) key has address %s", addressOf(k))
	}
}

func statuses(results []adapter.PropertyResult) map[string]adapter.Status {
	m := map[string]adapter.Status{}
	for _, r := range results {
		m[r.Property] = r.Status
	}
	return m
}

func failing(results []adapter.PropertyResult) []string {
	var out []string
	for _, r := range results {
		if r.Status == adapter.Fail {
			out = append(out, r.Property)
		}
	}
	sort.Strings(out)
	return out
}

var positiveStatuses = map[string]adapter.Status{
	PropPayloadForm: adapter.Pass, PropPayerSignature: adapter.Pass, PropChain: adapter.Pass,
	PropAsset: adapter.Pass, PropPayeeBinding: adapter.Pass, PropAmountBinding: adapter.Pass,
	PropValidityWindow: adapter.Pass, PropSettleForm: adapter.Pass, PropSettlement: adapter.NotChecked,
	PropReceiptSignature: adapter.Pass,
}

func check(f *fixture, p adapter.TrustPolicy) []adapter.PropertyResult {
	return Adapter{}.Check(f.headerBlock(), p)
}

func TestPositive(t *testing.T) {
	results := check(newFixture(), fixturePolicy())
	var decl []adapter.Property
	for _, p := range (Adapter{}).NativeObject().Properties {
		if !legOnly[p.Name] {
			decl = append(decl, p)
		}
	}
	if len(results) != len(decl) {
		t.Fatalf("%d results for %d declared properties", len(results), len(decl))
	}
	for i, r := range results {
		if r.Property != decl[i].Name {
			t.Errorf("result %d is %s, declared %s", i, r.Property, decl[i].Name)
		}
		if r.Method == "" {
			t.Errorf("%s has no method", r.Property)
		}
		if r.Status != positiveStatuses[r.Property] {
			t.Errorf("%s = %s (%s), want %s", r.Property, r.Status, r.Detail, positiveStatuses[r.Property])
		}
	}
	if r, _ := adapter.Find(results, PropSettlement); r.Method != "tx lookup, not performed offline" {
		t.Errorf("settlement method %q", r.Method)
	}
}

// Each one-bit change to a transmitted member fails exactly the properties
// that cover it, and leaves every other property as in the positive case.
// value and to are members of the signed authorization, so changing either
// fails the signature as well as the binding to the accepted requirements.
// accepted.network is not signed by the payer, and the signature is checked
// under the pinned domain, so changing it fails chain alone.
func TestOneBitMutations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f *fixture)
		policy func(p *adapter.TrustPolicy)
		fail   []string
	}{
		{"signature", func(f *fixture) { f.Signature = flipBit(f.Signature, 10) }, nil, []string{PropPayerSignature}},
		{"value", func(f *fixture) { f.Value = flipDecimal(f.Value) }, nil, []string{PropAmountBinding, PropPayerSignature}},
		{"to", func(f *fixture) { f.To = flipBit(f.To, 19) }, nil, []string{PropPayeeBinding, PropPayerSignature}},
		{"chain", func(f *fixture) { f.Network = "eip155:" + flipDecimal("84532") }, nil, []string{PropChain}},
		{"asset", func(f *fixture) { f.Asset = flipBit(f.Asset, 19) }, nil, []string{PropAsset}},
		{"nonce", func(f *fixture) { f.Nonce = flipBit(f.Nonce, 0) }, nil, []string{PropPayerSignature}},
		{"validBefore", func(f *fixture) { f.ValidBefore = flipDecimal(f.ValidBefore) }, nil, []string{PropPayerSignature}},
		{"clock after window", nil, func(p *adapter.TrustPolicy) { p.Now = fxNow.Add(time.Hour) }, []string{PropValidityWindow}},
		{"response payer echo", func(f *fixture) { f.RespPayer = flipBit(f.RespPayer, 0) }, nil, []string{PropSettleForm}},
		{"response tx hash shape", func(f *fixture) { f.Tx = f.Tx[:65] }, nil, []string{PropSettleForm}},
		{"receipt signature", func(f *fixture) { f.ReceiptSig = flipBit(f.ReceiptSig, 3) }, nil, []string{PropReceiptSignature}},
		{"receipt payer", func(f *fixture) { f.ReceiptPayer = flipBit(f.ReceiptPayer, 0) }, nil, []string{PropReceiptSignature}},
		{"pinned payee", nil, func(p *adapter.TrustPolicy) { p.Pins[PinPayTo] = flipBit(addressOf(testPayeeKey).String(), 0) }, []string{PropPayeeBinding}},
		{"pinned amount", nil, func(p *adapter.TrustPolicy) { p.Pins[PinAmount] = "10001" }, []string{PropAmountBinding}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture()
			if c.mutate != nil {
				c.mutate(f)
			}
			p := fixturePolicy()
			if c.policy != nil {
				c.policy(&p)
			}
			results := check(f, p)
			got := failing(results)
			want := append([]string{}, c.fail...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("FAIL on %v, want exactly %v\n%+v", got, want, results)
			}
			for prop, st := range statuses(results) {
				if st != adapter.Fail && st != positiveStatuses[prop] {
					t.Errorf("%s = %s, want %s as in the positive case", prop, st, positiveStatuses[prop])
				}
			}
		})
	}
}

func TestMissingResponse(t *testing.T) {
	f := newFixture()
	f.WithResponse = false
	st := statuses(check(f, fixturePolicy()))
	for _, p := range []string{PropSettleForm, PropSettlement, PropReceiptSignature} {
		if st[p] != adapter.NotPresent {
			t.Errorf("%s = %s, want NOT_PRESENT", p, st[p])
		}
	}
	if st[PropPayerSignature] != adapter.Pass {
		t.Errorf("payer_signature = %s without a response", st[PropPayerSignature])
	}
}

func TestMissingPayload(t *testing.T) {
	results := Adapter{}.Check([]byte("Content-Type: application/json\r\n"), fixturePolicy())
	for _, r := range results {
		if r.Status != adapter.NotPresent {
			t.Errorf("%s = %s, want NOT_PRESENT", r.Property, r.Status)
		}
	}
}

func TestSettlementFailureReported(t *testing.T) {
	f := newFixture()
	f.Success = false
	st := statuses(check(f, fixturePolicy()))
	if st[PropSettleForm] != adapter.Fail || st[PropSettlement] != adapter.NotPresent || st[PropReceiptSignature] != adapter.NotPresent {
		t.Fatalf("%v", st)
	}
}

func TestHighSRejected(t *testing.T) {
	f := newFixture()
	sig := mustHex(t, f.Signature)
	s := new(big.Int).SetBytes(sig[32:64])
	s.Sub(secp256k1.S256().N, s)
	s.FillBytes(sig[32:64])
	sig[64] = 55 - sig[64] // 27 <-> 28: the same key recovers from (r, n-s, v')
	f.Signature = hex0x(sig)
	r, _ := adapter.Find(check(f, fixturePolicy()), PropPayerSignature)
	if r.Status != adapter.Fail || !strings.Contains(r.Detail, "upper half") {
		t.Fatalf("%+v", r)
	}
}

func TestVZeroOneAccepted(t *testing.T) {
	f := newFixture()
	sig := mustHex(t, f.Signature)
	sig[64] -= 27
	f.Signature = hex0x(sig)
	if r, _ := adapter.Find(check(f, fixturePolicy()), PropPayerSignature); r.Status != adapter.Pass {
		t.Fatalf("%+v", r)
	}
}

func TestPolicyWithoutPins(t *testing.T) {
	st := statuses(check(newFixture(), adapter.TrustPolicy{}))
	want := map[string]adapter.Status{
		PropPayerSignature: adapter.Inconclusive, PropChain: adapter.NotChecked, PropAsset: adapter.NotChecked,
		PropValidityWindow: adapter.NotChecked, PropReceiptSignature: adapter.Inconclusive,
		PropPayeeBinding: adapter.Pass, PropAmountBinding: adapter.Pass, PropSettleForm: adapter.Pass,
	}
	for p, w := range want {
		if st[p] != w {
			t.Errorf("%s = %s, want %s", p, st[p], w)
		}
	}
}

func TestContractPayers(t *testing.T) {
	p := fixturePolicy()
	p.Pins[PinPayerAccounts] = "eoa-or-contract"
	f := newFixture()
	f.From = addressOf(testPayeeKey).String() // a different account than the signer
	f.RespPayer = f.From
	if r, _ := adapter.Find(check(f, p), PropPayerSignature); r.Status != adapter.Inconclusive {
		t.Fatalf("mismatch under eoa-or-contract: %+v", r)
	}
	g := newFixture()
	g.Signature = g.Signature + strings.Repeat("00", 31) + erc6492Magic
	if r, _ := adapter.Find(check(g, p), PropPayerSignature); r.Status != adapter.NotChecked {
		t.Fatalf("ERC-6492 under eoa-or-contract: %+v", r)
	}
	if r, _ := adapter.Find(check(g, fixturePolicy()), PropPayerSignature); r.Status != adapter.Fail {
		t.Fatalf("ERC-6492 under eoa: %+v", r)
	}
}

func TestOtherTransferMethodsNotChecked(t *testing.T) {
	for _, m := range []string{"erc7710", "permit2"} {
		f := newFixture()
		f.TransferMethod = m
		results := check(f, fixturePolicy())
		r, _ := adapter.Find(results, PropPayerSignature)
		if r.Status != adapter.NotChecked {
			t.Fatalf("%s: %+v", m, r)
		}
		if m == "erc7710" && !strings.Contains(r.Detail, "possession") {
			t.Errorf("erc7710 detail %q", r.Detail)
		}
		if adapter.AnyFail(results) {
			t.Errorf("%s: a FAIL where nothing was checked: %v", m, failing(results))
		}
	}
}

func TestDuplicateMemberRejected(t *testing.T) {
	f := newFixture()
	f.DuplicateMember = true
	st := statuses(check(f, fixturePolicy()))
	if st[PropPayloadForm] != adapter.Fail || st[PropPayerSignature] != adapter.NotChecked {
		t.Fatalf("%v", st)
	}
}

func TestV1Payload(t *testing.T) {
	f := newFixture()
	f.Version = 1
	f.Network, f.RespNetwork = "base-sepolia", "base-sepolia"
	f.ReceiptFormat = ""
	st := statuses(check(f, fixturePolicy()))
	want := map[string]adapter.Status{
		PropPayloadForm: adapter.Pass, PropPayerSignature: adapter.Pass, PropChain: adapter.Pass,
		PropAsset: adapter.NotPresent, PropPayeeBinding: adapter.NotPresent, PropAmountBinding: adapter.NotPresent,
		PropValidityWindow: adapter.Pass, PropSettleForm: adapter.Pass, PropReceiptSignature: adapter.NotPresent,
	}
	for p, w := range want {
		if st[p] != w {
			t.Errorf("%s = %s, want %s", p, st[p], w)
		}
	}
}

func TestReceiptVariants(t *testing.T) {
	v2 := newFixture()
	v2.ReceiptVersion = 2
	v2.ReceiptResponseHash = hex0x(keccak256([]byte("response body")))
	v2.sign()
	r, _ := adapter.Find(check(v2, fixturePolicy()), PropReceiptSignature)
	if r.Status != adapter.Pass || !strings.Contains(r.Detail, "v2") {
		t.Fatalf("v2: %+v", r)
	}
	// A v2 receipt hashed under the v1 schema does not verify: the hash
	// fields are signed.
	v2.ReceiptResponseHash = hex0x(keccak256([]byte("other body")))
	if r, _ := adapter.Find(check(v2, fixturePolicy()), PropReceiptSignature); r.Status != adapter.Fail {
		t.Fatalf("v2 responseHash changed: %+v", r)
	}
	j := newFixture()
	j.ReceiptFormat = "jws"
	if r, _ := adapter.Find(check(j, fixturePolicy()), PropReceiptSignature); r.Status != adapter.NotChecked {
		t.Fatalf("jws: %+v", r)
	}
	none := newFixture()
	none.ReceiptFormat = ""
	if r, _ := adapter.Find(check(none, fixturePolicy()), PropReceiptSignature); r.Status != adapter.NotPresent {
		t.Fatalf("absent: %+v", r)
	}
	p := fixturePolicy()
	delete(p.Keys, KeyReceiptSigner)
	if r, _ := adapter.Find(check(newFixture(), p), PropReceiptSignature); r.Status != adapter.Inconclusive {
		t.Fatalf("no pinned signer: %+v", r)
	}
}

func TestHeaderBlockErrors(t *testing.T) {
	f := newFixture()
	block := string(f.headerBlock())
	for name, raw := range map[string]string{
		"duplicate header": block + "payment-signature: e30=\r\n",
		"v1 and v2":        block + "X-PAYMENT: e30=\r\n",
		"not base64":       "PAYMENT-SIGNATURE: ***\r\n",
		"not a header":     "PAYMENT-SIGNATURE\r\n",
	} {
		r, _ := adapter.Find(Adapter{}.Check([]byte(raw), fixturePolicy()), PropPayloadForm)
		if r.Status != adapter.Fail {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

// FR-28: the same native FAIL, evaluated against a requirement that IS the
// payment check and against one the check only supports, maps to what each
// predicate implies and nothing more.
func TestTwoLayers(t *testing.T) {
	isCheck := adapter.Requirement{ID: "payer-authorized-payment", Relation: adapter.IsTheCheck,
		DependsOn: []string{PropPayerSignature}}
	supports := adapter.Requirement{ID: "resource-paid-for", Relation: adapter.SupportedByTheCheck,
		DependsOn: []string{PropPayerSignature, PropChain, PropAsset, PropPayeeBinding, PropAmountBinding}}

	f := newFixture()
	f.Signature = flipBit(f.Signature, 10)
	failed := check(f, fixturePolicy())
	if r, _ := adapter.Find(failed, PropPayerSignature); r.Status != adapter.Fail {
		t.Fatalf("precondition: %+v", r)
	}
	if ev := adapter.Evaluate(failed, isCheck); ev.Row != "5" || ev.Sufficiency != "SATISFIED" || ev.Verdict != "not_met" {
		t.Errorf("requirement that is the check: %+v", ev)
	}
	if ev := adapter.Evaluate(failed, supports); ev.Row != "6" || ev.Sufficiency != "INSUFFICIENT" || ev.Verdict != "not_evaluable" {
		t.Errorf("requirement the check supports: %+v", ev)
	}

	passed := check(newFixture(), fixturePolicy())
	if ev := adapter.Evaluate(passed, isCheck); ev.Sufficiency != "SATISFIED" || ev.Verdict != "met" {
		t.Errorf("passing check, requirement that is the check: %+v", ev)
	}
	if ev := adapter.Evaluate(passed, supports); ev.Sufficiency != "" || ev.Verdict != "" {
		t.Errorf("passing check decides a requirement it only supports: %+v", ev)
	}
	// Settlement is never established offline: a requirement that depends on
	// it is not_evaluable even when every signature passed.
	settled := adapter.Requirement{ID: "payment-settled", Relation: adapter.IsTheCheck, DependsOn: []string{PropSettlement}}
	if ev := adapter.Evaluate(passed, settled); ev.Sufficiency != "UNKNOWN" || ev.Verdict != "not_evaluable" {
		t.Errorf("settlement requirement: %+v", ev)
	}
}

func TestCommittedFixturesThroughCheck(t *testing.T) {
	p, err := adapter.LoadTrustPolicy(filepath.Join("testdata", "trust-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	pos, err := os.ReadFile(filepath.Join("testdata", "exchange-positive.http"))
	if err != nil {
		t.Fatal(err)
	}
	if res := (Adapter{}).Check(pos, p); adapter.AnyFail(res) {
		t.Fatalf("positive fixture fails: %v", failing(res))
	}
	neg, err := os.ReadFile(filepath.Join("testdata", "exchange-signature-bit-flip.http"))
	if err != nil {
		t.Fatal(err)
	}
	if got := failing(Adapter{}.Check(neg, p)); strings.Join(got, ",") != PropPayerSignature {
		t.Fatalf("bit-flip fixture fails %v", got)
	}
}
