# evidencebook design

`evidencebook` is a Go implementation of the evidence layer described in
`draft-mih-agent-evidence-layer-00` ("the I-D" below). The I-D is
implementation-independent: a store conforms by meeting its requirements, not
by using this module, and this module is one way to meet them.

Section numbers below refer to the I-D. Request and response shapes follow
`draft-mih-agent-evidence-request-00` ("the request draft"); bundles follow
the Evidence Bundle v2 format verified by `agent-action-capsule/go/bundle`.

## Layering

```
Book            records · epistemic types · links · disclosure · retention
                three index classes · request/respond · reconcile/close
  embeds
Substrate       append order · inclusion · consistency · checkpoint identity
  ├─ CLLSubstrate           Checkpointed Local Log (reference)
  └─ SCITTReceiptSubstrate  receipt-holding stub
```

The `Book` struct holds its substrate in an unexported field, and no exported
function, method, field or interface method returns or accepts a cll-go type.
`TestNoPublicAPIExposesACLLType` type-checks the package and walks the whole
exported surface to enforce that. Callers ask for records, bundles, answers,
key-range proofs and closes, and the book composes each proof: interval and
membership evidence inside `Bundle`, consistency inside the `checkpoints`
answer, and authenticated-index proofs through `ProveKeyRange`.

## How a record is committed

`Append`:

1. validates the entry against the record model (§4, §4.1, §5);
2. commits each payload by the SHA-256 of its bytes (§6);
3. canonicalizes the header (JCS) and seals it as an Agent Action Capsule
   whose `agent_input_digest` is the digest of the header bytes, with every
   link also carried as a `references[]` entry by digest. The entry carries
   no `citation_purpose`: a link type is an Evidence Layer link type
   (agent-action-capsule `spec/REGISTRY.md` §18), not a citation purpose
   (§11), and the link type is committed in the header's `links[]`, which is
   where it is read. Records sealed before this rule carry the link type as
   `citation_purpose`; they verify as before, since a verifier treats an
   unregistered value as informational and nothing here reads it back;
4. stores the record capsule, its Producer Envelope and its payload bytes
   in the artifact store, in one `Put` (only a record with payloads, or one
   its caller sealed);
5. writes the complete record to the `Store`;
6. appends the capsule ID, the record ID, to the substrate and requires the
   position it gets back to equal the header's `seq`;
7. updates the operational, authenticated and discovery indexes.

`AppendCapsule` commits a capsule its caller seals itself as the record,
with no wrapper record around it: a host's own capsule, signed by the book's
signer, becomes a record of the book. Under its writer lock the book takes
the position, builds the header and hands it to the caller's sealer, which
must carry it as the capsule's `evidence_record_header` member
(`RecordHeaderMember`); capsule_id covers that member like every other, so
the capsule commits its header without a digest. The book refuses a capsule
that does not carry exactly that header or is signed by another key, and
then continues at step 4. The caller's originals may be bound to the
capsule's own digests (its payload to `agent_input_digest`, say). The
position exists only in memory until step 5: a failure before it leaves no
record and the next record takes the same position, while the artifact
store may keep the abandoned capsule, which no log entry names.

Because a record is an ordinary capsule, a bundle this module produces is
checked by the neutral Evidence Bundle verifier with nothing from this module.
On open, a record stored but not yet appended (a crash between steps 4 and 5)
is re-appended at its own `committed_at`. Within a running book, a failed
substrate append leaves the stored record as a pending tail that keeps its
position and is retried before anything else is appended, so a transient
failure never lets two records claim one position. A substrate identity with
no stored record is reported as corruption, because the book could never
produce it. When a record is committed but a later step (indexing, an
automatic checkpoint) fails, `Append` returns the committed record together
with the error so the caller does not append it twice.

## Public types by I-D section

