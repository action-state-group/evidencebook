// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/action-state-group/agent-action-capsule/go/canonical"

	"github.com/action-state-group/evidencebook/adapter"
)

var update = flag.Bool("update", false, "rewrite testdata fixtures")

// DEVELOPMENT KEYS, published: the default accounts #0 and #1 of the
// Hardhat Network and Foundry Anvil local development chains, derived from
// the public mnemonic "test test test test test test test test test test
// test junk" and listed in both tools' documentation. Anyone can sign with
// them; they are used here only to construct fixtures. The fixtures are
// constructed, not recorded: no payment was made.
var (
	testPayerKey = mustKey("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80") // account #0, 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
	testPayeeKey = mustKey("59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d") // account #1, 0x70997970C51812dc3A010C7d01b50e0d17dc79C8
)

func mustKey(h string) *secp256k1.PrivateKey {
	b, err := hex.DecodeString(h)
	if err != nil {
		panic(err)
	}
	return secp256k1.PrivKeyFromBytes(b)
}

// The fixture's chain and token are Base Sepolia (testnet) USDC, as in the
// x402 reference default-asset table.
const (
	fxNetwork = "eip155:84532"
	fxAsset   = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
	fxName    = "USDC"
	fxVersion = "2"
	fxURL     = "https://api.example.com/premium-data"
)

// fxNow is the fixture's settlement time: the block timestamp of the
// recorded lookup, the receipt's issuedAt and the leg's observed_at.
var fxNow = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func addressOf(k *secp256k1.PrivateKey) address { return pubKeyAddress(k.PubKey()) }

// sign65 signs digest and returns r||s||v with v in {27, 28}.
func sign65(k *secp256k1.PrivateKey, digest []byte) []byte {
	compact := ecdsa.SignCompact(k, digest, false) // [27+recid][r][s]
	out := make([]byte, 65)
	copy(out, compact[1:])
	out[64] = compact[0]
	return out
}

func hex0x(b []byte) string { return "0x" + hex.EncodeToString(b) }

// fixture is one x402 exchange. The signatures are computed over the fields
// when sign is called; a mutation changes a transmitted field afterwards.
type fixture struct {
	Version                                  int
	Network, Asset, Name, TokenVersion       string
	AcceptedAmount, AcceptedPayTo            string
	From, To, Value, ValidAfter, ValidBefore string
	Nonce, Signature                         string
	WithResponse                             bool
	Success                                  bool
	Tx, RespNetwork, RespPayer               string
	ReceiptFormat                            string // "", "eip712", "jws"
	ReceiptVersion                           int
	ReceiptNetwork, ReceiptPayer, ReceiptTx  string
	ReceiptSig                               string
	ReceiptResponseHash                      string
	ReceiptVariant                           string // "" (per version), "3186", "3140"
	NoWrapReceipt                            bool   // the leg does not wrap x402.receipt on its own
	DuplicateMember                          bool
	ExtraPayload                             string // raw JSON members appended into payload (e.g. permissionContext)
	TransferMethod                           string // accepted.extra.assetTransferMethod, "" for absent
	LegAmount, LegNetwork                    string // settlement.amount.value, settlement.payment_ref.network
}

func newFixture() *fixture {
	payer, payee := addressOf(testPayerKey).String(), addressOf(testPayeeKey).String()
	sum := keccak256([]byte("fixture nonce"))
	tx := keccak256([]byte("fixture transaction (not a real transaction)"))
	f := &fixture{
		Version: 2, Network: fxNetwork, Asset: fxAsset, Name: fxName, TokenVersion: fxVersion,
		AcceptedAmount: "10000", AcceptedPayTo: payee,
		From: payer, To: payee, Value: "10000",
		ValidAfter:   fmt.Sprint(fxNow.Unix() - 60),
		ValidBefore:  fmt.Sprint(fxNow.Unix() + 240),
		Nonce:        hex0x(sum),
		WithResponse: true, Success: true, Tx: hex0x(tx), RespNetwork: fxNetwork, RespPayer: payer,
		ReceiptFormat: "eip712", ReceiptVersion: 1, ReceiptNetwork: fxNetwork, ReceiptPayer: payer, ReceiptTx: hex0x(tx),
		LegAmount: "10000", LegNetwork: fxNetwork,
	}
	f.sign()
	return f
}

