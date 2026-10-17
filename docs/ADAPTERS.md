# Foreign-evidence adapters

An adapter checks an object produced by another protocol under that
protocol's own verification method, over the octets as the protocol delivered
them. It never re-signs, re-encodes or canonicalizes the object. Holding a
foreign object, or its digest, proves possession of those octets and nothing
more. A check reaches `PASS` only when the adapter ran the native check and
the check succeeded.

Adapters live in `adapter/` (the interface) and one package per protocol
(`adapter/x402`). They make no network calls. A check that needs one reads a
recording the relying party supplies, and is `NOT_CHECKED` without one.

## The interface

```go
type Adapter interface {
	Name() string
	NativeObject() NativeObject                               // the declaration, as data
	Check(raw []byte, policy TrustPolicy) []PropertyResult    // one report per check
}
```

- `NativeObject()` returns the adapter's declaration: (a) each native object
  and its format, (b) its issuer and how the relying party resolves it, (c)
  the native verification method, (d) the exact predicate a passing check
  supports, with the statements it does not support, and (e) the checks
  reported, each with its method.
- `Check` reads `raw`, the octets as received, and returns property reports
  `{property, status, method, detail}` (Minimum Evidence Interoperability
  Profile, `agent-action-capsule` `spec/interop-profile-v0.md` §7.1). The
  status is one of `PASS`, `FAIL`, `NOT_PRESENT`, `NOT_CHECKED` and
  `INCONCLUSIVE`.
- **Report shape.** The profile defines one property per foreign object,
  `native_check` (§7.2). An adapter that splits the native check into named
  checks reports each one as a `native_check` report, with the check's name
  and its object in a `detail` object. This is the shape of the adapter
  declarations, for example:

  ```json
  {"property": "native_check", "status": "FAIL", "method": "…", "detail": {"check": "payer_signature", "object": "x402.payment-payload"}}
  ```

  - `adapter.NativeCheckReport` builds such a report.
  - `adapter.CheckName` reads the check's name back.
  - The report also keeps a human-readable explanation in
    `PropertyResult.Detail`. It is not part of the report's JSON; the CLI
    adds it under `-explain`.
  - `adapter.NativeCheckResult` (text detail `"<check>: <explanation>"`)
    remains for adapters written against it.
  - A check that is not a native check (for example, a carrier's digest
    check) keeps its own property name.
- `TrustPolicy` is data:
  - `pins`: values the relying party fixes in advance;
  - `keys`: accepted key identifiers per issuer role;
  - `recorded`: the relying party's own recorded observations, such as an
    on-chain lookup made earlier through a source it trusts;
  - `now`: a clock.

  One JSON file can serve every adapter. Each adapter names the pins, roles
  and recordings it reads.

### From a check to a requirement

A check's result never says whether a requirement is satisfied.
`adapter.Evaluate` maps the results a requirement rests on, matched by check
name, to that requirement's status, sufficiency and verdict, and nothing
more. It assumes the envelope carrying the object has already verified
(profile §8.1). A failed envelope makes the object unusable for every
requirement.

| Native results the requirement rests on | Requirement **is** the check | Requirement is only **supported** by the check |
|---|---|---|
| any `FAIL` | `SATISFIED` / `SATISFIED` / `not_met` (row 5) | `INSUFFICIENT` / `INSUFFICIENT` / `not_evaluable` (row 6), unless other evidence settles it |
| any `NOT_PRESENT`, including an object that has no native signature | `INSUFFICIENT` / `INSUFFICIENT` / `not_evaluable` | the same |
| any `NOT_CHECKED` or `INCONCLUSIVE` | `UNKNOWN` / `UNKNOWN` / `not_evaluable` | the same |
| all `PASS` | `SATISFIED` / `SATISFIED` / `met` | left to the contract's evaluation of the content (row 7) |

