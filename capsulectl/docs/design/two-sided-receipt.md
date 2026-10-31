# Design note: the receipt with the other party's half (9b frames 6 and 7)

Issues: evidencebook-deal#78 (a Mesh `x-mesh-*` half as the other party's copy), #79 (the
receipt read from a composition), #81 (a payer bundle with two rounds).
Follows evidencebook-deal#83 (0f873c1), which draws the receipt for one book.
Status: round 2, for verification by two Claude stand-ins and Sherlock2.

## Problem

Steven's 9b canvas draws the receipt in three cases: **stamped by both** (frame 6), **your
receipt only** (frame 7), and **no record** (named in the canvas's explainer). #83 draws the
receipt from one deal file, which holds one book. One book cannot say whether the other side
agrees, so that page says "Your copy only" and "Their side: not in this file". Sherlock2
accepted this as interim, not as the end state.

Frames 6 and 7 need the other party's half on the page, checked by the page. Sherlock2's final
answer on #78 (Buzz `069958ca`, with its conditions in `4835fefa`) sets four conditions:

- **C1.** The receipt keeps the same three cases and uses no Mesh wording. A missing field reads
  "not stated" and is never filled in.
- **C2.** "Stamped by both" shows only when the counterparty's half was received **and** its
  signature verifies. A half that is present but does not verify counts as "your receipt only".
- **C3.** The plugin builds the bundle, so the disclosure check runs on the node, not in the page.
- **C4.** An "as agreed" line about price or spend, or a "paid" line, goes to Steven if a Mesh
  record would be the source. Until he rules, the receipt shows neither from a Mesh record.

Sherlock2 accepted round 1 of this note with four copy conditions (PR comment
`issuecomment-6547329184`, Buzz `05a5ced4`). They are applied below and marked **S1** to **S4**.

This note settles where the other half rides, how the page reads it, and what each line says.

## What exists

- **A container for both halves: AAC `composed/v1`.** See agent-disclosure-bundle-01,
  "The composed/v1 Extension". The containing bundle is an ordinary `evidence-bundle/v2` whose
  root is the composing party's own record. Each member is the outcome of one Evidence Request
  and carries that request's digest: `artifact` (a bundle, plus the signed response without the
  artifact bytes), `refusal` (the signed refusal) or `absence` (the requester's recorded absence,
  with its window). One member is enough (`members: [+ ...]`). Joins declare that two members'
  root records concern the same thing. `capsulectl verify` checks the block, including member
  closure and join states. The core reports per member, per join and per pair
  (`ComposedResult` in agent-action-capsule `ts/src/composed.ts`): `refusalSignature` and
  `responseSignature` (`verified`, `invalid` or `signature_unverified`), join `result`, and a
  pair's `corroboration` (`redundant` on `same_observer`, `same_custody_domain` or
  `same_key_id`).
- **`capsulectl-deal deal request` and `deal compose`** (`internal/deal/deal_evidence.go`) already
  build `composed/v1` from recorded answers and absences. The root is a composition record on
  the deal's compositions log, action id `capsulectl-deal-composition`, payload
  `{type: deal_composition, profile: x-deal-v0, deal_id, composed_digest, request_records}`.
  A member's observer is the responder key its answer verified under, with that key's checkpoint
  key as custody domain; an **absence member's observer is the composer**, since only the
  composer observed it. Each pair of members is joined on `/deal_id`. `deal compose --html --keep`
  joins the composer's own keep copy at the page, through the bootstrap; today it carries only
  lines (label, note, the asked words when they recompute, the `did` lines). The keep copy is
  never a member. The page is the core's composed section, not deal-view.
- **Who the other party is.** No `x-deal-v0` record names the counterparty's signing key: its
  counterparty identifiers are per-deal HMAC fingerprints (PROFILE §4). The observer key of a
  member is what the composer declared it to be, checked only in that the member's records
  verify under it.
