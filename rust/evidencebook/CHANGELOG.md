# Changelog: evidencebook (Rust)

## Unreleased

- **`store`: the read-only store an answer reads** (evidencebook#35). Two
  traits: `CheckpointedLog` (leaf count, stated checkpoints with their COSE
  statements, inclusion paths and consistency parts), which `CllSubstrate`
  implements in place, and `RecordSource` (records by committed entry and by
  leaf index, correlation, citing records), moved here from `answer` and
  re-exported there. `RecordSource::leaf_index_of` finds a record's position
  without reading it (selection and the citing-records anchor use it), and
  `RecordSource::entry_of` says what the log commits for a
  record: the SHA-256 of its octets by default, a `capsule_id` for a capsule
  ledger. A caller with its own checkpointer implements both over its own
  index and the crate opens none of its files.
- **`answer` reads the store traits** instead of a concrete `CllSubstrate`,
  and is split into `select` (the serving checkpoint and the records) and
  `build` (prove and sign), so a responder can apply its own policy to the
  selection in between. `answer` is both and answers exactly as before.
  `serving_checkpoint` is the citing-records anchor rule on its own.
  `build` takes `Options`: `prove_beyond_profile` adds the anchor checkpoint
  and one inclusion proof per record to a record set (the profile defines
  none, P-13), and `derive` answers the caller's own derivations. Every
  served record is checked against `entry_of`; `AnswerError::RecordDigest`
  now means its octets do not recompute its committed entry.
- `index::committed_records` and `reconcile::HalfSet::from_store` build an
  operational index's rebuild input and one side's halves from a store.
  `SubstrateError::Record` reports a leaf a store cannot produce.
- **`received`**: the "asked of you" log, ported from the evidence
  plugin with its line shape, requester-id cut and byte cap. The caller
  decides the directory and whether it may write it.
- **`served_summary`**: the `served_summary/1` fold, ported from the same
  plugin (floor, percentiles, coverage block). What a group and a variant
  are, and the published labels, are the caller's (`Labels`).

## 0.0.5

- **`answer`: the artifact response to an evidence request, built from the
  book's log in the interop profile's envelope** (agent-action-capsule
  `spec/interop-profile-v0.md` §6.2; agent-action-capsule#270). It is the
  Checkpointed Local Log builder that was `evidence-request`'s `answer`
  feature, moved here and changed to the profile's forms, which the Go
  implementation also builds: a record is its own octets, proven by the
  anchor checkpoint statement, its inclusion proof and, when the checkpoint
  extends another, the consistency proof (§6.2.1); a record set, the
  checkpoint list and `history_card/1` are RFC 8785 objects with no material
  (P-13). Anchors are the checkpoints with a COSE wire statement
  (`CllSubstrate::stated_checkpoints`), identified by its SHA-256.
- Depends on `evidence-request` 0.2 with its `cll` feature, on the same
  `checkpointed-local-log` line as this crate.

## 0.0.4

- First release from this repository. It carries everything below.
- **0.0.3 was published from the checkpointed-local-log repository's
  pre-move copy.** That release has only the `checkpointed-local-log` 0.3.1
  dependency and none of the changes listed here. Use 0.0.4.
- **0.0.2 was never published.** The checkpointed-local-log repository's
  manifest carried it before that repository released from merged version
  bumps, so no release ever fired for it, and its next release took 0.0.3.
  crates.io therefore has 0.0.1 and 0.0.3 from checkpointed-local-log and
  0.0.4 from here.
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
