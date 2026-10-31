# Vectors

The canonical index of the published vector corpus, by SDK verb, is planned in
agent-action-capsule; it is tracked in
[agent-action-capsule#420](https://github.com/action-state-group/agent-action-capsule/issues/420)
and not yet published. Until it is, this file lists only the sets this
repository holds.

Vendored sets are byte for byte their upstream files; where a set carries a
`SHA256SUMS`, a test fails if a copy drifts from it. A commit names the
repository the set was taken from or written in.

## Root module (`testdata/`)

| Path | What it is | Owning spec | Pinned commit | Read by |
|---|---|---|---|---|
| `testdata/evidence-request/` | the request draft's request, resolution and refusal vectors, spec-derived; `SHA256SUMS` is upstream's lines for the four files | `draft-mih-agent-evidence-request-00` (agent-action-capsule `spec/`) | agent-action-capsule `vectors/evidence-request/`, last changed at `ee83efa5d0bc2096fc084ea232dfb62fc5cb03ee`, read at main `6cae247f0d9a82ee88a53d02022ed04a55aaabfa` | `interop_vectors_test.go`, `Example_joinTheLoop`, `ExampleBook_Respond`; the Python package's `tests/test_vectors.py` |
| `testdata/deal-reconcile-v0/` | the Deal Reconcile Contract's conformance vectors, spec-derived, with upstream's `SHA256SUMS` | agent-action-capsule `spec/deal-reconcile-contract-v0.md` | agent-action-capsule `vectors/deal-reconcile-v0/` at main `2cf9037cdddac1e92953eb72053c7a1783e68a01` | `deal_reconcile_test.go`, `ExampleBook_SealResult` |
| `testdata/derivation/` | definition-digest derivations: identifiers and reply octets from a Go book | agent-action-capsule `spec/interop-profile-v0.md` (derivations, P-14) | this repository at `f509823c7ed34e8dc52dc2dedee7fdae9ec06d74`, written by `TestDerivationVectors` | `interop_derivation_vectors_test.go`, `ExampleBook_Respond`; the Rust crate's `tests/derivation.rs` |
| `testdata/refusal-interop/` | a refusal first signed by capsule-emit's Python `capsule_emit.evidence_request`, now removed; `generate.py` signs with agent-action-capsule's `agent_action_capsule.evidence_request.sign_refusal` (same body) | `draft-mih-agent-evidence-request-00` (refusals) | this repository at `2d50ccc2462649e308fd4e45b33617f8d5652665`; `generate.py` now signs with agent-action-capsule 0.13.0; the committed `refusal.json` is still the original capsule-emit-signed file (same -00 refusal shape, verified by `request_test.go`) | `request_test.go`; the Rust crate's `tests/refusal_interop.rs` |
| `testdata/reconcile-parity/` | exchange halves and the Python correlator's outcome for each pair | `draft-mih-agent-evidence-layer-00` §10 (Reconcile and Close) | capsule-emit-mesh `af1e969503d44f66b7598d5b26186e114cf8e760` (recorded in `fixture.json`), written by `generate.py` | `reconcile_test.go` |
| `testdata/cadence/` | the cadence tree and a witnessed chain written by capsule-cli's code | evidencebook#36 §2 (the cadence tree) | capsule-cli `7a6b1014afb559eeff799c29c2743da22f320334` (`internal/cli/deal_cadence.go`) | `cadence_tree_test.go`, `cadence_chain_test.go` |
| `testdata/legacy-citation-purpose/` | a book and a bundle written by the previous record writer, which put link types in `references[].citation_purpose` | agent-action-capsule registry conformance (agent-action-capsule#241, merged at `7fabe9a5c3fe2efbeb45ac4b222f0021455acf7f`) | this repository at `281d7b052bf772c9e3d4ce71be7ceff983532979`, written by `test/interop/capsules` against `book.go` at `db4e312` | `citation_purpose_test.go` |

## Report module (`report/testdata/`)

| Path | What it is | Owning spec | Pinned commit | Read by |
|---|---|---|---|---|
| `report/testdata/presentation/` | report pages `capsulectl book report build` writes, with their bundles | presentation contract v0 | capsule-cli `cli/testdata/presentation/`, last changed at `62d569d79b23b56a874c8db9ad8f9c1e159095de` | `report/example_test.go` (`ExampleBuild`), `report/report_test.go` |
| `report/testdata/book-form-bundle.json` | a book-form Result-root bundle as `disclose` writes it | Evidence Bundle v2; Evidence Result v0 | this repository's `capsulectl/scripts/bookdemo/testdata/own-capsule-bundle.json`, byte for byte | `ExampleSealPage` |
| `report/testdata/incomplete-payload-bundle.json` | a payload-form bundle with no checkpoint or producer signatures | Evidence Bundle v2 | this repository's `capsulectl/internal/book/testdata/result/result-root-bundle.sealed.json`, byte for byte | `ExampleBuild_incomplete` |

## Checked in CI from other repositories

CI also runs sets it does not vendor, at the agent-action-capsule commits pinned
in `.github/workflows/ci.yml`: the interop profile's conformance vectors, the
Personal-Agent History vectors (rebuilt byte for byte from this responder's run),
and registry conformance. The workflow names each pin.
