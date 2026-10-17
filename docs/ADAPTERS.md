# Foreign-evidence adapters

An adapter checks an object produced by another protocol under that
protocol's own verification method, over the octets as the protocol delivered
them. It never re-signs, re-encodes or canonicalizes the object. Holding a
foreign object, or its digest, proves possession of those octets and nothing
more. A property reaches `PASS` only when the adapter ran the native check and
the check succeeded.

Adapters live in `adapter/` (the interface) and one package per protocol
(`adapter/x402`). They make no network calls. A check that needs one is
reported `NOT_CHECKED`.

## The interface

```go
type Adapter interface {
	Name() string
	NativeObject() NativeObject                               // the declaration, as data
	Check(raw []byte, policy TrustPolicy) []PropertyResult    // one result per declared property
}
```

- `NativeObject()` returns the adapter's declaration: (a) each native object
  and its format, (b) its issuer and how the relying party resolves it, (c)
  the native verification method, (d) the exact predicate a passing check
  supports, with the statements it does not support, and (e) the properties
  reported, each with its method.
- `Check` reads `raw`, the octets as received, and reports every declared
  property in declaration order as `{property, status, method, detail}`. The
  status is one of `PASS`, `FAIL`, `NOT_PRESENT`, `NOT_CHECKED` and
  `INCONCLUSIVE`, with the meanings of the Minimum Evidence Interoperability
  Profile (`agent-action-capsule` `spec/interop-profile-v0.md` §7.1).
- `TrustPolicy` is data: a clock (`now`), `pins` (values the relying party fixes
  in advance, compared with what was transmitted and never taken from it), and
  `keys` (accepted key identifiers per issuer role). One JSON file can serve
  every adapter; each adapter names the pins and roles it reads.

### From a property to a requirement

A per-property result never says whether a requirement is satisfied.
`adapter.Evaluate` maps the results a requirement depends on to that
requirement's sufficiency and verdict, and nothing more. It assumes the
envelope carrying the object has already verified (profile §8.1; a failed
envelope makes the object unusable for every requirement).

| Native results the requirement depends on | Requirement **is** the check | Requirement is only **supported** by the check |
|---|---|---|
| any `FAIL` | `SATISFIED`, `not_met` (row 5) | `INSUFFICIENT`, `not_evaluable` (row 6), unless other evidence settles it |
| any `NOT_PRESENT` | `INSUFFICIENT`, `not_evaluable` | `INSUFFICIENT`, `not_evaluable` |
| any `NOT_CHECKED` or `INCONCLUSIVE` | `UNKNOWN`, `not_evaluable` | `UNKNOWN`, `not_evaluable` |
| all `PASS` | `SATISFIED`, `met` | left to the contract's evaluation of the content (row 7) |

A failed check never yields `met`. For example, a payer signature that fails
decides "did the payer authorize this payment?" (`not_met`), but it does not
establish that a resource was not paid for: that requirement becomes
`not_evaluable`.

## x402

### What it reads

An x402 v2 payment, scheme `exact`, on an EVM chain, with the EIP-3009
transfer method (the default for tokens such as USDC). The input is detected
as one of three forms.

1. **A settlement-records leg.** A JSON Agent Action Capsule with a
   `settlement` member (`draft-mih-agent-settlement-records-00`, The Leg
   Record), typically the `payer_observed` leg. The adapter takes the
   `settlement.wrapped[]` entry of type `x402.payment-payload`, decodes its
   `content` (base64url without padding), and checks it against `digest`
   (SHA-256, lowercase hex) before reading it. It does the same for an
   `x402.settle-response` entry if one is present. It then compares
   `settlement.amount.value` with the payload's `accepted.amount` and
   `settlement.payment_ref.network` with `accepted.network`. The Capsule's
   own envelope (`capsule_id`, signature) is verified by a Capsule verifier,
   not by this adapter. An entry that carries a digest but no `content`
   proves possession of a digest only, and nothing in it is checked natively.
2. **The bare payment payload.** The base64-decoded `PAYMENT-SIGNATURE`
   value, a JSON object with `x402Version`. There is no settlement response,
   so the response properties are `NOT_PRESENT`.
3. **An HTTP header block**, one `Name: value` per line, as received:

   ```
   PAYMENT-SIGNATURE: <base64 of the PaymentPayload JSON>
   PAYMENT-RESPONSE: <base64 of the SettlementResponse JSON>
   ```

   Names are case-insensitive; other headers are ignored; a repeated header
   is an error. `PAYMENT-RESPONSE` is optional.

