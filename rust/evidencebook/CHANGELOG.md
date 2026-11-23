# Changelog: evidencebook (Rust)

## Unreleased

## 0.0.26 (2026-11-22)

Every pull request merged since 0.0.25 that changed the crate:

- [#614](https://github.com/action-state-group/evidencebook/pull/614): feat(dealprofile): rule 12 counterparty copy withholds answers, tellings, share checks and the close (#613)
- [#756](https://github.com/action-state-group/evidencebook/pull/756): feat(rust): serve a Terms Pack by the digest of its definition, with grants, nested fields and set proofs (#746)
- [#757](https://github.com/action-state-group/evidencebook/pull/757): fix(dealprofile): the x-deal-v1 payment check runs on the payment's own legs beside a refund (#722)

Breaking changes in this release: `Registered::recipient` is replaced by `Registered::grants` (the grant is now a set: `recipient` and `recipients` together), `DerivationDefinition` gains the fields `recipients` and `proofs` and derives `Default` (a struct literal that does not name them writes `..Default::default()`), and `deal::v1`'s `REASON_REFUND_CITES_OFFER` is removed.

- Serve a Terms Pack by the digest of its definition, as the Go book does (#746). `DerivationDefinition` gains `recipients` (one definition granted to several requesters; `recipient` and `recipients` are one grant set), dotted field paths (`statement.generated_at` reveals that member only, nested as the record holds it) and `proofs` (the answer carries the anchor's `cll-checkpoint`, one `cll-inclusion-proof` per record in record order and, when the checkpoint extends an earlier one, the `cll-consistency-proof`). `DerivationDefinition` now derives `Default`, so a literal that names no new field writes `..Default::default()`; `Registered::recipient` is replaced by `Registered::grants`, with `proofs` and `paths` beside it. New `evidencebook::derived`: `verify_set_inclusion` (the requester's check of those proofs, the envelope first, under the pinned checkpoint key) and `derived_inputs` (the rows of a derivation answer, `proven` only from a successful check). The Go reply bytes are the vectors (`testdata/derivation/proofs-vectors.json`) and the Go reply in `report/testdata/derived-report/bundle.json` is proven by the Rust check. The report over a derived answer is not ported (#755).

### Fixed (breaking): `deal::v1::payment_check` runs on the payment's own legs beside a refund (evidencebook#722)

- A refund or remedy leg citing the offer's terms leg no longer stops the check with `refund_cites_offer`. That let a 1-unit refund turn a failing payment into "not checked".
- The check now runs on the payment's own legs. Those refund legs, when not superseded, are listed in the new `PaymentResult::not_checked`, and a status row counts them in `check_not_checked`. This matches Go byte for byte, on the shared vectors.
- A payment leg is now superseded only by a later payment leg answering the same terms leg that AAC's verifier reads beside the offer. Before, a refund, a leg of another terms leg, or a malformed leg carrying `supersedes` could take the payment leg out of the check, which read "not agreed" instead of failed. A refund is still superseded by any later leg of its side.
- `REASON_REFUND_CITES_OFFER` is removed.
- This is a correctness fix to a function the crate already has, not a new port.

## 0.0.25 (2026-11-21)

Every pull request merged since 0.0.24:

- [#600](https://github.com/action-state-group/evidencebook/pull/600): feat(rust): x-deal-v1 verify_report_bundle and DealStatus::report_with_book (#598)
- [#608](https://github.com/action-state-group/evidencebook/pull/608): feat(dealprofile): seal the seller's verdict on an agent claim apart from the claim (#607)

- `deal`: the x-deal-v0 `claim_authority` record (`TYPE_CLAIM_AUTHORITY`, `ClaimAuthorityBody`,
  `Body::ClaimAuthority`), the seller's verdict on its agent's claim sealed apart from the claim,
  and the chain check's section 6 rule 12 on it, as Go's `CheckChain` does (evidencebook#607).
- `deal::v1`: `verify_report_bundle` checks a sealed v1 report with the x-deal-v1 bundle extension
  that carries it (`ReportBundle`, `ReportBook`), as Go's `VerifyReportBundle` does, replaying
  `testdata/deal-v1/report-bundle/vectors.json`; `DealStatus::report_with_book` gives a report and
  its book pairs from one snapshot (evidencebook#598). The x-deal-v1 differential check compares
  the verifier with Go's.
- `deal::v1`: the text check refuses a number that overflows a float64, as Go's `checkText` does
  (`check`, `verify_sent`, `verify_book`, `verify_report` and the extension), at the
  canonicalization stage; it read such a number before and failed later or not at all.

## 0.0.24 (2026-11-21)

Every pull request merged since 0.0.23:

- [#590](https://github.com/action-state-group/evidencebook/pull/590): feat(dealprofile): x-deal-v1 admit rows take admission pack row ids
- [#597](https://github.com/action-state-group/evidencebook/pull/597): feat(dealprofile): x-deal-v1 report relies on the payment legs, and the bundle extension carries them
- [#601](https://github.com/action-state-group/evidencebook/pull/601): feat: follow Evidence Request -03 checkpoint_range (AAC go v0.24.0, evidence-request 0.8.0)

The crate requires `evidence-request` 0.8.0, so a consumer that also depends on it moves to 0.8.0 with this release; a ranged checkpoints request is `{"checkpoint_range": [a, b]}` and `{"checkpoints": [a, b]}` is `request_malformed`.

- `deal::v1`: an `admit`'s `rows` take admission pack row ids, hyphens included (`^[a-z][a-z0-9_-]*$`; capsule-engine#303), as Go; the vendored x-deal-v1 schema moves with it, and `admit-rows.json` is decided the same.

- `deal::v1`: a payment row's `relied_on` in the v1 report lists each settle leg the payment
  check read, as Go does (evidencebook#588); `build_report` refuses a payment result that does
  not begin with the offer's digest or names a record the books do not hold.

- Requires `evidence-request` 0.8.0: a ranged checkpoints request is `{"checkpoint_range": [a, b]}` (`Subject::CheckpointRange`), as Evidence Request -03 names it, and `{"checkpoints": [a, b]}` is `request_malformed` (evidencebook#595).

## 0.0.23 (2026-11-20)

Every pull request merged since 0.0.22 (the Rust changes are described below the list):

- [#569](https://github.com/action-state-group/evidencebook/pull/569): feat(capsulectl): answer refusals uniformly, the library default (#473)
- [#578](https://github.com/action-state-group/evidencebook/pull/578): docs(design): an untimed deal is counted, local, unless its agreement names a witness (u608, #570, SH-12)
- [#582](https://github.com/action-state-group/evidencebook/pull/582): feat: RespondOptions.OnRefusal reports the decided reason locally; README names policy_declined (#580, #581)
- [#583](https://github.com/action-state-group/evidencebook/pull/583): feat(dealprofile): x-deal-v1 status value carries the first act (#575)
- [#584](https://github.com/action-state-group/evidencebook/pull/584): feat: CheckStaple reads a standard_history/3 staple, in Go, Rust and Python (#579)
- [#586](https://github.com/action-state-group/evidencebook/pull/586): feat(shelf): MCP and A2A entries for Shelf.Receive (#577)

The x-deal-v1 status value now carries `start.first_act` (#583), so a status value's commitment changes: a consumer that pins or seals status values regenerates them before bumping its pin.

### Added: the x-deal-v1 status value carries the first act (#575)

- `StatusStart::first_act` (`{"time": <status time>}` or `null`) is the holder's first `perform` or `settle` record for a deal whose counterparty runs no layer, byte for byte with Go's `xdealv1.StatusStart.FirstAct` on the shared status vectors. Every status value now carries `"first_act": null` or the act, so a status value's commitment changes.

### Added: the MCP and A2A entries of `Shelf::receive` (evidencebook#577)

- `Shelf::push_mcp` and `Shelf::push_a2a` (and `push_a2a_extension`), behind the `shelf` feature, carry a record push and a stamp proof over MCP (the tool `record_push`) and A2A (one Agent Card extension, one `data` part) to `Shelf::receive`, as `spec/bindings/record-push-v0.md` of agent-action-capsule specifies and Go's `Shelf.PushMCP` and `PushA2A` do, byte for byte on the shared carriage vectors (`testdata/shelf/receive-carriage`). A part's refusal is an answer; a malformed push is an invalid-params error and a receiver fault an internal error, never an answer.

### Added: `check_staple` reads a `standard_history/3` staple (#579)

- `DERIVATION_STANDARD_HISTORY_3` names `standard_history/3`, and `check_staple` accepts a staple of it over `full_history` whose card has exactly the /3 card's members and names its derivation. As for `standard_history/1`, the card's remaining form (SH §6 `card_form`) has no Rust checker yet. No book builds a /3 staple. `standard_history/1` is unchanged. `StapleCheck` gains `card_form_checked`: false for a `standard_history/1` or `/3` card (members and derivation only), so the departure is in the result, true for a history card.

## 0.0.22 (2026-11-19)

Every pull request merged since 0.0.21 (the Rust changes are described below the list):

- [#574](https://github.com/action-state-group/evidencebook/pull/574): fix(rust): verify_book refuses a header correlation name the schema does not list (#563)
- [#570](https://github.com/action-state-group/evidencebook/pull/570): feat(dealprofile): x-deal-v1 counts a deal whose counterparty agreed and then went silent (#511)
- [#573](https://github.com/action-state-group/evidencebook/pull/573): feat(shelf): Shelf.StampTime, the witnessed time of an x-deal-v1 stamp (#572)
- [#566](https://github.com/action-state-group/evidencebook/pull/566): fix(rust): deal::v1 check_authorizations reads at as Go's time.Parse does (#551)
- [#567](https://github.com/action-state-group/evidencebook/pull/567): docs(dealprofile): a finding's limit and value are a money cap's amounts in minor units (#456)
- [#571](https://github.com/action-state-group/evidencebook/pull/571): docs(deal): list not_counted in the §9 report row remedy
- [#568](https://github.com/action-state-group/evidencebook/pull/568): fix: bind stated freshness to the signed checkpoint for history_card/1 and checkpoints answers
- [#565](https://github.com/action-state-group/evidencebook/pull/565): fix(dealprofile): the x-deal-v0 reference checker reads schema patterns as RE2 (#548)
- [#564](https://github.com/action-state-group/evidencebook/pull/564): feat(python): x-deal-v1 StatusProvider and the report from a shelf, as Go's (#461 PR 6d-3c)
- [#562](https://github.com/action-state-group/evidencebook/pull/562): fix(dealprofile): a sent or book pair's members are its own exactly (#559)
- [#561](https://github.com/action-state-group/evidencebook/pull/561): feat(python): x-deal-v1 naming of rulings, as Go's NameRulings (#461 PR 6d-3b)
- [#560](https://github.com/action-state-group/evidencebook/pull/560): feat(python): evidencebook.deal.v1 verify_report, Go's VerifyReport (#554)
- [#558](https://github.com/action-state-group/evidencebook/pull/558): feat(rust): deal::v1 verify_report, Go's VerifyReport (#554)
- [#556](https://github.com/action-state-group/evidencebook/pull/556): fix(dealprofile): VerifyReport refuses null lists and a negative count (#554)
- [#555](https://github.com/action-state-group/evidencebook/pull/555): feat(dealprofile): verify a sealed v1 deal report against its deal's first record (#554)
- [#553](https://github.com/action-state-group/evidencebook/pull/553): feat(python): x-deal-v1 receive profile and act gate, as Go's (#461 PR 6d-3a)
- [#552](https://github.com/action-state-group/evidencebook/pull/552): feat(python): x-deal-v1 sent records, book pairs and the payment check (#461 PR 6d-2b-2)
- [#550](https://github.com/action-state-group/evidencebook/pull/550): feat(python): x-deal-v1 authorization of acts, as Go's CheckAuthorizations (#461 PR 6d-2b-1)
- [#549](https://github.com/action-state-group/evidencebook/pull/549): feat(python): x-deal-v1 status value and report from books, byte for byte with Go (#461 PR 6d-2a)
- [#547](https://github.com/action-state-group/evidencebook/pull/547): fix(deal): read schema patterns as Go's RE2 in Python and Rust (#546)
- [#545](https://github.com/action-state-group/evidencebook/pull/545): feat(python): x-deal-v1 records, checks and start, byte for byte with Go (#461 PR 6d-1)
- [#544](https://github.com/action-state-group/evidencebook/pull/544): fix(shelf): read the counted prefix in one snapshot in Go and Python (#543)
- [#542](https://github.com/action-state-group/evidencebook/pull/542): feat(python): shelf hooks for x-deal-v1 (held readers, record times, append_at, resolve_payload, seal_sent, book status, after_receive) (#461 PR 6c-1)
- [#541](https://github.com/action-state-group/evidencebook/pull/541): feat(rust): deal::v1 book_entry, ruling naming, DealStatus and the report from a shelf (#461 PR 6b-2c)
- [#540](https://github.com/action-state-group/evidencebook/pull/540): fix(rust): Shelf::records reads field_commitments as Go's object (#538)
- [#539](https://github.com/action-state-group/evidencebook/pull/539): feat(rust): deal::v1 ReceiveProfile, can_act and gate_act on a shelf; ShelfBook::resolve_payload (#461 PR 6b-2b)
- [#537](https://github.com/action-state-group/evidencebook/pull/537): feat(rust): deal::v1 book pairs and the payment check (#461 PR 6b-2a)
- [#536](https://github.com/action-state-group/evidencebook/pull/536): feat(rust): shelf hooks for x-deal-v1 (seal_sent, record times by key and received, BookStatus, after_receive) (#461 PR 6b-1)
- [#535](https://github.com/action-state-group/evidencebook/pull/535): fix(rust): deal refuses the members serde_json reads as something else (#534)
- [#533](https://github.com/action-state-group/evidencebook/pull/533): feat(rust): deal::v1, the x-deal-v1 profile in Rust, with a Go/Rust differential check (#461 step 5, PR 6a)
- [#530](https://github.com/action-state-group/evidencebook/pull/530): feat(dealprofile): x-deal-v1 sealed deal report (#461 step 5, PR 5)
- [#532](https://github.com/action-state-group/evidencebook/pull/532): chore(release): evidencebook v0.18.1
- [#531](https://github.com/action-state-group/evidencebook/pull/531): fix(dealprofile): xdealv1.BookEntry seals header record_type deal_step
- [#529](https://github.com/action-state-group/evidencebook/pull/529): chore(release): evidencebook v0.18.0
- [#528](https://github.com/action-state-group/evidencebook/pull/528): feat(dealprofile): x-deal-v1 Receive and each stamp name a counted ruling's arrival (#527)
- [#526](https://github.com/action-state-group/evidencebook/pull/526): feat(dealprofile): x-deal-v1 status value and StatusProvider (#461 step 5, PR 4c-2b-2)
- [#525](https://github.com/action-state-group/evidencebook/pull/525): feat(shelf): a book status source is handed the book and journal (#461 step 5, PR 4c-2b-1)
- [#520](https://github.com/action-state-group/evidencebook/pull/520): docs(dealprofile): x-deal-v1 carries no demo member
- [#524](https://github.com/action-state-group/evidencebook/pull/524): chore(release): evidencebook v0.17.0
- [#506](https://github.com/action-state-group/evidencebook/pull/506): chore(ard-commerce): the ARD demo moved to evidencebook-skills demo/ard
- [#519](https://github.com/action-state-group/evidencebook/pull/519): fix(dealprofile): x-deal-v1 admit and check carry a nonce (u528)

### Added: `deal::v1`, the x-deal-v1 deal record profile (evidencebook#461 step 5, PR 6a)

- **What it is.** The Rust counterpart of Go's `dealprofile/xdealv1`, behind the `deal` feature. It gives the same bytes and refusals as Go.
- **What it covers:**
  - names and record types;
  - `decode`, `encode`, `digest` and `check`, plus `check_sealed`, `check_agreement`, `check_dispute` and `check_ruling`;
  - `start` and `start_from_books`;
  - `row_inputs`, `build_status` and `resolve_row`;
  - `build_report`, `report_payload` and `settled`;
  - `verify_sent`, `pair` and `terms_leg`;
  - `check_authorizations`, including the check that journal records do not share a nonce, and `new_nonce`.
- **Tests.** Each of these reads the shared vectors: every fixture under `dealprofile/x-deal-v1/fixtures`, and the records, start, status, report, settled, decline and authorization vectors under `testdata/deal-v1`.
- **Schema.** The x-deal-v1 schema is vendored byte for byte, and a test holds the vendored copy equal to the source.
- **`row_status::compute_late`** returns the late statements and late disputes, as Go's `ComputeLate` does, and passes `testdata/row-status/late.json`. `row_status::Date` and `Row` now serialize as Go writes them.
- **Not yet ported:** the shelf-coupled parts. These are the receive profile, the book status source, ruling naming and the payment check, and they come in PR 6b.

### Added: the shelf hooks a deal profile runs on (`shelf` feature; evidencebook#461 step 5, PR 6b-1)

The Go shelf's hooks from #484, #490, #517, #525, #526, #528 and #530, which x-deal-v1's shelf parts (PR 6b-2) need:

- **`ShelfBook::append_at(seq, entry)`** appends only as the book's next record, counting a record stored and awaiting its log append; any other position is `ShelfError::Busy`.
- **Held readers.** `Shelf::held_first`, `Shelf::held_by_deal` and `Shelf::held_proofs` (`HeldProof`) read received records and stamp proofs without the shelf lock.
- **`Shelf::seal_sent(entry, members, action_id)`** appends a journal record and seals its standalone `fyi` Capsule over the same canonical JSON bytes under the caller's `action_id`, with `members` carried in that Capsule only. A record stored whose log append failed comes back in `SealedSent` with its Capsule and the error. `seal::seal_payload` seals such a Capsule.
- **Record times by key and for received records.** `Shelf::record_time_by_key`, `received_record_time` and `received_record_time_by_key`, and the stamps that give a record its time, `Shelf::record_stamp` and `received_record_stamp`.
- **`BookStatus` is a trait** in place of the closure type (Go's four status interfaces in one): `values` is handed a `StatusInput` (the stamp's tick, the book's ids, the book and the journal if the shelf has one), and `before_stamp` runs before each stamp under the receive lock. A cadence with a book status therefore takes the receive lock at each stamp, so a `ReceiveProfile` callback must not call `Cadence::stamp` or `tick`. A closure of a book id and log id still is one. `Shelf::existing_journal` returns the journal without creating it.
- **One book's status error no longer fails the stamp.** That book keeps its last fact and is listed in `TickResult::unstated`; `TickResult::before_stamp` reports a `before_stamp` error.
- **`ReceiveProfile::after_receive`** runs on every `Shelf::receive` call once its parts committed; its error is `ReceiveError::after`. `RecordOutcome::deal_id` is the deal the profile read the record as.

Breaking for a caller that builds these structs literally: `TickResult` gains `unstated` and `before_stamp`, `ReceiveError` gains `after`, and `RecordOutcome` gains `deal_id`. `CadenceConfig::status` is `Option<Arc<dyn BookStatus>>`; a closure passed there needs its parameter types written (`|book_id: &str, log_id: &str|`).

### Added: `deal::v1` book pairs and the payment check (evidencebook#461 step 5, PR 6b-2a)

- **`deal::v1::book_pair` and `verify_book`** (`BookRead`, `STAGE_BOOK`), as Go's `BookPair` and `VerifyBook`. A deal-book or case-book record travels as its signed Capsule, the header the Capsule commits and the x-deal-v1 record the header commits as its one payload.
- **`deal::v1::payment_check`** (`PaymentInput`, `STAGE_PAYMENT`, the pass/fail values and the not-run and failure reasons), as Go's `PaymentCheck`. It checks a payment row over the settlement legs both deal books hold, using AAC's settlement verifier, and returns the `PaymentResult` a status value carries.
- **Tests.** All of these read the new `testdata/deal-v1/payment/vectors.json`, which Go's `TestPaymentVectors` writes from the Go payment and book pair tests, case for case. It also holds the refusals of resealed headers and the expected `BookPair` bytes.
- **`deal::v1::pair` now writes Go's exact bytes.** It escapes `<`, `>`, `&`, U+2028 and U+2029 as Go's `json.Marshal` does. Before, it wrote those characters unescaped. `book_pair` writes Go's bytes too.
- **`verify_book` reads the header as Go's typed `Header` does.** Each member is checked against its Go type, null reads as the zero value, and member names match in any case.
- **Dependency.** The `deal` feature now turns on agent-action-capsule's `settlement` feature.

### Added: the x-deal-v1 holder on a shelf (`deal` and `shelf` features; evidencebook#461 step 5, PR 6b-2b)

- **`deal::v1::ReceiveProfile`** is x-deal-v1's `shelf::ReceiveProfile`, as Go's `xdealv1.ReceiveProfile` is:
  - **`read`** takes a book pair or a sent pair. A sent record is held in `x-deal-v1/sent/<record digest>` (`SENT_LOG_PREFIX`).
  - **`admit`** applies EBL's push rule: offers from any key; a decline from the key the offer was sent to; a cancellation from the offer's signer; a ruling from our adjudicator; and a deal-book record from the deal's counterparty, on a log whose first record is its agreement of this deal.
  - **`answered`** says whether our side answered a held offer.
  - **`proof_sender`** relates a stamp proof to its sender, refusing a fork at the genesis or two candidates.

  Give the profile its shelf with `set_shelf` once the shelf is open. The profile holds a `WeakShelf`, so the two do not keep each other alive.
- **`ReceiveProfile::can_act`** (`CanActInput`) is the first-act coverage gate: a held stamp proof must cover the counterparty's agreement. **`gate_act`** (`ActGateInput`, `ActGate`, `GATE_NO_PROOF`, `GATE_NO_LAYER`) holds a first perform or payer settle until that is so, and lets it through unguarded when the counterparty runs no layer.
- **`ShelfBook::resolve_payload`** reads one payload of a record as the record's retention state allows. A `WITHHELD` or `DELETED` record's payload is the new `ShelfError::Unavailable`.
- **`Shelf::records` and `resolve_payload` read a lifecycle record's statement as Go does.** `retention_state` and `reason` match in any case, and a state that is not a string makes the book's listing `Corrupt`. Before, Rust read only the lowercase name and took any other value as no state.
- **`ShelfBook::record_by_seq`** returns one committed record.
- **`Shelf::downgrade`** and **`WeakShelf`** give a handle on a shelf that does not keep it alive.

Breaking for code that matches on every `ShelfError` variant: `ShelfError` gains `Unavailable`.

### Added: a v1 deal book's status, ruling naming and report on a shelf (`deal` and `shelf` features; evidencebook#461 step 5, PR 6b-2c)

- **`deal::v1::book_entry`** gives the entry that seals a record in its deal book: one payload, header record_type `deal_step`, the deal id as the subject, and a cites link to the record before it.
- **`deal::v1::name_rulings` and `name_all_rulings`** seal, in the holder's deal book, a statement naming each counted ruling of the deal's adjudicator that the book does not name yet (PROFILE §5; evidencebook#527).
  - The x-deal-v1 `ReceiveProfile` now names rulings on every receive (`after_receive`).
  - A book that is not readable whole is never written to.
  - A failure returns, beside its error, the record ids sealed before it (`NamingError`).
- **`deal::v1::DealStatus`** is Go's `xdealv1.StatusProvider` as a `shelf::BookStatus`. It gives a v1 deal book's profile, link value and status value at each stamp, and names rulings before each stamp.
  - `DealStatus::report` is the report of a deal book from the shelf, resting on its latest checkpoint.
  - An optional `OfferPair` lets a payment row's check run.

- **Fixed (`deal`, x-deal-v0):** a record that names a member `"$serde_json::private::Number"` or `"$serde_json::private::RawValue"` is now refused at the canonicalization stage and by `deal::decode`, whether or not the name is escaped (evidencebook#534). serde_json reads an object whose first member has one of these names as a number or as embedded JSON (`arbitrary_precision`, `raw_value`), so the record was not read as written and two texts could share one digest. Go reads such a member as written, so this is a stricter refusal than Go's, named in the module docs. `deal::v1` already refused it; both now use one check.

- **Fixed (`shelf`):** `Shelf::records` now gives a retention state to a record that commits a field and no payload, as Go does (evidencebook#538). Go writes `field_commitments` as an object, and Rust read it as a list, so such a record listed no retention state. The shelf interlock now compares every language's dump of a Go-written shelf holding such a record.

- **Fixed (`deal`):** the x-deal-v0 and x-deal-v1 schema stages read a schema pattern's `\s` as Go's validator (RE2) does: the tab, newline, form feed, carriage return and space only (evidencebook#546). A platform written with a no-break space or a vertical tab was refused here and passed in Go. The parity vectors carry the cases.

- **Fixed (`deal::v1`, feature `deal`):** `check_authorizations` reads each `at` as Go's `time.Parse(time.RFC3339, at)` does (evidencebook#551). On a record that was never checked it takes a one-digit hour, a `.` or `,` fraction of any length and a zone offset; a refusal is Go's text; and a journal's `at` never decreases from Go's zero Time. The crate replays Go's `testdata/deal-v1/time` vectors and the error texts of `testdata/deal-v1/authorization/approval-migration.json`.

- **Added (`deal::v1`, feature `deal`):** `verify_report` and `report_pair`, Go's `VerifyReport` and `ReportPair`. They check a sealed v1 deal report against its deal's first record: the holder's key, the deal and log the report rests on, the closed `ruling.against`, and the values `build_report` never writes (evidencebook#554). The crate replays Go's `testdata/deal-v1/report-verify` vectors, and the `x-deal-v1` differential compares it with Go on mutated reports.

- **Fixed (`deal::v1`, feature `deal`):** one exact-name rule (`exact.rs`, Go's `exactObject`) now covers a pair, the signed Capsule's members the profile reads, a settlement member, and a record header with its links (evidencebook#559). A member spelled otherwise than one read is refused, and a header carrying a `correlation` is refused. The crate replays Go's `testdata/deal-v1/pair-names` vectors.

- **Fixed (`deal::v1`, feature `deal`):** `verify_book` refuses a header `correlation` member that `record-header.schema.json` does not list, as Go's `VerifyBook` and Python's `verify_book` do (evidencebook#563). The crate holds the schema's three names as SHA-256 digests, so it still spells no deployment vocabulary; a test holds the digests to the schema, and `testdata/deal-v1/pair-names` gains Go-written cases for a listed and an unlisted name.

## 0.0.21 (2026-11-18)

- `history_card/2` (Evidence Request -03 §8.1.2, evidencebook#507), as Go's: positions from evidence-request's `history_card::positions`
  (P(n, W)), the answer `respond` builds over `checkpoints` or `full_history`
  (`ResponderPolicy::history_window`, 1 to 64, default 16, not capped by `max_answer_items`),
  `staple::check_history_card_2` (the spec's seven ordered checks) and
  `staple::staple_history_card_2`; `check_staple` accepts the card. A `checkpoints` subject with a
  range is answered with the entries and a `consistency_to_anchor` proof, without a card (`Subject::CheckpointRange`). It reads the shared vectors in
  `testdata/history-card-2`. Needs evidence-request 0.7.0.

## 0.0.20 (2026-11-17)

- `shelf::Handover::witnessed` and `shelf::verify_handover_times`: a handover carries the earlier witnessed
  shelf checkpoints and their receipts, `verify_handover` checks them, and a reader resolves record times
  from them as Go does (evidencebook#461, invariant C; `testdata/shelf/handover/earliest-receipt-not-anchor.json`).

- `shelf` (feature): `shelf_receipts` is keyed by checkpoint, witness URL and directory key id, as
  Go's (evidencebook#492): a witness whose directory row lists two keys keeps a receipt under each.
  Opening a shelf with the old table rebuilds it in place, keeping every row; the shelf version is
  unchanged. `Cadence::handover` orders receipts by URL and then key.

- `shelf` (feature): Go's genesis-fact ties in the whole-shelf handover (#461 §2.7), byte for byte on `testdata/shelf/handover.json` and `handover-result.json`. `Handover::ties` (`HandoverTie`, in book id order, always written, `[]` when none) carries, for each book whose log a witnessed tree tick held at or below its genesis size, that tick's leaf and 16-hash path, its root's position in the shelf log, the cadence checkpoint and its receipts, the old book checkpoint and, when the log grew since, a consistency proof to the genesis checkpoint; a leaf that kept no checkpoint statement gives no tie. `verify_handover` checks each tie against the handed-over entries and the reader's witness directory and fails on one that does not verify, on two ties of one book and on a tie whose book has no genesis fact in the handover; a genesis fact without one is still reported in `untied`.

## 0.0.19 (2026-11-17)

Tag `crates/evidencebook-v0.0.19`. Pull requests since 0.0.18 that touch `rust/`: [#474](https://github.com/action-state-group/evidencebook/pull/474) (the shelf's journal), [#475](https://github.com/action-state-group/evidencebook/pull/475) (the store of received records and `Receive`), [#477](https://github.com/action-state-group/evidencebook/pull/477) (`RecordTime` and the witness receipts) and [#480](https://github.com/action-state-group/evidencebook/pull/480) (capsule-emit-artifact 0.0.4).

### Changed: `shelf` feature on capsule-emit-artifact 0.0.4 (#479)

- The `evidencebook` crate (`shelf` feature) and the interop crate depend on `capsule-emit-artifact` 0.0.4, which is on agent-action-capsule 0.11, so agent-action-capsule 0.11.0 is the only copy in the graph and one `SqliteStore` can be handed to both this crate and `capsule-emit` 0.0.13. A consumer that also depends on `capsule-emit-artifact` moves its requirement to 0.0.4 (0.0.3 and 0.0.4 do not unify under Cargo, so a 0.0.3 pin builds a second copy and a second `SqliteStore` type).

- `shelf` (feature): Go's shelf journal (#461 §2.2 and §3), byte for byte. `Shelf::journal` opens the journal, a book of kind `journal` under `JOURNAL_LOG_ID` (`journal`), one per shelf, on first use, on a shelf of book facts. A stamp writes it a journal fact when it grew: `size`, `checkpoint_sha256` and `prev_fact_size` are zero in the clear and their true values are inside its status commitment, whose opening goes only to the owner and an auditor. The journal is not one of `Shelf::books`, has no extent on a `standard_history/1` or `/2` card, and a handover carries none of its openings. `Shelf::open` and `Shelf::host_log` refuse the journal's log id. A file whose `shelf_logs` table predates the kind gets the table rebuilt, with the same rows, when the journal is first opened.

- `shelf` (feature): Go's store of what the other side pushes (#461 §3), byte for byte on `testdata/shelf/receive`. `Shelf::receive` takes a record, a `StampProof`, or both, each part on its own, and returns the deal ids with records that entered or left their sender's counted prefix, those with new fork or checkpoint evidence, and those whose records a held proof newly covers (once the records reach its size and recompute its root). A `ReceiveProfile` (in `ShelfConfig::receive`) reads each record and holds the push rule, so the crate knows no record format. The store acknowledges a duplicate, stores a gap without counting it until it is filled, keeps both records of a fork as evidence and ends the counted prefix before it for good, and bounds record size, records per book, unanswered unsolicited offers (dropping the oldest first, after asking the profile's `answered` outside the write transaction, under a mark of the holder's own books; `ShelfError::Busy` after three tries) and what offers leave behind. A proof that contradicts a held proof at the same size or the records held is refused and its evidence kept; every accepted proof is kept. `Cadence::stamp_proof` builds a proof for an audience and `verify_stamp_proof` checks one. `Shelf::forget` deletes a settled deal's records under the holder's retention (by default never), leaving tombstones that read as `ShelfError::Deleted`. `Shelf::counted_prefix`, `Shelf::received_record_of` and `Shelf::evidence` take no shelf lock, so a `BookStatus` callback may call them during a stamp. Works on version 1 and 2 shelves. `shelf_facts::Opening` gains serde. Breaking: `ShelfConfig` gains `receive: ReceiveConfig` (a caller that builds the struct literally adds `receive: Default::default()`), and `ShelfError` gains `Refused`, `Deleted`, `Busy` and `Profile`.

- `shelf` (feature): Go's record time (#461 §2.6), byte for byte on `testdata/shelf/record-time.json` and `after-record-time.json`. `Shelf::record_time(book_id, position, witnesses)` returns the earliest registration time, among the receipts held from the named witnesses (their receipt URLs), of a shelf checkpoint covering the first fact of the book whose size covers the position, or `None`; position 0 is `ShelfError::Invalid`. It takes no shelf lock, so a `BookStatus` callback may call it. `Cadence::witnessed` keeps every verified receipt in the new `shelf_receipts` table, one per witness with the directory key id it verified under and its registration time, the earliest kept; a witnessed tick missing there is filled in when the cadence opens and after `Cadence::import_legacy`. `Cadence::handover` carries every receipt held for its anchor, in witness URL order, and `verify_handover` reports `witnessed` only for a receipt that gives a registration time.

## 0.0.18 (2026-11-17)

Tag `crates/evidencebook-v0.0.18`. Pull requests since 0.0.17 that touch `rust/`: [#466](https://github.com/action-state-group/evidencebook/pull/466) (`row_status`), [#469](https://github.com/action-state-group/evidencebook/pull/469) (`shelf_facts`), [#471](https://github.com/action-state-group/evidencebook/pull/471) (shelf migration and stamps), [#472](https://github.com/action-state-group/evidencebook/pull/472) (the whole-shelf handover) and [#453](https://github.com/action-state-group/evidencebook/pull/453) (checkpointed-local-log 0.6, evidence-request 0.6 and agent-action-capsule 0.11).

- `row_status` (new module, no feature): the Go package `rowstatus`'s term row status, result for result over `testdata/row-status/vectors.json` (evidencebook#461, acceptance 6). Additive.

### Changed: Rust crate on checkpointed-local-log 0.6.0, evidence-request 0.6.0 and agent-action-capsule 0.11.0 (#452)

- The `evidencebook` crate and the interop crate depend on checkpointed-local-log 0.6.0 (CLL 0.9.0, -02 checkpoints whose `sub` is the bare log id), evidence-request 0.6.0 and agent-action-capsule 0.11.0, so one copy of each new version is in the graph again, except agent-action-capsule, which `capsule-emit-artifact` 0.0.3 still brings at 0.10.0. The pinned answer envelopes are rewritten: each envelope's anchor digest, request digest, signature and embedded checkpoint change, and so do the artifact bytes and digest wherever the artifact carries that anchor or checkpoint.
- The crate now signs checkpoints with the bare-log-id `sub`. A verifier that insists on `<log id>#<size>`, such as capsule-anchor's witness check before capsule-anchor#94 (merged 2026-11-16, not yet released), rejects them: run a capsule-anchor release that includes #94 before adopting this crate version.
- The interop `responder` and `personal-history` programs write each log key identity with the bare log id as `sub`, matching the checkpoints it signs and the Go responder.

- `shelf_facts` (new module, no feature): the Go package's shelf log entry documents, commitments, openings and audiences, byte for byte over `testdata/shelf/facts/vectors.json` (#461). Additive.

- `shelf` (feature): Go's whole-shelf handover and its replay (#461 §2.7), byte for byte. `Cadence::handover` hands the shelf log over up to a finished stamp (the newest when `n` is 0) with its entry documents, status openings, book checkpoints and consistency proofs, and `verify_handover` replays it under the holder's checkpoint key and a witness directory: it refuses a handover of another version, an entry held back or changed, a document left out or not its canonical JSON, a changed or missing opening, a book checkpoint left out or not the holder's, and a replaced book, and returns the coverage start, whether a receipt verifies, each book's latest fact and the untied books. `shelf_facts::BookFact` is added, and `shelf_facts::Opening` gains serde (`{salt, value}`).

- `shelf` (feature): Go's shelf log of book facts (#461), byte for byte. `Cadence::migrate`, `Cadence::stamp` and `Shelf::openings` are added, and shelf file versions 1 and 2 are opened. Breaking: `CadenceConfig` gains `status: Option<BookStatus>`, the values each book's fact commits to; a caller that builds the struct literally adds `status: None`.

## 0.0.17 (2026-11-16)

Tag `crates/evidencebook-v0.0.17`. Pull request since 0.0.16 that touches `rust/`: [#464](https://github.com/action-state-group/evidencebook/pull/464) (the temporary `outcome_note` record).

- **TEMPORARY** `deal::TYPE_OUTCOME_NOTE` and `OutcomeNoteBody`, the x-deal-v0 `outcome_note` record (evidencebook#462), checked by the chain and counted `did_not_hold` by `standard_history_deal`.

## 0.0.16 (2026-11-16)

Tag `crates/evidencebook-v0.0.16`. Pull requests since 0.0.15 that touch `rust/`: [#454](https://github.com/action-state-group/evidencebook/pull/454) (the `sale_unpost` record), [#445](https://github.com/action-state-group/evidencebook/pull/445) (the `rules_verdict` record, described in the root CHANGELOG's v0.14.0 entry; the crate carries `RulesVerdictBody`), [#448](https://github.com/action-state-group/evidencebook/pull/448) (clippy 1.100 fix, internal) and [#450](https://github.com/action-state-group/evidencebook/pull/450) (a test change only, internal).

- Internal: the shelf commit loop iterates the records instead of indexing them, so `cargo clippy -- -D warnings` passes on Rust 1.100 (#446). No behaviour change.

- `deal`: the sale-log chain stage and the record types follow x-deal-v0's new `sale_unpost` record (a listing taken down; PROFILE.md section 6, rule 15), with `SaleUnpostBody` and `TYPE_SALE_UNPOST`. The vendored schemas are updated to match (#451).

## 0.0.15 (2026-11-09)

Tag `crates/evidencebook-v0.0.15`. Pull request [#442](https://github.com/action-state-group/evidencebook/pull/442): chore(rust): move evidence-request to 0.5.0.

- Breaking for hosts: `evidence-request` moves from 0.4.1 to 0.5.0 (also `rust/interop`), which is on `checkpointed-local-log` 0.5, so a host's graph carries one `checkpointed-local-log` line instead of two; the 0.0.14 note about carrying both no longer applies. `evidence-request` 0.5.0 implements `evidence-interop/2`, which the book does not answer: the responder reads requests against its own list of answered profiles (`/0` and `/1`), so a request naming `/2` is still `request_malformed` listing `/0` and `/1`, and `answer` refuses it with `AnswerError::Sign`, as the Go and Python books do. Types from `evidence-request` in this crate's public API (`AnswerError::Sign`, `Envelope`) are now 0.5.0's.

## 0.0.14 (2026-11-09)

Pull requests merged since 0.0.13 that touch `rust/` (the entries below are the user-facing changes):

- [#440](https://github.com/action-state-group/evidencebook/pull/440): chore(rust): move the Rust crates to AAC 0.10 / cll 0.5 / capsule-emit-artifact 0.0.3 (#437)
- [#433](https://github.com/action-state-group/evidencebook/pull/433): feat(respond)!: uniform is the default refusal policy, specific an opt-in (#206 row 8)
- [#430](https://github.com/action-state-group/evidencebook/pull/430): build(capsulectl): require capsule-cli rc38 and evidencebook v0.13.9; cut the CHANGELOG for capsulectl/v0.1.0-rc38
- [#409](https://github.com/action-state-group/evidencebook/pull/409): feat(rust): pair a failed interaction's halves by outcome in reconcile_halves
- [#394](https://github.com/action-state-group/evidencebook/pull/394): feat(dealprofile): a verdict's rules body records the pinned pack's kind
- [#390](https://github.com/action-state-group/evidencebook/pull/390): chore(release): v0.13.5
- [#389](https://github.com/action-state-group/evidencebook/pull/389): fix(dealprofile): price a BTC deal in msat on the standard-history card
- [#386](https://github.com/action-state-group/evidencebook/pull/386): docs(dealprofile): for currency BTC, amount_minor counts millisatoshi
- [#377](https://github.com/action-state-group/evidencebook/pull/377): fix(dealprofile): a disagreeing reconcile Result counts as undecided, never did_not_hold (#212)
- [#372](https://github.com/action-state-group/evidencebook/pull/372): test: Respond refuses a non-deterministic CBOR request in Rust and Python (#365)
- [#371](https://github.com/action-state-group/evidencebook/pull/371): chore(release): v0.13.3
- [#367](https://github.com/action-state-group/evidencebook/pull/367): fix(dealprofile): rule_inputs_version accepts 3 and 4 (#363)
- [#359](https://github.com/action-state-group/evidencebook/pull/359): feat: bound Cadence.HistoryCard at DefaultMaxAnswerItems (#354)
- [#342](https://github.com/action-state-group/evidencebook/pull/342): docs(guide): derive and check the node's History in path 4 (#322)
- [#351](https://github.com/action-state-group/evidencebook/pull/351): feat(python): the Goose close step cites the counterparty's checkpoint (#303)
- [#353](https://github.com/action-state-group/evidencebook/pull/353): feat: Python and Rust responders answer standard_history/1 (#328)
- [#346](https://github.com/action-state-group/evidencebook/pull/346): chore(release): v0.13.2
- [#352](https://github.com/action-state-group/evidencebook/pull/352): fix: CheckStaple verifies a history card's checkpoints form one chain (#344)
- [#343](https://github.com/action-state-group/evidencebook/pull/343): feat: Cadence.HistoryCard builds the profile's history_card/1 (#341)
- [#327](https://github.com/action-state-group/evidencebook/pull/327): feat: stapled History card, capped at the standard card (#275)
- [#320](https://github.com/action-state-group/evidencebook/pull/320): feat: the reproduction header member and Config.Reproduction (#232)
- [#307](https://github.com/action-state-group/evidencebook/pull/307): feat(python): the Goose deal cites its admission and exports it with the opening as one bundle (#302)
- [#309](https://github.com/action-state-group/evidencebook/pull/309): feat(deal): the x-deal-v0 chain stage (§6) in Go and Rust (#308)
- [#305](https://github.com/action-state-group/evidencebook/pull/305): fix(python): a null deal record is no step; pin xn-- label vectors (#298, #299)
- [#294](https://github.com/action-state-group/evidencebook/pull/294): fix(dealprofile): fold both cases of a Cherokee name to the capitals (#283)
- [#295](https://github.com/action-state-group/evidencebook/pull/295): test(rust): the deal feature reads dealprofile/testdata/parity-vectors.json (#284)

### `staple::check_staple` and `staple::staple_history_card`, a stapled History card (evidencebook#275)

- New: `staple::check_staple(request, reply, holder)`, a reader's check of a staple it received:
  the holder's own request and the envelope its book answered, presented by `holder`, carrying
  `history_card/1` over `checkpoints` or `standard_history/1` over `full_history` and nothing
  else. It returns the anchor, nonce and issue time for the reader's replay check. A
  `history_card/1` card must have the card's members and nothing else and end at its anchor. For
  `standard_history/1` it checks the card has exactly the standard card's members; the rest of
  the card's form has no Rust checker yet. Go writes the shared vectors,
  `testdata/staple/staples.json`.
- New: `staple::staple_history_card(book, responder)` builds a `history_card/1` staple as of the
  book's newest checkpoint (cut if the book grew), with a fresh nonce. It grows no record and is
  not counted by the responder's limiter. `standard_history/1` is stapled with
  `staple::staple_standard_history` (evidencebook#328).

### Breaking: `Header.reproduction`, the reproduced-scratch-book marker (evidencebook#232)

- New: `Header::reproduction` (`Reproduction`, `REPRODUCTION_CLOCK_CALLER`), read, validated
  and written as Go does, so a reproduced book's header keeps its identity here. Writing one
  is not implemented in this crate. `Header` gains a public field and has no `Default`, so a struct literal of it no longer compiles: set
  `reproduction: None`.

### `deal::check_chain`, the chain stage (PROFILE.md §6) (evidencebook#308)

- New: `deal::check_chain` (and `check_chain_values`), with `deal::STAGE_CHAIN`: one deal's
  records read in `seq` order against PROFILE.md §6, as the reference checker's `check_chain`
  and Go's `dealprofile.CheckChain` do, with the same refusals in the same order and the same
  text. `dealprofile/testdata/chain-vectors.json` pins it (every prefix of the positive
  chains, each negative fixture and single-member mutations).

### Breaking: `deal::Error` has an `Unpriced` variant (evidencebook#284)

- `deal::Error` gains `Unpriced(String)`, a deal with no recorded amount
  (`standard_history_deal`). The enum is public and not `#[non_exhaustive]`, so
  an exhaustive `match` on it no longer compiles: add an arm or a `_` arm.

### `deal` reads Go's parity vectors (evidencebook#284)

- New: `deal::standard_history_deal` (with `HistoryInput`, `HistoryRecord`,
  `HistoryResult`) and `deal::minor_units`, the `standard_history/1` deal entry
  and ISO 4217 minor units, at byte parity with Go's `dealprofile` and the
  Python package.
- Fixed: a domain with an `xn--` label whose part before the last hyphen holds
  non-ASCII characters (`xn--zcaé-.com`) is read as Go reads it, not refused.
- A name with Cherokee letters folds as CaseFolding.txt does, which Go's `dealprofile` now matches
  (evidencebook#283); the parity vectors pin it.

- `Cadence::history_card(n)` returns the profile's `history_card/1` over the cadence log as a `Staple`, as Go's
  `Cadence.HistoryCard` does (#341); `check_staple` accepts it for the shelf signer's key id.

- Breaking: `ShelfError` gains the variant `OverLimit(String)`, and the enum is not `#[non_exhaustive]`, so an exhaustive `match` on it needs a new arm. `Cadence::history_card` returns the new `ShelfError::OverLimit` when the card would list more than `DEFAULT_MAX_ANSWER_ITEMS` (10,000) ticks, as Go's `Cadence.HistoryCard` does (evidencebook#354).

### Breaking: `deal::VerdictBodyRules` has a `kind` field (evidencebook#394)

- `VerdictBodyRules` gains `kind: Option<String>`, the pinned pack's kind, read and written as Go's verdict `rules` body does. A struct literal of it needs `kind: None` (or `..Default::default()`), and an older release refuses a verdict body carrying `kind` (`deny_unknown_fields`).

### Changed: deal records accept rule_inputs_version 4 (#363)

- The x-deal-v0 schemas accept `rule_inputs_version` 3 or 4 (`enum [3, 4]`, was `const 3`); 4 is the rule inputs version in which a rule's `channel` comes from the latest counterparty message only. A positive fixture (`positive-rule-inputs-4`) seals a pay check under 4 (#363).

- `respond::respond` answers `standard_history/1` over `full_history` when `Responder::standard_history` holds a
  `standard_history::StandardHistorySource` (holder kind and the host's inputs), byte for byte with Go's
  `StandardHistorySource` (evidencebook#328): to any authenticated requester and never a stranger; without a source it is
  `derivation_unsupported`. `standard_history::derive_card` ports SH §4's card derivation (the cards in
  `dealprofile/testdata/parity-vectors.json` are derived byte for byte); the card is not checked against its own inputs
  afterwards, as the crate has no card checker. `staple::staple_standard_history` builds a `standard_history/1` staple.
- Breaking: `Responder` has a new field `standard_history` and `answer::Options` a new field `standard_card`; set them to
  `None`.

- `check_staple` refuses a `history_card/1` staple whose checkpoints are not one chain (signature under the first's key,
  one log, signed `prev_size`/`prev_root`, consistency proofs), as Go's `CheckStaple` does (evidencebook#344).

- `deal::standard_history_deal` prices a deal whose check states currency `BTC` as `{assetCode: "BTC", assetScale: 11}` (`amount_minor` counts millisatoshi) instead of returning `Error::Unpriced`; `minor_units("BTC")` stays unpriced, and other codes with no ISO 4217 minor units are still refused (evidencebook#388).

### Added: Rust reconcile pairs a failed interaction's halves by outcome (#400)

- `Half::failed` marks the record of an interaction that did not complete; `from_store` sets it to `false`, since a header carries no outcome, so the caller marks failed halves. `reconcile_halves` now mirrors the Go `ReconcileHalves` (#205, #399): the primary-key join prefers a half of the same outcome, two failed halves are `MATCHED`, a failed half against a served one is `CONFLICTING`, and the content test runs only on two served halves. Adding a public field to `Half` is source-breaking for code that builds it with a struct literal.

### Breaking: depend on agent-action-capsule 0.10, checkpointed-local-log 0.5 and capsule-emit-artifact 0.0.3 (#437)

- `agent-action-capsule` 0.10.0 (was 0.8.0), `checkpointed-local-log` 0.5.0 (was 0.4.0) and, with the `shelf` feature, `capsule-emit-artifact` 0.0.3 (was 0.0.2).
- With the `shelf` feature, `HostLogStores::log` is now `checkpointed-local-log` 0.5's `SqliteStore`, so a host that names it moves its own `checkpointed-local-log` pin to 0.5 with this crate.
- `evidence-request` 0.4.1 still depends on `checkpointed-local-log` 0.4, so a build carries both lines.

### Changed (breaking): `uniform` is the default refusal policy (#225, #206 row 8)

- `RefusalPolicy::default()`, and so `ResponderPolicy::default()`, is now `Uniform`: a responder answers `policy_declined` wherever a more specific token would tell a counterparty whether the holder has a record for a key it does not hold. `RefusalPolicy::Specific` is an explicit operator opt-in; set `refusal_policy: RefusalPolicy::Specific` to keep the old answers.

## 0.0.13 (2026-11-01)

### Breaking: `deal::Record` has a `counterparty_class` field (evidencebook#273)

- `deal::Record` gains `counterparty_class: Option<String>`, the x-deal-v0
  top-level member SH §7 reads from a pay check (PROFILE.md section 1). The
  struct is public and not `#[non_exhaustive]`, so a struct literal that names
  every field no longer compiles: add `counterparty_class: None`. The vendored
  schema allows the member on a pay check only, and refuses another value or
  another record.

## 0.0.12 (2026-10-31)

### Breaking: `deal::EvidenceBody` has a `preimage` field (evidencebook#260, evidencebook#269)

- `deal::EvidenceBody` gains `preimage: Option<String>`, the x-deal-v0
  `body_evidence` member that carries the preimage of a Lightning payment the
  user received (evidencebook#260, shipped in Go v0.12.1). The struct is public
  and not `#[non_exhaustive]`, so a struct literal that names every field no
  longer compiles. Migrate by adding `preimage: None` or ending the literal
  with `..Default::default()`. An absent `preimage` stays absent when read and
  written. A `body_evidence` that carries `preimage`, which `deny_unknown_fields`
  made this crate reject before, now reads.
- `#[non_exhaustive]` is deliberately not added. No type in this crate uses the
  attribute, the deal types mirror the profile's schema one to one, and adding it now
  would break every literal outside the crate a second time. A profile member
  added later is a breaking change of the crate's pre-1.0 version, as here.
- The crate has no counterpart of Go's `dealprofile.PreimageMatches`; the field
  is carried and not checked. The Python package has no deal profile, so it has
  no matching change.

### Added: the shelf, behind the feature `shelf` (evidencebook#262)

- Module `shelf`: one subject key's books in one SQLite file, the file Go's
  shelf writes, table for table (evidencebook#254 §2). `Shelf::open_sqlite`
  takes the shelf's writer lock (`flock` on `<file>.lock` or a lock file the
  host names, waiting up to `lock_timeout`) or checks a lock the host already
  holds (`HeldLock`, which takes none of its own and opens off unix too). It
  refuses a `shelf_meta` version it does not know and fills `shelf_meta` and
  `shelf_logs` in a file from before them.
- `ShelfBook` is a book that writes: `append` seals each record as `seal`
  does, keeps its payloads in the shelf's artifact store
  (capsule-emit-artifact, SQLite backend), stores it in `book_records` and
  appends its id to the book's CLL log; `checkpoint` commits the default
  authenticated indexes' changed roots, then checkpoints with the log's
  runner. Opening a book commits a record a crash left stored but not in its
  log, and refuses a log entry with no stored record. It implements
  `RecordSource`, `CheckpointedLog` and `ask::Book`, so `answer`, `respond`
  and the derivations run on it unchanged.
- `Shelf::host_log` and `Shelf::legacy` open a host log: a log the host
  seals in its own format on the shelf's CLL backend and artifact store, in
  the cadence or outside it (`HostLog::write`, `HostLog::stores`).
  `shelf_logs` keeps book and host ids apart across reopens. `HostLogStores`
  is the one public item that names a `cll` type (`tests/public_api.rs`):
  its `log` is checkpointed-local-log's concrete `SqliteStore`, so a breaking
  checkpointed-local-log release is a breaking change of this crate, and a
  host's own `checkpointed-local-log` pin moves with this crate's.
- A shelf on a host-held lock opens off unix (an `Arc` of the host's lock is a
  `HeldLock`); CI runs the shelf's tests on Windows. A reader in another
  process may open a log or the artifact store on the file without the
  shelf, taking no lock.
- `Cadence`: `tick`, its unfinished-tick resume, `witnessed` (receipts
  verified with agent-action-capsule's witness verifier), `live`, `last`,
  `get`, `witnessed_tick`, `chain`, `standard_history_anchor` and
  `import_legacy`, with every salt, position, nonce and jitter drawn from one
  injected `RandomSource` (`SeededStream` is the vectors' stream).
- `Shelf::records` lists the committed records of every book without
  opening one, with each record's retention state and the records a crash
  left pending.
- The Rust shelf reads `testdata/shelf` byte for byte, and joins the shelf
  storage interlock: Go, Python and Rust each continue the others' files,
  and the writers of one file in any two of them exclude each other
  (`evidencebook-interop`, bin `shelf`).

### Changed: checkpointed-local-log 0.4.0, evidence-request 0.4.1, agent-action-capsule 0.8.0

- The crate requires `checkpointed-local-log` 0.4.0, `evidence-request`
  0.4.1 and `agent-action-capsule` 0.8.0, so one CLL line is in its graph
  (evidencebook#254 §7.4). CLL 0.4.0 refuses a small-order checkpoint key
  itself, as this crate's strict check already did.

### Added: `respond` answers `evidence-interop/1` requests (evidencebook#248)

- `respond` answers a request naming `evidence-interop/1` with the same
  artifact as under `/0`, in an envelope naming `/1`; a request naming `/0` or
  no profile is answered as before. An unknown profile is refused
  `request_malformed` with the detail `profiles supported: evidence-interop/0,
  evidence-interop/1`. `respond::PROFILE_V1` names the identifier.
- A `/1` request refused for a limit of the book's own, its size, the rate
  limiter or the answer item cap, is refused `capacity_exceeded`, under either
  refusal policy (ER -02 §11). A `/0` request keeps `policy_declined`.
- The crate still asks under `/0`, and `Refusal::reason_is_registered` stays
  the `/0` check. `Refusal::reason_is_registered_under(profile)` checks a
  refusal under the profile its request asked for (v1 §6.3), so a `/1`
  `capacity_exceeded` is registered.

### Added: `ask::request_sent` records a request another tool sent (evidencebook#247)

- `ask::request_sent` records a request that another tool composed and sent,
  exactly as given, as Go's `Book.RequestSent` does (evidencebook#245): every
  member is kept, including those the request draft does not define (the
  capsules plugin's `audience`, `requester_id` and `requester_auth`), and no
  nonce or profile is added. The octets must be a well-formed request in
  JCS, so the recorded digest is the one the responder signed its reply over;
  `evidence-interop/0` and `/1` requests are read. A nonce the book already
  used is refused; a request without one is recorded every time. The record marks the request
  `composed_elsewhere`, so the book is not read as vouching for members it keeps but never
  checks, such as `requester_auth`. The live-16
  requests (`testdata/live16-ask/`) are recorded with the digest their
  replies answer.

## 0.0.11 (2026-10-31)

### Breaking: `evidence-request` 0.3, and the envelope names the profile asked for

- `evidencebook` and `interop` require `evidence-request` `0.3`
  (agent-action-capsule#485), which registers the Evidence Request -02 refusal
  reasons `capacity_exceeded` and `request_unauthenticated` and reads
  `evidence-interop/1`. `evidence-request` types are in this crate's API
  (`AnswerError::Sign`, `Reason`), so a dependent moves with it, and a
  dependent that keeps one `evidence-request` in its graph needs this to take
  0.3.
- `request::reasons::ALL` lists the registry's ten tokens (`[&str; 10]`).
  `Refusal::reason_is_registered` stays the check of `evidence-interop/0`, the
  only profile this crate answers and asks under: its eight ER -01 tokens, as
  Go and Python check, so a refusal carrying either -02 token is not
  registered here.
- `answer::build` signs the envelope under the profile the request's octets
  name (`evidence-interop/0` when they name none or an empty one), so a
  caller that answers an `evidence-interop/1` request gets a /1 envelope. A
  request naming a profile `evidence-request` does not implement is
  `AnswerError::Sign`. This crate's own `respond` still refuses anything but
  /0 before it builds.

### Changes

- `respond` answers an underived (or shared-derivation) `correlation`
  subject under the rule of a record or citing-records subject, as Go's
  `Respond` now does (evidencebook#114): under `counterparties`, the default,
  only to a counterparty every record held under the key names; under
  `peers` to any authenticated peer; never to a stranger. Before, every
  underived correlation was refused `not_authorized`. `range` and
  `full_history` stay refused unless a recipient-specific derivation grants
  them.

## 0.0.10 (2026-10-30)

### Changes

- **Requires agent-action-capsule 0.5.0** (was 0.4.1): witness receipts are
  verified with strict Ed25519 (agent-action-capsule#447). 0.5.0 adds fields to
  its `WitnessReceiptResult` and `ReceiptCheck`; evidencebook builds none by
  literal, so no code here changes.

- **`deal` record types** (feature `deal`; evidencebook#93): `Record` (the
  x-deal-v0 block and a `Body`, one variant per record type), the typed
  action records and contract documents, and `decode`, which reads a
  record's text into them and refuses text they would not encode back to.
  They follow the schemas member for member, as Go's `dealprofile` types
  do, and carry no rule: `encode` checks a record built from them as it
  checks its text. Every record in the profile's fixtures and the baseline,
  and a sample of each type they lack, round-trips byte for byte.
- **`deal`, the x-deal-v0 deal record profile, behind the new feature
  `deal`** (off by default; evidencebook#93). The Rust counterpart of the Go
  package `dealprofile`, byte for byte in its payloads, digests and Capsule
  members (a sealed Capsule's layout is the sealer's; the module docs say
  where capsule-emit 0.0.8's differs from Go's): the profile's names; `deal_key`,
  `profile_key`, `normalize`, `fingerprint` and `commit_text`; `encode`,
  `check`, `digest` and `check_digest`, which refuse a record at its first
  failed record-level stage (canonicalization, personal_data, wording,
  schema, digest) as a `StageError`; `report_payload` and `report_capsule`;
  and `step_capsule`, which returns a step's Capsule members as plain data
  and its record header fields, for the producer to seal. The profile's
  schemas and golang.org/x/net v0.59.0's public suffix tables are vendored.
  The chain stage stays with the reference checker. The feature's
  dependencies (`boon`, `hmac`, `icu_casemap`, `icu_normalizer`,
  `icu_properties`, `idna`, `regex`) are built only with it.

## 0.0.9

### Breaking changes and how to migrate

- **`answer::Derive`** (#165, #35): an unregistered definition-digest
  derivation now reaches the profile's `Derive`. To migrate, have a
  `Derive` that answers every name it is given check the name first. A
  derivation restricted to a recipient must be registered; the crate checks
  no requester for a value `Derive` answers. Function signatures are
  unchanged.

### Changes

- **Breaking (behavior): `answer::Derive` answers a holder's own digest
  derivation** (evidencebook#35). A request whose `derivation` is a
  definition digest that `Options::derivations` does not register now goes
  to the profile's `Derive`, as a token does. `Derive` is called with the
  digest, and its value is carried under the member it names, with the
  anchor checkpoint as material when `prove_beyond_profile` is set. A
  registered digest is still answered from its registration, and an
  unregistered digest that `Derive` does not answer is still
  `DerivationUnsupported`. To migrate, check the
  name: a `Derive` that answers every name it is given now also answers
  such digests. The crate checks no requester for a value `Derive`
  answers, token or digest, and `Derive` is not given one: a derivation
  restricted to a recipient must be registered. `select` still selects a
  digest's subject records and `Derivations::check` refuses an
  unregistered digest, so a caller serving its own digest decides it first
  and builds with an empty `Selection`.

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