- **The page runtime can verify a bundle inside a bundle.** `EvidenceGraph.buildVerifiedBundleContext`
  (capsule-viewer `ts/src/browser.ts`) is in the runtime's module contract "for each member of a
  composed/v1 bundle" (capsule-viewer#167).
- **Precedence.** The core's composed view, `aac.builtin.composed/v0`, is a *section*. It is
  resolved in its own registry after the page (capsule-viewer `go/presentation/builtin.go`), so a
  page module that requires `composed/v1` is not ambiguous with it.
- **Mesh** (Mesh-LLM/capsules-plugin at `mesh/main` cc21587, `crates/evidence-plugin/src/`):
  - Each node seals its own `x-mesh-poc-v1` half on its one ledger. Both halves of a proxied
    exchange carry the same client nonce,
    `/model_attestation/compute_attestation/x-mesh-poc-v1/client_nonce`, which the plugin itself
    uses as the join key between them (`capsule_emit.rs` ~995, `evidence_log::client_nonce_digest`).
    A half with no forwarded nonce carries the placeholder `host-served-no-nonce`, which names no
    exchange. `serving_provenance.exchange_id` is minted per node and does not match across the
    halves (`capsule_emit.rs` ~999). `effect.request_digest` is the request body's digest, the
    same for the same prompt sent twice.
  - Neither half cites the other by a checked reference: "each is sealed before its node can know
    the other's id" (`producer/capsule.rs` ~750). A requester's half may carry the provider's
    *self-asserted* `peer_capsule_id`, forwarded by the host.
  - A received half is held, not chained. The receiver seals a citing record
    (`received_half.cited_capsule_id`, `own_half_capsule_id`, `signature_ok`, `sender_key_id`,
    and `same_controller_as_receiver` when both sides' controller bindings are known).
  - A node's checkpoint key is its announced signing key (`record_push_receive.rs` ~769), and its
    `key_id` is the raw 64-hex Ed25519 key.
  - The payer's deal receipt (`deal_receipt.rs`) carries only its own `x-deal-v0` records, with
    audience `keep`, and explicitly not the exchange record. Its sealed report's `did[].steps`
    cite the payer's exchange capsule id (`deal_flow.rs` 812-870).
  - Asking for the other half (`ask_book.rs`) seals `evidence_request`, then
    `evidence_response` or `recorded_absence`, under evidencebook's Evidence Request rules: the
    same profile `deal request` uses. A pushed half has no Evidence Request behind it.
- **Two rounds.** `x-deal-v0` PROFILE rule 7 lets an `open` close be followed by a later
  outcome, close and report. A later bundle carries every earlier report's Capsule with its
  payload withheld (PROFILE "Later bundles"). Mesh-LLM/capsules-plugin#169 (open) seals such a
  second round.

## Decision

### D1. The other half rides in `composed/v1`, read by deal-view

The receipt with both halves is a `composed/v1` bundle. deal-view registers a second page
manifest, `capsulectl.deal-view-composed/v0`, from the same script and stylesheet. It requires
`composed/v1` and forbids what the single-book manifest forbids. The single-book manifest adds
`composed/v1` to its `forbids.extensions`, so a composition that also carries `x-deal-v0`
resolves to one module. The core's composed section still renders below the page, unchanged, as
the raw view of the members.

The module renders only a **deal composition**, and refuses the page unless all of these hold
(**V0**):

- The root is a record sealed under action id `capsulectl-deal-composition`, with the disclosed
  payload `{type: deal_composition, profile: x-deal-v0, deal_id, composed_digest, ...}`. The Mesh
  plugin seals the same shape (D4), so the page has one rule.
- The root's signature verifies, and the containing bundle holds under `dealViewVerified`
  (closure, interval coverage, membership proven, every capsule OK, disclosures matched or
  withheld).
- The core's composed result is not malformed. `composedDigest` matches, the root payload's
  `composed_digest` equals the block's, and `compositionClosure` is `pass`.
- No join reports `join_state_mismatch`. The page re-derives what it shows (D3), and a block
  whose declared state disagrees with its records says something its records do not.
- The file is one `capsulectl verify` calls VALID, or INCOMPLETE only because it holds a
  refusal member (capsule-cli reads what was asked and not served as INCOMPLETE, `cli/composed.go`
  `composedOutput`): the block's status is not `fail`, and every carried member's own verdict is
  VALID, since `verify --bundle` counts each member's verdict in the file's (capsule-cli
  `cli/verify_bundle.go` ~239-245). The only findings tolerated are a refusal member and the
  members' `refusalSignature` and `responseSignature` being `signature_unverified`, which the
  core reports whenever a refusal or response is carried (`ts/src/composed.ts` ~730).
  `deal compose --html` refuses any file `verify` does not call VALID through `PageGate`
  (`deal_compose_page.go` 103), the refusal case included (see "Found while building").
  A viewer opening the file does not pass it through `PageGate`, and the page cannot recompute
  the whole verdict: parts of it (the report entries, sale and history entries, the witness
  directory, `cli/verify_bundle.go` ~415-480) exist only in capsule-cli. So the page checks what
  it can: the core's composed result (status not `fail`, no member claim `fail`, no digest
  `mismatch`, no signature `invalid`, no response binding finding), V1 on each of their
  artifacts, and the #83 sealed or standalone report check on any of their report text it draws.
  The rest of the verdict is `capsulectl verify`'s alone, and the page's "how to check" line
  names that command.