| I-D section | Types and functions |
|---|---|
| §4 Record Model | `Header`, `Record`, `Entry`, `HeaderVersion`, `StoredRecord` (durable form), `Book.Append`, `Book.Get` |
| §4.1 Epistemic Type | `EpistemicType` and its eight constants, `EpistemicTypes`; `schemas/vendor/epistemic-types.json` (vendored set), `schemas/record-header.schema.json` |
| §5 Typed Links | `Link`, `LinkType` and its six constants, `LinkTypes`, `Header.LinksTo` |
| §6 Record/Payload Separation | `ArtifactStore`, `OpenOriginalsFile`, `MigratePayloadDir`, `Book.ResolvePayload`, `Book.ReplyOctets`, `UnavailableError`, `Book.AppendCapsule`, `CapsuleSealer`, `RecordHeaderMember` |
| §6.1 Retention States | `RetentionState` and its five constants, `LifecycleStatement`, `Book.SetRetention`, `Book.Retention` |
| §6.2 Disclosure Records | `DisclosureStatement`, `WithheldItem`, `PayloadsMode`, `Bundle.Disclosure` |
| §7 Index Classes, operational | `OperationalIndex`, `SQLiteIndex`, `Filter`, `RecordSource`, `Book.Query`, `Book.Rebuild`, `Book.IndexState` |
| §7 Index Classes, authenticated | `AuthenticatedIndex`, `SortedMerkleIndex`, `IndexRoot`, `LeafProof`, `KeyRangeProof`, `KeyRangeAnswer`, `Book.ProveKeyRange`, `VerifyLeaf`, `VerifyKeyRange`, `NewRecordIDIndex`, `NewSubjectIndex` |
| §7 Index Classes, discovery | `DiscoveryIndex`, `TokenIndex`, `Candidates`, `Book.Discover` |
| §8 Commitment-Substrate Interface | `Substrate`, `SubstrateInfo`, `SubstrateEntry`, `Checkpoint`, `InclusionEvidence`, `ConsistencyEvidence`, `IntervalEvidence`, `CLLSubstrate` / `OpenCLL`, `VerifyCheckpoints`, `SCITTReceiptSubstrate` / `NewSCITTReceiptSubstrate`, `Registrar`, `Receipt`, `Book.Checkpoint`, `Book.Checkpoints` |
| §8, Evidence Bundle v2 wire | `CompletenessCertificate`, `RangeWitness`, `Membership`, `LogCoordinates`, `MMRInclusion`, `BundleCheckpoint`, `Bundle`, `BundleRequest`, `Book.Bundle`, `VerifyBundle`, `VerifiedBundle`, `PeerRecord` |
| §9 Answering a Request | `SubjectKind` (six forms), `Subject`, `Coverage`, `Freshness`, `EvidenceRequest`, `EncodeRequest`, `Message`, `Envelope`, `Anchor`, `VerificationEntry`, `Reply`, `ParseReply`, `Book.Respond`, `RespondOptions`, `ResponderPolicy`, `RequestLimiter`, `WindowLimiter`, `AnsweredStatement`, `SharePolicy`, `DerivationDefinition`, `Book.RegisterDerivation`, `Book.CommitRetention`, `Book.Manifest`; MCP carriage (profile §11.4.5): `Book.MCP`, `MCPOptions`, `MCPServer`, `MCPServer.Handle`, `MCPProtocolVersion`, `MCPToolManifest`, `MCPToolRequest`; A2A carriage (profile §11.4.4): `Book.A2A`, `A2AOptions`, `A2AServer`, `A2AServer.AgentCard`, `A2AServer.Handle`, `A2ARequest`, `A2AResponse`, `A2AProtocolVersion`, `A2AExtensionURI`, `A2AProtocolBinding` |
| §9.1 Three Kinds of "No" | non-membership: `Book.ProveKeyRange` returning no matches; withheld: `WithheldItem`, `UnavailableError`; asserted absence: a `Refusal` with `no_such_subject` |
| Request draft: outcomes | `Refusal` (signed), `Envelope` (signed), `SentRequest`, `RequestStatement`, `Book.Request`, `Book.RecordResponse`, `ResponderKeys`, `WitnessRow`, `ParseWitnessDirectory`, `ResponseStatement`, `Book.RecordAbsence`, `AbsenceRecord`, `VerifyBundleAnswer` |
| §10 Reconcile and Close | `ExchangeState` (six states), `Half`, `HalfSet`, `Comparator`, `SamePayloadCommitments`, `PairResult`, `Tallies`, `ReconcileHalves`, `ReconcileInput`, `Reconciliation`, `Book.Reconcile`, `CloseInput`, `CloseStatement`, `Book.Close`, `Book.Acknowledge`, `Book.Rebut`, `CloseStatus`, `StatusOfClose` |
| Out of scope in the I-D, needed by any implementation | `Signer`, `Ed25519Signer`, `KeyID`, `Store`, `FileStore`, `Config`, `Book`, `Open`, `Release`, the error values |

