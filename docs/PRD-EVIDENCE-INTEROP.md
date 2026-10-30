# PRD: Evidence Layer — Evidence Interoperability and History Sharing

**Draft · 2026-10-12 · status updated 2026-10-16**

**Status:** product requirements proposal. Not a standard, not a published protocol, and this document supersedes no published spec. The neutral pieces it builds on are individual Internet-Drafts and pre-Internet-Draft specifications, none adopted by any working group.

**Builds on:** `draft-mih-agent-evidence-request-00`, `draft-mih-agent-bilateral-attestation`, `draft-mih-agent-settlement-records-00`, `draft-mih-agent-evidence-layer-00`, `draft-mih-agent-disclosure-envelope-00`; Evidence Contract schema v0; Evidence Plan IR v0; Evidence Result v0. `evidencebook` is one implementation of the evidence layer and of the request/response outcomes; see [design.md](design.md) for how its types map to those drafts.

**Editor of the neutral drafts:** Action State Group (current editor only; the layer is meant to be implementable without any Action State Group software).

**Host profiles that plug into this layer:** e.g. a personal-agent history profile (`agent-action-capsule` `spec/personal-agent-history-profile-v0.md`, Basic level) and an inference-service profile (its requirements are in `docs/PRD-SERVICE-EVIDENCE.md` in [Mesh-LLM/capsules](https://github.com/Mesh-LLM/capsules); the profile itself is planned).

Status words used below: **exists** = verified in the cited file; **merged** = on the repository's `main`, cited as repository, pull request and short commit; **planned** = to be written; **unverified** = asserted by design, not checked for this PRD. Section 11 marks each decision **resolved** (with a pointer to the merged text that settles it) or **open**.

---

## 1. Problem

Agents now discover each other, invoke work, buy and pay across protocols (A2A, MCP, UCP, ACP, AP2, x402, MPP). Each protocol produces its own records, but a party that wants to rely on another agent's past or present conduct has no common way to:

- find out what evidence a counterparty can provide;
- ask for a specific piece of it and get an attributable answer, including an attributable "no";
- check that evidence **independently**, against **its own** requirements and trust roots;
- tell "requirement not met" apart from "not enough evidence to say", and ask for what is missing.

Today each integration invents this ad hoc, usually by trusting a summary from the party being evaluated, or by treating a digest of someone else's signed object as if it had been verified.

## 2. Users

| User | Wants | Success looks like |
|---|---|---|
| **Requester developer** — an unaffiliated developer building an agent that asks another agent for evidence | Discover evidence capabilities; send a well-formed request; receive artifact, signed refusal or record absence | Builds a requester from the spec text alone |
| **Responder developer** — an unaffiliated developer building an agent that holds evidence about its own actions | Advertise what it can prove; answer or refuse under its own disclosure policy; contribute its half of a two-sided record in its own native format | Never has to rewrite its records into another format or install another vendor's tools |
| **Relying party's verifier** — code (or a developer writing it) that decides whether received evidence meets the relying party's requirements | Verify source proofs per property; evaluate requirements; get "not established" rather than a false failure | Reproduces the same Evidence Result as any other conforming verifier from the same inputs and the same pinned policy |

## 3. Non-goals

- **No settlement network.** Payments stay on their rails; this layer records observations of them.
- **No reputation, score, ranking or aggregation** across relying parties.
- **No agent registry or directory.** Discovery reuses A2A Agent Cards and MCP tool listings.
- **No dispute adjudication.** Formal disputes stay with commerce, payment or legal processes; this layer records their outcomes as their statements.
- **No central trust authority.** Trust roots are the relying party's policy (FR-30).
- **Never the authoritative source of someone else's transaction.** A UCP order state, AP2 mandate, x402 payment response or peer-node observation keeps its origin and is verified with that source's own method.
- Not a replacement for A2A, MCP or any commerce/payment protocol; it is carried by them.

## 4. The minimum valuable standard

> *Tell me what evidence you can provide, let me request it, let me independently verify it against my requirements, and let me ask for what's missing.*

Everything else (history views, requirement sets, two-sided records, payment records, judging) composes around this exchange. Lifecycle:

| Phase | Question | Neutral pieces | Owned by others |
|---|---|---|---|
| **Agree** | What work and evidence are expected, and did the other side accept before acting? | Evidence Contract + signed acceptance (FR-10..FR-15) | A2A tasks, UCP/ACP checkout, AP2 mandates |
| **Record** | What happened and what did each party observe? | Agent Action Capsule, evidence layer, bilateral attestation, settlement records (payments only) | Payment rails, merchant order systems, node logs |
| **Resolve** | What can be verified, accepted, asked about or disputed? | Evidence Request, Evidence Result, lifecycle links | Formal dispute processes |

Flow of one exchange: **Discover** → **Request** → **Authorize and respond** → **Independently verify** → **Evaluate** → **Ask again** for what is not established.

## 5. Functional requirements

### 5.1 Capability discovery

- **FR-1** A responder MAY advertise evidence capability through an optional **A2A Agent Card extension** whose URI names the neutral `draft-mih-agent-evidence-request` draft, never a company or product. *(planned; exact URI pinned by the Interop Profile)*
- **FR-2** The same capability MAY be advertised as an **MCP tool listing** (request tool + manifest), carrying the same fields.
- **FR-3** The advertisement states: supported Interop Profile and draft versions, subject forms and derivations supported, request transports, refusal policy summary, and where retention commitments can be fetched. This mirrors the responder manifest the draft already recommends for its HTTP binding (`draft-mih-agent-evidence-request-00` §Transport Bindings).
- **FR-4** Discovery MUST NOT contain history, per-action identifiers, counterparty identities or counts. Discovery says what can be asked, not what exists.

### 5.2 Evidence Request

- **FR-5** Requests map onto `draft-mih-agent-evidence-request-00` exactly: a `subject` in one of its six forms (`full_history`, `checkpoints`, `record`, `range`, `correlation`, `exchange`), a coverage anchor, optional `derivation`, `deadline`, `nonce`, `route`. An inquiry that does not map is **refused** (`request_malformed`, `no_such_subject`, `derivation_unsupported`), never stretched with new fields.
- **FR-6** Three outcomes, kept distinct end to end: an **artifact response** (a signed response envelope carrying the canonical Evidence Artifact, FR-8); a **responder-signed refusal** with the request digest, exactly one registered reason token and a signed issuance time; a **requester-recorded absence** (request digest, route attempted, window). Pending is not an outcome. Transport errors are never refusals.
- **FR-7** `history_card/1`, the derivation registered by the draft (checkpoints, receipts and consistency proofs, no record content), is **preserved unchanged**: never renamed, re-scoped or repurposed. New history views are new, separately versioned derivations or profiles (FR-34).
- **FR-8** **Invariance: canonical Artifact, signed envelope, recipient-specific disclosures.** Three objects are kept separate:
  - **Canonical Evidence Artifact.** Its bytes are a deterministic function of (subject, resolved coverage anchor, derivation if present) only, and are independent of requester identity, principal reference, `nonce`, `route` and transport binding. This is the draft's caller-invariance rule: the same subject under the same coverage anchor "MUST yield a byte-identical evidence artifact, regardless of the requester's identity, the transport binding used, or any other property of the request" (`draft-mih-agent-evidence-request-00` §Caller Invariance; `nonce` and `route` "MUST NOT influence the artifact", §Nonce and §Route). Serialization is deterministic (RFC 8949 §4.2 for CBOR, RFC 8785 for JSON). Verification material (e.g. a consistency proof from the requester's earlier anchor) MAY differ per requester; the Artifact may not.
  - **Signed response envelope.** Per request; binds the request digest to the invariant Artifact digest, the resolved anchor, the responder identity and the issuance time. Two envelopes for the same subject and anchor differ in request digest and issuance; they name the same Artifact digest. *(The draft's envelope carries the artifact by digest, the resolved anchor and verification material (§The Artifact Response); the request-digest and issuance binding of the envelope is pinned by the Interop Profile, planned.)*
  - **Recipient-specific disclosures.** A projection made for one recipient (e.g. a privacy-safe History prepared for one counterparty) is a **distinct, explicitly identified subject or derivation** with its own identifier and declared field set (§Derivation-Scoped Disclosure). It is itself invariant for its own (subject, anchor, derivation), and is never presented as the canonical Artifact of a different subject. Different answers MUST NOT carry the same canonical Artifact identity.

  Authorization may decide **whether** to answer and which named derivations a requester may ask for; it never changes **what** a given (subject, anchor, derivation) yields.
- **FR-9** A cross-agent "ask about this exchange" uses the draft's `exchange` subject (the symmetric ask). Its user-facing name is neutral (open decision 6).

### 5.3 Evidence Requirements and acceptance (Agree)

- **FR-10** Requirements are expressed as an **Evidence Contract**: per requirement, the claim, acceptable epistemic types and sources, independence, minimum assurance, freshness, coverage, and whether evidence is needed before the action or may follow. The schema exists (`capsule-engine` `capsule_engine/schemas/evidence-contract-v0.json`; required root fields `id`, `version`, `requirements`). **A normative prose specification is planned**; until it exists, outside developers treat the Evidence Contract as a planned dependency.
- **FR-11** **New signed acceptance record** *(planned; no such record exists in any draft or schema today)*. States: **proposed** (requester sends a contract version bound to a transaction reference: A2A task id, UCP/ACP checkout, AP2 mandate, x402 resource, or a native exchange id) → **accepted / declined / countered** by the performing party, signed **before** the action, naming the contract version digest, the transaction reference and both parties → **pinned** (every later record and Evidence Result cites the accepted digest). A counteroffer is a new contract version, not an edit.
- **FR-11a** **Temporal ordering of acceptance (backdating).** A signature over a claimed timestamp proves only that the signer asserted that time; it does not prove the acceptance existed before the work. A verifier grants the strongest claim, **"accepted before action"**, only when temporal ordering evidence exists, such as:
  - a signed protocol exchange in which the counterparty received the acceptance before the action (e.g. a counterparty-signed receipt or the action request citing it);
  - execution records causally bound to the acceptance (they commit to the acceptance digest and are themselves anchored at a later position in an append-only log);
  - an independently registered commitment of the acceptance (a transparency-service receipt or a checkpoint witnessed by a third party) whose time precedes the action.

  Without such evidence the verifier reports only **"acceptance exists and execution cites it"**, and ordering as not established.
- **FR-12** A bilateral-attestation **acknowledgment is receipt only** and MUST NOT be treated as acceptance. `draft-mih-agent-bilateral-attestation` defines acknowledgment as recording receipt that "does not assert agreement with the attestation's contents"; an action attestation records how a request was handled, not acceptance of requirements.
- **FR-13** **Acceptance is not authority.** Agreeing to produce evidence does not show the agent may spend or bind its principal. A record asserting authority MUST cite the principal authorization (AP2 mandate, Verifiable Intent, delegation or task-authority record); otherwise verifiers label it an **agent-origin assertion**.
- **FR-14** **Session-level service evidence agreements:** one signed acceptance may cover a bounded session (requirements, policy, permitted spend, expiry); each exchange in it cites the agreement digest instead of renegotiating.
- **FR-15** Without an accepted contract, requirements are the requester's own expectations and verifiers MUST present them as such, not as commitments by the other party.

### 5.4 Two-sided evidence (public bilateral contract)

- **FR-16** Any party (a merchant, a service node, any A2A agent) can contribute its own signed record **in its native format**, without installing any particular vendor's software and without rewriting it as an Agent Action Capsule.
- **FR-17** The exchange semantics are those of `draft-mih-agent-bilateral-attestation` (request attestation, action attestation with disposition performed / declined / escalated, acknowledgment as receipt). Wire encodings are explicitly left to a future revision of that draft; the Interop Profile pins them with conformance examples.
- **FR-18** Each party's record is attributed to that party only. Agreement between halves is a verifier finding, not a claim either party can make alone.

### 5.5 Foreign-evidence adapters

- **FR-19** Every adapter for a foreign object declares: (a) native object and format, (b) issuer identity and how it is resolved under the relying party's trust policy, (c) native verification method, (d) the exact predicate it can support, (e) per-property result with the method that produced it.
- **FR-20** **Digest-wrapping is not verification.** Carrying a foreign signed object by digest proves possession of those bytes only; it does not establish that its signature or meaning verified, and the wrapping party gains no authority from it. Settlement records already carry foreign objects as exact octets without re-signing (`draft-mih-agent-settlement-records-00` §Wrapping Existing Signed Objects); a verifier still runs the native check.
- **FR-21** Adapters are built on demand after the reference exchange (§10); a first payment adapter (e.g. x402) and a first commerce adapter (e.g. UCP or ACP) are the expected first examples.

### 5.6 Payment profile

- **FR-22** `draft-mih-agent-settlement-records-00` (terms, payer-observed, payee-observed, delivered legs; typed payment reference; verifier-derived one-sided / agreed / mismatched state) is used **only when a payment is involved**. Non-payment work binds an accepted obligation to source evidence and MUST NOT carry invented payer/payee fields.

### 5.7 Evaluation (Evidence Result)

- **FR-23** Evaluation output is an **Evidence Result v0** claim per requirement, carrying `contract_ref`, `sufficiency` (`SATISFIED | GAP | INSUFFICIENT | UNKNOWN`) and `verdict` (`met | not_met | not_evaluable`).
- **FR-24** **Exact rule** (`agent-action-capsule` `spec/evidence-result-v0.md` §1): `verdict` is `met` or `not_met` **only when** `sufficiency` is `SATISFIED`; for `GAP`, `INSUFFICIENT` or `UNKNOWN`, `verdict` MUST be `not_evaluable`. Verifiers present `not_evaluable` as **"not established from the available evidence"**, never as failure.
- **FR-25** Per-requirement results are never rolled into one overall score or "trusted" flag; aggregates carry a coverage statement.
- **FR-26** A `not_evaluable` requirement yields a concrete follow-up request (FR-5) naming the missing evidence.

### 5.8 Source-proof verification statuses

- **FR-27** Verifiers report each checked property as one of **`PASS / FAIL / NOT_PRESENT / NOT_CHECKED / INCONCLUSIVE`** with the method used. *(planned vocabulary; defined by the Interop Profile, distinct from the Evidence Contract's per-requirement `bundleAssertionStatus`.)*
- **FR-28** **Two layers, not collapsed.** The source-proof verification result (FR-27) records exactly which check failed (e.g. invoice signature `FAIL`, method Ed25519 over the as-transmitted bytes). Sufficiency and verdict (FR-23) are specific to the **requirement evaluated**, and a `FAIL` maps to whatever that requirement's predicate implies, nothing more:
  - for a requirement that *is* the check ("is this invoice signature valid?"), a definitive `FAIL` is decisive evidence: `sufficiency` `SATISFIED`, `verdict` `not_met`;
  - for a requirement the check was meant to *support* ("was the service delivered?"), a failed invoice signature removes that evidence but does not establish non-delivery: the requirement is `GAP` or `INSUFFICIENT` with `verdict` `not_evaluable`, unless other evidence settles it.

  A verification failure never yields `SATISFIED` + `met` for a requirement that depends on the failed property (consistent with the informative mapping in `draft-mih-agent-evidence-request-00` §Mapping to a Requirement-Satisfaction Status, where a failed artifact verification is "never treated as satisfied").

### 5.9 Lifecycle links

- **FR-29** Corrections and challenges are new signed records citing the earlier one by digest; signed records are never mutated. Required semantics: **amends, contradicts, disputes, resolves, supersedes**, each carrying issuer, affected claim, basis and effective scope.
  - An issuer may **amend** or **supersede** only its **own** earlier statements; a `supersedes` by a non-issuer is invalid.
  - Others may **dispute** or **contradict**, never replace.
  - **Resolves** cites the resolving process (merchant, payment network, adjudicator) and is that process's statement.
  - Verifiers show both the **historical claim** and the **latest supported state**, including unresolved conflicts.
  - *Gap:* `draft-mih-agent-evidence-layer-00` registers `cites | adjudicates | supersedes | acknowledges | rebuts | closes` and forbids overloading tokens. `amends`, `contradicts`, `disputes` and `resolves` are **not registered**; they must be registered as new tokens or explicitly mapped (open decision 1).

### 5.10 Trust roots

- **FR-30** Key and issuer trust is the **relying party's policy** through a pluggable interface: issuer credentials, PKI, registries (e.g. ERC-8004), DNS-anchored keys (DKIM), or pinned keys. No mandatory central authority.
- **FR-31** Verification is evaluated **as of the event** and **as of now** separately (key rotation, compromise). A static artifact never implies current endorsement.
- **FR-32** **Verifier code comes from a source the relying party trusts** (a pinned published release or an independent implementation), never from the file being verified.

### 5.11 History as a view

- **FR-33** A History is a **generic selected-evidence view**: a signed selection over records from one subject, whose **count units are defined by its profile** (e.g. deal, exchange, checkpoint). It is a view, not the transaction protocol.
- **FR-34** A History profile declares its subject, units, selection/coverage rule, allowed assertions and passing-verification rule. **Completeness is not asserted** unless the profile's coverage rule (e.g. a pinned checkpoint interval) supports it. A History prepared for one recipient is a recipient-specific disclosure under FR-8, not a different version of another History.
- **FR-35** Distinct families (personal agent history, service history) remain separate subjects; composites only by explicit, evidence-backed links.

### 5.12 Proof-cost escalation

- **FR-36** Routine low-value activity carries minimal overhead (local seal, periodic checkpoint). The Evidence Contract MAY require stronger evidence (witnessing, counterparty confirmation, adjudication) per transaction.
- **FR-37** Evidence production and request handling carry explicit cost and abuse policies (rate limits, deadlines, `policy_declined` refusals) on the responder side.

## 6. Deliverable: Minimum Evidence Interoperability Profile v0

A separate, neutral, short normative document from which an unaffiliated developer can build a requester and verifier **from the text alone**. It MUST pin:

1. **Versions:** profile version, draft revisions referenced, and the version negotiation / unsupported-version behaviour.
2. **Wire examples** for every message: capability advertisement, request, artifact response envelope and canonical Artifact, signed refusal, absence record, Evidence Contract proposal, acceptance / decline / counter, Evidence Result.
3. **Canonical bytes:** encoding per object (deterministic CBOR for the `evidence-request/1` stream binding as the draft specifies; JSON with RFC 8785 where JSON is used), and the as-transmitted rule for foreign objects.
4. **Digest and signature binding:** what each signature covers; the envelope binding of request digest to Artifact digest, responder and issuance (FR-8); request-digest binding of refusals; contract-digest binding of acceptances and results; how recipient-specific disclosures are identified.
5. **Carriage:** A2A (Agent Card extension, message/task mapping, artifact parts) and MCP (tool names, inputs, outputs). **The native peer-to-peer stream binding and A2A run the same normative exchange; MCP is a third adapter**, not a separate protocol. The draft today defines only a peer-to-peer stream binding and an HTTP binding; A2A and MCP bindings are new.
6. **Result mappings:** request outcomes → per-property statuses (FR-27) → Evidence Result (FR-24, FR-28).
7. **Error codes:** the draft's refusal registry (`not_authorized`, `no_such_subject`, `coverage_unsatisfiable`, `derivation_unsupported`, `policy_declined`, `deadline_unmet`, `request_malformed`, `retention_expired`) plus any profile-level codes, kept separate from transport errors.
8. **Conformance vectors**, positive and negative. **Every fixture pins** its trust roots, verification policy, Evidence Contract version, evaluation time and input artifacts, so independent verifiers are compared on correctness, not on differing legitimate policies. At minimum:
   - valid positive exchange;
   - **invariance:** two requests for the same subject and anchor with different nonces, sent over two transports → identical canonical Artifact bytes, different envelopes (different request digests) naming the same Artifact digest;
   - **tampered** artifact; wrong request digest; forged refusal;
   - **declined** (signed refusal) request;
   - **missing evidence** (absence and `GAP`);
   - **unsupported version**;
   - foreign object that fails its native check, evaluated against both a requirement that is the check and one it merely supports (FR-28);
   - acceptance absent, or after the action;
   - **backdated acceptance:** an acceptance constructed after execution carrying an earlier claimed timestamp, with no ordering evidence → MUST NOT receive "accepted before action" (FR-11a);
   - non-issuer `supersedes`.

## 7. Non-functional requirements

**Privacy**
- **NFR-1** Minimum disclosure: derivations and disclosure tiers reveal only declared fields; payloads are withheld unless the profile and holder policy release them.
- **NFR-2** Every disclosure is **byte-scanned** for fields outside its declared set before release, and checked for **linkage** (stable identifiers, handles or digests that let a recipient join shares or identify a counterparty).
- **NFR-3** Discovery leaks nothing about history (FR-4).

**Security**
- **NFR-4** For any protected (non-public) evidence, the **authenticated requester is bound to the authorization and the final disclosure decision** before release. A caller-supplied requester id is not access control.
- **NFR-5** Handles, routes and follow-up addresses are **not bearer tokens**: possessing one grants no access; each follow-up is re-authorized by the holder.
- **NFR-6** Refusals are signed with issuance time and request digest, so they cannot be replayed as current policy.

**Offline verification**
- **NFR-7** Given the artifact, its verification material and the relying party's trust roots, verification works offline; any online check (as-of-now key status, native protocol lookups) is reported as a separate property, `NOT_CHECKED` when unavailable.

**Cost**
- **NFR-8** Routine exchanges add bounded overhead; `history_card/1` size is linear in checkpoints and independent of record count (per its definition in the draft). Profiles state their expected cost class.

## 8. Acceptance

**Mandatory before any standards or interoperability claim — the third-party verifier test:** an outside developer, using **none** of the editor's client or verifier libraries and their **own** trusted keys, implements request and verification from the Interop Profile, processes at least one personal-evidence disclosure and one service-evidence response, keeps their different units and source assertions, and returns compatible Evidence Results under each fixture's pinned policy (§6 item 8). **Pass only if** the positive, invariance, tampered, declined, missing-evidence, backdated-acceptance and unsupported-version vectors yield the specified results. Two programs from the same author exchanging well-formed bundles prove integration, not interoperability.

Also required:
- A responder that has never run the editor's software contributes a native record that verifies under FR-19.
- No `met`/`not_met` is ever produced for a non-`SATISFIED` requirement in any vector.
- For the same subject and anchor, two requests with different nonces over two transports yield byte-identical canonical Artifacts and distinct envelopes.
- Success is measured by independent recipients verifying supported claims and counterparties responding without adopting any one implementation.

**Status (2026-10-16): not passed.** The pass of this section remains the precondition for any public interoperability claim.

| Part | Status |
|---|---|
| Third-party verifier pack (service evidence) | merged: `evidencebook` #16 `30e3473` ([THIRD-PARTY-VERIFIER.md](THIRD-PARTY-VERIFIER.md), which pins `agent-action-capsule` `c86a73a`); this repository's CI runs the regenerated vectors at `agent-action-capsule` `dfc4703` (`evidencebook` #26 `43f3cb1`) |
| Personal-evidence half | vectors merged: `agent-action-capsule` #296 `0b7b182` (`vectors/personal-history-basic-v0/`, 12 cases); the guide section that points to them exists: [THIRD-PARTY-VERIFIER.md](THIRD-PARTY-VERIFIER.md) §6, #28 `3f68739` |
| Live node's bundle (§7 of the guide) | added: a lab node's `evidence-bundle/v2` from `evidence-network/loop` `b02f8cf`, with its specified results (#175); one exchange between two lab nodes, both halves, at `cfeaf09` (§7.4) |
| Clean-room rehearsal | internal, not an outside developer, so it does not count toward this section: a verifier written from the public texts only reproduced every result of the guide's §7 once the guide named the checkpoint's CLL -01 form (#41). The points it had to infer are AAC #460 |
| Run by an outside developer | **in progress**, owned by the project lead; not passed |

**How host profiles plug in**

*Personal-agent history profile.* The subject is a holder's key, the unit is a holder-selected deal, coverage is an explicit set with completeness not asserted, and every outcome claim carries its source label. A History prepared for one recipient is a recipient-specific disclosure (FR-8). It uses this layer's request, verification, Evidence Result and follow-up rules unchanged, and must pass the third-party verifier test. Its Basic level is specified in `agent-action-capsule` `spec/personal-agent-history-profile-v0.md` (#259 `995c377`); outcome claims with source labels are left to a later level there (PA-9).

*Inference-service history profile.* The subject is a node or service key; units are exchanges, checkpoints, witness receipts and referee verdicts over a pinned checkpoint interval; it may incorporate `history_card/1` unchanged and uses session-level agreements (FR-14). Checkpoint continuity is never presented as proof of execution. Requirements are written in `Mesh-LLM/capsules` `docs/PRD-SERVICE-EVIDENCE.md` (#24 `b982f8a`, a draft requirements document); the host profile itself is **planned**.

## 9. Dependencies and status

Every row cites the merged commit it was checked at, as repository, pull request number and short SHA ("AAC" is `action-state-group/agent-action-capsule`; a bare number is this repository), or says **planned**. Checked 2026-10-16 against AAC `main` `7662a00` and `evidencebook` `main` `43f3cb1`. "IP" is the Interop Profile v0, `agent-action-capsule` `spec/interop-profile-v0.md`. A rule IP pins is IP's own open point (P-n) until a draft revision adopts it.

| Dependency | Kind | Where | Status for this PRD | Merged at |
|---|---|---|---|---|
| `draft-mih-agent-evidence-request` | Internet-Draft | -00; `agent-action-capsule` `spec/` | exists; A2A and MCP carriage pinned by IP §11.4 (P-37, P-40), not yet in a draft revision; envelope request-digest binding pinned by IP §6.2 (P-7) | draft: AAC #133 `f350bf7`; IP §6.2: AAC #231 `3910a79`; IP §11.4: AAC #235 `db6fec1` |
| `draft-mih-agent-bilateral-attestation` | Internet-Draft | -03; `agent-action-capsule` `spec/` | exists; wire encodings TBD in draft | AAC #174 `0c9095a` |
| `draft-mih-agent-settlement-records` | Internet-Draft | -00; `agent-action-capsule` `spec/` | exists; payment profile only | AAC #159 `565d1f0` |
| `draft-mih-agent-evidence-layer` | Internet-Draft | -00; `agent-action-capsule` `spec/` | exists; implemented by `evidencebook` ([design.md](design.md)). Lifecycle links (FR-29) defined by IP §11.2: `supersedes` and `rebuts` as registered, `amends`, `disputes` and `resolves` as IP's own tokens (P-32); their registration in -01 is **planned** | draft: AAC #277 `4decf59`; IP §11.2: AAC #234 `11de30a` |
| `draft-mih-agent-disclosure-envelope` | Internet-Draft | -00; `agent-action-capsule` `spec/` | exists | AAC #133 `f350bf7` |
| Evidence Contract v0 | JSON Schema + profile prose | `capsule-engine` `capsule_engine/schemas/evidence-contract-v0.json` (embedded byte-identical in `capsule-cli` `internal/cli/schemas/`); prose in IP §11.3 | schema exists, pinned by IP as EC; prose exists as IP §11.3 (describes the schema, adds no member) | schema: `capsule-engine` #149 `fd43d43` (same bytes on `main`); IP §11.3: AAC #236 `6cae247` |
| Evidence acceptance record | profile record type | IP §11.1 | exists in IP, incl. ordering evidence (§11.1.5, FR-11a) and the backdated-acceptance vector (§11.1.9); not in a draft revision | AAC #232 `2e418f1` |
| Evidence Plan IR v0 | in-repo spec + schema | `agent-action-capsule` `spec/evidence-plan-ir-v0.md`, `schemas/evidence-plan-ir-v0.json` | exists (pre-I-D) | AAC #181 `b3e7620` |
| Evidence Result v0 | in-repo spec + schema | `agent-action-capsule` `spec/evidence-result-v0.md`, `schemas/evidence-result-v0.json` | exists (pre-I-D) | AAC #297 `7662a00` |
| Per-property verification statuses | profile vocabulary | IP §7 | exists | AAC #231 `3910a79` |
| Interop Profile v0 + vectors | neutral spec (pre-I-D) | `agent-action-capsule` `spec/interop-profile-v0.md`, `vectors/interop-profile-v0/` | exists; core, then §11.1 to §11.4; vectors and generator (§11.5); errata from the first responder build; vectors regenerated to 53 cases | core: AAC #231 `3910a79`; §11.1 to §11.4: AAC #232 `2e418f1`, #233 `370cc4e`, #234 `11de30a`, #235 `db6fec1`, #236 `6cae247`; vectors: AAC #237 `5beee54`; errata: AAC #243 `c86a73a`; 53 cases: AAC #263 `dfc4703` |
| Reference verifier | Python, in `agent-action-capsule` | `python/agent_action_capsule/interop_profile/` | exists; in every Python release from `python/v0.8.0`; `python/v0.9.1` is the first release that contains #263 | AAC #238 `dafa5a4` |
| Personal-agent history profile v0 | host profile (pre-I-D) | `agent-action-capsule` `spec/personal-agent-history-profile-v0.md`, `vectors/personal-history-basic-v0/` | Basic level exists; acceptance vectors exist (14 cases); outcome claims with source labels **planned** (PA-9) | profile: AAC #259 `995c377`; vectors: AAC #296 `0b7b182`; multi-stream cases (14 total): AAC #311 `f0cdd67` |
| Inference-service history profile | host profile | `Mesh-LLM/capsules` `docs/PRD-SERVICE-EVIDENCE.md` (requirements) | requirements draft exists; the profile is **planned** | `Mesh-LLM/capsules` #24 `b982f8a` |
| `evidencebook` responder | implementation | this repository | exists: conforming `evidence-interop/0` responder; fails closed without an authenticated requester (NFR-4); MCP carriage (IP §11.4.5); A2A carriage (IP §11.4.4) | responder: #10 `5a86956`; requester gate: #9 `9deaf49`; MCP: #11 `db4e312`; A2A: #13 `59ad618` |
| `evidencebook` interop CI | conformance run | this repository, `.github/workflows/ci.yml` | exists; runs the reference verifier and vectors pinned to AAC `9d653f4` | pin: #17 `8f655cd`; re-pin to 53 cases: #26 `43f3cb1`; re-pin to AAC `9d653f4`: #128 |
| Payment and commerce adapter declarations (FR-19) | neutral spec (pre-I-D) | `agent-action-capsule` `spec/adapters/x402-v0.md`, `ucp-v0.md`, `acp-v0.md`; `vectors/adapters/x402/`, `vectors/adapters/ucp/` | exist for x402, UCP and ACP; x402 and UCP ship native-check vectors (FR-21); ACP defines no issuer signature over its objects, so acp-v0 §6 has no native-check vector and the adapter reports its native checks `NOT_PRESENT` or `NOT_CHECKED` | x402: AAC #299 `3c778da`; UCP: AAC #300 `7500969`; ACP: AAC #301 `2ef8aeb` |
| `evidencebook` adapters | implementation | this repository, `adapter/`, `evidencebook adapt` ([ADAPTERS.md](ADAPTERS.md)) | exist: shared interface and x402, then UCP and ACP; the x402 and UCP tests run the AAC vectors byte for byte | interface and x402: #30 `7c57ff9`; x402 vectors: #32 `9348e6e`; ACP: #31 `5dcefed`; UCP: #33 `9696c02` |
| Third-party verifier pack (§8) | guide | [THIRD-PARTY-VERIFIER.md](THIRD-PARTY-VERIFIER.md) | exists for service evidence (#16 `30e3473`) and personal evidence (§6, #28 `3f68739`) | #16 `30e3473`; #28 `3f68739` |

Canonical draft pages: `https://datatracker.ietf.org/doc/<draft-name>/`. Tooling note: `capsule-cli` `contract validate` validates a document against a caller-supplied schema and `contract diff` diffs two Evidence Contract versions against the embedded schema (`internal/cli/contract.go`, `contract_diff.go`); neither is required to implement this layer.

## 10. Milestones and sequencing

The layer does not wait on specialized evidence producers: TEE attestation, split-stage execution and referee topologies are evidence sources a profile may use, not prerequisites.

| # | Milestone | Exit criterion |
|---|---|---|
| M1 | **Invariant Artifact, envelope and disclosure contract** | Interop Profile v0 core: canonical Artifact rules, envelope binding, recipient-specific disclosure identification (FR-8); refusal and absence shapes; acceptance record spec with ordering evidence; lifecycle-link issuer rules; Evidence Contract prose spec outline; §6 item-8 vectors with pinned policy, including invariance and backdated-acceptance |
| — | **Host profiles (in parallel with M1–M2)** | Personal-agent and inference-service history profiles written against the M1 contract |
| M2a | **A2A reference exchange** | One reference responder and one reference verifier pass all vectors over A2A and the native stream binding (MCP adapter passes the same vectors). **Landed:** the reference verifier (AAC #238 `dafa5a4`) over the `evidencebook` responder (#10, MCP #11, A2A #13) runs the 53 vectors with 0 failed: 7 against its replies, 21 as mutations of them, 25 skipped as not responder output (verifier-side version cases, foreign objects, acceptance, lifecycle). Canonical Artifact bytes are identical across callers, nonces, encodings and the native, MCP and A2A carriages. At AAC `dfc4703`, `evidencebook` `43f3cb1` (#26) |
| M2b | **Unaffiliated verifier** | An unaffiliated developer passes §8 without the editor's libraries, with findings fed back into the profile before any standards claim. **Open** (§8 status); M2 is not complete until M2b passes |
| M3 | **Payment and commerce adapters, on demand** | Each adapter declares FR-19 items and ships a native-check vector (FR-21). **Landed** for x402, UCP and ACP: declarations AAC #299 `3c778da`, #300 `7500969`, #301 `2ef8aeb`; adapters #30 `7c57ff9`, #32 `9348e6e`, #33 `9696c02`, #31 `5dcefed`. ACP has no native-check vector because ACP signs none of its objects (acp-v0 §6); its adapter checks possession of exact octets only |

## 11. Open decisions

1. **Lifecycle-link tokens:** register `amends`, `contradicts`, `disputes`, `resolves` as new tokens in the evidence-layer link registry, or map them onto existing tokens (`rebuts`, `adjudicates`, `supersedes`) with qualifiers. **Resolved** in IP §11.2.1 (AAC #234 `11de30a`, P-32): contradiction is carried as `rebuts`; `amends`, `disputes` and `resolves` are new tokens, registration proposed for evidence-layer -01. The -01 registration is **planned**.
2. **Acceptance record shape:** fields, signature coverage, session-level agreement form (FR-14), and which ordering-evidence forms (FR-11a) the profile names normatively. **Resolved** in IP §11.1 (AAC #232 `2e418f1`, P-24 to P-30): one record with four states, session scope (§11.1.3) and ordering evidence (§11.1.5).
3. **A2A and MCP bindings:** Agent Card extension URI, message/task mapping and artifact parts for A2A; tool names and schemas for MCP. **Resolved** in IP §11.4.4 and §11.4.5 (AAC #235 `db6fec1`, P-37 to P-40): the extension URI is the draft's Datatracker URL; MCP tools `evidence_manifest` and `evidence_request`. Not yet in a draft revision.
4. **`evidence-request/1` naming across encodings:** reconcile the CBOR stream binding's identifiers and map keys with the JSON names used by the HTTP, A2A and MCP bindings. **Resolved** in IP §4 (AAC #231 `3910a79`, P-5): one name table, text keys only; JSON bindings reuse the stream binding's string identifiers verbatim.
5. **Recipient-specific disclosure identity:** whether a per-recipient projection is identified as a new derivation (by definition digest) or as a distinct subject form. **Resolved** in IP §6.5 (AAC #231 `3910a79`, P-14): a new derivation identified by the digest of its definition, not a new subject form. IP does not fix the definition document's member names (still P-14); the personal-agent history profile fixes them for its Basic level (AAC #259 `995c377`, §4.1).
6. **Neutral name** for the cross-agent inquiry type over the `exchange` subject. **Resolved** in IP §11.4.6 (AAC #235 `db6fec1`, P-44): "exchange inquiry".
7. **History commitments:** whether deal-level commitments in a History are salted-digest commitments (opened by revealing nonce and text, checked like the Disclosure Envelope). **Open.** IP §6.5 item 5 (AAC #231 `3910a79`, P-20) makes withheld text in a recipient-specific projection a salted-digest commitment checked with the Disclosure Envelope's DE-3 computation. The personal-agent history profile's Basic level carries no payload commitment and leaves outcome claims to a later level (AAC #259 `995c377`, PA-9). No merged text says whether deal-level commitments in a History use this form.
8. **Evidence Contract prose specification:** standalone pre-I-D document or section of the Interop Profile. **Resolved** as a section of IP: §11.3 (AAC #236 `6cae247`).

## Sources checked

Public repositories; file paths are repository-relative.

- `action-state-group/agent-action-capsule`
  - `spec/draft-mih-agent-evidence-request-00.md` (subjects, nonce, route, artifact response, refusal registry, absence, status mapping, Caller Invariance, derivation-scoped disclosure, transport bindings, `history_card/1`)
  - `spec/draft-mih-agent-bilateral-attestation-03.md` (acknowledgment definition, dispositions, wire encodings TBD)
  - `spec/draft-mih-agent-settlement-records-00.md` (legs, derived states, wrapping existing signed objects)
  - `spec/draft-mih-agent-evidence-layer-00.md` (typed links and registry, three kinds of "no")
  - `spec/draft-mih-agent-disclosure-envelope-00.md` (scope)
  - `spec/evidence-result-v0.md`, `spec/evidence-plan-ir-v0.md`, `schemas/` (listing)
  - `spec/interop-profile-v0.md` (§4, §6.2, §6.5, §7, §11.1 to §11.5, §13), `vectors/interop-profile-v0/` (53 cases at `9d653f4`)
  - `spec/personal-agent-history-profile-v0.md`, `vectors/personal-history-basic-v0/` (14 cases at `9d653f4`)
  - `python/agent_action_capsule/interop_profile/` (reference verifier); release tags `python/v0.8.0` to `python/v0.9.1`
- `action-state-group/capsule-engine` `capsule_engine/schemas/evidence-contract-v0.json` (`fd43d43`)
- `action-state-group/capsule-cli` `internal/cli/contract.go`, `contract_diff.go`, `internal/cli/schemas/evidence-contract-v0.json` (`e9b1f21`; byte-identical to the capsule-engine schema)
- `Mesh-LLM/capsules` `crates/evidence-plugin/src/evidence_answer.rs` (serves `history_card/1`); `docs/PRD-SERVICE-EVIDENCE.md` (`b982f8a`)
- `action-state-group/evidencebook` `README.md`, `docs/design.md`, `docs/THIRD-PARTY-VERIFIER.md`, `.github/workflows/ci.yml` (`43f3cb1`)