So a member whose own bundle fails, whose response does not bind, or whose refusal signature is
`invalid` makes the file INVALID, and no page is drawn: `capsulectl verify` names the member. ◇
Sherlock2: C2's "present but does not verify" then has two readings. A half the core verifies
but the page does not (V1's stricter membership, V2, V3's anchor, V4) is drawn as **your receipt only** with
the failed box. A half the core itself fails gives no receipt page at all, as every INVALID file
does today. The alternative, a page for an INVALID file, would need capsule-cli's verdict to
change first.

For a composition whose root is not a deal composition, `canRender` is false and the page
resolves as it does today. A deal composition that fails V0 is refused, never drawn.

Rejected:

- **A new extension in the single-book bundle carrying the other half.** It changes the
  `dealprofile` wire shape (evidencebook) to say what `composed/v1` already says, and it would be
  a second container for both halves.
- **A Mesh receipt model in deal-view.** Sherlock2 withdrew route (b) and ruled out Mesh wording
  (C1).

### D2. Who "you" are, and which half is theirs

- **You** are the composer: the key that sealed the composition's root record.
- **Your half** is the first of these that exists. It must hold under the same rules as a
  single-book page (`dealViewVerified`, the sealed or standalone report check), carry audience
  `keep`, have its root key equal to the composer's key, and name the composition's deal id. If
  any of these fails, the page is refused, as `privateHalfOf` refuses at build time.
  1. **The keep copy in the bootstrap** (capsulectl-deal, whose deal book and compositions log
     are separate logs). The bootstrap carries the keep **bundle**, not lines, and beside it what
     the #83 model reads from the bootstrap and cannot recompute: its `openings`, `countersign`
     and `words`, computed for the keep bundle exactly as `dealReportHTML` computes them
     (`deal_report.go` 441-466). The page verifies the bundle with `buildVerifiedBundleContext`
     and builds the #83 single-book model from it.
  2. **The containing bundle's own deal records** (Mesh, where the composition record sits on the
     same ledger as the payer's deal records, D4): the bundle's `x-deal-v0` records and its
     `extensions["x-deal-v0"].sealed_report`, read by the same #83 model. A page a viewer opens
     has no bootstrap: no openings (the asked words show as committed, as `deal-receipt-bundle`
     keeps them closed today), no countersign line, and the module's own words.
  3. Neither: "Your side" says it is not in this file, and the banner is never "stamped by both":
     it reads "Your copy is not in this file · not fully checked".
