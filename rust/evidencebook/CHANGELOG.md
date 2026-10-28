# Changelog: evidencebook (Rust)

## Unreleased

- **`answer::Derive` answers a holder's own digest derivation**
  (evidencebook#35). A request whose `derivation` is a definition digest
  that `Options::derivations` does not register now goes to the profile's
  `Derive`, as a token does. `Derive` is called with the digest, and its
  value is carried under the member it names, with the anchor checkpoint as
  material when `prove_beyond_profile` is set. A registered digest is still
  answered from its registration, and an unregistered digest that `Derive`
  does not answer is still `DerivationUnsupported`. A `Derive` that answers
  every name it is given now also answers such digests, so check the name.

## 0.0.8

### Breaking changes and how to migrate

- **`served_summary::summary_value`** (#161, #35): the third argument is now
  `leaf_index_of: &dyn Fn(&str) -> Option<u64>`, replacing
  `is_own_record: &dyn Fn(&str) -> bool`. To migrate, return the leaf index
  of the owner's own record with that id, and `None` for any other id.
  `adjudications_received` then counts only verdicts inside the covered
  range.
- **`served_summary::Coverage`** (#119, #115): new fields `statement` (the
  checkpoint's COSE wire statement, `StatedCheckpoint::statement`) and
  `witness_directory` (a `witnesses.json` value). To migrate, pass both.
  With no directory no receipt verifies, so `witnessed` is `false`. Each
  receipt then reads `NOT_CHECKED`, except when the statement itself does
  not hold, in which case every receipt reads `FAIL`. The new dependency
  on `agent-action-capsule` turns on `serde_json`'s `arbitrary_precision`
  feature for the whole build.
- **`served_summary/1` output** (#162): `coverage` gains `witness_check`
  and `witness_receipts`. This is additive, but a consumer that pins the
  summary's bytes sees both members. Show whether a range is witnessed from
  `witnessed` and `witness_receipts`, never from `witness_check` alone.
- **`Derivations::register`** (#92, #86): registering a document again
  with different declarations now returns `DerivationError::Redeclared`
  and keeps the first registration. To migrate, register a changed
  definition under its own document. `DerivationError` is not
  `#[non_exhaustive]`, so an exhaustive `match` on it needs an arm for
  `Redeclared`.
- **Strict Ed25519** (#160, checkpointed-local-log#108):
  `Checkpoint::verify_signature_offline`, `SealedRecord::verify` and the
  served summary's statement check now refuse small-order and non-canonical
  keys and signatures. Honest keys need no migration; such signatures, which
  anyone can make without the private key, now fail.

### Changes

- **The served summary states each witness receipt's check** (evidencebook#35,
  #115). `coverage` gains `witness_check` and `witness_receipts`
  (`[{ts_url, status}]`), each `PASS`, `NOT_CHECKED` or `FAIL` from AAC's
  witness verifier, and `witness_check` is `null` when the checkpoint
  carries no receipt.
  - A receipt that cannot be checked (no directory, no row or key, a stub,
    no statement) reads `NOT_CHECKED` and is still listed, never a lower
    count.
  - No statement to check against (an empty statement) means not checked:
    every receipt reads `NOT_CHECKED`. It can never yield a false
    `witnessed`.
  - A statement that does not verify, or does not sign the checkpoint as
    stated, fails every receipt.
  - `witnesses` and `witnessed` still count verified receipts only.
  - A `FAIL` outranks a `PASS`: one good receipt beside a bad one gives
    `witnessed: true` with `witness_check: "FAIL"`. Show whether a range is
    witnessed from `witnessed` and `witness_receipts`, never from
    `witness_check` alone.

- **Breaking: the served summary counts adjudications only within the
  checkpoint** (evidencebook#35). `served_summary::summary_value` takes
  `leaf_index_of: &dyn Fn(&str) -> Option<u64>` (the leaf index of one of
  the owner's own records, `None` for any other id) in place of
  `is_own_record: &dyn Fn(&str) -> bool`. `adjudications_received` now
  counts only verdicts inside the covered range that name an own record
  inside it, so a verdict or record appended after the checkpoint never
  changes the summary for that checkpoint. It used to count verdicts across
  the whole log. A downstream plugin's own served summary already applies
  this rule; porting it lets that plugin use the crate's.

- **`seal`: a record sealed as the Go book seals it** (evidencebook#139).
  `seal::header` builds a record's header the way the Go book does, at its
  position and commit time, with the commit time truncated to the
  microsecond in Go's RFC 3339 form. `seal::seal_record` seals it as the Go
  book's record capsule: an Agent Action Capsule of action type `fyi` that
  commits the header's JCS, with each link also a reference, and a Producer
  Envelope over the capsule id. It returns a `SealedRecord` (the record id,
  the header, the capsule, the committed body and the envelope), byte for
  byte what the Go book stores. It refuses a header Go would write
  differently: a time not in Go's form, or an empty ref or correlation
  value. Headers with field commitments are not supported. `SealedRecord::verify`
  checks a stored record: the capsule must be exactly the record capsule of
  its header, and the envelope must verify under the book's key. A caller's
  `ask::Book::seal` uses these. With them, the crate reproduces the Go book's
  Personal-Agent History run (its `run_sha256`). It also answers the
  interop-profile vectors: every member its run writes matches the Go run's,
  and 20 of 53 cases pass and 33 are skipped (Go's 25 skips plus the 8
  A2A/MCP carriage cases), the same split as the Python package. The
  programs that make those runs are `rust/interop`, which is not published.
  No new dependency.

- **Security: Ed25519 signatures are verified strictly**
  (checkpointed-local-log#108). `substrate::Checkpoint::verify_signature_offline`
  and the served summary's checkpoint statement check went through
  checkpointed-local-log 0.3.1. That release uses ed25519-dalek's `verify`,
  which accepts a signature by a small-order public key, and anyone can make
  such a signature without the private key. Both checks, and
  `SealedRecord::verify`, now refuse non-canonical keys and R, small-order
  keys and R, and S >= L, and check the cofactorless equation, as the shared
  `ed25519-vectors.json` requires. The tests run those vectors.

- **`respond` and `ask`: the request verb at parity with the Go book**
  (evidencebook#131, part 1). `respond::respond` answers one request, in JSON
  or CBOR, over a caller's [`ask::Book`] (its records, clock, `seal` and
  `checkpoint`) with a signed envelope or a signed refusal of exactly one
  registered reason. It ports Go's `Respond` (`interop_respond.go`): the cost
  policy and rate limiter, the profile check, the deadline, derivation
  support, the requester gate (`SharePolicy`, a stranger is answered only the
  public questions), coverage and a freshness floor met by cutting a
  checkpoint only for an authenticated, well-formed request, retention
  lapse, the uniform refusal policy, and the answer limit. For an
  authenticated requester it seals `evidence_request_answered`, which names
  no requester and is never served. `ask::request` seals `evidence_request`
  before the request is sent (fresh nonce, a used nonce refused) and
  `ask::record_absence` seals `recorded_absence` when the waiting window
  closes. The record kinds and statements are the Go book's. The crate does
  not own a record store: the caller's `Book` seals the entries, as it seals
  every other record. New dependency: `ciborium`, already in the build
  through `evidence-request`.

- **Breaking: the served summary counts only verified witness receipts**
  (evidencebook#115). `served_summary::Coverage` takes the checkpoint's COSE
  `statement` and a `witness_directory`; `coverage.witnessed` and
  `coverage.witnesses` count only receipts that pass AAC's witness verifier
  (`agent_action_capsule::witness::verify_witness_receipts`, a new
  dependency with its `strict` feature) against that statement under that
  directory. A receipt the checkpoint only carries no longer reads as
  witnessed. The dependency enables `serde_json`'s `arbitrary_precision`
  feature in the build.

- **Breaking: `Derivations::register` refuses to change a registration**
  (evidencebook#86), as the Go book does. Registering a document again with
  the same recipient and the same sets of forms and fields returns the same
  identifier; with anything different it is the new
  `DerivationError::Redeclared`, and the first registration stands. Before,
  it replaced what was declared.

## 0.0.7

- **`derivation`: definition-digest derivations** (evidencebook#62), at
  parity with the Go book's `Book.RegisterDerivation`. `Derivations::register`
  takes a `DerivationDefinition` (the definition document, an optional
  recipient, the subject forms and the record header members it declares)
  and returns its identifier, the SHA-256 of the document's RFC 8785 form;
  the document must be a JSON object, read as the Go request reader reads it.
  A definition is never over `checkpoints` and declares only `HEADER_FIELDS`.
  Registrations are held in memory only. `Derivations::check` takes the
  derivation steps of a decision in the Go responder's order
  (`evidencerequest.Decide`): `derivation_unsupported`, then `not_authorized`
  for anyone but a named recipient (or, with no recipient, anyone the
  caller's share policy refuses), then `no_such_subject` for an undeclared
  form. It returns the plain reason: a responder under the uniform refusal
  policy maps it as it maps every other. `Derivations::forms_of` answers
  `Responder::derivation_forms`. The crate
  reads none of the definition's members (P-14).
- **`answer` answers a registered derivation** with `Options::derivations`
  set: RFC 8785 of `{anchor, derivation, evidence_stream, records, subject}`,
  each record its `digest`, `leaf_index` and the declared header members it
  has, read through the new `RecordSource::header_of` (default: no header,
  `AnswerError::RecordHeader`). `select` now selects a definition-digest
  derivation's subject records, as the subject without it; the underived
  artifact is unchanged. `testdata/derivation/vectors.json`, generated from a
  real Go book, is answered byte for byte by both implementations.
  A derivation prepared for a recipient is built for `Options::requester`
  only, whatever the caller decided before (`AnswerError::NotAuthorized`).
- **Breaking:** `Options` has two new fields, `derivations` and `requester`
  (add `..Options::default()` to a struct literal), and `AnswerError` two
  new variants, `RecordHeader` and `NotAuthorized`.

## 0.0.6

- **`store`: the read-only store an answer reads** (evidencebook#35). Two
  traits: `CheckpointedLog` (leaf count, stated checkpoints with their COSE
  statements, inclusion paths and consistency parts), which `CllSubstrate`
  implements in place, and `RecordSource` (records by committed entry and by
  leaf index, correlation, citing records), moved here from `answer` and
  re-exported there. `correlation` and `citing` now answer with leaf
  positions, not entries, so an entry a log commits twice keeps both.
  `RecordSource::leaf_index_of` finds a record's position
  without reading it (a record subject's selection uses it), and
  `RecordSource::entry_of` says what the log commits for a
  record: the SHA-256 of its octets by default, a `capsule_id` for a capsule
  ledger or a Go book (whose record id is its record capsule's
  id). A caller with its own checkpointer implements both over its own
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
  now means its octets do not recompute its committed entry, and
  `AnswerError::Artifact` is a derived value with no RFC 8785 form (it was
  a panic path once callers could derive).
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
