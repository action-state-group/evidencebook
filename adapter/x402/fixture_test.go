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

	"github.com/action-state-group/evidencebook/adapter"
)

var update = flag.Bool("update", false, "rewrite testdata fixtures")

// TEST KEYS. Throwaway keys derived from public labels, for fixtures only.
// They have never held funds on any chain and must never be funded.
var (
	testPayerKey = secp256k1.PrivKeyFromBytes(keccak256([]byte("evidencebook x402 adapter TEST payer key (throwaway, never funded)")))
	testPayeeKey = secp256k1.PrivKeyFromBytes(keccak256([]byte("evidencebook x402 adapter TEST payee key (throwaway, never funded)")))
)

// The fixture's chain and token are Base Sepolia (testnet) USDC, as in the
// x402 reference default-asset table.
const (
	fxNetwork = "eip155:84532"
	fxAsset   = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
	fxName    = "USDC"
	fxVersion = "2"
	fxURL     = "https://api.example.com/premium-data"
)

// fxNow is the relying party's clock in the fixture policy.
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
		h, _, err := receiptStructHash(obj(v.(map[string]any)))
		if err != nil {
			panic(err)
		}
		f.ReceiptSig = hex0x(sign65(testPayeeKey, typedDataDigest(receiptDomainSeparator(), h)))
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
	if f.ReceiptVersion == 2 {
		extra = fmt.Sprintf(`,"responseHash":%s,"responseHashAlg":"sha256","responseHashEncoding":"raw"`, q(f.ReceiptResponseHash))
	}
	return fmt.Appendf(nil, `{"version":%d,"network":%s,"resourceUrl":%s,"payer":%s,"issuedAt":%d,"transaction":%s%s}`,
		f.ReceiptVersion, q(f.ReceiptNetwork), q(fxURL), q(f.ReceiptPayer), fxNow.Unix(), q(f.ReceiptTx), extra)
}

func (f *fixture) responseJSON() []byte {
	ext := ""
	switch f.ReceiptFormat {
	case "eip712":
		ext = fmt.Sprintf(`,"extensions":{"offer-receipt":{"info":{"receipt":{"format":"eip712","payload":%s,"signature":%s}}}}`,
			f.receiptPayloadJSON(), q(f.ReceiptSig))
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
	assetCode := "eip155:84532/erc20:" + strings.ToLower(fxAsset)
	leg := map[string]any{
		"action_id":           "settle-fixture-payer",
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

func fixturePolicy() adapter.TrustPolicy {
	return adapter.TrustPolicy{
		Now: fxNow,
		Pins: map[string]string{
			PinChain: fxNetwork, PinAsset: fxAsset, PinAssetName: fxName, PinAssetVersion: fxVersion,
		},
		Keys: map[string][]string{KeyReceiptSigner: {addressOf(testPayeeKey).String()}},
	}
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
	policy, err := json.MarshalIndent(fixturePolicy(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"exchange-positive.http":                     positive.headerBlock(),
		"exchange-signature-bit-flip.http":           mutated.headerBlock(),
		"trust-policy.json":                          append(policy, '\n'),
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