- **Their members** are every `artifact` or `refusal` member whose observer is not the composer,
  and every `absence` member (whose observer is always the composer). A composition of one deal
  has at most one other party, so more than one non-composer observer is refused.
- **They are not you.** For each of their `artifact` and `refusal` members (an absence's
  observer is you by definition), their observer and its declared custody domain, and for an
  artifact also the `key_id` of its root, must each differ from the composer's key and checkpoint key. If any is equal,
  their member is your own record under another label: the page is refused. This is the check the
  core reports as `redundant` between two members, applied between their member and you.
- **What "their key" proves.** Nothing in the records ties their observer key to the
  counterparty in the world: the composer declared it. The page says whose key it is only as
  "their key" with its first 16 hex digits in the fold-out, so a reader who knows the
  counterparty's key can compare. For a Mesh half that was also pushed, the payer's citing record
  is checked against it (D4); an ask-only half has no citing record, and its absence proves
  nothing. Sherlock2 accepted "stamped by both" on a declared key that differs from yours, with
  these guards (round 2, Buzz `51726629`), on condition that the page never shows a name or label
  for them from the composer's declaration: only "their key" and its digits.

### D3. The three cases

Each of their members has a state:

| Their member | Checked by the page | State | Their side box |
|---|---|---|---|
| `artifact`, V1-V4 below all pass | yes | verified | amber, "Their side · in their book · as received" |
| `artifact`, V1, V2, V3's anchor check or V4 fails (the core's V3 checks failing make the file INVALID: V0) | the failing check | failed | grey dashed; their record is in the file but did not check: "failed" on that stamp, never a line that reads like absence (S4) |
| `refusal`, `refusalSignature` `verified` | the refusal's signature | refused | grey dashed; "They answered that they would not share their record, on DATE." (S2) |
| `refusal`, `refusalSignature` `signature_unverified` | the refusal's presence | refused, unchecked | grey dashed; "A refusal in their name came back on DATE; its signature was not checked here." (S2) |
| `absence` | the absence member | absent | grey dashed; "No record. Your agent asked and nothing came back by DATE; that is written down too." |

The TS verifier reports every refusal `signature_unverified` today, so the unchecked refusal line
is the one a page shows until a verifier implements the deployment's signature profile.

The case follows from all of their members together, in this order:

1. Any of their members **failed**: **Your receipt only**, with the failed box. A passing copy
   beside a failing one never gives "stamped by both".
2. Otherwise any member **verified**: **Stamped by both**. A refusal or absence beside it is
   listed in the fold-out, by date.
3. Otherwise a **refused** member (the latest, by its `issued_at`): **Your receipt only**, with
   its refusal box.
4. Otherwise an **absent** member (the latest window): **Your receipt only**, with the absence
   box.
5. No member of theirs: **No record**, grey dashed, "No record. Nothing from their side is in
   this file."

Their `artifact` member verifies when:

- **V1.** `dealViewVerified(await buildVerifiedBundleContext(member.bundle))` holds: closure,
  interval coverage, membership proven (stricter than the core, which accepts unbound
  membership), every capsule OK, disclosures matched or withheld. The core's per-member result
  (`ComposedMemberResult`: its three `bundle` claims and `digest` `reproduced`) must agree, or
  V0 has already refused the page.
- **V2.** Its root record's producer signature verifies (`verifyProducerEnvelope`) under a key
  equal to the member's declared observer. Its checkpoint's key is that observer's
  `custody_domain`. This is C2's "its signature verifies": the half's own signature, as sealed in
  their book. For an `x-deal-v0` copy the observer is the key the response was signed with
  (`deal_evidence.go` 1039), and `deal request --response` does not require the copy's root to
  be sealed under that same key: a responder that rotated its key, keeping the old one trusted,
  answers under the new key for steps sealed under the old (`TestTermsResultRefusesACloseUnderAnotherKey`).
  Such a copy is recorded as an artifact, as today, and V2 fails it on the page: the failed box,
  since the page cannot tie the two keys.
