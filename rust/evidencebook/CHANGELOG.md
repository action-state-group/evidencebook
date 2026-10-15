# Changelog: evidencebook (Rust)

## 0.0.4

- First release from this repository. It carries everything below.
- **0.0.3 was published from the checkpointed-local-log repository's
  pre-move copy.** That release has only the `checkpointed-local-log` 0.3.1
  dependency and none of the changes listed here. Use 0.0.4.
- Depends on `checkpointed-local-log` 0.3.1 (0.3.0 dropped the default witness
  client). Nothing in this crate used the removed default.
- **Moved** to the evidencebook repository (`rust/evidencebook/`), next to the
  Go implementation, with its history. It depends on `checkpointed-local-log`
  from crates.io instead of a path. 0.0.1 stays published with its
  checkpointed-local-log repository metadata.
- **The request draft's registry and refusal profile come from the
  `evidence-request` crate** (protocol core, `default-features = false`, so no
  second checkpointed-local-log line). `Refusal::signing_body` is its RFC 8785
  body, `reason_is_registered` reads its `Reason` registry, and `verify` lets
  it check the signature, now with strict Ed25519 verification (small-order
  keys and non-canonical signatures are refused, where the Go and Python
  verifiers still accept them; honest signers are unaffected). Tests pin the
  subject and reason tokens to its registry. The public types, their
  serialized forms, `Outcome` and `NoKind` are unchanged.
- `tests/refusal_interop.rs` reads the repository's
  `testdata/refusal-interop/refusal.json` in place instead of a copy.

## 0.0.1

- First release on crates.io. Depends on `checkpointed-local-log` 0.2.0.
- Content as in the `rust-evidencebook-v0.1.0` git tag. That tag is not a
  crates.io version and is not moved.
