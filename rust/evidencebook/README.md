# evidencebook (Rust)

Evidence semantics over an embedded Checkpointed Local Log: record headers,
epistemic types, typed links, retention states, disclosure records, the three
index classes, request subjects and outcomes, and reconcile/close. It
implements the store-level semantics of the Evidence Layer Internet-Draft
([`draft-mih-agent-evidence-layer-00`](https://github.com/action-state-group/agent-action-capsule/blob/main/spec/draft-mih-agent-evidence-layer-00.md)).
Each public item's documentation names the draft section it implements.

The `cll` library (published as `checkpointed-local-log`) is the
commitment substrate, and it is embedded, not exposed: no public item takes or
returns a `cll` type (`tests/public_api.rs`).
The substrate keeps one sequence commitment, the MMR.

`answer` builds the artifact response to an evidence request
(`draft-mih-agent-evidence-request-00`) from the book's log, in the interop
profile's signed envelope (agent-action-capsule `spec/interop-profile-v0.md`
§6.2): the canonical artifact for each subject form, the Checkpointed Local
Log material that proves a record under its anchor (§6.2.1), and the
signature. The request protocol itself, and checking a received envelope and
its material, is the `evidence-request` crate's.

The crate carries no deployment vocabulary. Record kinds, correlation key
names and content comparators come from the calling profile.
`scripts/check-vocabulary.sh` enforces this in CI.

## Shared with the other implementations

- `schemas/vendor/epistemic-types.json` is the vendored epistemic-type set,
  the same file the Go and Python implementations read (the Go parity test
  checks the two copies are byte-identical).
  `EpistemicType::ALL` is tested against it.
- `testdata/refusal-interop/refusal.json` (at the repository root) is a
  refusal signed by the Python implementation. The Go implementation verifies
  it, and `tests/refusal_interop.rs` reads the same file in place.

## Changes

See [CHANGELOG.md](CHANGELOG.md).

## Test

```
cargo test
./scripts/check-vocabulary.sh
```
