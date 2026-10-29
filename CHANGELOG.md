# Changelog

## Unreleased

### Added: `book result build --plan` binds a Result to the dataset plan it ran (#107)

- `capsulectl book result build --plan PLAN.json` runs an agent-action-capsule
  Plan IR v0 dataset plan (§5.1) over the profile's book with aac's
  `go/evidenceplan` gate. It sets the Result's `plan_ref` and `dataset_ref`
  (Result v0 §7.3) to the digests of the plan and of the dataset it selected,
  and refuses stated ones that differ. It refuses a plan over another book, one
  that reads past the book's last record, and one the gate refuses. The plan
  and the dataset are sealed as `evidence_plan` and `evidence_dataset` records,
  which the Result cites, and the same plan reproduces both digests however the
  book grows. A Result that states either member without `--plan` is refused.
  A different plan or dataset is a different Result. `capsulectl` builds
  against agent-action-capsule go `8c673d1` and capsule-cli `<CC>`, whose
  Result gate admits the two members (capsule-cli#490).

### Added: `report.Seal.Revision` numbers the pages sealed over one Result (#107)

- `report.SealPage` returns `Revision` (`revision` in JSON): the seal's place,
  from 1, among the book's page seals with the same Result root, in log order.
  A page that changes only its presentation embeds the same Result, so sealing
  it bumps the revision and the Result stays as it was. A repeat of a page
  already sealed keeps its revision. No record changes: the revision is read
  from the page seals the book already holds. `report` builds against
  capsule-cli `1a6d212b` (capsule-cli#490), whose Result gate admits
  `plan_ref` and `dataset_ref`, so `report.Build` accepts a page over a
  plan-driven Result. Needs a new `report/v*` tag; none is cut here.

### Changed: `report` takes its page runtime and block names from capsule-viewer/go (evidencebook#152)

- `report` imports `pageruntime.IIFE` and the `presentation.CardExtension` and
  `presentation.PresentationExtension` block names from `capsule-viewer/go`
  v0.3.0, not from capsule-cli, which removed them (capsule-cli#486). Report
  pages now embed the viewer's own runtime, so every page changes bytes; the
  build-provenance values are the viewer's: commit `3fe9ad7b0737` and viewer
  version `0.3.0` (verifier `0.8.0` unchanged). `report` and `capsulectl` build
  against capsule-cli `93dff5d`. Needs a new `report/v*` tag; none is cut here.

### Added: `report build --wording` (evidencebook#155)

- `capsulectl book report build --wording FILE --wording-digest HEX` lays an
  `aac.wording-pack/v0` wording pack over the viewer's own words in the written
  page, as the SDK's `report.View.Wording` does. The digest is the lowercase
  hex SHA-256 of the file's bytes and is required with the pack. The build is
  refused, and no page is written, when the digest does not match, the pack is
  not a valid `aac.wording-pack/v0`, or either flag comes without the other.
  A worded page gets no permalink, so `--permalink` with `--wording` is
  refused. `capsulectl/go.mod` now requires `report` v0.2.0.

### Changed: `capsulectl book result open` validates against the Result v0 schema (#106)

- `result open` checked only that `aggregate.coverage` existed and called Result v0
  a draft. The Result v0 schema is no longer DRAFT (agent-action-capsule
  `spec/evidence-result-v0.md`), so `result open` now validates
  with the same schema gate as `result build` and `report build`. It is stricter: a
  document it accepted before may now be refused, with every schema violation
  reported. Its note says the schema is no longer DRAFT and names what the spec still
  marks PROPOSED: the §4.1 `type: close` claim, §7.1 `coverage_report` and §7.2
  `coverage_declaration`.

### Changed: builds against the capsule-cli whose core names no company plugin (capsule-cli#428)

- The `report` and `capsulectl` modules build against capsule-cli `8a856b75` and capsule-viewer `389c1bac`
  (pseudo-versions, unreleased). A book profile now names its owner: a jsonl
  profile with a `log_id` needs `log: book` (`capsulectl profile create ... --log
  book`, or `capsulectl profile update --profile NAME --log book` for an older
  profile). The core refuses a profile that names none and prints the fix
  (`capsulectl profile update --profile NAME --log <cll or the plugin that keeps its
  log>`; for a book the value is `book`), and `capsulectl doctor` lists every
  profile with no declared log owner. The core's transitional aliases are gone and
  the old commands no longer exist: `capsulectl close`, `reconcile`, `request` and
  `respond` are `capsulectl book close|reconcile|request|respond`, and `capsulectl
  store migrate` is `capsulectl book migrate`. The scripted demo creates
  its profiles with `--log book`. The two `report` page goldens are re-emitted with
  the newer page runtime (its banner check no longer discards a render when the page
  grows a scrollbar, capsule-viewer#113).

### Changed: capsulectl-book names no host-package book export (capsule-cli#428)

- `capsulectl-book` registers its log backend by its own name
  (`cli.RegisterLogBackend`), checks for its own profiles with the log owner
  (a jsonl profile whose `log:` is `book`) and keeps its own `book` name and
  alias-removal constants, so the neutral capsule-cli core can drop every
  export named for the book. It builds against capsule-cli `0e9eef2b` and
  agent-action-capsule `1d1752bf` (both unreleased); a book profile's behavior is unchanged. The two `report` page
  goldens are rewritten because that capsule-cli vendors a newer viewer
  runtime, and the `report` module's own pins (capsule-cli, capsule-viewer
  v0.2.0, agent-action-capsule) move with them so both modules build the same
  pages.

## v0.8.1 (2026-10-27)

### Added: the SDK guide (evidencebook#129)

- `docs/GUIDE.md` walks the SDK as four paths (join the loop, seal a Result,
  build a Report, serve a History), each with a runnable Go `Example` that
  reads a corpus vector from `testdata/`: `Example_joinTheLoop`,
  `ExampleBook_SealResult`, `ExampleBook_Respond`, and the `report` module's
  examples. Terms Packs and evaluation are not in this module; the guide
  links capsule-engine and evidencebook-skills for them.
- `VECTORS.md` lists this repository's vector sets with their owning spec
  and pinned commit.
- The install lines in the README and the guide name v0.8.1.

### Added: a wording pack and the contract report in `report.View` (evidencebook#147)

- `report.View` takes `Wording`, an `aac.wording-pack/v0` pack with its
  SHA-256 (`report.Wording`), the wording layer over the viewer's own base, and
  `ContractReport`, which records the empty `contract-report/v1` block so the
  viewer draws the contract report. `Build` refuses a pack that does not
  check, or a different `contract-report/v1` block already in the bundle, as
  `ErrRefused`. A page with a wording layer gets no permalink, which would
  carry the bundle without it. With neither, pages are byte-identical to before; the page
  now goes through capsule-viewer's `BuildOfflineHTML`, whose default
  settings write the same page.

### Added: package `report`, the report builder in the SDK (evidencebook#132)

- Released separately: the module as `report/v0.1.0`, and the
  capsulectl-book change in the last item with the next `capsulectl/v*`.
  Neither is part of this module's v0.8.1.
- `github.com/action-state-group/evidencebook/report`, a new nested module, renders a verified
  Evidence Bundle rooted at a sealed Evidence Result v0 into one offline HTML
  page: `Build(bundle, View{Card, Presentation, Draft})` verifies the bundle,
  gates the root (exactly one Result), checks the Result, and renders only
  when `verify --bundle` calls the bundle VALID; otherwise it returns what it
  found with the page gate's refusal. `FormPayload`/`FormBook` and
  `VerificationPass`/`VerificationClosureWithheld` name `Page.Form` and
  `Page.Verification`. `Page.Permalink` encodes the same
  embedded bundle. Refusals wrap `report.ErrRefused`.
- `SealPage` seals a page's bytes in a book as one `report_page_seal` record,
  stating only what the page itself carries, and makes sure a checkpoint
  covers it; `CheckPage` compares a page file with those seals (`match`,
  `mismatch`, `no sealed record`, `not covered by a checkpoint`) and judges
  the embedded evidence apart, which `InspectPage` does with no book.
- This is the code behind `capsulectl book report build` and `report check`
  (#108), moved from `capsulectl/internal/book`; runnable examples render the
  presentation fixtures, now in `report/testdata/presentation`.
- `report` is its own nested module (released as `report/v<version>`), so
  the root module's requirements and module graph are unchanged. The report
  module requires `evidencebook` v0.8.0, `agent-action-capsule/go` v0.13.1,
  `capsule-cli` v0.1.0-rc23 (a prerelease) and a `capsule-viewer/go`
  pseudo-version, for the Result and page gates and the pinned viewer
  runtime; an importer of `report` gets these in its module graph, with
  capsule-cli's own requirements (cobra, viper, the MySQL driver) as indirect
  ones.
- `capsulectl book report build` and `report check` now run on the `report`
  module (`capsulectl/go.mod` requires `report` v0.1.0), and the plugin's own
  copy is gone. Pages, output and exit codes are unchanged.

## v0.8.0 (2026-10-27)

### Added: `report build --seal` and `report check` (evidencebook#108)

- `capsulectl book report build --seal --profile NAME` seals the written
  page's SHA-256 in the book as one `report_page_seal` record and cuts a
  checkpoint over it. The record's statement holds the page digest, the
  digest of the Evidence Bundle the page embeds and the Result root, and
  nothing else; its `subject_ref` is the bundle digest. A repeat of the same
  page finds its record; `--dry-run` seals nothing.
- `capsulectl book report check --profile NAME --page FILE` compares a page
  file with the book's seals for the bundle it embeds and reports `match`,
  `mismatch`, `no sealed record`, or `not covered by a checkpoint`, with the
  embedded evidence's `verify --bundle` verdict in `verification`, apart from
  the page answer. Exit codes: the evidence's, when it is not VALID (1 INVALID,
  3 INCOMPLETE); else 0 match, 5 mismatch, 3 no seal or none covered yet; 2 for
  a file that embeds no Evidence Bundle (not a report page).

### Added: `capsulectl book doctor [--probe]` (evidencebook#130)

- `capsulectl book doctor` prints the book's `Book.Doctor` findings as
  `{"findings": [...]}`, a list and never a score, and seals nothing. It reads
  the share policy from `--policy` (as `book respond --policy` does; default
  the documented defaults), controller binding records from
  `--controller-binding` (repeatable), and the witness deliveries in
  `book/witness.json`.
- `--probe` asks the book's own responder, the path `book respond` answers
  with no `--requester`, for `checkpoints` and then `history_card/1` as a
  stranger, and verifies both under the profile's own signing and checkpoint
  keys. A profile records no responder address, so the probe goes through
  that path in-process. The book grows by no record and no checkpoint.
- The plugin's metadata declares `doctor`, which capsulectl's own `doctor`
  lists (capsule-cli#459).
- `capsulectl/go.mod` requires the library at `f8de416` (#133). With it,
  `book respond` answers under #120's subject-scoped authorization: the
  plugin's tests now ask for `checkpoints` where they asked for
  `full_history`, which is refused to everyone, and expect `not_authorized`
  for a record subject asked by a requester no record names.

### Added: `Book.Doctor`, a node's check of itself (evidencebook#130)

- `Book.Doctor` returns a list of findings, never a score: what is
  self-attested, whether the latest checkpoint is witnessed, the
  `NOT_CHECKED` properties of received answers, what this book never
  answers, whether a controller binding names the signing key (which binding,
  by digest; its signatures are `NOT_CHECKED`, so the key is never shown
  bound), whether sharing is on, and what a stranger and an identified
  requester would receive under the policy. Whether the policy digest is in a
  commitment is `NOT_CHECKED`: the book publishes no commitment document.
- With `DoctorOptions.Probe`, it asks the node's own responder for
  `checkpoints` and then `history_card/1` as a stranger, verifies both under
  the advertised keys, and reports a mismatch when an answer is refused, does
  not verify, or is not anchored at the latest checkpoint; with no checkpoint
  to verify against, a refusal is `NOT_CHECKED`. Both subjects are public
  under every policy, so a match shows what a stranger is served, not what an
  identified requester is. The identified view is computed from the policy,
  not asked: `Respond` seals a record for every authenticated requester.
  Doctor seals nothing and issues no checkpoint.

### Fixed

- `bookdemo aac-verify` decodes the bundle with `json.Number` as `verify
  --bundle` does, so a bundle holding an `own_capsule` record no longer fails
  graph closure on integer `seq` and `v`; `report build` omits `unmet_claims`
  when the verdict is `VALID` instead of printing the fallback failure
  message (evidencebook#113).

### Changed (breaking): responding is subject-scoped, counterparties by default (evidencebook#114, #120)

- `DefaultSharePolicy` defaults to `history_segments` for counterparties, and
  `prospective` is read as counterparties. A record or exchange subject is
  answered under counterparties only to a counterparty that every record it
  names names, decided before resolution. `range`, `correlation` and
  `full_history` are refused under every tier unless a recipient-specific
  derivation grants them to its recipient (Steven's ruling on #114).

### Breaking: the cadence verifies witness receipts itself (evidencebook#115)

- `Cadence.Witnessed` verifies the receipt against the tick's signed cadence
  checkpoint with AAC's witness verifier (`go/witness.VerifyReceipts`), under
  the new `CadenceConfig.Witnesses` directory, instead of trusting the host's
  check. It now returns a `WitnessCheck`: `VERIFIED` witnesses the tick as
  before; `NOT_CHECKED` (the witness has no directory row or usable key, or
  no directory is configured) changes nothing and is not an error; `INVALID`
  changes nothing and is an `ErrInvalid` naming the reason. Each state
  carries AAC's finding and reason, except a receipt missing a member, which
  is `INVALID` before any check.
- `ImportLegacy` verifies the receipt of each witnessed pre-shelf tick it
  stores or promotes the same way and refuses an import whose receipt is not
  `VERIFIED`, naming the tick. A tick already held witnessed is not checked
  again, so a re-run after a key rotation still imports new ticks.
- Hosts migrate by passing their witness directory
  (`witness.ParseDirectory`) in `CadenceConfig.Witnesses` and reading the
  returned state. Ticks witnessed before this release stay witnessed: they
  were recorded on the host's check.
- The SCITT receipt substrate stub's receipts read `NOT_CHECKED`: they
  register a record identity, which AAC's verifier has no binding for. They
  are still labelled `scitt-receipt-unverified` and never count as witnessed.

### Added: the capsulectl-book agent skill

- `capsulectl/skills/capsulectl-book` is the agent skill for the book verbs
  `request`, `respond`, `close`, `reconcile`, `result build` and `report build`
  (`capsulectl book VERB`), moved out of capsule-cli's `capsulectl` skill
  (capsule-cli#326, capsule-cli#447). It has a `SKILL.md`, an `AGENTS.md`, a
  reference and a scripted demo; CI checks that its verbs are the plugin's and
  runs the demo against `capsulectl` plus a `capsulectl-book` built here.

### Changed: capsulectl-book builds against capsulectl v0.1.0-rc23

- `capsulectl/go.mod` requires capsule-cli at the published release
  `v0.1.0-rc23` instead of the commit it was imported from (e0511c4), so the
  first capsulectl-book release is `capsulectl/v0.1.0-rc23`
  (`capsulectl/RELEASING.md`).

### Added: capsulectl-book, as the nested module capsulectl/

- Released separately, as `capsulectl/v*`: not part of this module's v0.8.0.
  The `capsulectl` plugin over evidence books moved here from capsule-cli
  (capsule-cli#326), imported at capsule-cli commit e0511c4 (capsule-cli#446), as the
  nested Go module `github.com/action-state-group/evidencebook/capsulectl`
  with its own `capsulectl/v*` releases (`capsulectl/RELEASING.md`), each
  taking the version of the capsulectl release it pairs with. It
  requires this library at a version, so it adds nothing to the library's
  own module graph.

### Added: sealing an Evidence Result into a book

- `Book.SealResult` seals a checked Evidence Result v0 as one
  `RecordTypeEvidenceResult` (`evidence_result`) record about its contract,
  reusing a record an earlier run sealed, and cuts a checkpoint when none
  covers it yet. `Book.CheckpointCovers` reports whether the latest
  checkpoint covers a sequence number. Both were capsule-cli's, shared by
  `capsulectl book result build` and the deal plugin's sealed terms Result;
  they move here so the two plugins, which leave capsule-cli for separate
  repositories, keep one sealing path (capsule-cli#326).
  `RecordTypeEvidenceResult` names the record type they seal, which the
  caller's `add` writes (`Book.Append` or `AppendCapsule`). Go only: they compose existing book operations
  and add no wire format.
- `SealResult` is not atomic: reuse, add and the checkpoint run one after
  another, so callers that may seal the same Result concurrently must
  serialize. A failure after the record is committed (an index or automatic
  checkpoint failure) returns the record with the error, so its id is never
  lost.

### Changed (breaking): a deal reconcile over a period with no half is refused

- `RecomputeDealReconcile` returns `ErrInvalid`, and no Result, when the
  period holds no half of either book. Before, it sealed both reconcile
  claims with an empty `evidence`, tallies of zero and verdict `not_met`.
  Evidence Result v0 requires every claim to cite at least one record
  (agent-action-capsule#353).

### Changed (breaking): a derivation cannot be registered again with other declarations

- `Book.RegisterDerivation` no longer replaces a registration whose
  definition document has the same identifier (#86). Registering it again
  with the same recipient and the same sets of forms and fields returns the
  same identifier and changes nothing; with a different recipient, forms or
  fields it is `ErrInvalid`, and the first registration stands. Before, `{}`
  registered for alice and then for bob answered bob, not alice.
- `testdata/derivation/vectors.json` gains `reregistered`, which both
  implementations check.

## v0.7.0 (2026-10-20)

Everything since v0.6.0, with two breaking changes. A record that carries a
correlation identifier is answered under it (#81). A CLL book's `Book.Bundle`
builds through agent-action-capsule's producer, so its bundles carry each
record's Producer Envelope and read VALID under strict verification; a SCITT
receipt book keeps the book's own assembly, so its records carry no Producer
Envelope (#89). JSON with trailing data is now refused: `ErrCorrupt` for
stored capsules and headers, `ErrInvalid` for bundles, bundle extensions and
reconcile halves (#89). The module now requires agent-action-capsule Go
v0.11.0.

### Changed (breaking): a correlation subject reads the header's correlation identifier

For capsule-cli#264, following Steven's direction of 2026-10-19 (PT); the
spec reading is agent-action-capsule#328 O2, still open (#81).

- `Book.Respond` answers a `correlation` subject with the records whose
  header carries that identifier as `correlation.exchange_id`. A record with
  no correlation identifier is still held under its `subject_ref`, so a book
  whose records carry none answers exactly as before.
- **Behaviour change:** a record that carries `correlation.exchange_id` is
  no longer answered under its `subject_ref`. For example, a reconcile half
  filed under `subject_ref` `order-17` with `exchange_id` `ex-9` is now
  answered for `ex-9`, not for `order-17`.
- A record whose correlation carries only a request digest or a twin bracket
  is still held under its `subject_ref`. The two key spaces answer the same
  requested identifier.
- The `subject_ref` authenticated index (`NewSubjectIndex`) still proves the
  records under one `subject_ref`. Its documentation now says that does not
  establish a correlation answer's completeness, and a correlation answer
  carries no verification material, underived or derived.

### Changed (breaking): `Book.Bundle` builds through agent-action-capsule's producer

capsule-emit#338's step for this repository; design note on #84. A CLL
book's bundle is now built by agent-action-capsule's
`bundle.BuildEvidenceBundle` (Go v0.11.0, the new minimum), over an
unexported proof source on the book's own log. The book keeps the disclosure
act: its closure picks the range start and the disclosed records, and it
still builds the overlay and `evidencebook/payloads` and commits the
disclosure record. A SCITT receipt book keeps the book's own assembly.

- **Producer signatures.** Every carried record, ride-alongs included, carries
  its Producer Envelope as `signature` and `key_id`, so a book bundle reads
  VALID under strict verification instead of INCOMPLETE. The recipient sees
  the key of each record's signer, every signer key the book holds included
  (ruled on #84 §4).
- **Closure.** `records_mode` and `missing` are the verifier's: the capsule's
  chain parent and `references[]`. `missing` is in first-seen order. A
  citation below the range start is `ErrCorrupt`.
- **`BundleRequest.ProducerKey`** declares the producer key
  (`producer-key/v1`). The extension itself is now refused in `Extensions`, as
  is `composed/v1`.
- **Refused as `ErrInvalid` before the build:** a `Suppress` name other than
  `agent_input` or `agent_output` (the list is now stated sorted and without
  repeats, in the bundle and the disclosure record alike), a negative
  `ClosureDepth`, a `ProducerKey` that is not 32 bytes, and an extension that
  is not JSON, has no canonical form or nests too deeply for the bundle.
- **`payloads=all`** is refused when a disclosed capsule commits an eligible,
  unsuppressed member the bundle does not reveal (an output with no original,
  or one declared never retained); the error says to use `selected` or to
  suppress that member.
- The `checkpoint` member also states `key_id`, `timestamp`, `prev_size` and
  `prev_root` from the signed statement.

### Fixed: JSON with trailing data is refused

JSON the book reads with trailing data after the value, such as a stray `]`
or `}`, is now refused: as `ErrInvalid` for a bundle in `VerifyBundle`,
reconcile halves and bundle extensions, and as `ErrCorrupt` for stored
capsules and headers. Before, it was read as the leading value.

## v0.6.0 (2026-10-19)

Everything since v0.5.0: the APIs capsule-cli#242 needs to move deals onto
shelf books (#73, #74) and the Deal Reconcile Contract's recompute (#79),
which completes evidencebook#36. No exported identifier is removed or
changed; this release only adds API and fixes the witnessed index.

### Added: the Deal Reconcile Contract's recompute (#79)

evidencebook#36's last acceptance item, for capsule-cli#242 (note §2.10).

- `RecomputeDealReconcile(DealReconcileInput)` recomputes a reconcile
  under agent-action-capsule's Deal Reconcile Contract, `ec:deal-reconcile@1`
  (`DealReconcileContractRef`). It reads each half through its book's
  `HalfMapping` (JSON Pointers onto the six abstract members) and opens
  `field_openings` by half digest. It returns the opening statuses, each
  side's set status, the per-deal states, the tallies, sufficiency and
  verdict per requirement, the optional deal state, and the Evidence Result
  with one `type: reconcile` claim per requirement, which cites no Close.
- Each pairing is decided by `ReconcileHalvesWith` on the two halves
  projected onto the requirement's one field, so a failed opening fails only
  that requirement. The contract's rule 1 (more than one half on a side),
  rule 3's precedence (a missing algorithm, commitment or opening before a
  failed opening; an unsupported algorithm or malformed commitment counts as
  missing) and the set status of a coverage declaration are applied around
  that call.
- A half that is malformed by the contract (an absent or ill-formed
  `deal_id`, `book_id` or `recorded_at`, or a `book_id` that is not its book)
  is an `ErrInvalid` error, never a claim.
- The contract's 14 conformance vectors are vendored from
  agent-action-capsule `2cf9037` with their upstream SHA256SUMS, and every
  expected member is checked.

### Added: import a pre-shelf cadence; list a shelf's records (#73)

evidencebook#36 §2 and §4, for capsule-cli#242's migration.

- `Cadence.ImportLegacy(ctx, LegacyCadence{Logs, Ticks})` carries a cadence a
  pre-shelf writer kept (capsule-cli's `deal_cadence` rows, on the same
  cadence log) into the shelf's, in one transaction: every legacy log counts
  into H, each tick not already held is stored and numbered after the
  cadence's last, witnessed ticks are indexed, and the live rule is applied
  to every registered log. It is re-runnable: a held tick keeps its number
  and is promoted to witnessed when it now carries a receipt. A tick that
  would rewrite a held one, or is older than the newest held, is refused.
- `Shelf.Records(ctx, bookIDs...)` lists the committed records of a shelf's
  books, with each record's retention state and a count of pending tails,
  without opening a book.
- `CadenceConfig.WitnessIDs` queues each cadence checkpoint for those
  witnesses in the checkpoint's own commit; `CadenceConfig.NoJitter` makes
  every tick due exactly `Interval` after the last; `Cadence.Last` returns
  the newest tick.

### Fixed: an unchanged checkpoint is indexed to its newest witnessed tick (#74)

- The witnessed index now keeps the newest witnessed tick that carried a
  log checkpoint, whatever order receipts arrive in, for `Witnessed` and
  `ImportLegacy` alike. It kept the first, so after a host changed its
  witness, `Chain` kept returning the old witness's tick.

## v0.5.0 (2026-10-19)

Everything since v0.4.0: the originals store and per-record retention (#58),
and the deal features of evidencebook#36 (#61, #63, #65, #67, #68). #36's
end-to-end acceptance (a deal on the new book passing
`capsulectl verify --bundle`, an older deal store still verifying, a two-book
reconcile reaching `reconciled`) lands with capsule-cli#242.

- **BREAKING (#58)** for Go callers:
  - `Config.Payloads`, `PayloadResolver`, `PayloadDir` and `OpenPayloadDir`
    are removed. Set `Config.Originals` to an `ArtifactStore` instead
    (`OpenOriginalsFile` opens the JSONL backend), and move an existing
    payload directory's bytes into it once with `MigratePayloadDir`.
  - `Book.Retention(ctx, recordID)` and
    `Book.SetRetention(ctx, recordID, state, reason)` drop their payload
    digest argument: retention applies to the whole record.
  - `LifecycleStatement` drops `PayloadCommitment` (`payload_commitment`);
    a lifecycle record written before that names one is read as applying to
    the whole record.

### Added: a log's witnessed cadence chain for its bundle (#68)

evidencebook#36 §2.

- `Cadence.Chain(ctx, statement)` is capsule-cli's `cadenceChain` for a
  log's checkpoint, member for member, for the `x-cadence-witness/v0`
  bundle extension (`CadenceExtension`): `all` from a witnessed tick that
  carries the checkpoint, else `part` with the newest earlier witnessed
  checkpoint and a consistency proof to it. A profile adds its own members
  (a deal's step counts) and its pending states.
- `Cadence.Witnessed` now takes the receipt the host verified, stored with
  the tick for the chain.
- `testdata/cadence/chain-verified-by-capsule-cli-7a6b101.json` holds an
  `all` and a `part` chain this code built; capsule-cli 7a6b101's own
  verifier (`cadenceClaim`) passes both up to the witness receipt check,
  and fails each when its salt or consistency proof is changed.

### Added: the heartbeat cadence over a shelf, ended logs leaving the tick (#67)

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

### Added: salted per-field commitments and a three-way reconcile check (#61)

evidencebook#36 §3 (the deal profile on the book); the construction is the
one evidencebook#44 rules a History's deal-level commitments use.

- `Entry.Fields` commits texts by field name, each with a fresh 32-byte
  nonce, as `sha256-jcs-nonce256` (`CommitAlgNonce256`): SHA-256 over
  RFC 8785 of `{"nonce", "text"}`, agent-action-capsule
  presentation-contract §7.2 and capsule-cli's deal records. The header
  carries `field_commitments` (by field name; §7.2's records carry the same
  values as `<name>_commitment` members, so the placement differs and the
  values do not) and `commit_alg`; the openings
  (`{nonce, text}`) are originals in the artifact store, under the
  record's retention, never in the record. `Book.Opening` reads one;
  `VerifyOpening` checks one. Openings are not payloads:
  `payload_commitments` and `ResolvePayload` never include them.
  `schemas/record-header.schema.json` declares `field_commitments` and
  `commit_alg` (and requires them together, with `retention_state`).
  `Get`, `Query` and `Append` return records with their own copies of the
  header's maps and slices, so a caller cannot change what the book trusts.
- Reconcile: `Half` gains `FieldCommitments` and `Opened`. `OpenHalf`
  checks openings against a half's own commitments, and an opening that
  does not open makes the half untrusted (`UNRESOLVED`).
  `ReconcileHalvesWith` takes a `ContentCheck` returning `Agree`,
  `Disagree` or `Undecided`; `Undecided` is `INSUFFICIENT`, so a missing
  opening is never disagreement. `OpenedFieldsAgree(fields...)` needs both
  halves' openings, since each record's nonce is its own.
  `ReconcileHalves` and `Comparator` keep their meaning
  (`Comparator.Check`). `ReconcileInput` gains `Check` beside `Compare`
  (exactly one is set) and `OwnOpenings`/`PeerOpenings`.

### Added: a shelf of books, one log per deal (#63)

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
  returns one `*Book` per log id for its lifetime.
  `AuthenticatedIndexes` chooses each book's authenticated indexes (an
  empty list gives none, so a deal's log holds no `index_root` records).
  After `Release`, the shelf and its books refuse writes (`ErrReleased`; `Book.Release` now does
  the same for any book, `SetRetention`'s purge retry included).
  `OpenCLLSQLite` provisions only a log the file holds nothing of: a log
  with state that fails to open is reported, never initialized over.

### Added: the cadence tree, byte for byte capsule-cli's (#65)

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

### Changed (breaking): payload bytes live in an artifact store (#58)

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

### Added: caller-sealed records and kept replies (#58)

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