- **What `deal request` recorded.** `verifyDealCopy` records a genuinely signed copy as
  `artifact_failed_verification` for reasons the composed block does not carry: a wrong anchor,
  not the pinned checkpoint, too little coverage or freshness. The page
  cannot see that judgment, and it cannot recheck coverage, since the request is not carried.
  So `deal compose` refuses a member whose recorded outcome is `artifact_failed_verification`,
  naming it and the recorded reason: no file is written, as for an INVALID file (V0), rather than
  a composition that draws as verified or one that leaves the half out and reads as no record.
- **V3.** The member carries its `response`, and the core reports it bound: digest `reproduced`,
  no `response_request_digest_mismatch` or `response_artifact_digest_mismatch` finding, and
  `responseSignature` not `invalid`. Beyond the core, the page checks that the response's
  anchor is the member bundle's checkpoint; a member that fails only this gets the failed box.
  The response comes in one of two forms, told apart by its `anchor`:
  - **The deal wire's response** (`deal_wire.go`, an `x-deal-v0` copy): `anchor` is the string
    `root:mmr_size` of the member bundle's checkpoint, as `verifyDealCopy` checks it.
  - **An evidence-interop envelope** (interop profile v0 or v1 §6.2, a Mesh half, D4): `profile`
    is `evidence-interop/0` or `evidence-interop/1`, `anchor` is `{digest, freshness?}`, and
    `artifact_digest` equals the member digest, so the envelope's artifact octets are the JCS
    of the bundle with no countersignatures (`BundleDigest`). `anchor.digest` is the SHA-256
    over the octets of the member bundle's checkpoint COSE, in the profile's one
    `cll-checkpoint` form (§6.2.1). The page does not compute it: the module never hashes, and
    reads only the verified context. That binding, like the envelope's signature, is checked by
    `capsulectl verify`, and the page holds the envelope only to its profile. The deal wire's
    string anchor is no stronger on the page, since its signature is not checked there either.

  The response's own signature is a deployment signature that this verifier reports
  `signature_unverified`. The page does not count it as checked, and their side's fold-out says
  that the reply's signature and the checkpoint it names are checked by `capsulectl verify`, not
  on the page (Sherlock2's condition, round 5, Buzz `edd2c83a`).
  `deal compose` checked that signature when it recorded the answer; for an interop envelope the
  plugin checked it when it recorded the ask's outcome.
- **V4.** It is about this deal:
  - For an `x-deal-v0` copy: its root carries the composition's deal id, the same id your half
    carries. When a member of yours is also in the block, the core's join between the two
    derives `agree`.
  - For a Mesh half: see D4.

The page never says "stamped by both" from the block's join states alone: it derives V1-V4
itself.

The banner reads **"Stamped by both sides · checked"** only when the case is stamped by both
**and** your half holds as #83 defines it. Otherwise the banner names the case: "Your receipt
only · checked" or "No record · checked" when your half holds, and the "· not fully checked" form
when it does not. A single-book page (no composition) keeps #83's "Your copy only" and "Not in
this file": one file cannot know whether the other side keeps a record.

### D4. A Mesh half as their member (#78)

The plugin builds the composition (C3). This repository defines what deal-view reads, and the
plugin-side work is its own task in Mesh-LLM/capsules-plugin. Every field below is what the page
reads; a composition missing one is refused (V0) or fails the member (V1-V4).

- **Both bundles are interval-complete.** `dealViewVerified` needs interval coverage `pass`,
  and a producer selection, which `deal-receipt-bundle` is today, reads `withheld`
  (`deal_receipt.rs` 13-14). So the containing bundle and the provider's member each carry every
  record of their ledger in the interval they cover, the records outside the deal and the
  exchange with payloads withheld. ◇ Sherlock2, for the plugin task: those records' Capsule
  headers are then in the file.
- **The containing bundle** is a bundle of the payer node's ledger under one signed checkpoint.
  - Its root is the payer's composition record, as V0 says: action id
    `capsulectl-deal-composition`, payload `{type: deal_composition, profile: x-deal-v0,
    deal_id, composed_digest, request_records}`, where `deal_id` is the payer's deal for the
    exchange and `request_records` the ask records the members answer.
  - It also carries the payer's own half: the records `deal-receipt-bundle` carries today (its
    `x-deal-v0` records and `extensions["x-deal-v0"].sealed_report`, audience `keep`) **and**
    the payer's exchange half that the report's `did[].steps` cite. That half's payload may be
    withheld: the client nonce is in the Capsule's `model_attestation`, not its payload.
  - It may carry the payer's citing record for the provider's half.
