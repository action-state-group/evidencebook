# Changelog

## Unreleased

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
  `checkpoints`) are answered only to a requester the transport has
  authenticated, passed in the new `RespondOptions.AuthenticatedRequester`.
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
  `not_authorized` for every subject except `checkpoints`.
- Records sealed before this change are immutable and still contain the
  `requester_ref` they were written with. Operators of books with such
  history should take that into account before serving `full_history` or
  `range` subjects that cover it.
