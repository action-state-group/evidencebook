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
2. commits each payload by the SHA-256 of its bytes and stores the bytes
   through the `PayloadResolver` (§6);
3. canonicalizes the header (JCS) and seals it as an Agent Action Capsule
   whose `agent_input_digest` is the digest of the header bytes, with every
   link also carried as a typed `references[]` entry;
4. writes the complete record to the `Store`;
5. appends the capsule ID, the record ID, to the substrate and requires the
   position it gets back to equal the header's `seq`;
6. updates the operational, authenticated and discovery indexes.

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
| §6 Record/Payload Separation | `PayloadResolver`, `PayloadDir`, `Book.ResolvePayload`, `UnavailableError` |
| §6.1 Retention States | `RetentionState` and its five constants, `LifecycleStatement`, `Book.SetRetention`, `Book.Retention` |
| §6.2 Disclosure Records | `DisclosureStatement`, `WithheldItem`, `PayloadsMode`, `Bundle.Disclosure` |
| §7 Index Classes, operational | `OperationalIndex`, `SQLiteIndex`, `Filter`, `RecordSource`, `Book.Query`, `Book.Rebuild`, `Book.IndexState` |
| §7 Index Classes, authenticated | `AuthenticatedIndex`, `SortedMerkleIndex`, `IndexRoot`, `LeafProof`, `KeyRangeProof`, `KeyRangeAnswer`, `Book.ProveKeyRange`, `VerifyLeaf`, `VerifyKeyRange`, `NewRecordIDIndex`, `NewSubjectIndex` |
| §7 Index Classes, discovery | `DiscoveryIndex`, `TokenIndex`, `Candidates`, `Book.Discover` |
| §8 Commitment-Substrate Interface | `Substrate`, `SubstrateInfo`, `SubstrateEntry`, `Checkpoint`, `InclusionEvidence`, `ConsistencyEvidence`, `IntervalEvidence`, `CLLSubstrate` / `OpenCLL`, `VerifyCheckpoints`, `SCITTReceiptSubstrate` / `NewSCITTReceiptSubstrate`, `Registrar`, `Receipt`, `Book.Checkpoint`, `Book.Checkpoints` |
| §8, Evidence Bundle v2 wire | `CompletenessCertificate`, `RangeWitness`, `Membership`, `LogCoordinates`, `MMRInclusion`, `BundleCheckpoint`, `Bundle`, `BundleRequest`, `Book.Bundle`, `VerifyBundle`, `VerifiedBundle`, `PeerRecord` |
| §9 Answering a Request | `SubjectKind` (six forms), `Subject`, `Coverage`, `Pin`, `Freshness`, `EvidenceRequest`, `ArtifactResponse`, `Response`, `Book.Respond`, `RespondOptions`, `AnsweredStatement`, `SharePolicy` |
| §9.1 Three Kinds of "No" | non-membership: `Book.ProveKeyRange` returning no matches; withheld: `WithheldItem`, `UnavailableError`; asserted absence: a `Refusal` with `no_such_subject` |
| Request draft: outcomes | `Refusal` (signed), `ArtifactResponse` (signed), `SentRequest`, `RequestStatement`, `Book.Request`, `Book.RecordResponse`, `ResponseStatement`, `Book.RecordAbsence`, `AbsenceStatement` |
| §10 Reconcile and Close | `ExchangeState` (six states), `Half`, `HalfSet`, `Comparator`, `SamePayloadCommitments`, `PairResult`, `Tallies`, `ReconcileHalves`, `ReconcileInput`, `Reconciliation`, `Book.Reconcile`, `CloseInput`, `CloseStatement`, `Book.Close`, `Book.Acknowledge`, `Book.Rebut`, `CloseStatus`, `StatusOfClose` |
| Out of scope in the I-D, needed by any implementation | `Signer`, `Ed25519Signer`, `KeyID`, `Store`, `FileStore`, `Config`, `Book`, `Open`, `Release`, the error values |

## Decisions worth knowing

**Header as the capsule's input.** The header is committed by digest, not
carried inside the capsule. A bundle discloses headers as the `agent_input`
member of its disclosure overlay, and the neutral verifier reports each one as
matched against its commitment or as withheld. Suppressing `agent_input`
leaves every header withheld with its digest (§6.2). `VerifyBundle` reads
every member from the one decoded tree the neutral verifier checked, by
exact key, and refuses a bundle holding two keys that differ only by case
anywhere in it, so the verifier and the reader can never see different
members. It reads a disclosed header only if re-encoding it reproduces the
disclosed JSON exactly.