- **The provider's member** comes only from an ask (`ask_book.rs`). Its `request_digest` is the
  ask's `evidence_request` digest, and its outcome is that ask's recorded outcome:
  - `artifact`: the provider's evidence bundle, whose root is its own `x-mesh-poc-v1` half,
    proven under its signed checkpoint and interval-complete. Its `response` is the signed
    evidence-interop envelope the provider answered with, as received, minus `artifact` (V3's
    second form). That needs an answer form the plugin does not have yet: one that serves this
    bundle, its artifact octets the JCS of the bundle (Mesh-LLM/capsules-plugin#196, gap G1);
    no answer form today is an Evidence Bundle;
  - `refusal`: the signed refusal;
  - `absence`: the payer's `recorded_absence`, with its window.

  A half received only by push has no Evidence Request, so it is not a member. To put a pushed
  half on the receipt, the payer asks for it. The payer keeps each reply's bytes, with their
  digest sealed in its `evidence_response` record, so a member can be rebuilt from its record
  (gap G2); today the reply is dropped.
- **Observers.** An observer's `id` is the node's raw 64-hex Ed25519 key (its `key_id`), its
  `custody_domain` that node's checkpoint key, its role `party`. An absence's observer is the
  payer.
- **Joins:** none are needed. The payer's half is not a member, so there is no pair to join.
- **V4 for Mesh.** The page derives it from the two halves' sealed fields:
  - the provider's root and the payer's cited exchange half carry the same `client_nonce`, and
    each half's `client_nonce_source` is `client_supplied` or `host_forwarded_nonce`. The other
    sources carry a fixed placeholder that names no exchange: `host-served-no-nonce`
    (`host_served_observed`) and `plugin_generated` (`plugin_generated_fallback`,
    `capsule_emit.rs` ~777-781);
  - their `/effect/request_digest` values are equal;
  - the payer's sealed report cites the payer's exchange half in `did[].steps`.

  The request digest alone is not enough: the same prompt sent twice gives the same digest. The
  per-node `exchange_id` is not used.
- **The citing record.** The plugin seals one only for a pushed half (`record_push_bridge.rs`,
  `evidence_key_acceptance.rs`; `ask_book.rs` seals none), so it is present only when the same
  half was also pushed, and leaving it out proves nothing. When the containing bundle carries one whose
  `received_half.cited_capsule_id` is the provider's root: its `sender_key_id` must equal the
  provider's observer, and its `same_controller_as_receiver` must not be `true` (one operator on
  both sides). Otherwise V2 fails.
- **What their side box shows** comes only from the provider's sealed record, so C1 holds. Its
  time is the record's `timestamp`. "Stamped into their book" carries the time of their
  checkpoint. Their words are "not stated": Mesh seals no words. "As agreed" and "paid" lines
  never come from a Mesh record (C4).

### D5. The stamps

Each stamp says only what the page checked, in the five words. Words that differ from the frame
are marked ◇ for Sherlock2.