The `NOT_PRESENT`, `NOT_CHECKED` and `INCONCLUSIVE` rows are mapped as profile
§8.1 rows 3-4, pending a profile row. They never give `met` or `not_met`. A
check the requirement does not rest on never counts for or against it. In
particular, the `NOT_PRESENT` of an object that has no native signature never
counts against a requirement that does not rest on that signature.

## x402

The adapter implements the x402 adapter declaration v0 (`agent-action-capsule`
`spec/adapters/x402-v0.md`, at main `3c778daa`, which folds in the rulings on its
first implementation). It reproduces the declaration's vectors
(`vectors/adapters/x402/`, the same commit) exactly: `TestAACVectors`.

### What it reads

x402 version 2, scheme `exact`, an `eip155` network, and the `eip3009`
transfer method. Anything else is read and reported `NOT_CHECKED` (out of
scope):

- x402 version 1;
- other schemes, and networks outside `eip155`;
- the `permit2` and `erc7710` transfer methods. ERC-7710 carries no payer
  signature that can be checked offline, so holding it proves possession only;
- signatures that are not 65 bytes, such as contract-account signatures.

The input is detected as one of three forms:

1. **A settlement-records leg.** A JSON Agent Action Capsule with a
   `settlement` member (`draft-mih-agent-settlement-records-00`, The Leg
   Record).
   - The adapter takes the `settlement.wrapped[]` entries of type
     `x402.payment-payload`, `x402.settle-response` and `x402.receipt`.
   - It decodes each entry's `content` (base64url without padding) and runs
     the draft's content check (Wrapping Existing Signed Objects, rule 2). It
     reports that as `wrapped_content_check`, with the wrapped type in
     `detail`. A mismatch is `FAIL`, with `wrapped_digest_mismatch` in
     `detail`.
   - It then runs the native checks on the decoded octets.
   - It compares the leg's `settlement.amount.value` and
     `settlement.payment_ref.network` with the payment payload's `accepted`
     members (`leg_amount`, `leg_network`).
   - These leg properties are not native checks and keep their own property
     names.
   - The Capsule's own envelope is verified by a Capsule verifier, not here.
   - An entry with a digest and no `content` proves possession of a digest
     only.
2. **The bare payment payload.** The base64-decoded `PAYMENT-SIGNATURE` value.
   No settlement response is held, so the response and receipt checks are
   `NOT_PRESENT`.
3. **An HTTP header block**, one `Name: value` per line, as received:

   ```
   PAYMENT-SIGNATURE: <base64 of the PaymentPayload JSON>
   PAYMENT-RESPONSE: <base64 of the SettlementResponse JSON>
   ```

   Names are case-insensitive, and other headers are ignored. Decoding a
   header is a transport step. A value that does not decode, or a malformed
   or repeated header, yields no octets, and the object is reported as not
   held (`NOT_PRESENT`, with the reason in the explanation).

Input that starts with `{` is JSON: a leg if it has a `settlement` member,
otherwise the bare payload. Anything else is read as a header block.

The receipt is its own object (`x402.receipt`, issued by the payee). In a leg
it can be wrapped on its own. Otherwise the adapter uses the one inside the
settlement response, at `extensions["offer-receipt"].info.receipt`.

### Declaration

The objects, issuers and predicates are those of the declaration (§4, §5, §8):

- **Payment payload.** Signed by the payer. The signer address is recovered,
  and the relying party's policy binds it to a party or not.
- **Settlement response.** Unsigned: possession and transport-level
  provenance only. Settlement rests on a transaction lookup on the chain that
  the response names.
- **Receipt.** Signed by the payee. The relying party's policy decides whether
  the signer is authorized for `resourceUrl`.

The adapter supports predicates X1 to X5 of the declaration's §8 and nothing
more. `go run ./cmd/evidencebook adapt x402 -declare` prints the declaration
as data.

### Checks

Every row of the declaration's §7 table is reported as a `native_check`
report, in this order. `detail` is `{"check": <row>, "object": <wrapped type>}`,
plus `variant` on receipt rows. `method` is the table's text, and the policy
rows report `relying party policy <id>`, with the id taken from the pin
`policy.id`.

