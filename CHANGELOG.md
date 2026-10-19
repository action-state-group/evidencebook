# Changelog

## Unreleased

### Added: the heartbeat cadence over a shelf, ended logs leaving the tick

evidencebook#36 §2 and §4.

- `Shelf.Cadence(ctx, CadenceConfig)` runs a profile's heartbeat ticks,
  ported from capsule-cli's `deal tick`. A tick is due on time alone
  (`Interval` 5m ± `Jitter` 1m by default, the next due time drawn after
  each tick). It checkpoints every live log locally at the tick's minute,
  commits the checkpoints as salted leaves of the cadence tree, appends the
  root (and padding to `PadBucket`) to the profile's cadence log, and
  checkpoints that log; the host delivers it to the witness and reports a
  verified receipt with `Cadence.Witnessed`.
- The live set: a log is checkpointed while it has not ended (the profile's
  `Ended` predicate, evaluated when needed) or has grown past the largest
  checkpoint of it a witnessed tick carries. Appends put a log back in the
  same transaction as its record; `Witnessed` takes logs out in one
  transaction, and a late receipt never moves a log back. Tick work is per
  live log; each tick keeps the lifetime count plus one occupied positions
  with seed-derived fillers.
- `Cadence.WitnessedTick` finds a bundle's tick from an index of witnessed
  log checkpoints (the very checkpoint, or the newest earlier one), not by
  scanning ticks. `Book.CheckpointAt` cuts a checkpoint at a given time.
- `Shelf.Legacy` joins a deal log from before the upgrade to the cadence as
  it is (never provisioning one); `LegacyLog.Append` runs the profile's own
  write under the shelf's lock and puts the log back in the live set.

### Added: the cadence tree, byte for byte capsule-cli's

evidencebook#36 §2 (the heartbeat cadence); the tick and live set come next.

- `cadence_tree.go` ports capsule-cli's cadence tree unchanged: leaf
  `SHA-256(0x00 ‖ JCS{checkpoint_sha256, log_id, salt, size})`, node
  `0x01`, empty `0x02`, filler `0x03 ‖ nonce`, depth 16
  (`CadenceDepth`), the sparse path and fold. `testdata/cadence` holds
  vectors written by capsule-cli 7a6b101's own code, and the port
  reproduces them.
- `TickLeaves` / `TickLeaf` are capsule-cli's stored tick leaves member for
  member, plus `filler_seed` and `filler_count`. `DerivedFillers` keeps a
  tick's occupancy at the profile's lifetime log count plus one when ended
  logs leave the tick, so a bundle's path shows no more than today; a tick
  stores one seed instead of a filler per ended log. `TickLeaves.Tree`
  rebuilds any tick's tree from what it stored; ticks a released build
  stored read unchanged.

### Added: a shelf of books, one log per deal

evidencebook#36 §1 (the deal profile on the book; one log per deal,
evidencebook#59).