**What a bundle discloses.** Only the selected set (the root's closure plus
`Include`) is ever disclosed. Other records in the interval ride along as
digest-only capsules, because Evidence Bundle v2 needs a membership for every
position up to the checkpoint's tip, and the disclosure record lists each of
their headers as withheld with its digest. `payloads: all` carries every
payload of the selected set and is refused if any is unavailable;
`payloads: selected` carries those not withheld; `WithholdPayloads` carries
headers only.

**Retention belongs to the record.** Retention state is kept per (record,
payload) pair, folded from lifecycle records that cite the record. Two records
that commit identical bytes have independent retention. Bytes are destroyed
only when no record still holds them in a resolvable state, and a failed
deletion is retried by asking for `DELETED` again. A later record that commits
the same bytes stores them again for itself; the earlier record stays
`DELETED`.

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

**Relationship gating.** `Respond` classifies a requester as `stranger` (no
identity), `counterparty` (named in `counterparty_ref` on one of the book's
records) or `identified`, and answers per `SharePolicy.HistorySegments` with
the same tiers and defaults as the mesh plugin's `share.*` object. The other
three switches travel unchanged; `Respond` does not act on them. Every subject
except `checkpoints` is gated. The requester identity is whatever the
transport supplied and is not authenticated here, so the gate is scope
reduction, not access control. Records of answering and of asking name the
other party in their statements, never in `counterparty_ref`, so neither
asking nor being asked changes anyone's relationship. An answer carries
headers only, never payload bytes. A subject the book holds only beyond the
resolved anchor is refused `coverage_unsatisfiable`, never `no_such_subject`.
A `min_freshness.time` in the future is refused without doing any work.

**Caller invariance.** An artifact depends only on the subject and the
resolved anchor, and is built under that checkpoint, so records committed
after it (including the record of the request itself) never enter it. Under
`expected_pin` it is byte-identical for every caller; `TestCallerInvariance`
checks that across nonces, routes and requesters. Under `min_freshness` the
book answers from the newest checkpoint it has issued that meets the floor,
and checkpoints only when none does, so two requests can resolve to different
anchors if a checkpoint was issued between them.

**Refusal signatures.** A refusal signs the sorted, compact JSON of
`issued_at`, `reason` and `request_digest`, the same body `capsule_emit`
signs. The reason tokens are the request draft's registry; `no_such_subject`
replaces the older `no_such_record`.

**Recorded absence belongs to the requester.** A responder grants or refuses.
Absence is the requester's own signed record that nothing arrived in its
window. `RecordResponse` and `RecordAbsence` take the id of the committed
request record and re-derive the request bytes, digest, deadline and window
start from it. `RecordAbsence` refuses while the request is pending,
including a window shorter than `MinimumAbsenceWindow` from the recording,
and both refuse once any outcome is recorded, so outcomes never convert.
`RecordResponse` requires `ResponderKeys`, the responder's signing and
checkpoint keys as the requester obtained them. A response that does not
verify under the pinned signing key is refused and not recorded, so a forged
message can neither become an outcome nor foreclose the real one. A signed
artifact is then checked against the recorded request: the signed anchor,
signed by the pinned checkpoint key; the subject the bundle states it
answers; the pinned checkpoint or the freshness floor; and, for a
`checkpoints` answer, every field of every checkpoint taken from its signed
statement plus every consistency proof. A signed artifact that fails those
checks is recorded as received-and-failed. `Request` gives every request a
unique nonce, so no two requests share a digest and an old answer cannot be
replayed onto a new request.

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
| Checkpoint consistency proofs | cll-go `mmr.VerifyConsistency` | `VerifyCheckpoints`, `TestCheckpointsArtifactProofsAreVerified` |
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
published vectors; the `ArtifactResponse` signing body has no second
implementation yet; the header JSON schema is checked for vocabulary parity,
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
- **The request wire shape is this package's.** The request draft fixes the
  six subject forms and the coverage rule, not a JSON encoding. The
  `EvidenceRequest` encoding here (`digest`, `first`/`last`, `value`;
  `min_freshness` as `size`/`time`) differs from the one `capsule_emit`
  currently speaks, so the two do not yet exchange requests directly.
- **The refusal signing body has no context member.** The artifact response
  body carries `"type": "evidence-artifact-response/v1"`. The refusal body is
  kept byte-compatible with `capsule_emit`, which signs exactly
  `issued_at`, `reason`, `request_digest` (verified in both directions), so
  adding a context member there is a coordinated change across both
  implementations, not a change this package makes alone. The two bodies
  have disjoint member sets, so neither signature verifies as the other.
- **The signer is called with the book locked.** A slow remote signer
  serializes the book.
- **Index operations are linear.** The in-memory authenticated index and the
  record cache are sized for single-node books; both rebuild from the log.