## Decisions worth knowing

**Header as the capsule's input.** The header is committed by digest, not
carried inside the capsule. A bundle discloses headers as the `agent_input`
member of its disclosure overlay, and the neutral verifier reports each one as
matched against its commitment or as withheld. Suppressing `agent_input`
leaves every header withheld with its digest (§6.2). A record its caller
sealed (`AppendCapsule`) is the exception: its header travels inside its
capsule, and its overlay members are the capsule's own (`agent_input` its
payload, `agent_output` its output), disclosed from the originals bound to
them and suppressed by the same names. `VerifyBundle` reads
every member from the one decoded tree the neutral verifier checked, by
exact key, and refuses a bundle holding two keys that differ only by case
anywhere in it, so the verifier and the reader can never see different
members. It reads a disclosed header only if re-encoding it reproduces the
disclosed JSON exactly.

**How a bundle is built.** On a CLL book, agent-action-capsule's producer
(`bundle.BuildEvidenceBundle`) builds the bundle from the book's own log,
through an unexported proof source that restores the MMR once per bundle; a
SCITT receipt book keeps the book's own assembly. The book keeps the
disclosure act: its closure over header links picks the range start and the
records it discloses, it builds the overlay and `evidencebook/payloads`, and
it commits the disclosure record. The bundle's closure claims
(`records_mode`, `missing`) are the verifier's, over each capsule's
`chain.parent_capsule_id` and `references[]`; a citation below the range
start is refused as `ErrCorrupt`. Request inputs the producer would refuse are
refused first as `ErrInvalid`, so a refusal it still returns is about the
book's data.