1. "Your yes is signed with your key" is unchanged from #83, read from your half.
2. "Each side's record is stamped into its own book and can't be changed after": **passed** when
   your half's records all matched (#83) and their artifact verified V1-V2. **missing** when
   they have no artifact member. **failed** when their artifact is present and V1 or V2 failed.
3. ◇ "Their record and yours are about the same deal" (`x-deal-v0`) or "...the same exchange"
   (Mesh): **passed** on V4, and only when V1 and V2 passed. **failed** when V4 failed, or V1 or
   V2 failed. **missing** when they have no artifact member.
   - The frame's "Their record points at yours, and yours at theirs" is not drawn. Neither
     profile's halves cite each other by a checked reference: an `x-deal-v0` copy only names the
     deal id, a Mesh half cannot know the other's id, and a requester's `peer_capsule_id` is the
     peer's own assertion.
   - Its fold-out says: "Both records name the same deal; neither points at the other." For Mesh:
     "Both records name the same request; neither points at the other." (S1)
4. "The payment is confirmed by …": **not checked**, as in #83. The page checks no settlement,
   and "paid" from a Mesh record is Steven's (C4).
5. "An outside witness recorded both books' snapshots" (S3): **not checked** when either book
   carries a witness extension, since the page can't check a witness's signature; otherwise
   **missing**.

### D6. Two rounds (#81)

A two-round bundle is a later bundle under PROFILE "Later bundles":

- `extensions["x-deal-v0"].sealed_report` names the latest report, which is disclosed.
- Each earlier report's Capsule is present with its payload withheld.

deal-view already refuses only *disclosed* other reports (`otherReports`). Withheld ones are not
in `matched`, so the model should already read the latest report. This work adds a test that
shows it:

- A deal closed `open`, then a later outcome, close and report, cut into one bundle.
- The page shows the latest round's state.
- The earlier report is neither shown as missing evidence nor a refusal.

If the test shows a misreading, the fix goes in the model and gets its own test. No new copy is
expected. Any copy that turns out to be needed goes to Sherlock2.

## Producer side

- **capsulectl-deal.**
  - `deal compose --html` builds its page through deal-view's composed manifest. It keeps the
    core's composed section below the page.
  - `--keep` carries the keep bundle in the bootstrap, instead of its lines.
  - `deal compose` takes one `--member` when `--keep` is given: the receipt with their half needs
    only their answer, since your half is the keep copy. Without `--keep` it still needs two.
  - `deal compose` refuses a member recorded as `artifact_failed_verification` (D3).
  - The composed bundle, its digest and every shared file stay byte-for-byte unchanged.
