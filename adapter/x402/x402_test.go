// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
// FiatToken (USDC) contracts, the worked example of EIP-712 itself (domain
// separator, struct hash, digest, and the signature by the key
// keccak256("cow")), and the published addresses of the development keys.
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
	for k, want := range map[*secp256k1.PrivateKey]string{
		testPayerKey: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
		testPayeeKey: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
	} {
		w, _ := parseAddress(want)
		if addressOf(k) != w {
			t.Errorf("development key has address %s, documented %s", addressOf(k), want)
		}
	}
}

// key names a result: the check's name, or for a wrapped content check the
// property and the wrapped type.
func key(r adapter.PropertyResult) string {
	if r.Property == PropWrappedContent {
		typ, _, _ := strings.Cut(r.Detail, ": ")
		return PropWrappedContent + "/" + typ
	}
	return adapter.CheckName(r)
}

func statuses(results []adapter.PropertyResult) map[string]adapter.Status {
	m := map[string]adapter.Status{}
	for _, r := range results {
		m[key(r)] = r.Status
	}
	return m
}

func keys(results []adapter.PropertyResult) []string {
	var out []string
	for _, r := range results {
		out = append(out, key(r))
	}
	return out
}

func failing(results []adapter.PropertyResult) []string {
	var out []string
	for _, r := range results {
		if r.Status == adapter.Fail {
			out = append(out, key(r))
		}
	}
	sort.Strings(out)
	return out
}

func find(t *testing.T, results []adapter.PropertyResult, name string) adapter.PropertyResult {
	t.Helper()
	for _, r := range results {
		if key(r) == name {
			return r
		}
	}
	t.Fatalf("%s not reported in %v", name, keys(results))
	return adapter.PropertyResult{}
}

var tableRows = []string{RowPayloadForm, RowPayerSignature, RowPayerTrust, RowAssetTrust, RowAuthorizationTerms,
	RowSettleResponseForm, RowSettleResponseJoin, RowSettleResponseSignature, RowSettlementOnchain,
	RowReceiptSignature, RowReceiptSignerAuthorized, RowDeliveryBinding}

// positiveStatuses: the positive fixture, offline (no recorded lookup).
var positiveStatuses = map[string]adapter.Status{
	RowPayloadForm: adapter.Pass, RowPayerSignature: adapter.Pass, RowPayerTrust: adapter.Pass,
	RowAssetTrust: adapter.Pass, RowAuthorizationTerms: adapter.Pass,
	RowSettleResponseForm: adapter.Pass, RowSettleResponseJoin: adapter.Pass,
	RowSettleResponseSignature: adapter.NotPresent, RowSettlementOnchain: adapter.NotChecked,
	RowReceiptSignature: adapter.Pass, RowReceiptSignerAuthorized: adapter.Pass, RowDeliveryBinding: adapter.NotChecked,
	PropWrappedContent + "/" + wrappedPaymentPayload: adapter.Pass,
	PropWrappedContent + "/" + wrappedSettleResponse: adapter.Pass,
	PropWrappedContent + "/" + wrappedReceipt:        adapter.Pass,
	PropLegAmount: adapter.Pass, PropLegNetwork: adapter.Pass,
}

func check(f *fixture, p adapter.TrustPolicy) []adapter.PropertyResult {
	return Adapter{}.Check(f.headerBlock(), p)
}