**What a bundle discloses.** Only the selected set (the root's closure plus
`Include`) has its headers and payloads disclosed. Other records in the
interval ride along without them, because Evidence Bundle v2 needs a
membership for every position up to the checkpoint's tip, and the disclosure
record lists each of their headers as withheld with its digest (unless the
record carries its header inside its capsule). Every carried record, ride-alongs
included, carries its Producer Envelope (`signature`, `key_id`), so a
recipient learns the key of each record's signer, every signer key the book
holds included (ruled on #84 §4). `payloads: all` carries every payload of
the selected set and is refused if any is unavailable, or if a disclosed
capsule commits an eligible, unsuppressed member the bundle does not reveal;
`payloads: selected` carries those not withheld; `WithholdPayloads` carries
headers only.

**Retention belongs to the record.** A record has one `retention_state`,
the one its header committed, changed only by later records: a lifecycle
record citing it, or a retention commitment covering it. There is no
per-payload state. A record keeps its own copy of its originals in the
artifact store, so two records committing identical bytes hold two copies
and there is no reference count. `DELETED` purges the record's originals
(`Purge`); its commitments stay, and a failed purge is retried by asking for
`DELETED` again. A retention commitment (`CommitRetention`) holds every
record its subject covers (the record, a range of positions, or the records
citing an exchange) until its `until`: an `AVAILABLE` or `PARTIAL` record
reads as `LEGAL_HOLD`, a `WITHHELD` one stays `WITHHELD`, and `DELETED` is
refused for all of them, as it is under a `LEGAL_HOLD` a lifecycle record
set. A lifecycle record written while state was kept per payload also names
a `payload_commitment`; it is read as applying to its whole record.

**Where a reply is kept.** `RecordResponse` keeps the reply's octets, as
received, as the response record's one payload, so the record that observes
a reply is where its bytes are read back (`ReplyOctets`).

**Record types the book writes.** `close`, `disclosure`, `lifecycle`,
`index_root`, `evidence_request`, `evidence_request_answered`,
`evidence_response`, `recorded_absence`, `acknowledgement` and `rebuttal`.
`record_type` stays open vocabulary for callers.

**Authenticated index roots are records.** `Book.Checkpoint` commits each
changed index root as an `index_root` record, epistemic type
`derived_metric`, before it checkpoints the substrate. Root records are not
themselves indexed. Otherwise each root commit would make the next one stale.

**Discovery never reaches a proof.** `Candidates` has no exported fields and
no exported function other than its own methods accepts it.
`TestDiscoveryCandidatesNeverReachProofs` enforces this by type.

**The profile, and what it pins.** `Respond` implements the responder side
of `evidence-interop/0` (agent-action-capsule `spec/interop-profile-v0.md`)
and nothing the profile does not define. The request map is the request
draft's (§6.1): a one-member subject map, `expected_pin` as a digest,
`min_freshness` as a size or a time, unknown members ignored, member names
matched exactly, and the profile's validation order, so both coverage members
or neither is `coverage_unsatisfiable` and every other non-conforming value is
`request_malformed`. Duplicate members and trailing bytes make a request not
well formed. A request is read in JSON or in CBOR (the `evidence-request/1`
frame), its digest is SHA-256 over the octets as received, and the reply is
written in the request's encoding: JCS for JSON, deterministic CBOR for CBOR.

**The canonical Artifact.** It is a deterministic function of the subject,
the resolved anchor and the derivation. For a `record` subject it is the
record's own octets, the capsule preimage whose SHA-256 is the record id, so
the envelope proves it with the profile's Checkpointed Local Log material
(`cll-checkpoint`, `cll-inclusion-proof`, and `cll-consistency-proof` when the
anchor extends an earlier checkpoint). The anchor is a checkpoint, named by
the SHA-256 of its signed statement; that digest is what a requester pins.
For the other forms it is JCS JSON in the stream binding's member names
(`anchor`, `evidence_stream`, `subject`, `records` with `leaf_index`,
`digest` and `body`, `checkpoints`, `history` with `checkpoint` and
`consistency_from_previous`, `derivation`); `range` positions are zero-based
log positions. No member names this product, and nothing the responder says
about its own checks is carried. For `checkpoints` and `history_card/1` the
Artifact is its own verification material. The envelope signs exactly the
profile's members (`profile`, `request_digest`, `subject`, `derivation`,
`anchor`, `artifact_digest`, `verification`, `responder`, `issued_at`) in
JCS, the same bytes in either encoding. `TestCallerInvariance` checks the
Artifact is byte-identical across nonces, routes, requesters, the profile
member and both encodings. Under `min_freshness` the book answers from the
newest checkpoint it has issued that meets the floor, and checkpoints only
when none does and the requester is authenticated, so two requests can
resolve to different anchors if a checkpoint was issued between them. An
`exchange` subject pinned by the requester's own half resolves to the
earliest checkpoint covering every checkpointed record citing it.