- **Mesh** (new task, Mesh-LLM/capsules-plugin): the composition in D4, sealed under the payer
  node's key, with the node's disclosure policy toward the provider applied before anything goes
  in (C3, Sherlock2's u136 condition). It is not built in this repository. Until it exists, D4 is
  exercised by a synthetic fixture shaped exactly as D4 says. The fixture names its source as
  this note, and it marks itself as synthetic.

## One-way doors

- **The new manifest id `capsulectl.deal-view-composed/v0`, and the module's pins.** These are
  frozen at release. This is engineering's call: the id carries no product meaning, and the
  release is pre-approved for its named consumers.
- **What the composer's own page file carries.** ◇ Sherlock2: today `deal compose --html --keep`
  writes the keep copy's lines into the page. Under D2 it writes the whole keep bundle: every
  payload and opening that copy discloses, including what a shared copy holds back. It is still
  only the composer's own page, and the composed bundle and every shared file are unchanged. A
  composer who passes the page file on passes their keep copy with it. Sherlock2 accepted this
  (round 2, Buzz `51726629`) on condition that the page says on its face that it is your private
  copy and that the composed bundle is the one to share, and that no share path ever emits it:
  the banner says so whenever the bootstrap carries a keep copy, and only `deal compose --html`
  writes this page.
- **What the Mesh composition carries.** ◇ Sherlock2, for the plugin task: the payer's
  composition carries its own exchange half (payload withheld), which `deal-receipt-bundle` does
  not carry today, and, to be interval-complete, the Capsule headers of the other ledger records
  in its interval (payloads withheld). The provider's member carries the same for its ledger. The
  composition is the payer's own file, audience `keep`; the provider's member is what the
  provider chose to answer with.
- **What a counterparty can see:** unchanged. The page reads only what the composition and the
  composer's own keep copy already carry.
- **Copy:**
  - the case banners;
  - the refusal, absence and no-record lines;
  - stamp 3's new words and fold-out, and stamp 5's words;
  - "their key" with its digits;
  - "not stated" for their words.

  These are wording, for Sherlock2 under the delegation.
- **C4 lines ("as agreed", "paid" from a Mesh record):** not drawn. If they are wanted later,
  they go to Steven.

## Found while building

- **A refusal member reads INCOMPLETE.** `capsulectl verify` reads a composition with a refusal
  member as INCOMPLETE ("what was asked for was not served"), so `PageGate` refuses it and
  `deal compose --html` writes no page for it. The refusal rows of D3 are drawn when a viewer
  opens the file itself; the tests emit that page ungated.
- **A viewer-opened page has no openings.** Without the bootstrap, the words the payer asked
  are not checked on the page, so a Mesh payer's half reads "not fully checked" and the banner
  is "Stamped by both sides · not fully checked" at best.
- **The core page builder repeats a shared script.** `capsulectl presentation build` puts one
  module slot per declared presentation, so this plugin's two manifests would inline the one
  script twice. capsule-cli dedups the slots by script digest (coordinator's decision, rc27).
- **`capsulectl verify` reads one response form.** It checks a carried response's signature
  only in the deal wire's form (`ResponseSignatureProfile`, capsule-cli `cli/composed.go`); an
  interop envelope, with `sig` and no `key_id`, reads `invalid`, so a Mesh composition would be
  INVALID until capsule-cli also checks the interop envelope: its signature under `responder`,
  and that `anchor.digest` is the digest of the member bundle's checkpoint.

## Tests

- **Golden pages** from synthetic compositions, each at 390 px and compared with frames 6 and 7:
  - stamped by both, for `x-deal-v0` and for Mesh;
  - their artifact failing V1 (unbound membership), V2 and V4 in turn, each drawn with the
    failed box;
  - a member the core fails (its bundle, its response binding, an `invalid` refusal signature):
    the page is refused, and `deal compose --html` refuses it;
  - a copy whose root key is not the response's signer (a rotated responder): the failed box
    on V2;
  - a failed artifact and an absence, asserting that the two boxes differ (S4);
  - a refusal with its signature verified and unverified, an absence, and no member;
  - several members of theirs: a failing and a passing artifact; a verified artifact beside a
    refusal;
  - two non-composer observers (refused);
  - their observer, custody domain or root key equal to yours (refused);
  - a Mesh citing record whose `sender_key_id` differs, or with `same_controller_as_receiver`;
  - a Mesh half with each placeholder nonce source (`host_served_observed`,
    `plugin_generated_fallback`), and two halves sharing a request digest but not a nonce;
  - a composition with only an absence member of theirs, which must render (case 4);
  - `deal compose` with a member recorded as `artifact_failed_verification` (refused);
  - their member whose checkpoint is not its response's `anchor` (failed box);
  - a Mesh composition whose payer half is not cited by its report (refused);
  - a Mesh member whose response is neither form (failed box); the interop envelope's anchor
    digest is capsule-cli's test;
  - a containing bundle failing each V0 check (refused);
  - a keep copy failing each D2 check in turn: not verifying, audience not `keep`, another
    root key, another deal id (refused).
- **Two rounds:** a test as D6 describes.
- **The single-book page** stays as #83 drew it, and a composition that is not a deal
  composition renders as today.