| Check | Object | Notes |
|---|---|---|
| `payload_form` | `x402.payment-payload` | JSON parse of the registered octets, no duplicate members |
| `payer_signature` | `x402.payment-payload` | EIP-712 `TransferWithAuthorization` recovery, compared to `authorization.from`. The domain is the policy's entry for `accepted.network` and `accepted.asset`, and is `NOT_CHECKED` without one. `v` 0/1 is normalized to 27/28, and `s` must be low. A mismatch is `INCONCLUSIVE` under the default policy, and `FAIL` under policy `eoa` or when the recorded lookup shows no code at `from` |
| `payer_trust` | `x402.payment-payload` | the policy binds (`PASS`), rejects (`FAIL`) or holds no binding for (`INCONCLUSIVE`) `authorization.from` |
| `asset_trust` | `x402.payment-payload` | the policy has a domain for `accepted.asset` on `accepted.network` (`PASS`), rejects the pair (`FAIL`), or has no entry (`INCONCLUSIVE`) |
| `authorization_terms` | `x402.payment-payload` | `to = payTo`, `value = amount` |
| `authorization_window` | `x402.payment-payload` | `validAfter < t < validBefore`, `t` the settling block's timestamp from the recorded lookup. `NOT_CHECKED` offline, `NOT_PRESENT` without a transaction |
| `settle_response_form` | `x402.settle-response` | JSON parse and 5.3.2 member types. A response stating `success: false` is well formed (`PASS`) |
| `settle_response_join` | `x402.settle-response` | `network = accepted.network`, `payer = authorization.from` |
| `settle_response_signature` | `x402.settle-response` | always `NOT_PRESENT`: there is no signature to check |
| `settlement_onchain` | `x402.settle-response` | the recorded lookup: receipt status success, and `Transfer` and `AuthorizationUsed` emitted by `accepted.asset` matching the authorization. `NOT_CHECKED` offline, `NOT_PRESENT` when `transaction` is empty |
| `receipt_signature` | `x402.receipt` | EIP-712 recovery. Version 1, or version 2 under #3186 or #3140, selected by member names. JWS is `NOT_CHECKED` |
| `receipt_signer_authorized` | `x402.receipt` | the recovered signer is listed under `x402.receipt_signer` |
| `delivery_binding` | `x402.receipt` | version 2 only. The response body is not held in v0, so a v2 hash is `NOT_PRESENT` |