**Relationship gating.** `Respond` classifies a requester from
`RespondOptions.AuthenticatedRequester` only: `stranger` (not authenticated),
`counterparty` (the authenticated identity is named in `counterparty_ref` on
one of the book's records) or `identified`, and answers per
`SharePolicy.HistorySegments` with the same tiers and defaults as the mesh
plugin's `share.*` object. The other three switches travel unchanged;
`Respond` does not act on them. `checkpoints` and `history_card/1` are the
only public questions. Every other subject is gated and fails closed: a
stranger is refused `not_authorized` under every tier, including `peers`,
which means any authenticated peer. The gate is per subject
(evidencebook#114). Under `counterparties`, the default, a `record` or
`exchange` subject is answered only to a counterparty, and only when every
record it names (the record, or every record citing the half) names that
requester in `counterparty_ref`; `prospective` is read as `counterparties`,
as the mesh plugin reads it. It is decided before the subject is resolved,
so a requester no record names gets one refusal whether a record is held or
not; the uniform refusal policy also folds a counterparty's
`no_such_subject` into `policy_declined`. `range`, `correlation` and
`full_history` reach records no one requester is party to, and are refused
under every tier: only a recipient-specific derivation, the holder's grant
to the one requester it names, answers them. An artifact carries the
records its subject names and nothing else: a cited record appears only as
the digest in the citing record's links, and payloads only as their
commitments. The book does not authenticate anyone
itself: the transport sets `AuthenticatedRequester` only after it has
authenticated the caller. A declared identity (`RespondOptions.RequesterID`,
deprecated) is ignored, and a `counterparty_ref` value, a route or a handle
is not a credential, so knowing one grants nothing. Authorization decides
whether to answer, never what a (subject, anchor, derivation) yields.

**MCP is a carriage, not an answer and not a credential.** `MCPServer`
(`Book.MCP`) lists the profile's two tools and nothing else, moves request
and reply octets as opaque base64url, and calls `Respond` with the octets it
decoded, so the request digest, the reply's encoding and the canonical
Artifact are those of the native stream. `Handle` takes the authenticated
requester from the host, exactly as `RespondOptions.AuthenticatedRequester`;
an MCP client name, `_meta` member, argument or connection is never read as
one. What is not a request under §11.4.5 gets a JSON-RPC error, never an
envelope, a refusal or an `isError` result. The server implements only the
JSON-RPC methods the profile's binding needs (`server/discover`, `tools/list`,
`tools/call`) and leaves the transport to the host, so it takes no MCP SDK
dependency.

**A2A is a carriage too, and an Agent Card is not a credential.** `A2AServer`
(`Book.A2A`) lists one Agent Card extension, under the request draft's
Datatracker URI with `required` false and the manifest as its `params`, and
answers `SendMessage` whose `Message` holds the request octets in one `raw`
part by calling `Respond` with exactly those octets. The reply is a completed
`Task` holding one `Artifact`, marked with the extension URI, whose one `raw`
part is the reply octets in the request's media type, for an envelope and a
refusal alike; the canonical Artifact is the native stream's. `Handle` takes
the authenticated requester from the host; an Agent Card, `metadata`,
`tenant`, `contextId` or `messageId` is never read as one. What is not a
request under §11.4.4 gets a JSON-RPC or A2A error, never a reply and never a
rejected task. No task is kept, so none can be listed or fetched by anyone
(§11.4.3), and task, context and artifact identifiers are 128 random bits.
The server implements A2A 1.0's JSON-RPC binding for the one method the
profile needs and answers the others with the A2A error the protocol names
for an agent without streaming, push notifications or an extended card, so
it takes no A2A SDK dependency.

**Recipient-specific disclosure is a derivation.** A projection prepared for
one recipient is registered with `RegisterDerivation` and named by the
SHA-256 of the JCS of its definition document; the recipient is part of the
definition, so two recipients' projections have two identities. Registering
a definition again never changes what it declares: the same recipient, forms
and fields return the same identifier, and anything different is refused. It is
answered only to its recipient, carries each record's digest and position
and only its declared header fields, and is never listed in the manifest. The
canonical Artifact of the underived subject is never varied per recipient.

**Which records a correlation subject names.** A record is held under its
header's correlation identifier (`correlation.exchange_id`) when it carries
one, and under its `subject_ref` only when it carries none. So a host can
keep `subject_ref` for its own use, for example one value per step of a deal
for retries, and still answer for the deal as a whole. The two key spaces
answer the same requested identifier: a record filed under `subject_ref`
`deal-a` with no `exchange_id` is answered with records whose `exchange_id`
is `deal-a`. The `subject_ref` authenticated index proves `subject_ref`
membership only, and does not establish that a correlation answer is
complete (Personal-Agent History Profile v0 §1). This follows Steven's
direction of 2026-10-19 (PT); the spec reading is agent-action-capsule#328
O2, still open.

**What an answer may disclose.** Every artifact is scanned before release
against its form's declared field set, and refused `policy_declined` if it
carries a member outside it, or if any record it carries mentions a principal
or counterparty reference the book holds. The responder's bookkeeping
records (`evidence_request_answered`, `disclosure`) are never served, by any
subject form, so one requester's asking never reaches another. A book seals
an `evidence_request_answered` record only for an authenticated, well-formed
request; it names no requester.

**Cost and abuse.** `Config.Responder` (`ResponderPolicy`) sets a per-requester
rate limit (`RequestLimiter`, reference `WindowLimiter`), a request size cap
and an answer size cap, each refused `policy_declined` before any work, and
the refusal policy: `specific`, or `uniform`, which answers `policy_declined`
wherever `not_authorized`, `no_such_subject` or `coverage_unsatisfiable` would
say what the policy protects. A deadline already passed, or passed while the
answer was built, is `deadline_unmet`. A lapsed retention commitment
(`CommitRetention`) turns `no_such_subject` into `retention_expired`.
Unauthenticated and malformed traffic never grows the log and never forces a
checkpoint, and a time floor in the future is refused without work.

**Refusal signatures.** A refusal signs the sorted, compact JSON of
`issued_at`, `reason` and `request_digest`, the same body `capsule_emit`
signs and the profile pins (§6.3), with `issued_at` at seconds precision. The
reason tokens are the request draft's registry and nothing else.

**Recorded absence belongs to the requester.** A responder grants or refuses.
Absence is the requester's own record that nothing arrived in its window, in
the profile's shape (`AbsenceRecord`: `outcome`, `request_digest`, `route`,
`window` with `opened` and `closed`, `recorded_at`), carrying no responder
act, sealed in the requester's book. `RecordResponse` and `RecordAbsence` take
the id of the committed request record and re-derive the request bytes,
digest, deadline, route and window start from it; an absence naming a route
other than the one the request named is refused. `RecordAbsence` refuses
while the request is pending, including a window shorter than
`MinimumAbsenceWindow` from the recording, and both refuse once any outcome
is recorded, so outcomes never convert. `RecordResponse` requires
`ResponderKeys`, the responder's signing and checkpoint keys as the requester
obtained them. A reply that is not signed under the pinned signing key for
this request's digest is refused and not recorded, so a forged message can
neither become an outcome nor foreclose the real one. A signed envelope is
then checked as profile §7.3 says: profile, issuance time, subject and
derivation, artifact digest, verification binding, anchor match, and anchor
inclusion by the method the Artifact's form allows (for a record, the §6.2.1
procedure under the pinned checkpoint key; for checkpoints and history cards,
every checkpoint and consistency proof). A property the profile gives no
method for is listed in `ResponseStatement.NotChecked`. A signed reply that
fails a check is recorded as received-and-failed
(`artifact_verification_failed`, or `refusal_nonconformant` for a signed
refusal with an unregistered reason). `Request` gives every request a unique
nonce and the `profile` member, so no two requests share a digest. Evidence
Bundles a holder discloses (`Bundle`) are a separate path, checked with
`VerifyBundleAnswer`, which also checks every witness receipt carried on the
anchor under the requester's own witness directory.

