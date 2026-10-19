# Foreign-evidence adapters

An adapter checks an object produced by another protocol under that
protocol's own verification method, over the octets as the protocol delivered
them. It never re-signs or re-encodes the object, and it canonicalizes the
object only where the native method itself does (UCP's JCS). Holding a
foreign object, or its digest, proves possession of those octets and nothing
more. A check reaches `PASS` only when the adapter ran the native check and
the check succeeded.

Adapters live in `adapter/` (the interface) and one package per protocol
(`adapter/x402`, `adapter/acp`, `adapter/ucp`). They make no network calls. A check that needs one reads a
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
  `INCONCLUSIVE`. A method or detail is plain text: a template such as
  `relying party policy <id>` keeps its `<` and `>` in the JSON.
- Every adapter reads foreign JSON with one reader, `adapter/internal/ijson`,
  as I-JSON (RFC 7493): invalid UTF-8, an escaped lone surrogate, a
  duplicate member name in any object or trailing data makes the input
  unreadable. The same package holds the adapters' one RFC 8785 serializer.
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
  - `key_sets`: key sets the relying party retained, each as the JSON value
    it was served as (for example a JWK Set), kept as written so the
    adapter's strict reader sees the octets;
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

## ACP

The Agentic Commerce Protocol, API version 2026-04-17, as declared in
`agent-action-capsule` `spec/adapters/acp-v0.md` (commit `2ef8aeb4`), read at
ACP commit `7fdd78df677a94dce04c770644b0fbbb1401272b`.

ACP 2026-04-17 defines no signature by the issuer over the checkout session,
the order, or the delegated payment token. The response reaches its caller over
TLS, and nothing in the octets authenticates it to anyone else. The order
webhook's `Merchant-Signature` is an HMAC under a secret the merchant and the
receiving platform share, so either of them could have produced it. This
adapter therefore supports **possession of exact octets and nothing more**.
Its `native_check` is never `PASS` or `FAIL`, and the tests go red if it ever
is.

### What it reads

| Object | Identified by | Validated against |
|---|---|---|
| checkout session | a `status`, `line_items`, `totals` or `currency` member, no `order` | JS-CHK `$defs/CheckoutSession` |
| completed session with its order (order path 1) | as above, with an `order` member | JS-CHK `$defs/CheckoutSessionWithOrder`, whole |
| order event (order path 2) | `type` and `data` members | OAS-WH `WebhookEvent`, `data` as OAS-WH `EventDataOrder` |
| delegated payment token | `created` or `metadata`, none of the above | JS-DP `$defs/DelegatePaymentResponse` |

An `Order` on its own (it has `checkout_session_id` or `permalink_url`) is
rejected. ACP delivers an order only inside a completed session or as an
order event's `data` (acp-v0 §2.2 (a)).

The input forms are detected from the content:

- a settlement-records leg (a JSON object with a `settlement` member) whose
  `settlement.wrapped[]` holds one entry of type `acp.checkout-session`,
  `acp.order-event` or `acp.delegate-payment-response`. These are the names
  acp-v0 open point A-3 proposes, and they are not registered. The octets are
  the entry's `content` (base64url without padding, decoded), and the carried
  digest is the entry's `digest`. The entry's type must match the object the
  content turns out to be. An order event in a leg has no header, so
  `order_event_hmac` is `NOT_PRESENT` there;
- a JSON body: the octets exactly as received;
- an MCP JSON-RPC response (`jsonrpc` member): the octets of the `result`
  member's value as they appear in the response. ACP does not fix the
  JSON-RPC serialization, so the same session over REST and over MCP has
  different octets and digests (acp-v0 open point A-2);
- an HTTP message: an optional request or status line, header fields, an empty
  line, then the body as received. This is how an order event arrives with its
  `Merchant-Signature`. A `Content-Encoding` other than `identity`, or any
  `Transfer-Encoding`, is rejected, because the declared octets are the content
  after decoding.

The schemas are ACP's own JSON Schema bundles, vendored unchanged in
`adapter/acp/schema/` with their SHA-256 (see `schema/NOTICE`;
`TestVendoredSchemaDigests` holds them to acp-v0 §1). ACP publishes the order
event envelope only in OpenAPI, so `adapter/acp/schema.go` transcribes
OAS-WH's `WebhookEvent` and `EventDataOrder`. `EventDataOrder` refers to
OAS-CHK's `Order`, which at 7fdd78df is JS-CHK's `Order` without three optional
members: `client_reference_id`, `confirmation.order_notes` and
`fulfillments[].destination.company`. The transcription excludes those three.

**`CheckoutSessionWithOrder` and open point A-4.** In OAS-CHK a
`CheckoutSessionWithOrder` cannot be satisfied: it is `allOf` a closed
`CheckoutSessionBase` that does not list `order` (acp-v0 §6, open point A-4).
JS-CHK does not have this defect, because its `CheckoutSessionBase` lists
`order`. The adapter validates checkouts against JS-CHK only, never against
OAS-CHK. So it validates a completed session as a whole object, and has no
workaround and no defect note. Validation
follows JSON Schema 2020-12 with `format` as an annotation, and never loads an
external reference. Duplicate member names are rejected before validation.

### Declaration

Every object is `natively_signed: false`. The predicate is possession only:
*the presenting party holds octets whose SHA-256 is D, and states they are the
body ACP returned from operation O at time T*. The operation, the time and the
merchant are the presenter's statement.

| Property | Status | Method |
|---|---|---|
| `octets_digest` | `PASS` or `FAIL` against the carried digest; `NOT_PRESENT` when there is none | `SHA-256 over the octets as delivered, compared with the carried digest`, then `: ` and the digest's source: the leg's wrapped entry digest, or the relying party's pin `acp.carried_digest` for a standalone adapt |
| `object_form` | `PASS` or `FAIL` | ACP object identified by its members and validated against the vendored schema; the octets are not authenticated |
| `native_check` `checkout_session_signature` | `NOT_PRESENT` | `none: ACP 2026-04-17 defines no issuer signature over CheckoutSession` |
| `native_check` `order_signature` (order path 1) | `NOT_PRESENT` | `none: ACP 2026-04-17 defines no issuer signature over Order` |
| `native_check` `order_event_hmac` (order path 2, header received) | `NOT_CHECKED` | `not run: HMAC-SHA256 under a shared secret (OAS-WH Merchant-Signature)` |
| `native_check` `order_event_hmac` (order path 2, header absent) | `NOT_PRESENT` | `none: no Merchant-Signature header was received with the order event body` |
| `native_check` `delegate_payment_signature` | `NOT_PRESENT` | `none: ACP 2026-04-17 defines no issuer signature over DelegatePaymentResponse` |

The `native_check` methods are acp-v0 §2's (e) tables, verbatim. Per ruling S2,
each named check is reported as a `native_check` report built by
`adapter.NativeCheckReport`. Its `detail` is the object `{"check", "object"}`,
for example `{"check": "order_signature", "object": "acp order (path 1)"}`.
The explanation is not part of the report, and `-explain` shows it. A completed session
therefore reports two `native_check` results, `checkout_session_signature`
and `order_signature`. `octets_digest` and `object_form` are not native
checks and keep their own property names. The webhook HMAC is not run even
when the relying party holds the secret. A `PASS` by either holder would show
only that one of the two produced it, and would not attribute the order to the
merchant (acp-v0 §3.3). The reason is given in `detail`.

### Mapping to a requirement

A `native_check` of `NOT_PRESENT` or `NOT_CHECKED` maps as profile §8.1 rows
3 and 4 until a profile row exists (m3-adapters ruling S1). `adapter.Evaluate`
already does this. Requirements rest on checks by
check name. A requirement that is or rests on a native check ("the merchant
issued this session", resting on `checkout_session_signature`) is `INSUFFICIENT`, or `UNKNOWN` for the
webhook, and `not_evaluable`, never `met` or `not_met`. A requirement that
does not rest on it ("the presenter holds the octets the Artifact cites",
which depends on `octets_digest` only) is unaffected. `TestS1Mapping` shows
both cases.

### Trust policy keys

| Key | Kind | Meaning |
|---|---|---|
| `acp.carried_digest` | pin | for a standalone adapt only: the SHA-256 (lowercase hex) that the Artifact citing the object states for it. The relying party takes it from that Artifact after verifying its envelope, and never from the ACP octets. In a settlement-records leg the wrapped entry's digest is the carried digest, and the pin is not read |

### Running it

```
go run ./cmd/evidencebook adapt acp -policy adapter/acp/testdata/trust-policy.json adapter/acp/testdata/checkout-session.json
go run ./cmd/evidencebook adapt acp adapter/acp/testdata/checkout-session-with-order.json
go run ./cmd/evidencebook adapt acp adapter/acp/testdata/order-event.http
go run ./cmd/evidencebook adapt acp adapter/acp/testdata/leg-checkout-session.json
go run ./cmd/evidencebook adapt acp - < adapter/acp/testdata/delegate-payment-response.json
go run ./cmd/evidencebook adapt acp -declare
```

The fixtures in `adapter/acp/testdata/` are built from ACP's published
examples, not from recorded traffic. The leg fixture is a constructed minimal
leg around one of them. `testdata/SOURCES` names the example
each one came from. Some published examples fail ACP's own schema and are not
used: the order events in `examples.agentic_checkout.json` and the results in
`examples.mcp.agentic_checkout.json`. There are no conformance vectors: acp-v0
§6 defers them, and a native-check-fails vector would be a pretend check.

### Dependencies

`github.com/santhosh-tekuri/jsonschema/v6` (JSON Schema 2020-12 validation,
Apache-2.0), and with it `golang.org/x/text` (BSD-3-Clause). The library is
pure Go and implements draft 2020-12 `$defs`/`$ref`/`allOf`, which both ACP
bundles use, and it lets the adapter refuse every external loader. The UCP
adapter can share it.

## UCP

The adapter implements the Universal Commerce Protocol adapter declaration
v0 (`agent-action-capsule` `spec/adapters/ucp-v0.md`, commit `75009695`) for
UCP `v2026-08-25` (`Universal-Commerce-Protocol/ucp` commit
`cd78fb38e819de77d9b527d110476eccb876f1bd`).

UCP has one object that the business (the merchant) signs as an object: the
checkout carrying `ap2.merchant_authorization`. It exists only when the
optional AP2 Mandates Extension (`dev.ucp.common.payment.ap2_mandate`) was
negotiated. Its native method canonicalizes: the signature is a JWS with
detached content (RFC 7515 Appendix F) over the RFC 8785 (JCS) form of the
checkout without `ap2`. The adapter rebuilds that form because the native
method does. It never re-signs the object. The order object is not signed as
an object. The Order Event Webhook that carries it is signed as an HTTP
message (RFC 9421, with an RFC 9530 `Content-Digest` over the raw body).

### What it reads

The input is detected as one of four forms.

1. **A checkout**: any JSON object without a `checkout_id` member, for
   example a `create_checkout`, `get_checkout` or `complete_checkout`
   response body, an MCP tool result's checkout, or the RFC 8785 octets of a
   `ucp.checkout` wrapped entry. The input is read as I-JSON (RFC 7493):
   invalid UTF-8, an escaped lone surrogate or a duplicate member name in
   any object makes the input unreadable. Any serialization of the same
   content gives the same results.
2. **An order body**: a JSON object with `checkout_id`, the webhook body or a
   `get_order` response held without its request. It has no native check.
3. **An Order Event Webhook request held whole**: the HTTP/1.1 request as
   received, with the request line, the field lines, an empty line and the
   raw body. Lines may end in CRLF or LF. The body is every octet after the
   empty line, unchanged.

   ```
   POST /webhooks/ucp/orders HTTP/1.1
   Host: platform.example
   Content-Type: application/json
   UCP-Agent: profile="https://merchant.example/.well-known/ucp"
   Content-Digest: sha-256=:<base64>:
   Signature-Input: sig1=("@method" "@authority" "@path" "content-digest" "content-type" "ucp-agent");created=1760702400;keyid="<kid>"
   Signature: sig1=:<base64>:

   {"id":"order_abc123","checkout_id":"chk_abc123",...}
   ```
4. **An operation response held whole**: the HTTP/1.1 response as received,
   with the status line, the field lines, an empty line and the raw body
   (a checkout or an order). The body is checked as in forms 1 and 2, and
   its RFC 9421 response signature is reported after those checks as
   `ucp.response_signature`.

   ```
   HTTP/1.1 200 OK
   Content-Type: application/json
   Content-Digest: sha-256=:<base64>:
   Signature-Input: sig1=("@status" "content-digest" "content-type");created=1760702400;keyid="<kid>"
   Signature: sig1=:<base64>:

   {"id":"chk_abc123",...}
   ```

### Declaration

**(a) Native objects and format.**

- The checkout with `ap2.merchant_authorization`: a UCP Checkout
  (`payment_ap2_mandate.json#/$defs/dev.ucp.shopping.checkout`).
  `merchant_authorization` has the form
  `<base64url-header>..<base64url-signature>`, and its protected header
  carries `alg` and `kid`. The octets a wrapper digests are the RFC 8785 form
  of the whole checkout as received, `ap2` included (proposed wrapped type
  `ucp.checkout`, not registered).
- The checkout without AP2, and the order body held alone: the octets as
  received. These prove possession only.
- The webhook request held whole: the request with its covered components
  and its raw body.

**(b) Issuers and how they are resolved.** The issuer is the business. The
JWS header names only `alg` and `kid`, so the signer is identified by
context. The business publishes `keys[]` (a JWK Set) in its profile at
`https://<business-origin>/.well-known/ucp`. The adapter never fetches a
profile. The relying party supplies a copy of `keys[]` that it retained, the
origin it came from and the time it was taken. A copy says whether a key was
listed when the copy was taken. The JWS carries no signing time, and
businesses remove keys from `keys[]` on rotation and compromise. For a
webhook, the signer is the profile named in `UCP-Agent`. A key set that was
retained for another origin is not used for it. Whether the origin is the
merchant the relying party means is the policy's decision
(`ucp.business_trust`).

**(c) Native verification method.**

- Checkout: UCP AP2 Mandates Extension §Step 1
  (`verify_merchant_authorization`). Split `merchant_authorization` at `..`.
  Decode the header. Require `alg` ∈ {`ES256`, `ES384`, `ES512`} and a
  `kid`. Remove `ap2` and keep every other member, `ucp` included. Form
  `header || "." || base64url(JCS(checkout − ap2))`. Find the key for `kid`
  among the keys usable for verification: a key marked `use:"enc"` is
  skipped, and so is a key whose `key_ops` lacks `"verify"`. Verify the
  fixed-width `r||s` signature.
- Webhook: UCP Message Signatures §REST Request Verification
  (`verify_rest_request`). Parse `Signature-Input`. Require UCP's covered
  set: `@method`, `@authority` and `@path`, plus `@query` when there is a
  query, `content-digest` and `content-type` when there is a body, and each of
  `ucp-agent`, `signature-agent` and `idempotency-key` that is present. Check
  `Content-Digest` against the SHA-256 of the raw body. Rebuild the RFC 9421
  signature base and verify it under the key that `keyid` names. The
  algorithm follows the key: `ecdsa-p256-sha256`, `ecdsa-p384-sha384` or
  `ed25519`. When there are several signatures, the first that passes every
  check is reported. If none does, the first is reported.

**(d) The predicate supported.** When the checkout's checks all pass: the
holder possesses a UCP checkout, and every member except `ap2` was signed, in
that exact JSON content, by the key `kid` in the business key set the
verifier used, as of the time that set was retained. When `status` is
`completed` and `order` is present, this covers the business's signed
statement of the order's `id` and `permalink_url`, and nothing else about the
order. When the webhook's checks all pass: the holder possesses a request
whose body is this order snapshot, signed over the components listed in
`Signature-Input` by the key `keyid` in the key set used for the profile in
`UCP-Agent`. For a checkout without AP2, or an order body alone: the holder
possesses these octets, and the wrapping party says where it received them.

It does **not** support any of the following: that the origin is the
merchant the relying party means; that the key was listed when it signed;
that the buyer authorized the purchase (that is `ap2.checkout_mandate`);
that payment settled, or that the order was fulfilled or not cancelled
later; that a checkout is the last state of its session; or, for a webhook,
that the order state is current, that the events arrived in order, or that
the signer is the business that took the order. UCP's webhook authorization
step needs the checkout that created the order, and the adapter does not
perform it.

### Checks

Each check is a `native_check` report whose `detail` is
`{"check": <name>, "object": "ucp.checkout"}` (or `"ucp.order"`), built with
`adapter.NativeCheckReport`. The object's native check passes when each of
its reports is `PASS`. There is no separate summary report, except for an
object with no native signature (§5), which gets one `NOT_PRESENT` report.
`ucp.business_trust` is not a native check. It keeps its own property and is
reported beside the native checks, as `responder_trust` is reported beside
the envelope signature.

Checkout, in report order:

| Check | Method | `PASS` when; other statuses |
|---|---|---|
| `ucp.checkout.schema` | `ucp-schema validate --response --op <operation>, UCP v2026-08-25` | validates against the AP2-extended checkout schema; `FAIL` on a violation or unreadable input. The operation is the `ucp.operation` pin, else inferred from `status`: `completed` gives `complete`, `canceled` gives `cancel`, and any other status gives `read`. UCP's schemas annotate only requests, so the operation does not change what a response is validated against |
| `ucp.merchant_authorization.present` | `UCP AP2 Mandates §Business Authorization` | present; `NOT_PRESENT` when absent (`merchant_authorization_missing` when AP2 was negotiated) |
| `ucp.merchant_authorization.form` | `RFC 7515 Appendix F detached JWS` | matches the pattern, and the header is an I-JSON object with string `alg` and `kid` and no `crit`; `FAIL` otherwise |
| `ucp.merchant_authorization.alg` | `AP2 v0.2 algorithm rule` | `ES256`, `ES384` or `ES512`, which are all verified here; `FAIL` for any other |
| `ucp.merchant_authorization.key` | `retained keys[] (<key-set source>)`, with ` as of <time>` when the retention time is known, or `UCP profile keys[] lookup by kid, live` | `kid` names a usable key; `FAIL` when it is not listed, or is listed only as unusable; `NOT_CHECKED` with no retained key set, or for a key type not implemented |
| `ucp.merchant_authorization.signature` | `<alg> over JWS signing input of JCS(checkout − ap2)` | verifies; `FAIL` (`merchant_authorization_invalid`) when it does not, or when the key's curve does not match `alg` |
| `ucp.checkout` | `none: UCP defines no object signature for this object` | reported only when AP2 was not negotiated: `NOT_PRESENT` |
| `ucp.business_trust` (own property) | `relying party policy <policy.id>` | the policy accepts the origin; `FAIL` when it rejects it; `INCONCLUSIVE` when the origin is unknown or not listed; `NOT_CHECKED` with no origin policy |

Order, in report order:

| Check | Method | `PASS` when; other statuses |
|---|---|---|
| `ucp.order.schema` | `JSON Schema 2020-12, UCP v2026-08-25` | the body validates against `order.json` |
| `ucp.webhook.components` | `RFC 9421 §2 component set` | every covered component is held; `NOT_PRESENT` for a body alone, or when a covered field is missing |
| `ucp.webhook.coverage` | `UCP Identity Resolution Algorithm step 5` | covers UCP's required set; `FAIL` (`coverage_insufficient`) otherwise |
| `ucp.webhook.content_digest` | `RFC 9530 sha-256 over raw body` | the `sha-256` member equals the body's digest; `FAIL` (`digest_mismatch`) |
| `ucp.webhook.key` | as `ucp.merchant_authorization.key` | `keyid` names a usable key in the set retained for the `UCP-Agent` origin; `NOT_CHECKED` when the set is for another origin; `FAIL` for a malformed or non-HTTPS `UCP-Agent` |
| `ucp.webhook.signature` | `RFC 9421 <alg> over signature base` | verifies; `FAIL` (`signature_invalid`); `NOT_CHECKED` for a component with parameters, a derived component other than `@method`, `@authority`, `@path`, `@query` and `@request-target`, or a `tag` other than `web-bot-auth` |
| `ucp.order` | `none: UCP defines no object signature for this object` | reported only for a body alone: `NOT_PRESENT` |
| `ucp.business_trust` (own property) | as above | the policy accepts the `UCP-Agent` origin |

An operation response held whole adds one property after the body's checks:

| Property | Method | `PASS` when; other statuses |
|---|---|---|
| `ucp.response_signature` (own property; `detail` `{"check": "ucp.response_signature", "object": <body object>}`) | `RFC 9421 <alg> over <covered components>` | `Content-Digest` is the body's SHA-256, and the signature verifies over the rebuilt signature base under a key in the retained `keys[]`; `FAIL` on a digest mismatch, a required component not covered (`@status`, plus `content-digest` and `content-type` with a body), a `keyid` that is not listed, or a bad signature; `NOT_PRESENT` when the response carried no signature or a covered field was not kept; `NOT_CHECKED` with no retained key set |

A response signature is message-level provenance: the holder of the
business's HTTP signing key sent these response bytes. It is not the
issuer's signature over the object, and it sits outside the object's
checks. An order, or a checkout without AP2, stays `NOT_PRESENT` whether or
not its response signature verified. A `PASS` supports a requirement about
where the bytes came from, never one that rests on an issuer signature over
the object.

A `FAIL` of a signature check decides a requirement that *is* that check
(`SATISFIED`, `not_met`, row 5). It leaves a requirement that only rests on
the check `INSUFFICIENT` (row 6). A `NOT_PRESENT` for an object that has no
native signature is mapped as profile §8.1 rows 3-4, pending a profile row.
It never counts against a requirement that does not rest on that signature.

### Trust policy

| Name | Kind | Meaning |
|---|---|---|
| `ucp.keys` | key_sets | the retained `keys[]`: a JWK Set `{"keys": [...]}` or a bare array. A `ucp.keys` pin (the v0.3.0 shape) is not read: the key check is `NOT_CHECKED` and its explanation names `key_sets` |
| `ucp.keys.origin` | pin | the business origin (`https://host`) whose `/.well-known/ucp` served that copy. For a checkout, this is also the origin `ucp.business_trust` asks about |
| `ucp.keys.source` | pin, optional | where the copy came from, named in the key check's method; by default the profile URL of `ucp.keys.origin` |
| `ucp.keys.retained_at` | pin, optional | when the copy was taken (RFC 3339), named in the key check's method |
| `ucp.operation` | pin, optional | the operation whose response a checkout is (`create`, `read`, `update`, `complete`, `cancel`), named in the schema check's method |
| `policy.id` | pin, optional | the policy identifier named in `ucp.business_trust`'s method (shared by every adapter) |
| `ucp.business_origin` | keys | the business origins the relying party accepts |
| `ucp.business_origin.rejected` | keys | the business origins the relying party rejects |

```json
{
  "key_sets": {
    "ucp.keys": {
      "keys": [
        {"kid": "rfc7515-appendix-a3", "kty": "EC", "crv": "P-256", "x": "f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU", "y": "x_FEzRu9m36HLN_tue659LNpXW6pCyStikYjKIWI5a0", "use": "sig", "alg": "ES256"}
      ]
    }
  },
  "pins": {
    "ucp.keys.origin": "https://merchant.example",
    "ucp.keys.retained_at": "2026-10-17T12:00:00Z",
    "policy.id": "fixture-accept-merchant.example"
  },
  "keys": {
    "ucp.business_origin": ["https://merchant.example"]
  }
}
```

### Running it

```
go run ./cmd/evidencebook adapt ucp -policy adapter/ucp/testdata/trust-policy.json adapter/ucp/testdata/checkout-positive.json
go run ./cmd/evidencebook adapt ucp -policy adapter/ucp/testdata/trust-policy.json adapter/ucp/testdata/checkout-member-mutated.json
go run ./cmd/evidencebook adapt ucp -policy adapter/ucp/testdata/trust-policy.json adapter/ucp/testdata/order-webhook.http
go run ./cmd/evidencebook adapt ucp -declare
```

The exit status is the same as for x402.

The fixtures in `adapter/ucp/testdata/` are constructed, not recorded.
`go test ./adapter/ucp -run TestFixturesUpToDate -update` regenerates them,
and without `-update` the test requires them to be byte-identical. They are
signed with the **ES256 example key of RFC 7515 Appendix A.3**, a key
published in the RFC as an example (UCP publishes no test key).
`TestKnownAnswerRFC7515A3` checks it against the RFC's own A.3 signature.
Signatures are deterministic (RFC 6979), so the fixtures can be reproduced.

- `checkout-positive.json`: a completed checkout, indented and not in JCS
  member order, signed.
- `checkout-member-mutated.json`: the same checkout with the `total` amount
  changed after signing. It still validates, and only
  `ucp.merchant_authorization.signature` fails.
- `checkout-no-ap2.json`: a checkout without AP2, which proves possession only.
- `order-webhook.http`: a signed webhook request.
  `order-webhook-body-mutated.http` has one body octet changed, so only
  `ucp.webhook.content_digest` fails.
  `order-webhook-example-coverage.http` has a signature that covers the
  components of the example in UCP's Order Capability. That example does not
  cover `ucp-agent`, so `ucp.webhook.coverage` fails.
- `order-body.json`: the webhook body alone.
- `checkout-response-signed.http` and `order-response-signed.http`: signed
  operation responses. For the order, `ucp.order` stays `NOT_PRESENT` and
  `ucp.response_signature` is `PASS`.
  `checkout-response-body-mutated.http` has one octet of whitespace added to
  the body after signing. The checkout's JCS form, and so its AP2 signature,
  is unchanged, and only `ucp.response_signature` fails.
  `checkout-response-unsigned.http` carries no signature, so
  `ucp.response_signature` is `NOT_PRESENT`.

The tests change, in turn, one member of every class covered by the checkout
signature: a top-level string and enum, the currency, the `ucp` namespace,
nested integers, array order, a removed element, URLs, a non-ASCII string, an
added or removed member, and the order's `id` and `permalink_url`. Each
change is required to fail `ucp.merchant_authorization.signature` alone, or
to fail it together with the schema check when the change also breaks the
schema. Re-serializing, escaping a character, spelling a number differently
or adding a member under `ap2` must leave every check `PASS`. The webhook
tests change the method, path, authority, a covered field, the signature
parameters and the body in turn.

`TestAACVectors` runs the `agent-action-capsule` vectors
`vectors/adapters/ucp/`, copied byte for byte into
`adapter/ucp/testdata/aac-vectors/`, with their `SHA256SUMS` pinned. Every
report matches the vector's property, status, method and whole `detail`
object.

### Schema validation

The UCP JSON schemas are copied unchanged from `source/schemas/` at commit
`cd78fb38` into `adapter/ucp/schema/v2026-08-25/`, with UCP's Apache-2.0
LICENSE. Only the transitive `$ref` closure of
`common/payment_ap2_mandate.json` and `shopping/order.json` is copied (47
files). Each file is checked against `SHA256SUMS`, whose own digest is pinned
in code. The validator is `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3,
draft 2020-12, with `format` as an annotation (the 2020-12 default), Go
regular expressions, and a loader that refuses every `$ref` it does not hold.

### Dependencies

`github.com/santhosh-tekuri/jsonschema/v6` v6.0.3 (Apache-2.0), which pulls
in `golang.org/x/text`. ECDSA, Ed25519 and SHA-2 come from the Go standard
library. RFC 8785 is implemented in the package, including number
serialization. evidencebook's own `canonicalJSON` uses the Agent Action
Capsule integer-only profile of RFC 8785, which refuses a non-integer number.
A checkout may carry one, so that profile cannot be used here. For every
input the profile accepts, the two produce the same bytes. RFC 8941
Structured Fields are parsed by a minimal reader in the package.
