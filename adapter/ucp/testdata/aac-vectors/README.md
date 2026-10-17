# UCP adapter vectors

Cases for `spec/adapters/ucp-v0.md` §3: the UCP checkout carrying `ap2.merchant_authorization`,
a JWS with detached content over the RFC 8785 form of the checkout without `ap2` (UCP
v2026-08-25, AP2 Mandates Extension, §Business Authorization). These are the only UCP vectors.
The order object and a checkout without AP2 have no native check (§4, §5), so a vector for them
would have nothing to check.

`python/scripts/generate_ucp_adapter_vectors.py` regenerates every file here except this README.

| File | Content |
|---|---|
| `key-set.json` | the business profile's `keys[]`, as a retained JWK Set: one ES256 public key, `kid` `rfc7515-appendix-a3` |
| `positive.checkout.json` | a `complete_checkout` response (`status` `completed`, with `order`), signed |
| `native-check-fails.checkout.json` | the same checkout with `totals[2].amount` (type `total`) changed from 5400 to 540 after signing. `ap2` is unchanged |
| `cases.json` | per case: the checkout file, the proposed `ucp.checkout` wrapped entry, and the expected reports |
| `manifest.json`, `SHA256SUMS` | file digests |

## Expected results

Each named check of §3(e) is reported as a `native_check` property report, with the check's name
in `detail` (ruling S2). In `positive` every check is `PASS`. In `native-check-fails`,
`ucp.merchant_authorization.signature` is `FAIL` and the rest are `PASS`. The schema still
validates, the JWS is well formed, and the key resolves. Only the signed content differs. The
failing case also records its §8.1 mapping: row 5 (`SATISFIED` / `SATISFIED` / `not_met`) for a
requirement that *is* the check, and row 6 (`INSUFFICIENT` / `INSUFFICIENT` / `not_evaluable`) for
one that rests on it. `ucp.business_trust` is `NOT_CHECKED` in both cases, because the vectors
supply no trust policy.

The wrapped entry has type `ucp.checkout`. It is **proposed, not registered** (§6). Its octets are
the RFC 8785 form of the whole checkout, `ap2` included, and `content` carries them.

## Signing key

UCP publishes no test key. The key is the **ECDSA P-256 example key of RFC 7515, Appendix A.3.1**
(<https://www.rfc-editor.org/rfc/rfc7515#appendix-A.3.1>). It is a published example key, not a
real one. Its private part `d` is in the generator, as RFC 7515 prints it. `key-set.json` carries
only the public part. The test also checks that this key verifies the RFC's own A.3 JWS.

ES256 signatures are randomized. To make these vectors reproducible, the generator derives the
nonce as RFC 6979 §3.2 specifies (HMAC-SHA-256), using its own P-256 arithmetic. The test checks
that arithmetic against RFC 6979 Appendix A.2.5 (P-256, SHA-256, messages "sample" and "test"). The
generator and the test both check every signature with `cryptography`. A verifier needs nothing
from RFC 6979: a deterministic ES256 signature verifies like any other.

## Schema validation

Both checkouts validate against UCP's published JSON Schema:

| | |
|---|---|
| Schema | `source/schemas/shopping/checkout.json` composed with `source/schemas/common/payment_ap2_mandate.json` (`$defs/dev.ucp.shopping.checkout`), in <https://github.com/Universal-Commerce-Protocol/ucp> at `cd78fb38e819de77d9b527d110476eccb876f1bd` (tag `v2026-08-25`) |
| Validator | `ucp-schema` 1.4.1, UCP's schema validator (<https://github.com/Universal-Commerce-Protocol/ucp-schema>, tag `v1.4.1`, `6dd2a613d38828c97dca723bdb9ccfc79b2a7e80`), the tool UCP's own CI uses to validate its examples |
| Direction, operation | response, `complete` |

```bash
ucp-schema validate vectors/adapters/ucp/positive.checkout.json \
  --schema-local-base <ucp>/source/schemas \
  --schema-remote-base https://ucp.dev/2026-08-25/schemas \
  --response --op complete --json
# {"valid":true}   (and the same for native-check-fails.checkout.json)
```

The checkout's own `ucp.capabilities` names both schemas, so `ucp-schema` composes the AP2
extension into the checkout schema. A malformed `merchant_authorization` fails the extension's
pattern. A missing `currency` fails, and so does an unknown `status`. That validator is not in
this repository's CI. `python/tests/test_ucp_adapter_vectors.py` runs it when `ucp-schema` is on
`PATH` and `UCP_SCHEMAS` points at `<ucp>/source/schemas`. Otherwise it skips that test.