**Reconcile correlation.** Pairing runs in two passes over all halves, so it
does not depend on input order: `exchange_id` first, preferring a counterpart
whose `request_digest` also agrees, then `request_digest` for halves still
unpaired. An agreeing `exchange_id` with disagreeing request digests is
`CONFLICTING` and is never paired on the weaker key. `twin_bracket_id` is
recorded when both halves carry it and is never a join key. The content test
is the profile's `Comparator`; the book supplies `SamePayloadCommitments` for
deterministic content only. Each side's window is in its own log positions.

**When absence is provable.** An unpaired half is `A_ONLY`/`B_ONLY` only when
the other side's account is complete, and `INSUFFICIENT` otherwise; it is
never `CONFLICTING`. The peer's account is complete only when its checkpoint
is authenticated, its interval starts at or before the peer window, and every
record it carries in the window has a verified header. A withheld header
could be anyone's counterpart, so it makes absence unprovable instead of
disappearing.

**Close.** `Close` runs the reconciliation itself from a `ReconcileInput`; a
caller never supplies tallies. It seals a `closes` link to every own record
placed. `StatusOfClose` reads the counterparty's records: a `rebuts` link to
the Close makes it `CONTESTED`, an `acknowledges` link `AGREED`, neither
`UNILATERAL`. It counts only records carrying the counterparty's book id,
under a checkpoint signed by the key the caller pinned for that counterparty.
An adjustment is a new Close carrying a `supersedes` link. The original is
never rewritten.