func (f *fixture) authorization() authorization {
	from, _ := parseAddress(f.From)
	to, _ := parseAddress(f.To)
	v, _ := parseUint256(f.Value)
	va, _ := parseUint256(f.ValidAfter)
	vb, _ := parseUint256(f.ValidBefore)
	n, _ := parseBytes32(f.Nonce)
	return authorization{From: from, To: to, Value: v, ValidAfter: va, ValidBefore: vb, Nonce: n}
}

// sign signs the payer authorization (under the token domain named in the
// fixture) and the receipt.
func (f *fixture) sign() {
	cid, _ := chainID(f.Network)
	contract, _ := parseAddress(f.Asset)
	d := typedDataDigest(tokenDomainSeparator(f.Name, f.TokenVersion, cid, contract), f.authorization().structHash())
	f.Signature = hex0x(sign65(testPayerKey, d))
	if f.ReceiptFormat == "eip712" {
		v, _ := decodeStrict(f.receiptPayloadJSON())
		payload := obj(v.(map[string]any))
		variant, err := selectVariant(payload)
		if err != nil {
			variant = receiptV1 // an unsupported receipt: sign anything
		}
		h, err := receiptStructHash(payload, variant)
		if err != nil {
			panic(err)
		}
		f.ReceiptSig = hex0x(sign65(testPayeeKey, typedDataDigest(receiptDomain(variant.domainVersion), h)))
	}
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

func (f *fixture) payloadJSON() []byte {
	var b bytes.Buffer
	auth := fmt.Sprintf(`{"from":%s,"to":%s,"value":%s,"validAfter":%s,"validBefore":%s,"nonce":%s}`,
		q(f.From), q(f.To), q(f.Value), q(f.ValidAfter), q(f.ValidBefore), q(f.Nonce))
	inner := fmt.Sprintf(`"signature":%s,"authorization":%s`, q(f.Signature), auth)
	if f.ExtraPayload != "" {
		inner += "," + f.ExtraPayload
	}
	if f.Version == 1 {
		fmt.Fprintf(&b, `{"x402Version":1,"scheme":"exact","network":%s,"payload":{%s}}`, q(f.Network), inner)
		return b.Bytes()
	}
	extra := fmt.Sprintf(`"name":%s,"version":%s`, q(f.Name), q(f.TokenVersion))
	if f.TransferMethod != "" {
		extra = fmt.Sprintf(`"assetTransferMethod":%s,`, q(f.TransferMethod)) + extra
	}
	accepted := fmt.Sprintf(`{"scheme":"exact","network":%s,"amount":%s,"asset":%s,"payTo":%s,"maxTimeoutSeconds":300,"extra":{%s}}`,
		q(f.Network), q(f.AcceptedAmount), q(f.Asset), q(f.AcceptedPayTo), extra)
	dup := ""
	if f.DuplicateMember {
		dup = `"x402Version":2,`
	}
	fmt.Fprintf(&b, `{"x402Version":2,%s"resource":{"url":%s,"description":"Test resource","mimeType":"application/json"},"accepted":%s,"payload":{%s}}`,
		dup, q(fxURL), accepted, inner)
	return b.Bytes()
}

func (f *fixture) receiptPayloadJSON() []byte {
	extra := ""
	if f.ReceiptVersion == 2 && f.ReceiptVariant == "both" {
		extra = fmt.Sprintf(`,"responseHash":%s,"contentHash":%s`, q(f.ReceiptResponseHash), q(f.ReceiptResponseHash))
	} else if f.ReceiptVersion == 2 && f.ReceiptVariant == "neither" {
		extra = ""
	} else if f.ReceiptVersion == 2 && f.ReceiptVariant == "3140" {
		extra = fmt.Sprintf(`,"commitmentId":"","settlementUnbound":false,"contentHash":%s,"contentHashAlg":"sha256","requestHash":"","receiptRef":""`, q(f.ReceiptResponseHash))
	} else if f.ReceiptVersion == 2 {
		extra = fmt.Sprintf(`,"responseHash":%s,"responseHashAlg":"sha256","responseHashEncoding":"raw"`, q(f.ReceiptResponseHash))
	}
	return fmt.Appendf(nil, `{"version":%d,"network":%s,"resourceUrl":%s,"payer":%s,"issuedAt":%d,"transaction":%s%s}`,
		f.ReceiptVersion, q(f.ReceiptNetwork), q(fxURL), q(f.ReceiptPayer), fxNow.Unix(), q(f.ReceiptTx), extra)
}

// receiptEnvelope is the receipt object {format, payload, signature}.
func (f *fixture) receiptEnvelope() []byte {
	return fmt.Appendf(nil, `{"format":"eip712","payload":%s,"signature":%s}`, f.receiptPayloadJSON(), q(f.ReceiptSig))
}

// receiptOctets is the registered octets of x402.receipt (EIP-712 format):
// the RFC 8785 form of the envelope.
func (f *fixture) receiptOctets() []byte {
	v, err := decodeStrict(f.receiptEnvelope())
	if err != nil {
		panic(err)
	}
	b, err := canonical.JCS(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (f *fixture) responseJSON() []byte {
	ext := ""
	switch f.ReceiptFormat {
	case "eip712":
		ext = fmt.Sprintf(`,"extensions":{"offer-receipt":{"info":{"receipt":%s}}}`, f.receiptEnvelope())
	case "jws":
		ext = `,"extensions":{"offer-receipt":{"info":{"receipt":{"format":"jws","signature":"eyJhbGciOiJFUzI1NksiLCJraWQiOiJkaWQ6d2ViOmFwaS5leGFtcGxlLmNvbSNrZXktMSJ9.e30.c2ln"}}}}`
	}
	if !f.Success {
		return fmt.Appendf(nil, `{"success":false,"errorReason":"insufficient_funds","transaction":"","network":%s,"payer":%s}`, q(f.RespNetwork), q(f.RespPayer))
	}
	return fmt.Appendf(nil, `{"success":true,"transaction":%s,"network":%s,"payer":%s%s}`, q(f.Tx), q(f.RespNetwork), q(f.RespPayer), ext)
}

// headerBlock is the exchange as the relying party received it.
func (f *fixture) headerBlock() []byte {
	ph, rh := "PAYMENT-SIGNATURE", "PAYMENT-RESPONSE"
	if f.Version == 1 {
		ph, rh = "X-PAYMENT", "X-PAYMENT-RESPONSE"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s: %s\r\n", ph, base64.StdEncoding.EncodeToString(f.payloadJSON()))
	if f.WithResponse {
		fmt.Fprintf(&b, "%s: %s\r\n", rh, base64.StdEncoding.EncodeToString(f.responseJSON()))
	}
	return b.Bytes()
}

// legWrappedEntry is a settlement-records wrapped entry for octets.
func legWrappedEntry(typ string, octets []byte) map[string]any {
	sum := sha256.Sum256(octets)
	return map[string]any{
		"type":       typ,
		"digest_alg": "SHA-256",
		"digest":     hex.EncodeToString(sum[:]),
		"content":    base64.RawURLEncoding.EncodeToString(octets),
	}
}

// legJSON is a payer_observed leg (draft-mih-agent-settlement-records-00)
// wrapping the payment payload and, when present, the settlement response.
// Only the members the adapter reads are realistic: the Capsule envelope
// (capsule_id, signature) is out of the adapter's scope and is omitted.
func (f *fixture) legJSON() []byte {
	wrapped := []any{legWrappedEntry(wrappedPaymentPayload, f.payloadJSON())}
	if f.WithResponse {
		wrapped = append(wrapped, legWrappedEntry(wrappedSettleResponse, f.responseJSON()))
	}
	if f.ReceiptFormat == "eip712" && !f.NoWrapReceipt {
		wrapped = append(wrapped, legWrappedEntry(wrappedReceipt, f.receiptOctets()))
	}
	assetCode := "eip155:84532/erc20:" + strings.ToLower(fxAsset)
	leg := map[string]any{
		"action_id":           "settle-fixture-payer-constructed-not-recorded",
		"action_type":         "fyi",
		"canonicalization_id": "jcs",
		"format_version":      "4",
		"settlement": map[string]any{
			"version":     "0",
			"leg":         "payer_observed",
			"sealer_role": "payer",
			"terms_ref":   hex.EncodeToString(keccak256([]byte("fixture terms leg"))),
			"payment_ref": map[string]any{"type": "x402.transaction", "value": strings.ToLower(f.Tx), "network": f.LegNetwork},
			"amount":      map[string]any{"value": f.LegAmount, "assetCode": assetCode, "assetScale": 6},
			"status":      "settled",
			"observed_at": fxNow.Format(time.RFC3339),
			"wrapped":     wrapped,
		},
	}
	b, err := json.MarshalIndent(leg, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

// fixturePolicy is the default policy: payer accounts eoa-or-contract,
// offline (no recorded lookup).
func fixturePolicy() adapter.TrustPolicy {
	return adapter.TrustPolicy{
		Pins: map[string]string{
			PinChain: fxNetwork, PinAsset: fxAsset, PinAssetName: fxName, PinAssetVersion: fxVersion,
		},
		Keys: map[string][]string{
			KeyPayer:         {addressOf(testPayerKey).String()},
			KeyReceiptSigner: {addressOf(testPayeeKey).String()},
		},
	}
}

// eoaPolicy is fixturePolicy with payer accounts "eoa": the policy the
// negative fixtures are evaluated under, so their FAIL is a real FAIL.
func eoaPolicy() adapter.TrustPolicy {
	p := fixturePolicy()
	p.Pins[PinPayerAccounts] = "eoa"
	return p
}

// recordedLookup is a constructed (not recorded) transaction lookup that
// matches the fixture.
func (f *fixture) recordedLookup() TxLookup {
	return TxLookup{
		Source: "constructed fixture (not a recorded lookup)", Network: fxNetwork, Transaction: f.Tx,
		Status: "success", Final: true, BlockTimestamp: fmt.Sprint(fxNow.Unix()),
		Logs: []TxLog{
			{Address: fxAsset, Event: "Transfer", From: f.From, To: f.To, Value: f.Value},
			{Address: fxAsset, Event: "AuthorizationUsed", Authorizer: f.From, Nonce: f.Nonce},
		},
	}
}

func withLookup(p adapter.TrustPolicy, l TxLookup) adapter.TrustPolicy {
	b, err := json.Marshal(l)
	if err != nil {
		panic(err)
	}
	p.Recorded = map[string]json.RawMessage{RecordedTxLookup: b}
	return p
}

// flipBit flips the lowest bit of byte i of a 0x-hex string's decoded bytes.
func flipBit(h string, i int) string {
	b, _ := hex.DecodeString(h[2:])
	b[i] ^= 0x01
	return hex0x(b)
}

// flipDecimal flips the lowest bit of a decimal integer.
func flipDecimal(s string) string {
	n, _ := new(big.Int).SetString(s, 10)
	return new(big.Int).Xor(n, big.NewInt(1)).String()
}

// The committed fixtures the CLI demo and docs run on. go test -update
// rewrites them; otherwise the test requires them to be byte-identical to
// what the generator produces (signing is deterministic, RFC 6979).
func TestFixturesUpToDate(t *testing.T) {
	positive := newFixture()
	mutated := newFixture()
	mutated.Signature = flipBit(mutated.Signature, 10)
	marshal := func(p adapter.TrustPolicy) []byte {
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return append(b, '\n')
	}
	files := map[string][]byte{
		"exchange-positive.http":                     positive.headerBlock(),
		"exchange-signature-bit-flip.http":           mutated.headerBlock(),
		"trust-policy.json":                          marshal(fixturePolicy()),
		"trust-policy-eoa.json":                      marshal(eoaPolicy()),
		"trust-policy-recorded-lookup.json":          marshal(withLookup(fixturePolicy(), positive.recordedLookup())),
		"leg-payer-observed.json":                    positive.legJSON(),
		"leg-payer-observed-signature-bit-flip.json": mutated.legJSON(),
		"payment-payload.json":                       positive.payloadJSON(),
		"payment-payload-signature-bit-flip.json":    mutated.payloadJSON(),
	}
	for name, want := range files {
		path := filepath.Join("testdata", name)
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run go test ./adapter/x402 -run TestFixturesUpToDate -update)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the generator's output (run with -update)", path)
		}
	}
}
