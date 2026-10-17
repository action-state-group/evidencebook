# x402 adapter v0 vectors (spec/adapters/x402-v0.md)

Vectors for the x402 adapter declaration in `spec/adapters/x402-v0.md`: an x402 v2 `exact`
payment on an `eip155` network with the `eip3009` transfer method. They cover the wrapped objects'
octets, the relying party's policy and recorded transaction lookup, and the `native_check` reports
a verifier gives.

**Constructed, not recorded.** No chain was called. The payment payload is signed with a published
development key. The settlement response and the transaction lookup are built to match that
payment. Every case is `"provenance": "constructed, not recorded"`. A recorded Base Sepolia payment
replaces the positive case when it is available.

**The development key.** This is the first default development account that Hardhat Network and
Foundry anvil derive from the mnemonic `test test test test test test test test test test test
junk`:

- private key `0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80`
- address `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266`

The pair appears in foundry-rs/foundry `crates/script/src/lib.rs` at
`131bd9d03941a79836653296d84921e2ab4d9fbb`. The key is public, controls nothing, and is never to
be funded. No other private key is in this corpus.

The asset contract and its EIP-712 domain (`name` "USDC", `version` "2", chain id 84532,
`verifyingContract` `0x036CbD53842c5426634e7929541eC2318f3dCF7e`) are the values of the x402 v2
specification's own example (sections 5.1.1 and 5.2.1). That example's signature recovers to its
stated payer under them, and the test checks this.

`python/scripts/generate_adapter_x402_vectors.py` regenerates every file here except this README,
byte for byte. `python/tests/test_adapter_x402_vectors.py` checks that. It also checks the
generator's Keccak-256 and secp256k1 against published values, verifies the positive signature
with an independent ECDSA implementation, and derives every expected report with its own reading
of the declaration.

## Files

| File | What it pins |
|---|---|
| `cases.json` | The cases. For each case: the wrapped objects (`type`, `digest_alg`, `digest`, and the path of their octets), the informative header files, the lookup, the relying party's `policy`, and `expect`. `expect.reports` holds one `native_check` report per named check, with the check's name and object in `detail`. `expect.requirements` holds the profile §8.1 mapping where the case shows one. Also carries the development key and its source. |
| `cases/<case>/payment-payload.octets` | The `x402.payment-payload` octets: the base64-decoded `PAYMENT-SIGNATURE` value. The wrapped digest is over these octets. |
| `cases/<case>/settle-response.octets` | The `x402.settle-response` octets: the base64-decoded `PAYMENT-RESPONSE` value. |
| `cases/<case>/payment-signature.header`, `payment-response.header` | Informative: the header values (standard base64 with padding) that decode to the octets. The transport step is checked once per file, not per property. |
| `cases/<case>/lookup.json` | The relying party's recorded chain observation: the transaction receipt and block in RPC result shapes, and the code at the payer's address. |
| `manifest.json` | SHA-256 of every generated file. |
| `SHA256SUMS` | SHA-256 of every file under this directory except itself. |

The octet files have no trailing newline. Read them as bytes.

## The cases

| Case | Policy | What it shows | Expected |
|---|---|---|---|
| `positive` | `eoa-or-contract` (the default) | A signed authorization for 10000 atomic units, settled in the recorded transaction, inside the validity window at the block's timestamp. | Every check with an input `PASS`; `settle_response_signature` `NOT_PRESENT` (the response is unsigned); the three receipt checks `NOT_PRESENT` (no receipt) |
| `native-check-fails` | `eoa` | The same payload with `authorization.value` changed to 10001 after signing. `value` is in the signed message, so recovery yields another address. | `payer_signature` `FAIL`, `authorization_terms` `FAIL`, `settlement_onchain` `FAIL` (the recorded `Transfer` is for 10000); profile §8.1 row 5 for "the payer signed this transfer authorization", row 6 for "the resource was paid for" |

The negative case declares the `eoa` signer policy. Under the default `eoa-or-contract`, a
recovered address that differs from `from` is `INCONCLUSIVE`, because the payer could be a contract
account (x402-v0.md §6.1). Under `eoa` it is a real `FAIL`.