func TestPositiveShape(t *testing.T) {
	results := check(newFixture(), fixturePolicy())
	if strings.Join(keys(results), ",") != strings.Join(tableRows, ",") {
		t.Fatalf("rows %v, want the §7 table %v", keys(results), tableRows)
	}
	for _, r := range results {
		if r.Property != adapter.NativeCheck {
			t.Errorf("%s reported as property %q, want native_check", key(r), r.Property)
		}
		if r.Method == "" {
			t.Errorf("%s has no method", key(r))
		}
		if r.Status != positiveStatuses[key(r)] {
			t.Errorf("%s = %s (%s), want %s", key(r), r.Status, r.Detail, positiveStatuses[key(r)])
		}
	}
	for row, method := range map[string]string{
		RowPayloadForm:             "x402 v2 PaymentPayload: base64 (RFC 4648 §4) and JSON parse, no duplicate members",
		RowAuthorizationTerms:      "x402 exact: authorization.to = accepted.payTo, authorization.value = accepted.amount, validAfter < validBefore",
		RowSettleResponseForm:      "x402 v2 SettlementResponse: base64 and JSON parse, 5.3.2 member types",
		RowSettleResponseJoin:      "x402 v2: network = accepted.network, payer = authorization.from",
		RowSettleResponseSignature: "none: x402 v2 SettlementResponse is unsigned",
		RowReceiptSignature:        "x402 offer-receipt v1, EIP-712",
	} {
		if got := find(t, results, row).Method; got != method {
			t.Errorf("%s method %q, want %q", row, got, method)
		}
	}
	if m := find(t, results, RowPayerSignature).Method; !strings.HasPrefix(m, "EIP-712 TransferWithAuthorization (EIP-3009), secp256k1 recovery, fields from the as-transmitted payload") || !strings.Contains(m, "v 0/1 normalized to 27/28") {
		t.Errorf("payer_signature method %q", m)
	}
}

// One change to a transmitted member, under policy eoa, fails exactly the
// rows that cover it (ruling Q1, Q2), and leaves every other row as in the
// positive case unless the case says otherwise.
func TestMutations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f *fixture)
		fail   []string
		other  map[string]adapter.Status
	}{
		{"signature", func(f *fixture) { f.Signature = flipBit(f.Signature, 10) }, []string{RowPayerSignature}, nil},
		{"value", func(f *fixture) { f.Value = flipDecimal(f.Value) }, []string{RowAuthorizationTerms, RowPayerSignature}, nil},
		{"to", func(f *fixture) { f.To = flipBit(f.To, 19) }, []string{RowAuthorizationTerms, RowPayerSignature}, nil},
		{"nonce", func(f *fixture) { f.Nonce = flipBit(f.Nonce, 0) }, []string{RowPayerSignature}, nil},
		{"validBefore", func(f *fixture) { f.ValidBefore = flipDecimal(f.ValidBefore) }, []string{RowPayerSignature}, nil},
		{"window inverted", func(f *fixture) { f.ValidAfter = f.ValidBefore }, []string{RowAuthorizationTerms, RowPayerSignature}, nil},
		{"accepted.network", func(f *fixture) { f.Network = "eip155:" + flipDecimal("84532") }, []string{RowAssetTrust, RowSettleResponseJoin}, nil},
		{"accepted.asset", func(f *fixture) { f.Asset = flipBit(f.Asset, 19) }, []string{RowAssetTrust}, nil},
		{"response payer", func(f *fixture) { f.RespPayer = flipBit(f.RespPayer, 0) }, []string{RowSettleResponseJoin}, nil},
		{"response tx shape", func(f *fixture) { f.Tx = f.Tx[:65] }, []string{RowSettleResponseForm},
			map[string]adapter.Status{RowSettleResponseJoin: adapter.NotChecked, RowSettlementOnchain: adapter.NotChecked,
				RowReceiptSignature: adapter.NotPresent, RowReceiptSignerAuthorized: adapter.NotPresent, RowDeliveryBinding: adapter.NotPresent}},
		{"receipt signature", func(f *fixture) { f.ReceiptSig = flipBit(f.ReceiptSig, 3) }, []string{RowReceiptSignerAuthorized}, nil},
		{"receipt payer", func(f *fixture) { f.ReceiptPayer = flipBit(f.ReceiptPayer, 0) }, []string{RowReceiptSignerAuthorized}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture()
			c.mutate(f)
			results := check(f, eoaPolicy())
			want := append([]string{}, c.fail...)
			sort.Strings(want)
			if got := failing(results); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("FAIL on %v, want exactly %v\n%+v", got, want, results)
			}
			for k, st := range statuses(results) {
				w, ok := c.other[k]
				if !ok {
					w = positiveStatuses[k]
				}
				if st != adapter.Fail && st != w {
					t.Errorf("%s = %s, want %s", k, st, w)
				}
			}
		})
	}
}