- `OpenSQLiteShelf` opens one profile's books in one SQLite file: each book
  its own CLL log (`OpenCLLSQLite`, many logs in one file, as capsule-cli
  keeps a profile's deal logs) and its records and checkpoints in tables
  keyed by book id, with the profile's one artifact store shared. A deal's
  bundle is built from its book alone, so it carries no other deal's
  records.
- A shelf is the profile's only writer: it holds an exclusive advisory lock
  (`LockPath`, which a host points at its own writer lock) from open to
  `Release`, waiting for it until the context is done, and `Shelf.Open`
  returns one `*Book` per log id for its lifetime. After `Release`, the
  shelf and its books refuse writes (`ErrReleased`; `Book.Release` now does
  the same for any book, `SetRetention`'s purge retry included).
  `OpenCLLSQLite` provisions only a log the file holds nothing of: a log
  with state that fails to open is reported, never initialized over.

### Changed (breaking): payload bytes live in an artifact store

capsule-cli#236: one originals store, kept per record.

- `Config.Payloads` and `PayloadResolver`/`PayloadDir`/`OpenPayloadDir` are
  removed. `Config.Originals` is an `ArtifactStore`, capsule-emit's
  artifact store contract (`Put`, `Get`, `Purge`); `OpenOriginalsFile`
  opens its JSONL backend, owner-only and synced on every write
  (`SyncedFile` gives a host's file-backed store the same). A store's `Put`
  and `Purge` must be durable when they return. A record with payloads is stored there with its
  capsule and envelope in one `Put`, after it is sealed and before it is
  stored. `MigratePayloadDir` moves a payload directory's bytes into the
  artifact store and changes no record, log entry or checkpoint; a record
  the store already holds without a payload the directory still has is an
  error, never skipped. It folds lifecycle records as `Retention` does: a
  record whose latest state is DELETED is not stored, and a payload an
  earlier per-payload deletion named is left out.
- Retention is kept per record. `Book.Retention(ctx, record)` and
  `Book.SetRetention(ctx, record, state, reason)` lose their payload
  digest, `LifecycleStatement` loses `payload_commitment`, and `DELETED`
  purges the record's originals. The dedupe-aware deletion is removed: each
  record keeps its own copy.
- A retention commitment holds every record it covers at `LEGAL_HOLD` until
  its `until`, so `SetRetention(..., DELETED, ...)` returns `ErrLegalHold`.

### Added

- `Book.AppendCapsule`: a capsule its caller seals is the record itself,
  carrying its header in the `evidence_record_header` member
  (`RecordHeaderMember`), sealed under the book's writer lock and signed by
  the book's signer. `VerifyBundle` reads such a header from the capsule.
  A bundle discloses such a record's originals bound to its capsule's
  `agent_input` or `agent_output` digest as those members, so a neutral
  verifier matches them against the capsule.
- `RecordResponse` keeps the reply's octets as the response record's
  payload; `Book.ReplyOctets` reads them back.

## v0.4.0 (2026-10-19)

Everything since v0.3.0.

### Changed: one I-JSON reader and JCS, typed key sets, plain methods (#57)

- **BREAKING:** the UCP adapter reads its retained JWK Set from the new
  trust-policy field `key_sets`, not from `pins`.
  - `adapter.TrustPolicy` gains `KeySets map[string]json.RawMessage`
    (`key_sets`), one retained key set per name the adapter documents, kept
    as the JSON value it was written as.
  - Policy files must move `"pins": {"ucp.keys": "<JSON string>"}` to
    `"key_sets": {"ucp.keys": {...}}`, the JWK Set (or bare array) as a JSON
    value. A v0.3.0 `ucp.keys` pin is not read: the key check reports
    `NOT_CHECKED` with a detail that names `key_sets`.
  - Go callers: the exported constant `ucp.PinKeys` is renamed
    `ucp.KeySetName` (value unchanged, `"ucp.keys"`), and a policy built in
    code sets `KeySets["ucp.keys"]` instead of `Pins["ucp.keys"]`.
- x402 `payload_form` and ACP `object_form` now FAIL on invalid UTF-8 and on
  an escaped lone surrogate, instead of reading them as U+FFFD. All adapters
  share one I-JSON (RFC 7493) reader and one RFC 8785 serializer in
  `adapter/internal/ijson`, which adds no public API.
- Report JSON is no longer HTML-escaped: `PropertyResult.MarshalJSON` and
  `evidencebook adapt -explain` write `<`, `>` and `&` as themselves, so a
  method such as `relying party policy <id>` matches `-declare`.
- `docs/ADAPTERS.md` documents `key_sets` and the shared reader.

### Not in the Go module

- The Rust crate's store traits, received log and served summary (#56)
  shipped separately as `crates/evidencebook-v0.0.6`.

## v0.3.0 (2026-10-18)

Everything since v0.2.0.

### Added: the ACP and UCP adapters

- `adapter/acp`: the Agentic Commerce Protocol 2026-04-17 checkout session,
  completed session with order, order event and delegated payment token,
  after agent-action-capsule `spec/adapters/acp-v0.md` (2ef8aeb4). ACP signs
  none of them, so the adapter supports possession of exact octets only.
  - `octets_digest` is checked against a settlement-records leg's wrapped
    entry digest, or against the relying party's `acp.carried_digest` pin
    for a standalone adapt. The method names which.
  - `object_form` validates against ACP's JSON Schema, vendored by SHA-256.
    A completed session validates whole against JS-CHK, and an order
    event's data against OAS-WH `EventDataOrder`.
  - Native checks, each with `detail` `{"check", "object"}`:
    `checkout_session_signature`, `order_signature` and
    `delegate_payment_signature` are `NOT_PRESENT`. `order_event_hmac`, the
    webhook's shared-secret HMAC, is `NOT_CHECKED`, or `NOT_PRESENT` without
    the header.
  - It reads a settlement-records leg (wrapped types as proposed by acp-v0
    A-3, unregistered), a JSON body, an MCP JSON-RPC result, or an HTTP
    message. `evidencebook adapt acp`.
- `adapter/ucp`: the UCP adapter declaration v0 (agent-action-capsule
  `spec/adapters/ucp-v0.md`) for UCP v2026-08-25.
  - The checkout carrying `ap2.merchant_authorization` is checked natively:
    a detached JWS (ES256, ES384 or ES512) over the RFC 8785 form of the
    checkout without `ap2`, under a `keys[]` copy that the relying party
    retained and supplies in its trust policy.
  - An Order Event Webhook request held whole is checked with RFC 9421 and
    RFC 9530. A checkout without AP2, and an order body held alone, report
    `native_check` `NOT_PRESENT`: they prove possession only.
  - Objects are validated against UCP's JSON schemas, which are copied
    unchanged and pinned by SHA-256.
  - An operation response held whole adds `ucp.response_signature`. This
    is the RFC 9421 signature on the response, reported as its own
    property and outside the object's checks. An order, or a checkout
    without AP2, stays `NOT_PRESENT` whatever that signature says.
  - `evidencebook adapt ucp`.
- New dependency: `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3 (JSON Schema
  2020-12, ACP and UCP schema validation), which brings in `golang.org/x/text`.

### Changed

- The ACP and UCP adapters, `docs/ADAPTERS.md` and the UCP vector pin cite the
  merged agent-action-capsule declarations (#300 `75009695`, #301 `2ef8aeb4`).
  The vendored UCP vectors are byte-identical to that commit.
- `docs/PRD-EVIDENCE-INTEROP.md` records M3 as landed for x402, UCP and ACP.

## v0.2.0 (2026-10-17)

Everything since v0.1.1.

### Added: foreign-evidence adapters, starting with x402

`docs/ADAPTERS.md` describes the adapter interface and how an adapter reports.

- `adapter`: one interface for every adapter. `Name` and `NativeObject` (the
  FR-19 declaration as data). `Check(raw, TrustPolicy)` returns profile §7.1
  reports. A protocol's named native checks are reported as `native_check`
  with the check's name in `detail` (`NativeCheckResult`, `CheckName`,
  `FindCheck`). `TrustPolicy` carries pins, keys, a clock and `recorded`
  observations. `Evaluate` maps the checks a requirement rests on to its
  status, sufficiency and verdict:
  - rows 5 and 6 for a FAIL, by whether the requirement is the check or is
    only supported by it;
  - `NOT_PRESENT`, `NOT_CHECKED` and `INCONCLUSIVE` as profile §8.1 rows 3-4,
    pending a profile row.
- `adapter/x402`: the x402 adapter declaration v0 (agent-action-capsule
  `spec/adapters/x402-v0.md` at `3c778daa`), reproducing its vectors
  (`vectors/adapters/x402/`) exactly. Each check is a `native_check` report
  with detail `{"check", "object"}` (`adapter.NativeCheckReport`), and the
  table includes `authorization_window`. The recorded chain observation has
  the node's result shapes.
  - It reports the thirteen checks of the declaration's §7 table.
  - The payer signature is checked by EIP-712 recovery under the policy's
    domain. A mismatch is `INCONCLUSIVE` unless the policy says `eoa`.
  - Settlement is checked from a recorded transaction lookup, including the
    validity window at block time, and is `NOT_CHECKED` offline.
  - Payee receipts: version 1, and version 2 under either open proposal.
  - Three input forms: a settlement-records leg (with the content check of
    each wrapped object and leg cross-checks), the bare payment payload, and
    an HTTP header block.
  - Fixtures are constructed with published development keys.
- `cmd/evidencebook`: `evidencebook adapt x402 [-policy file] [-explain]
  <file | ->` prints the reports as JSON and exits 1 on any `FAIL`.
  `-explain` adds a human-readable explanation to each report, and
  `-declare` prints the adapter's declaration.
- New dependencies: `golang.org/x/crypto` (Keccak-256) and
  `github.com/decred/dcrd/dcrec/secp256k1/v4` (public-key recovery).

### Added: the third-party verifier guide

`docs/THIRD-PARTY-VERIFIER.md` is new in this release. §1 to §5 are a path
for testing evidencebook's service-evidence responses with your own verifier,
reproduced by `test/interop/answer`.
`docs/PRD-EVIDENCE-INTEROP.md`, the evidence interop product requirements
draft, is new too.

`docs/THIRD-PARTY-VERIFIER.md` §6 covers the personal-evidence disclosure
that the third-party verifier test also requires: a History at the Basic level
of the Personal-Agent History Profile v0, with the conformance vectors
published at agent-action-capsule `0b7b1825` (`vectors/personal-history-basic-v0/`,
12 cases). It says how to reproduce the vectors' responder run, the result
specified for each case, and how to report the History apart from the
service-evidence response.

- `test/interop/personal-history` is the corpus's `producer/main.go`,
  unchanged. Its test checks that it writes the corpus's `responder-run.json`
  byte for byte (`run_sha256` `f7d77d56…`), and that every value the guide
  quotes is the corpus's (`testdata/vectors.json`, pinned by digest).
- CI: a `personal-history` job reproduces the run, rebuilds the corpus from it
  with the corpus generator, and runs the corpus test, with counts per case
  class. The `interop-profile` job moves to the same agent-action-capsule
  commit, at which `vectors/interop-profile-v0/` is unchanged.
- The service-evidence part (§1 to §5) moves to the same agent-action-capsule
  commit, so the guide pins one commit. Its results follow the 53-case vectors
  that CI runs. The positive class's row `envelope-7` is `UNKNOWN`, `UNKNOWN`,
  `not_evaluable`, where the earlier vectors gave `null`. The four
  signed-absence cases are routed under missing evidence. A test pins every
  case and result the §5 table quotes against those vectors
  (`test/interop/answer/testdata/`, pinned by digest).

### Added: the Rust `evidencebook` crate

The Rust implementation moved here from checkpointed-local-log with its
history (action-state-group/capsule-emit#290 stage B): `rust/evidencebook/`
in a `rust/` workspace, depending on `checkpointed-local-log` 0.3.1 from
crates.io. Rust CI (`rust.yml`: vocabulary gate, format, Clippy, tests,
rustdoc, package checks) and a crates.io publisher (`publish-rust.yml`,
`crates/evidencebook-v*` tags, separate from the Go module's `v*` tags) come
with it. The crate's own releases (0.0.4 and 0.0.5 so far) are in
`rust/evidencebook/CHANGELOG.md`.

### Changed: the Evidence Request protocol comes from agent-action-capsule

The request draft's protocol logic now lives in
`github.com/action-state-group/agent-action-capsule/go/evidencerequest`
(v0.8.0), next to `draft-mih-agent-evidence-request-00` and its conformance
vectors (action-state-group/agent-action-capsule#262). evidencebook builds on
it: request parsing and validation, the JSON and CBOR encodings and digests,
resolution (`Decide` with evidencebook's share policy, derivations and refusal
policy as its `Policy`), reply parsing, refusal and envelope signing, and the
CLL proof checks and encodings. The `Book`-bound responder and requester,
storage, and the MCP and A2A carriages stay here.

- **API.** Unchanged for callers: `EvidenceRequest`, `Subject`, `Coverage`,
  `Freshness`, `Refusal`, `Anchor`, `VerificationEntry`, `Envelope`, `Reply`
  and `Message` are now aliases of the package's types (same fields), the
  reason, subject and media-type constants are the package's values, and
  `EncodeRequest` and `ParseReply` errors still match `ErrInvalid`.
  `ErrInvalid` is now `evidencerequest.ErrInvalid` itself, so errors from the
  aliased types' own methods (`Refusal.Verify`, `Envelope.Verify`) match it
  too; its message reads `evidencerequest: invalid` instead of
  `evidencebook: invalid`.
- **Behaviour.** Stricter where the package is: a JSON request or reply with
  an unpaired UTF-16 surrogate escape is malformed instead of being decoded
  with U+FFFD, and a CLL inclusion or consistency proof deeper than the MMR can
  be is refused instead of panicking.

### Changed: sealed with `github.com/action-state-group/capsule-emit/go` v0.3.0

evidencebook now imports `github.com/action-state-group/capsule-emit/go`
v0.3.0 instead of `github.com/action-state-group/capsule-emit-go` v0.1.0, which
is archived. Go treats the two paths as different modules, so a build that
uses capsule-emit with evidencebook needs the new path too.

- **Wire.** A new record's capsule carries `spec_version`
  `draft-mih-scitt-agent-action-capsule-05` instead of `-04`, so its record id
  (capsule id) differs from what the previous writer sealed for the same
  entry. The third-party verifier guide quotes the new pins and record ids of
  `test/interop/answer`.
- **Old records.** `-04` records keep verifying and keep their ids
  (`testdata/legacy-citation-purpose/` is still `-04`).

### Breaking: record capsules no longer carry the link type as `citation_purpose`

Every link on an appended record used to be sealed as a `references[]`
entry with `citation_purpose` set to the link type (`cites`, `supersedes`,
`closes`, `acknowledges`, `rebuts`, `adjudicates`). Those are Evidence
Layer link types (agent-action-capsule `spec/REGISTRY.md` §18), not
registered citation purposes (§11). The entry now carries no
`citation_purpose`, which the Capsule draft allows (`-05`, "Cross-record
references": a `references` entry MAY carry it). No link type is mapped to
a citation purpose.

- **Wire.** A new record's capsule differs from what the previous writer
  sealed for the same entry, so its record id (capsule id) differs too.
  The header, including `links[].type`, is unchanged; it stays the one
  place the link type is recorded, committed through `agent_input_digest`.
- **Old records.** Records already sealed keep verifying and keep their
  ids: nothing here reads `citation_purpose` back (link types are read from
  the header), and a verifier reports an unregistered value as
  informational, never as a rejection. A book written by the previous
  writer opens, takes new records and bundles both
  (`testdata/legacy-citation-purpose/`, `TestLegacyCitationPurposeRecordsStillVerify`).
- **Conformance.** New CI job `registry-conformance` runs the AAC
  producer-side check `agent_action_capsule.registry_conformance` (AAC
  #241, pinned to its merge commit `7fabe9a5`)
  over every capsule a book seals with each link type and a bundle
  (`test/interop/capsules`), and requires the legacy records to fail it.

### Evidence interoperability profile: A2A carriage

`Book.A2A` returns an `A2AServer` that carries the evidence request exchange
over A2A 1.0 (JSON-RPC binding) as profile `evidence-interop/0` §11.4.4
writes it (agent-action-capsule `spec/interop-profile-v0.md` at main
`1ea8f75`). It is a carriage only: `Respond` answers, and the canonical
Artifact is the one the native stream and MCP get for the same request
octets.

- **Declaration.** `AgentCard` lists one extension,
  `https://datatracker.ietf.org/doc/draft-mih-agent-evidence-request/`, with
  `required` false and `Book.Manifest` as its `params` (byte for byte the
  manifest `evidence_manifest` serves), `application/cbor` and
  `application/json` as default modes, and no skill. The card is the same
  for every caller and carries nothing about what the book holds.
- **Request and reply.** `SendMessage` with `A2A-Version` `1.0` and the
  extension in `A2A-Extensions`, whose `Message` is `ROLE_USER`, names the
  extension and holds one `raw` part of one of the two media types, is
  answered by handing the decoded octets to `Respond` unchanged. The reply,
  envelope or refusal, is a `TASK_STATE_COMPLETED` `Task` with one `Artifact`
  marked with the extension URI and one `raw` part of the request's media
  type.
- **Authorization.** `Handle(ctx, request, authenticated)` takes the
  requester the host's A2A security scheme authenticated, the same value as
  `RespondOptions.AuthenticatedRequester`. Nothing A2A carries is read as an
  identity: not an Agent Card, `metadata`, `tenant`, `contextId` or
  `messageId`.
- **Not a request, not a refusal.** A `data` or `text` part, more than one
  part, another media type, a `taskId`, another role, a missing extension or
  version, or an unwanted output mode get a JSON-RPC or A2A error, which a
  requester treats as a transport condition. No task is ever left rejected
  or failed in place of a refusal.
- **No task state, no SDK.** No task is kept: `GetTask` and `CancelTask` find
  none and `ListTasks` lists none, so no task is visible to another client.
  Identifiers are 128 random bits. Streaming, push notifications and the
  extended card are declared off and answered with A2A's errors. The JSON-RPC
  surface is implemented with the standard library and the package's strict
  JSON decoder; the host supplies the HTTP endpoint and its authentication.
- **Conformance.** The reference verifier now runs every carriage vector
  over this responder's native, A2A and MCP replies (`carriage-invariance`,
  and the A2A and MCP negative halves of §11.4.8 as mutations of its
  replies); none is skipped. CI checks the canonical Artifact is
  byte-identical across the three.

### Evidence interoperability profile: MCP carriage

`Book.MCP` returns an `MCPServer` that carries the evidence request exchange
over MCP 2026-07-28 as profile `evidence-interop/0` §11.4.5 writes it
(agent-action-capsule `spec/interop-profile-v0.md` at main `a726b9f`). It is
a carriage only: `Respond` answers, and the canonical Artifact is the one the
native stream gets for the same request octets.

- **Tools.** `tools/list` lists exactly `evidence_manifest` and
  `evidence_request`, with the profile's input and output schemas, fixed
  descriptive text and no `x-mcp-header`; the list is the same for every
  caller. `evidence_manifest` returns `Book.Manifest` as its
  `structuredContent`, byte for byte the manifest served elsewhere.
  `evidence_request` decodes the base64url `request`, hands the octets to
  `Respond` unchanged, and returns `{"reply", "media_type"}` with `isError`
  false, for an envelope and for a refusal alike.
- **Authorization.** `Handle(ctx, message, authenticated)` takes the
  requester the host's transport authenticated, the same value as
  `RespondOptions.AuthenticatedRequester`. Nothing in the MCP message is
  read as an identity: not `_meta` (`clientInfo` included), not an argument,
  not the connection.
- **Not a request, not a refusal.** Arguments outside the schema, an unknown
  tool, a missing or other protocol version, a batch or a malformed message
  get a JSON-RPC error, which a requester treats as a transport condition.
  Notifications get no response. Carriage members (`_meta`,
  `inputResponses`, `requestState`, the JSON-RPC id) never reach `Respond`.
- **No SDK.** The MCP surface the profile needs is `server/discover`,
  `tools/list` and `tools/call` over JSON-RPC 2.0, implemented here with the
  standard library and the package's strict JSON decoder. The host supplies
  the transport (stdio or Streamable HTTP) and its authentication.
- **Conformance.** The reference verifier now runs the carriage vectors over
  this responder's native and MCP replies (`carriage-invariance`, and the
  MCP negative halves of §11.4.8 as mutations of its replies); CI checks the
  canonical Artifact is byte-identical across the two. (The A2A-only
  carriage vectors were skipped until the A2A carriage, above.)

### Evidence interoperability profile (responder)

`Respond` now implements the responder side of the Minimum Evidence
Interoperability Profile `evidence-interop/0` (agent-action-capsule
`spec/interop-profile-v0.md`, read at main `6cae247` and the conformance
vectors at `5abc0da7`), over the request map of
`draft-mih-agent-evidence-request-00`. It implements only what the profile
defines.

- **Wire.** Requests are read in JSON or deterministic CBOR (`Message`: octets
  and media type) in the draft's shape: a one-member subject map,
  `expected_pin` as a digest, `min_freshness` as a size or a time. Unknown
  members are ignored; duplicate members, trailing bytes and case-variant
  names are not members. The profile's validation order decides the reason,
  so both coverage members or neither is `coverage_unsatisfiable`. The
  request digest is over the octets as received. All 33 `request.json` and
  18 of 19 `resolution.json` vectors pass (the 19th describes a log with no
  positions, which a Checkpointed Local Log cannot be).
- **Envelope.** An answer is the profile's signed envelope (`Envelope`):
  `profile`, `request_digest`, `subject`, `derivation`, `anchor` (`digest`,
  and `freshness` under `min_freshness`), `artifact_digest`, `verification`,
  `responder` and `issued_at`, signed in JCS, with the Artifact and the
  verification material beside them. The reply is in the request's encoding.
  The anchor is a checkpoint named by the SHA-256 of its signed statement.
- **Canonical Artifact.** A `record` answer is the record's own octets, proven
  with the profile's `cll-checkpoint`, `cll-inclusion-proof` and
  `cll-consistency-proof` material. Other forms use the stream binding's
  member names. No product-named member and no producer self-report.
- **Derivations.** `history_card/1` is served for `full_history` and
  `checkpoints`, to anyone. A recipient-specific projection is a derivation
  named by the digest of its definition (`RegisterDerivation`), answered only
  to its recipient, with only its declared fields.
- **Disclosure hygiene.** Every artifact is scanned against its declared field
  set and for the book's principal and counterparty references before
  release. Records of answered requests and of disclosures are never served.
- **Cost and abuse.** `Config.Responder` (`ResponderPolicy`): per-requester
  rate limits, request and answer size caps (`policy_declined`), the
  `uniform` refusal policy, `deadline_unmet`, and `retention_expired` for a
  lapsed retention commitment (`CommitRetention`). Unauthenticated and
  malformed requests never grow the log or force a checkpoint.
- **Absence.** `RecordAbsence` writes the profile's absence record
  (`AbsenceRecord`) and takes the route from the committed request.
- **Discovery.** `Manifest` returns the profile's history-free manifest.
- **SCITT stub.** Its receipts are never served as checkpoints or proofs;
  its bundle inclusion evidence is labelled `scitt-receipt-unverified`.

### Breaking

- `Respond(ctx, Message, RespondOptions)` returns `(Reply, Record, error)`.
  The old JSON request shape (`subject.kind`, `expected_pin` as
  `{root, mmr_size}`, `min_freshness` as `{size, time}`), `ArtifactResponse`,
  `Response`, `Pin` and `ParsePin` are removed. `range` positions are
  zero-based log positions.
- `RecordResponse` takes the reply `Message` as received.
  `OutcomeArtifactFailed` is now `artifact_verification_failed`;
  `refusal_nonconformant` records a signed refusal with an unregistered
  reason. `ResponseStatement.ArtifactKind` is gone; `NotChecked` lists the
  properties the profile gives no method for.
- An answer is no longer an Evidence Bundle and never carries record headers.
  Header fields are disclosed only through a registered derivation. Holder
  disclosures stay on `Bundle`, checked with `VerifyBundleAnswer`.
- An unauthenticated `min_freshness` request no longer makes the book
  checkpoint: it is answered from an existing checkpoint or refused
  `coverage_unsatisfiable`.
- `AbsenceStatement` is replaced by `AbsenceRecord`.

### Security

- `Respond` now fails closed. Protected subjects (everything except
  `checkpoints` and the `history_card/1` derivation) are answered only to a
  requester the transport has authenticated, passed in the new
  `RespondOptions.AuthenticatedRequester`.
  An unauthenticated caller gets only public subjects; every other request is
  answered with a signed refusal, reason `not_authorized`. This holds under
  every `history_segments` tier, including `peers`.
- A declared requester id is no longer trusted. `RespondOptions.RequesterID`
  is deprecated and ignored: it selects no relationship and grants no scope.
  Counterparty scope comes only from an authenticated identity that matches a
  `counterparty_ref`; a `counterparty_ref` value alone grants nothing.
- Requester ids are no longer stored in answerable records. The
  `evidence_request_answered` record no longer carries `requester_ref` (the
  field is removed from `AnsweredStatement`), in any form. It still records the
  request digest, subject kind, outcome, reason, relationship class and anchor.

### Breaking

- Callers that relied on `RequesterID` to unlock answers must now
  authenticate the requester at the transport and set
  `AuthenticatedRequester`. Without that change, those callers receive
  `not_authorized` for every subject except `checkpoints` and the
  `history_card/1` derivation.
- Records sealed before this change are immutable and still contain the
  `requester_ref` they were written with. Operators of books with such
  history should take that into account before serving `full_history` or
  `range` subjects that cover it.

### Changed: the CLL Go module moves to `checkpointed-local-log/go`

evidencebook imports `github.com/action-state-group/checkpointed-local-log/go`
v0.1.0 instead of `github.com/action-state-group/cll-go`. Go treats the two
paths as different modules.

### Fixed: a bundle's checkpoint states its log id

- A book's bundle writes `checkpoint.log_id`, the log id its checkpoint
  statement signs (`BundleCheckpoint.LogID`). capsulectl's `verify --bundle`
  up to v0.1.0-rc8 required it and failed every book bundle without it.
- `VerifyBundle` refuses a stated `checkpoint.log_id` other than the signed
  one when the checkpoint is authenticated, and one that is present but not
  a non-empty string (`ErrInvalid`). A bundle that states none reads as
  before.