A leg adds `wrapped_content_check` (once per wrapped x402 type, before that
object's checks), `leg_amount` and `leg_network`.

A change to `authorization.value` or `authorization.to` fails both
`payer_signature` (the members are signed) and `authorization_terms`. A
changed `accepted.network` or `accepted.asset` names a pair the policy has no
domain for. `asset_trust` is then `INCONCLUSIVE` and `payer_signature` is
`NOT_CHECKED`, and a changed network also fails `settle_response_join`.

### Trust policy

| Name | Kind | Meaning |
|---|---|---|
| `policy.id` | pin | the policy's name, reported in the policy rows' `method` |
| `x402.chain`, `x402.asset`, `x402.asset.name`, `x402.asset.version` | pin | the policy's one domain entry: the (network, token) pair, and the token's EIP-712 name and version. Its `chainId` is the network's reference and its `verifyingContract` is the token |
| `x402.payer_accounts` | pin | `eoa-or-contract` (default) or `eoa` |
| `x402.payer_trust`, `x402.asset_trust`, `x402.receipt_signer_authorized` | pin | `excluded` gives `NOT_CHECKED` |
| `x402.payer`, `x402.payer_rejected` | keys | payer addresses bound to a party, or rejected |
| `x402.asset_rejected` | keys | `<network>/<asset>` pairs the policy rejects |
| `x402.receipt_signer` | keys | addresses authorized to sign receipts |
| `x402.lookup` | recorded | the recorded chain observation, below |

The recorded chain observation is the relying party's own; the adapter never
fetches it. It has the shape of the agent-action-capsule vectors'
`lookup.json`: the transaction receipt and the block, as a chain node returns
them, and the code at the payer's address.

```json
{
  "network": "eip155:84532",
  "source": "the node or service the relying party read the chain from",
  "transaction_receipt": {
    "transactionHash": "0x…", "blockNumber": "0x…", "status": "0x1",
    "logs": [{"address": "0x036C…CF7e", "topics": ["0x…", "0x…", "0x…"], "data": "0x…"}]
  },
  "block": {"number": "0x…", "timestamp": "0x…"},
  "code_at_payer": "0x"
}
```

- A `transaction_receipt` of `null` means the transaction is absent.
- An optional `"final": false` makes `settlement_onchain` `INCONCLUSIVE`.
- A recording of another transaction or network is ignored, which is the
  same as offline.

### Checking a recorded payment later

The authorization's validity window is checked against the settling block's
timestamp, from the recorded lookup. It is never checked against the time of
evaluation, which would fail every historical payment. A payment replayed days
later is therefore checked exactly as on the day it settled. Without a
recorded lookup, `settlement_onchain` and `authorization_window` are
`NOT_CHECKED`. A demo policy for a replay:

```json
{
  "pins": {
    "policy.id": "demo-replay",
    "x402.chain": "eip155:84532",
    "x402.asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    "x402.asset.name": "USDC",
    "x402.asset.version": "2",
    "x402.payer_accounts": "eoa"
  },
  "keys": {
    "x402.payer": ["0x<payer address>"],
    "x402.receipt_signer": ["0x<payee receipt signer>"]
  },
  "recorded": {
    "x402.lookup": {"network": "eip155:84532", "source": "…", "transaction_receipt": {"…": "…"}, "block": {"…": "…"}, "code_at_payer": "0x"}
  }
}
```

Leave out `x402.payer_accounts` for the default (`eoa-or-contract`). Leave out
`recorded` to check offline.

### Running it

```
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy.json adapter/x402/testdata/exchange-positive.http
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy.json adapter/x402/testdata/payment-payload.json
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy-recorded-lookup.json adapter/x402/testdata/leg-payer-observed.json
go run ./cmd/evidencebook adapt x402 -policy adapter/x402/testdata/trust-policy-eoa.json adapter/x402/testdata/leg-payer-observed-signature-bit-flip.json
go run ./cmd/evidencebook adapt x402 -explain -policy adapter/x402/testdata/trust-policy-eoa.json - < adapter/x402/testdata/payment-payload-signature-bit-flip.json
go run ./cmd/evidencebook adapt x402 -declare
```

Flags come before the file, and `-` reads standard input. The command prints
`{"adapter": "x402", "properties": [...]}` and exits 0 when no report is
`FAIL`, 1 when any is, and 2 on a usage or input error. `-explain` adds each
report's human-readable `explanation` beside the report members.

The fixtures in `adapter/x402/testdata/` are constructed, not recorded.
`adapter/x402/testdata/README.md` lists them and the published development
keys they are signed with. `go test ./adapter/x402 -run TestFixturesUpToDate
-update` regenerates them. The negative fixtures are meant for policy `eoa`,
under which a changed signature is `FAIL`. Under the default policy the same
fixture is `INCONCLUSIVE`.

The agent-action-capsule vectors (`vectors/adapters/x402/`) are vendored byte
for byte in `adapter/x402/testdata/aac-vectors/`, checked against their
`SHA256SUMS`. `TestAACVectors` requires every expected report to be
reproduced exactly, and the §8.1 mapping of each listed requirement. The
vectors are pinned to agent-action-capsule main `3c778daa`.

### Dependencies

`golang.org/x/crypto/sha3` (Keccak-256, BSD-3-Clause) and
`github.com/decred/dcrd/dcrec/secp256k1/v4` (public-key recovery, ISC).