Input that starts with `{` is read as JSON: a leg if it has a `settlement`
member, otherwise the bare payload. Anything else is read as a header block. The x402 v1 headers `X-PAYMENT` and
`X-PAYMENT-RESPONSE` are read too. A v1 payload carries no asset, payee or
amount of its own, so those properties are `NOT_PRESENT` unless the policy
pins them.

### Declaration

**(a) Native objects and format.**

- The payment payload: the base64-decoded `PAYMENT-SIGNATURE` value, a JSON
  `PaymentPayload` whose `payload` is `{signature, authorization}`, with
  `authorization = {from, to, value, validAfter, validBefore, nonce}`.
- The settlement response: the base64-decoded `PAYMENT-RESPONSE` value, a JSON
  `SettlementResponse` `{success, transaction, network, payer, ...}`.
- Optionally, inside the response, a receipt from the offer-and-receipt
  extension (`extensions["offer-receipt"].info.receipt`), format `eip712` or
  `jws`.

**(b) Issuers and how they are resolved.**

- The payment payload is the payer's. The payer is whoever holds the key that
  the signature recovers to. Nothing resolves an identity beyond that address.
  The chain, the token contract and the token's EIP-712 name and version come
  from the trust policy, never from the payload.
- The settlement response is the facilitator's, relayed by the resource
  server. It is not signed, so the facilitator is not identified
  cryptographically.
- A receipt is the payee's (the resource server). Its signer must be listed
  in the trust policy. How the relying party chose that address (the payee's
  `payTo` key, a DNS record, a registry) is outside the adapter.

**(c) Native verification method.**

- Payment payload: secp256k1 public-key recovery over the EIP-712 digest of
  `TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)`
  under the domain `{name, version, chainId, verifyingContract}`, with the
  members exactly as transmitted. The recovered address must equal
  `authorization.from`. As the token contracts require, `s` must be in the
  lower half of the curve order.
- Settlement response: structure only. Settlement itself is a transaction
  lookup on chain, which the adapter does not perform.