## Cross-implementation checks

| Check | Oracle | Where |
|---|---|---|
| Bundles and record capsules | `agent-action-capsule/go/bundle` and `go/verify` | `bundle_test.go` |
| A Go-built bundle, whole: graph closure, interval coverage, per-record membership, every capsule; a changed record fails | Python `agent_action_capsule.bundle.verify_bundle` (the reference the Go verifier ports), pinned 0.6.0 | `test/interop/python-aac/verify_bundle.py`, CI job `python-cll-interop` |
| A Python-built bundle (records sealed by Python `agent_action_capsule`, log, signed checkpoint, range and inclusion proofs by Python `cll`) fully verifies here; a changed record does not | `VerifyBundle` + `FullyVerified` | `test/interop/python-cll/build_bundle.py`, `test/interop/verify`, CI job `python-cll-interop` |
| Checkpoint statement, interval range proof, every membership proof | Python `cll` (`checkpointed-local-log`, pinned commit) | `test/interop/python-cll/verify_bundle.py`, CI job `python-cll-interop` |
| Checkpoint consistency proofs | cll-go `mmr.VerifyConsistency` | `VerifyCheckpoints` |
| Request map, resolution and refusal readings of `draft-mih-agent-evidence-request-00` | the draft's spec-derived vectors (`request.json` 33 cases in both bindings, `resolution.json` 19, `refusal.json` 21) | `testdata/evidence-request/`, `TestRequestVectors`, `TestResolutionVectors`, `TestRefusalVectors` |
| Profile envelope, refusal, CLL material, invariance, absence record, manifest, and A2A and MCP carriage (native, A2A and MCP replies to one request, the A2A and MCP negative halves of §11.4.8) | the profile's reference verifier (`agent_action_capsule.interop_profile`, its `carriage` module included) over the profile's conformance vectors | `test/interop/responder`, `test/interop/python-profile/verify_responder.py`, CI job `interop-profile` |
| The Python package (`python/`) against the same corpora: `request.json` 33 cases in both bindings, `resolution.json` 18 of 19 (the one with no positional ordering skipped, as in Go), `refusal.json` 21 | the same vendored vectors | `python/tests/test_vectors.py`, CI job `python` |
| The Python package's replies: 20 of the 53 profile vector cases run or mutated over them and passing; the 8 A2A and MCP carriage cases skipped, since the package answers over the native binding only; every member its run writes is equal to the Go run's, so each request and reply octet string in it is Go's byte for byte, and the artifact files are byte-identical | the profile's reference verifier, and the Go responder run | `test/interop/python-evidencebook/responder.py`, `test/interop/python-profile/verify_responder.py`, CI job `python` |
| The Python package reproduces the Personal-Agent History responder run, and its `-correlated` variant, byte for byte | the corpus's `run_sha256`, and the Go run | `test/interop/python-evidencebook/personal_history.py`, CI job `python` |
| Every registry-governed value a record capsule carries is registered on its own axis (no link type in `references[].citation_purpose`); records from the previous writer fail the check | `agent_action_capsule.registry_conformance` (AAC #241, merged `7fabe9a5`) | `test/interop/capsules`, `testdata/legacy-citation-purpose/`, `TestRecordCapsulesCarryNoLinkTypeAsCitationPurpose`, CI job `registry-conformance` |
| Reconcile correlation | `capsule-emit-mesh` `served_request_join.join_served_request`, on halves sealed by its sidecar | `testdata/reconcile-parity/` (generator and fixture), `TestReconcileParityWithMeshCorrelator` |
| Refusal signing body, both directions | Python `capsule_emit` | Python-signed, verified in Go: `testdata/refusal-interop/`, `TestRefusalSignedByCapsuleEmitVerifies`. Go-signed, verified by Python: `test/interop/python-cll/verify_refusal.py` in CI |
| Epistemic type value set | the vendored `epistemic-types.json` shared with other implementations | `TestEpistemicTypeThreeWayParity` |

The reconcile parity is correlation parity. The mesh correlator decides which
halves belong to the same exchange; it has no content test, so the parity
test runs with a comparator that always agrees. The fixture pairs one half
with one half; multi-half ordering is covered by
`TestPairingDoesNotDependOnOrder` in this repository only.

Not covered by an external oracle: the authenticated index's RFC 9162 hashing
is checked against itself (every leaf at every size from 1 to 17), not against
published vectors; the header JSON schema is checked for vocabulary parity,
not by validating produced headers against it.

## Known limits

- **The SCITT binding is a stub.** It keeps receipts in memory, cannot prove
  an interval or consistency (it returns `ErrUnsupported` instead), and its
  bundles carry per-record receipts with interval coverage reported as
  withheld. It exists to show that the book runs without CLL.
- **The checkpoint key is the substrate's.** `OpenCLL` takes an Ed25519
  private key because cll-go's checkpoint signer is key-based. Records,
  refusals and artifacts are signed through the `Signer` interface, so any
  custodian works for those. Routing checkpoint signing through `Signer` needs
  a cll-go signer constructor that accepts an external signing function.
- **Non-membership, range and completeness are the book's statements.** A
  membership proof is checkable against the committed root with no trust in
  the book. A non-membership, range or completeness proof checks neighbour
  ordering around the proved range, and is sound only if the committed tree
  is sorted and holds every record; a dishonest book can commit a tree that
  is neither and prove a false absence. Only a party holding the full leaf
  set can audit that, by rebuilding the root. `KeyRangeAnswer` has no
  standalone verifier for its root-to-record-to-anchor binding; a relying
  party checks it from a bundle carrying the `index_root` record.
- **Bundles run to the checkpoint tip.** Evidence Bundle v2 requires a
  membership for every position from the first carried record to the
  checkpoint's tip, so a bundle carries every record capsule in that
  interval. Capsules carry digests only. Headers and payloads of records
  outside the selected set are not disclosed.
- **Profile v0 has no proof method for record sets.** The CLL material kinds
  prove one record. For `full_history`, `range`, `correlation`, `exchange`
  and any registered derivation, the profile defines no material (P-13), so
  this responder sends none rather than invent a kind, and a verifier reports
  `anchor_inclusion` as `NOT_CHECKED`. Each served record carries its body,
  which hashes to its digest.
- **Only a Checkpointed Local Log answers.** A book over another substrate
  refuses every profile request whose answer would need an anchor
  (`coverage_unsatisfiable`): the SCITT stub's receipts are held, never
  verified, so they are never served as checkpoints or proofs, and its bundle
  inclusion evidence is labelled `scitt-receipt-unverified`.
- **The checkpoint's identity claims are cll-go's.** cll-go writes the
  checkpoint's CWT `sub` as `<log id>#<size>` and its `log_size` claim as the
  MMR size, so a relying party pins `(iss, sub, kid)` per checkpoint, and an
  envelope's size freshness is the MMR size. The size floor itself is
  compared with the number of entries, which never exceeds it, so no floor is
  met by weaker coverage.
- **Witness receipts are not in the history card.** The responder holds no
  witness receipts for its own checkpoints, so `history_card/1` lists each
  checkpoint and its consistency proof only.
- **The refusal signing body has no context member.** It is the profile's
  and `capsule_emit`'s body exactly (`issued_at`, `reason`,
  `request_digest`); the envelope's signed members differ (it always carries
  `profile` and `artifact_digest`), so neither signature verifies as the other.
- **The signer is called with the book locked.** A slow remote signer
  serializes the book.
- **Index operations are linear.** The in-memory authenticated index and the
  record cache are sized for single-node books; both rebuild from the log.