// Ruling Q5: under the default policy (eoa-or-contract) the same signature
// flip is INCONCLUSIVE, with the ruling's detail; a recorded code lookup
// showing no code at from makes it FAIL.
func TestDefaultPolicyMismatchInconclusive(t *testing.T) {
	f := newFixture()
	f.Signature = flipBit(f.Signature, 10)
	results := check(f, fixturePolicy())
	r := find(t, results, RowPayerSignature)
	if r.Status != adapter.Inconclusive || !strings.Contains(r.Detail, "recovered address differs; `from` may be a contract account; EIP-1271 not checked") {
		t.Fatalf("%+v", r)
	}
	if adapter.AnyFail(results) {
		t.Fatalf("FAIL under the default policy: %v", failing(results))
	}
	p := fixturePolicy()
	code, _ := json.Marshal(CodeLookup{Source: "constructed", Network: fxNetwork, Address: f.From, Code: "0x"})
	p.Recorded = map[string]json.RawMessage{RecordedCodeLookup: code}
	if r := find(t, check(f, p), RowPayerSignature); r.Status != adapter.Fail {
		t.Fatalf("with a recorded no-code lookup: %+v", r)
	}
	code, _ = json.Marshal(CodeLookup{Source: "constructed", Network: fxNetwork, Address: f.From, Code: "0x6080"})
	p.Recorded = map[string]json.RawMessage{RecordedCodeLookup: code}
	if r := find(t, check(f, p), RowPayerSignature); r.Status != adapter.Inconclusive {
		t.Fatalf("with code at from: %+v", r)
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
	r := find(t, check(f, fixturePolicy()), RowPayerSignature)
	if r.Status != adapter.Fail || !strings.Contains(r.Detail, "upper half") {
		t.Fatalf("%+v", r)
	}
}

func TestVZeroOneNormalized(t *testing.T) {
	f := newFixture()
	sig := mustHex(t, f.Signature)
	sig[64] -= 27
	f.Signature = hex0x(sig)
	if r := find(t, check(f, eoaPolicy()), RowPayerSignature); r.Status != adapter.Pass {
		t.Fatalf("%+v", r)
	}
}

// Ruling Q3: the window is checked at the settling block's timestamp from a
// recorded lookup, inside settlement_onchain; offline it is NOT_CHECKED.
func TestSettlementOnchainRecorded(t *testing.T) {
	cases := []struct {
		name string
		edit func(l *TxLookup)
		want adapter.Status
	}{
		{"matches", func(l *TxLookup) {}, adapter.Pass},
		{"block after window", func(l *TxLookup) { l.BlockTimestamp = flipDecimal(l.BlockTimestamp) + "0" }, adapter.Fail},
		{"block at validBefore", func(l *TxLookup) { l.BlockTimestamp = newFixture().ValidBefore }, adapter.Fail},
		{"transfer value", func(l *TxLookup) { l.Logs[0].Value = "10001" }, adapter.Fail},
		{"authorization nonce", func(l *TxLookup) { l.Logs[1].Nonce = flipBit(l.Logs[1].Nonce, 0) }, adapter.Fail},
		{"other emitter", func(l *TxLookup) { l.Logs[0].Address = flipBit(fxAsset, 19) }, adapter.Fail},
		{"failed", func(l *TxLookup) { l.Status = "failed" }, adapter.Fail},
		{"absent", func(l *TxLookup) { l.Status = "absent" }, adapter.Fail},
		{"not final", func(l *TxLookup) { l.Final = false }, adapter.Inconclusive},
		{"another transaction", func(l *TxLookup) { l.Transaction = flipBit(l.Transaction, 0) }, adapter.NotChecked},
	}
	for _, c := range cases {
		f := newFixture()
		l := f.recordedLookup()
		c.edit(&l)
		r := find(t, check(f, withLookup(eoaPolicy(), l)), RowSettlementOnchain)
		if r.Status != c.want {
			t.Errorf("%s: %+v", c.name, r)
		}
		if !strings.Contains(r.Method, "constructed fixture") && c.want != adapter.NotChecked {
			t.Errorf("%s: method does not name the source: %q", c.name, r.Method)
		}
	}
}

// Ruling Q9: success false is a well-formed response; the outcome is
// settlement_onchain's.
func TestSuccessFalse(t *testing.T) {
	f := newFixture()
	f.Success = false
	st := statuses(check(f, fixturePolicy()))
	if st[RowSettleResponseForm] != adapter.Pass || st[RowSettlementOnchain] != adapter.NotPresent {
		t.Fatalf("%v", st)
	}
}

func TestMissingResponse(t *testing.T) {
	f := newFixture()
	f.WithResponse = false
	st := statuses(check(f, fixturePolicy()))
	for _, row := range []string{RowSettleResponseForm, RowSettleResponseJoin, RowSettleResponseSignature, RowSettlementOnchain,
		RowReceiptSignature, RowReceiptSignerAuthorized, RowDeliveryBinding} {
		if st[row] != adapter.NotPresent {
			t.Errorf("%s = %s, want NOT_PRESENT", row, st[row])
		}
	}
	if st[RowPayerSignature] != adapter.Pass {
		t.Errorf("payer_signature = %s without a response", st[RowPayerSignature])
	}
}

func TestMissingPayload(t *testing.T) {
	st := statuses(Adapter{}.Check([]byte("Content-Type: application/json\r\n"), fixturePolicy()))
	for _, row := range tableRows {
		if st[row] != adapter.NotPresent {
			t.Errorf("%s = %s, want NOT_PRESENT", row, st[row])
		}
	}
}

func TestPolicyWithoutEntries(t *testing.T) {
	st := statuses(check(newFixture(), adapter.TrustPolicy{}))
	want := map[string]adapter.Status{
		RowPayerSignature: adapter.NotChecked, RowPayerTrust: adapter.Inconclusive, RowAssetTrust: adapter.Inconclusive,
		RowAuthorizationTerms: adapter.Pass, RowReceiptSignature: adapter.Pass, RowReceiptSignerAuthorized: adapter.Inconclusive,
	}
	for p, w := range want {
		if st[p] != w {
			t.Errorf("%s = %s, want %s", p, st[p], w)
		}
	}
	p := fixturePolicy()
	p.Pins[PinPayerTrust], p.Pins[PinAssetTrust], p.Pins[PinReceiptSignerAuthorized] = "excluded", "excluded", "excluded"
	st = statuses(check(newFixture(), p))
	for _, row := range []string{RowPayerTrust, RowAssetTrust, RowReceiptSignerAuthorized} {
		if st[row] != adapter.NotChecked {
			t.Errorf("excluded %s = %s", row, st[row])
		}
	}
	p = fixturePolicy()
	p.Keys[KeyPayerRejected] = p.Keys[KeyPayer]
	if r := find(t, check(newFixture(), p), RowPayerTrust); r.Status != adapter.Fail {
		t.Errorf("rejected payer: %+v", r)
	}
}

func TestOutOfScope(t *testing.T) {
	cases := map[string]func(f *fixture){
		"erc7710": func(f *fixture) { f.TransferMethod = "erc7710" },
		"permit2": func(f *fixture) { f.TransferMethod = "permit2" },
		"v1": func(f *fixture) {
			f.Version, f.Network, f.RespNetwork, f.ReceiptFormat = 1, "base-sepolia", "base-sepolia", ""
		},
		"erc6492": func(f *fixture) { f.Signature = f.Signature + strings.Repeat("00", 31) + erc6492Magic },
	}
	for name, mutate := range cases {
		f := newFixture()
		mutate(f)
		results := check(f, eoaPolicy())
		st := statuses(results)
		if st[RowPayloadForm] != adapter.Pass {
			t.Errorf("%s: payload_form %s", name, st[RowPayloadForm])
		}
		for _, row := range []string{RowPayerSignature, RowPayerTrust, RowAssetTrust, RowAuthorizationTerms} {
			if st[row] != adapter.NotChecked {
				t.Errorf("%s: %s = %s, want NOT_CHECKED", name, row, st[row])
			}
		}
		if name == "erc7710" && !strings.Contains(find(t, results, RowPayerSignature).Detail, "possession") {
			t.Errorf("erc7710 detail does not say possession only")
		}
	}
}

func TestDuplicateMemberRejected(t *testing.T) {
	f := newFixture()
	f.DuplicateMember = true
	st := statuses(check(f, fixturePolicy()))
	if st[RowPayloadForm] != adapter.Fail || st[RowPayerSignature] != adapter.NotChecked {
		t.Fatalf("%v", st)
	}
}

func TestAbsentAuthorizationMember(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal(newFixture().payloadJSON(), &v); err != nil {
		t.Fatal(err)
	}
	delete(v["payload"].(map[string]any)["authorization"].(map[string]any), "nonce")
	b, _ := json.Marshal(v)
	st := statuses(CheckObjects(b, nil, nil, fixturePolicy()))
	if st[RowPayerSignature] != adapter.NotPresent || st[RowAuthorizationTerms] != adapter.NotPresent {
		t.Fatalf("%v", st)
	}
}

// Ruling Q7: version 1 now, and both version 2 proposals by member names.
func TestReceiptVariants(t *testing.T) {
	for _, variant := range []string{"3186", "3140"} {
		f := newFixture()
		f.ReceiptVersion, f.ReceiptVariant = 2, variant
		f.ReceiptResponseHash = hex.EncodeToString(keccak256([]byte("response body")))
		f.sign()
		results := check(f, fixturePolicy())
		r := find(t, results, RowReceiptSignature)
		if r.Status != adapter.Pass || !strings.Contains(r.Method, "#"+variant) {
			t.Fatalf("%s: %+v", variant, r)
		}
		if r := find(t, results, RowReceiptSignerAuthorized); r.Status != adapter.Pass {
			t.Fatalf("%s signer: %+v", variant, r)
		}
		if r := find(t, results, RowDeliveryBinding); r.Status != adapter.NotPresent || !strings.Contains(r.Method, "#"+variant) {
			t.Fatalf("%s delivery: %+v", variant, r)
		}
		// The added members are signed.
		f.ReceiptResponseHash = hex.EncodeToString(keccak256([]byte("other body")))
		if r := find(t, check(f, fixturePolicy()), RowReceiptSignerAuthorized); r.Status != adapter.Fail {
			t.Fatalf("%s changed hash: %+v", variant, r)
		}
	}
	for _, variant := range []string{"both", "neither"} {
		f := newFixture()
		f.ReceiptVersion, f.ReceiptVariant, f.ReceiptResponseHash = 2, variant, "ab"
		f.sign()
		st := statuses(check(f, fixturePolicy()))
		if st[RowReceiptSignature] != adapter.NotChecked || st[RowReceiptSignerAuthorized] != adapter.NotChecked {
			t.Fatalf("v2 matching %s proposal: %v", variant, st)
		}
	}
	j := newFixture()
	j.ReceiptFormat = "jws"
	if r := find(t, check(j, fixturePolicy()), RowReceiptSignature); r.Status != adapter.NotChecked {
		t.Fatalf("jws: %+v", r)
	}
	none := newFixture()
	none.ReceiptFormat = ""
	st := statuses(check(none, fixturePolicy()))
	for _, row := range []string{RowReceiptSignature, RowReceiptSignerAuthorized, RowDeliveryBinding} {
		if st[row] != adapter.NotPresent {
			t.Fatalf("absent %s: %s", row, st[row])
		}
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
		if r := find(t, Adapter{}.Check([]byte(raw), fixturePolicy()), RowPayloadForm); r.Status != adapter.Fail {
			t.Errorf("%s: %+v", name, r)
		}
	}
	bad := "PAYMENT-SIGNATURE: " + strings.SplitN(strings.SplitN(block, "PAYMENT-SIGNATURE: ", 2)[1], "\r\n", 2)[0] + "\r\nPAYMENT-RESPONSE: ***\r\n"
	st := statuses(Adapter{}.Check([]byte(bad), fixturePolicy()))
	if st[RowPayerSignature] != adapter.Pass || st[RowSettleResponseForm] != adapter.Fail {
		t.Errorf("bad response header: %v", st)
	}
}

// FR-28 and ruling S1: the same native FAIL, against a requirement that IS
// the check and one it only supports; and the mapping of NOT_PRESENT,
// NOT_CHECKED and INCONCLUSIVE.
func TestTwoLayersAndS1(t *testing.T) {
	isCheck := adapter.Requirement{ID: "payer-signed", Relation: adapter.IsTheCheck, DependsOn: []string{RowPayloadForm, RowPayerSignature}}
	supports := adapter.Requirement{ID: "resource-paid-for", Relation: adapter.SupportedByTheCheck,
		DependsOn: []string{RowPayloadForm, RowPayerSignature, RowAuthorizationTerms, RowAssetTrust}}

	f := newFixture()
	f.Signature = flipBit(f.Signature, 10)
	failed := check(f, eoaPolicy())
	if ev := adapter.Evaluate(failed, isCheck); ev.Row != "5" || ev.Status != "SATISFIED" || ev.Sufficiency != "SATISFIED" || ev.Verdict != "not_met" {
		t.Errorf("is the check: %+v", ev)
	}
	if ev := adapter.Evaluate(failed, supports); ev.Row != "6" || ev.Status != "INSUFFICIENT" || ev.Sufficiency != "INSUFFICIENT" || ev.Verdict != "not_evaluable" {
		t.Errorf("supported by the check: %+v", ev)
	}

	// Under the default policy the same flip is INCONCLUSIVE: UNKNOWN, never not_met.
	undecided := check(f, fixturePolicy())
	for _, req := range []adapter.Requirement{isCheck, supports} {
		if ev := adapter.Evaluate(undecided, req); ev.Status != "UNKNOWN" || ev.Sufficiency != "UNKNOWN" || ev.Verdict != "not_evaluable" {
			t.Errorf("%s under default policy: %+v", req.ID, ev)
		}
	}

	passed := check(newFixture(), fixturePolicy())
	if ev := adapter.Evaluate(passed, isCheck); ev.Sufficiency != "SATISFIED" || ev.Verdict != "met" {
		t.Errorf("passing check: %+v", ev)
	}
	if ev := adapter.Evaluate(passed, supports); ev.Sufficiency != "" || ev.Verdict != "" {
		t.Errorf("passing check decides a requirement it only supports: %+v", ev)
	}
	// settle_response_signature is always NOT_PRESENT: INSUFFICIENT for a
	// requirement resting on it, and it never counts against one that does
	// not (the requirement above does not list it).
	facilitatorSigned := adapter.Requirement{ID: "facilitator-signed", Relation: adapter.IsTheCheck, DependsOn: []string{RowSettleResponseSignature}}
	if ev := adapter.Evaluate(passed, facilitatorSigned); ev.Status != "INSUFFICIENT" || ev.Sufficiency != "INSUFFICIENT" || ev.Verdict != "not_evaluable" {
		t.Errorf("facilitator-signed: %+v", ev)
	}
	// Offline, settlement is NOT_CHECKED: UNKNOWN.
	settled := adapter.Requirement{ID: "settled", Relation: adapter.IsTheCheck, DependsOn: []string{RowSettleResponseForm, RowSettlementOnchain}}
	if ev := adapter.Evaluate(passed, settled); ev.Status != "UNKNOWN" || ev.Verdict != "not_evaluable" {
		t.Errorf("settled offline: %+v", ev)
	}
	// With a recorded lookup it is decided.
	recorded := check(newFixture(), withLookup(fixturePolicy(), newFixture().recordedLookup()))
	if ev := adapter.Evaluate(recorded, settled); ev.Sufficiency != "SATISFIED" || ev.Verdict != "met" {
		t.Errorf("settled with a recorded lookup: %+v", ev)
	}
}

func TestCommittedFixturesThroughCheck(t *testing.T) {
	load := func(name string) adapter.TrustPolicy {
		p, err := adapter.LoadTrustPolicy(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	def, eoa, rec := load("trust-policy.json"), load("trust-policy-eoa.json"), load("trust-policy-recorded-lookup.json")
	for file, want := range map[string][3]string{ // FAIL set under default, eoa, recorded
		"exchange-positive.http":                     {"", "", ""},
		"exchange-signature-bit-flip.http":           {"", RowPayerSignature, ""},
		"leg-payer-observed.json":                    {"", "", ""},
		"leg-payer-observed-signature-bit-flip.json": {"", RowPayerSignature, ""},
		"payment-payload.json":                       {"", "", ""},
		"payment-payload-signature-bit-flip.json":    {"", RowPayerSignature, ""},
	} {
		raw, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range []adapter.TrustPolicy{def, eoa, rec} {
			if got := strings.Join(failing(Adapter{}.Check(raw, p)), ","); got != want[i] {
				t.Errorf("%s policy %d: FAIL on %q, want %q", file, i, got, want[i])
			}
		}
	}
	raw, _ := os.ReadFile(filepath.Join("testdata", "exchange-positive.http"))
	if r := find(t, Adapter{}.Check(raw, rec), RowSettlementOnchain); r.Status != adapter.Pass {
		t.Errorf("recorded lookup: %+v", r)
	}
}