- Receipt: secp256k1 public-key recovery over the EIP-712 digest of the
  extension's `Receipt` type under the domain `{name: "x402 receipt",
  version: "1", chainId: 1}`. Payload version 1 is the published schema.
  Version 2 adds `responseHash`, `responseHashAlg` and `responseHashEncoding`,
  from a proposed upstream revision that is not yet merged. JWS receipts are
  not checked in this revision.

**(d) The predicate supported.** When every payment-payload property passes:
the holder of the key for `authorization.from` authorized a transfer of
exactly `authorization.value` units of the pinned token contract on the
pinned chain to `authorization.to`, valid strictly between `validAfter` and
`validBefore`, usable once under its `nonce`. When the receipt check passes:
the pinned payee signer stated that `payer` paid for `resourceUrl`.

It does **not** support: that the transfer settled on chain; who the
facilitator is, or that it reported truthfully; that the resource was
delivered; any identity of the payer beyond the address. A payment that uses
the Permit2 or ERC-7710 transfer method, or that a contract account signed, is
not checked natively (`NOT_CHECKED`). Under ERC-7710, x402 carries no payer
signature a verifier can check offline, and holding the payload proves
possession only.

**(e) Per-property results.**

| Property | Method | Notes |
|---|---|---|
| `payment_payload_form` | base64, one JSON object with no duplicate member names, the x402 members | `FAIL` if unreadable |
| `payer_signature` | EIP-712 recovery under the pinned domain, compared to `authorization.from` | `INCONCLUSIVE` if the policy does not pin the domain |
| `chain` | transmitted network (CAIP-2) against the pin `x402.chain` | the payer's signature is checked under the pin, so a changed network fails here alone |
| `asset` | transmitted token contract and EIP-712 name/version against the pins | |
| `payee_binding` | `authorization.to` against `accepted.payTo`, and `x402.pay_to` when pinned | `to` is signed: changing it also fails `payer_signature` |
| `amount_binding` | `authorization.value` against `accepted.amount`, and `x402.amount` when pinned | `value` is signed: changing it also fails `payer_signature` |
| `validity_window` | `validAfter < now < validBefore`, `now` from the policy | `NOT_CHECKED` without a clock |
| `settle_response_form` | success, a 32-byte transaction hash, the pinned network, `payer` equal to `authorization.from` | structure only; a `success: false` response is `FAIL` |
| `settlement` | `tx lookup, not performed offline` | always `NOT_CHECKED` when a transaction hash is present |
| `payee_receipt_signature` | EIP-712 recovery, signer against `x402.receipt_signer` | `NOT_PRESENT` without a receipt; `INCONCLUSIVE` if no signer is pinned |

A leg adds four properties. They are reported for the leg form only.

| Property | Method | Notes |
|---|---|---|
| `wrapped_digest` | SHA-256 over the exact decoded `content` octets of the `x402.payment-payload` entry, compared to `digest` | `NOT_PRESENT` without the entry or its `content`. A mismatch is `FAIL`, and the octets held are still checked natively |
| `settle_response_wrapped_digest` | the same, for an `x402.settle-response` entry | `NOT_PRESENT` when the leg wraps no response |
| `leg_amount` | `settlement.amount.value` against `accepted.amount` | |
| `leg_network` | `settlement.payment_ref.network` (the `x402.transaction` qualifier) against `accepted.network` | |

A `wrapped_digest` `PASS` says only that the leg commits to the octets it
carries. A leg that wraps a payload whose signature was altered, with the
digest recomputed, reports `wrapped_digest` `PASS` and `payer_signature`
`FAIL`: the carrier is intact and the native check fails.

Properties per form:

- Header block and bare payload: `payment_payload_form`, `payer_signature`,
  `chain`, `asset`, `payee_binding`, `amount_binding`, `validity_window`,
  `settle_response_form`, `settlement`, `payee_receipt_signature`.
- Leg: `wrapped_digest`, then the ten above, with
  `settle_response_wrapped_digest` before `settle_response_form`, then
  `leg_amount` and `leg_network`.

### Trust policy keys

| Key | Kind | Meaning |
|---|---|---|
| `x402.chain` | pin | CAIP-2 network accepted, e.g. `eip155:84532` |
| `x402.asset` | pin | token contract address |
| `x402.asset.name`, `x402.asset.version` | pin | the token's EIP-712 domain name and version |
| `x402.pay_to`, `x402.amount` | pin, optional | the expected payee and atomic amount |
| `x402.payer_accounts` | pin, optional | `eoa` (default) or `eoa-or-contract`. Under `eoa-or-contract`, a signature that does not recover to the payer is `INCONCLUSIVE` and a non-65-byte signature is `NOT_CHECKED`, because a contract account's signature is checked on chain |
| `x402.receipt_signer` | keys | addresses accepted as receipt signers |

### A policy for replaying a recorded payment

An authorization is valid only inside its window, which is usually a few
minutes long. Replaying a recorded payment later therefore needs the clock
pinned to the time of the payment, for example the leg's `observed_at` or the
time the resource server received the payment, and not to the time of the
replay:

```json
{
  "now": "2026-10-17T12:00:00Z",
  "pins": {
    "x402.chain": "eip155:84532",
    "x402.asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    "x402.asset.name": "USDC",
    "x402.asset.version": "2",
    "x402.pay_to": "0x<payee address>",
    "x402.amount": "10000"
  },
  "keys": {
    "x402.receipt_signer": ["0x<payee receipt signer>"]
  }
}
```

With `now` omitted, `validity_window` is `NOT_CHECKED`. With `now` set to
the replay time, after the window has closed, it is `FAIL`, which reports
that the authorization was not usable at that time.

### Running it

```
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy.json adapter/x402/testdata/exchange-positive.http
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy.json adapter/x402/testdata/exchange-signature-bit-flip.http
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy.json adapter/x402/testdata/leg-payer-observed.json
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy.json adapter/x402/testdata/payment-payload.json
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy.json - < adapter/x402/testdata/payment-payload.json
go run ./cmd/evidencebook adapt x402 -declare
```

Flags come before the file. `-` reads standard input. The command prints the per-property results as
JSON and exits 0 when no property is `FAIL`, 1 when any is, and 2 on a usage or
input error.

The fixtures in `adapter/x402/testdata/` are generated by
`adapter/x402/fixture_test.go` with throwaway test keys that have never held
funds (Base Sepolia USDC as the pinned token): one positive file per input
form, and a `-signature-bit-flip` copy of each with one byte of the payer's
signature changed (for the leg, with `digest` recomputed). `go test ./adapter/x402 -run
TestFixturesUpToDate -update` rewrites them; without `-update` the test
requires them to be byte-identical to the generator's output. The tests change
one bit of the signature, value, recipient, network, asset, nonce, validity
bound, response and receipt in turn and require a `FAIL` on exactly the
properties that cover it. `TestKnownAnswers` checks the Keccak-256 and EIP-712
code against the EIP-712 worked example and the EIP-3009 type hash.

Conformance vectors for this adapter are expected in `agent-action-capsule`
(`vectors/adapters/x402/`). `TestAACVectors` is skipped until they are
published and pinned.

### Dependencies

`golang.org/x/crypto/sha3` (Keccak-256, BSD-3-Clause) and
`github.com/decred/dcrd/dcrec/secp256k1/v4` (public-key recovery, ISC).
