# Design note: the plugin's x-deal-v1 write path

Issue: evidencebook-deal#212 (evidencebook#461 step 5, the plugin half).
Status: round 1, for review by Codex, Kimi and Sherlock2 (it adds user-facing verbs and changes
what two existing verbs do on a new deal).
Checked on evidencebook main at `ae7e898` (x-deal-v1 records #487, sent records #488, library
calls #484/#486) and this repository at `d17d543`. The wire shapes are evidencebook's
`docs/design/461-deal-profile.md` (§3.1 to §3.5, §9); this note does not restate them.

Each rule is marked **Decided** (a source is named) or **Proposed** (this note's choice).

## 1. What this note covers

The part of #212 that lets the plugin write and send v1 records: version selection, the
deal-book writer, the journal writer, seal-and-send, receiving, and what `deal close` and
`deal terms-result` do on a v1 deal. The library's `ReceiveProfile` interface is on main
(`shelf_receive.go:47`), but its x-deal-v1 implementation, `Start`, `StatusProvider`,
`PaymentCheck`, `CanAct` and the v1 report are evidencebook PRs 3 to 5 and are not (design §8.3,
§11.1). So the write verbs and `deal offer`'s sent file can land first; `deal receive` and the
offerer's `deal agree` (which needs the acceptor's agreement received) are specified here and
land after PR 3 and 4. Not covered: row status in `deal report`/`list`/`settlement`, the
stamp-proof order, the v1 report and `standard_history`; a later note covers them.

## 2. What exists

- The deal engine is a v0 event fold: `dealEvent`, `foldDeal`, `sealedEvent` (`deal.go`), sealed
  as one `x-deal-v0` record per step through `sealBookStep` (`deal_books.go:612`) onto the deal's
  book (`s.book`), whose header comes from `dealprofile.StepCapsule`.
- A deal's layout is chosen per deal in `useDeal` (`deal.go:208-292`): legacy SQLite index, or a
  book; `shelfDealIndex` (`deal_books.go:49`) answers reads for both.
- Nothing sends a record to the other side. A copy is a file (`deal report --share`,
  `disclose --out`), and the counterparty verifies it with `verify`.
- The shelf already has `SealSent`, `Receive`, `CountedPrefix`, `Stamp` and `HeldFirst`
  (evidencebook `shelf_sent.go:47`, `shelf_held.go:25`).

## 3. Version selection

**Decided** (evidencebook design §2.3): a record's version is the one `x-deal-v<N>` member it
carries (`dealprofile.Version`), a book has the version of its first record, a deal has one
version on both sides, and the offer's version decides.
**Decided** (Q7, coordinator, 2026-11-17): v0 stays the default until `/3` is served; v1 is
selectable by setting.

**Proposed:**

1. **The setting** is the profile's `deal_version`, `x-deal-v0` (default) or `x-deal-v1`, set once
   with a new top-level `capsulectl deal set-version x-deal-v1` (a positional value; Sherlock2's
   name: `deal declare` would read as the same act as `deal standard-history declare` and hides that
   this one can move the store; not `--profile`, which is the global flag naming the profile). Declaring v1 migrates the deal store, as section
   3a says. It governs only the offers this profile writes. Every deal after
   that follows its offer's version (evidencebook design §2.3): a profile reads both versions
   whatever its setting, so a v0-setting profile that receives a v1 offer reads it and answers in
   v1, and a v1-setting profile that receives a v0 offer answers in v0.
2. **A local version table**, `deal_versions (deal_id, version, own_log_id, counterparty_log_id,
   state)`, is written at `deal offer` or at the first received offer, before any book exists.
   Version is read from this table, never by opening a book: a v1 deal has no book until its
   agreement (design §3.5, a declined offer opens none), and `Shelf.Open` creates a book on first
   use (`shelf.go:195`), so `useDeal` must not open one for a deal whose table row says no
   agreement yet.
3. **The deal book's log id** is `deal/` plus 32 lowercase hex of fresh randomness, minted by
   each side when it seals its agreement and stored as `own_log_id`; it is never derived from the
   deal id, and the two sides' ids differ (evidencebook design §3.1, "The deal book's log id").
   `counterparty_log_id` is learned from the `evidence_record_header` of the first record received
   from the counterparty's book. `useDeal` and `shelfDealIndex` (`bookDealIDs`, `dealOfBook`,
   `deal_books.go:289-323`) read the log id from this table for a v1 deal; they never parse
   `deal/<deal_id>`.
4. **One dispatch point.** `useDeal` reads the table (and, for an old deal with no row, treats it
   as v0) and sets `s.version` before `runDeal` calls `s.load` (`deal.go:957-960`). A verb that
   does not serve that version refuses there with the message of section 7, before anything loads:
   a v1 book's records have no v0 `deal_local` opening, so `bookSteps` (`deal_books.go:383-455`)
   would otherwise report a corrupt store.
5. **Mixed shelves.** `bookDealIDs` and every scan of the whole shelf (`deal list`,
   `deal_list.go:232`) skip a deal whose table row is v1; a v1 listing is a later slice. Because a v1 book's log id (`deal/` plus 32 random hex) does not
match `dealOfBook`'s `deal-<16 hex>` pattern (`deal_books.go:320-323`), the tick paths
(`records`, `coverLiveSteps`, `settleEnded`) already never see it; `dealOfBook` must not be changed
to recognise v1 log ids. A test
   holds one shelf with a legacy deal, a v0 book deal and a v1 book deal and reads the first two
   unchanged.

## 3a. The deal store migration (a one-way door)

**Decided** (evidencebook shelf note §4, acceptance 8, Steven's written confirmation of the #461
target, buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=28a481d9b8c3adc23d21cd73f7452d907815c437cfb0f88feae7dcc2ab8f06e2&thread=8a42e93c728b417ddb1a4a0f3669a2faaa4e0bd0e4016497dd6afdf862a33619;
the migration itself is the library's, so this door only carries out that confirmation): the journal,
stamps and facts exist only on a shelf of book facts. `Shelf.Journal` refuses an unmigrated shelf
("call Migrate", `shelf.go:233`), so no v1 record that goes to the journal (offer, decline,
cancellation, check, intent) can be written on a deal store as it is today, and the plugin never
calls `Migrate` today. `Cadence.Migrate(ctx, now)` is explicit, one-way and idempotent (a migrated
shelf is a no-op). Its order matters (`shelf_stamp.go:111-125`): it first commits the shelf file's
version 2, from which an older pinned build refuses the file, and only then writes the backup
`<db>.v1.bak`; a failed backup (no disk space, a file already at that name) leaves the store in
state `backing_up`, unopenable by older builds, until the same call is run again, which resumes. The
backup is the version 1 file as it was before the move, so restoring it drops every record written
after.

**Proposed:**

1. The plugin migrates in exactly one place: `deal set-version x-deal-v1 --migrate`. On an
   unmigrated store it refuses unless the user also passes `--migrate`, and changes nothing. On a
   store already migrated (the library says so), it records the setting and needs no `--migrate`.
   The one verb keeps the flag, and its one-line help says it also moves the deal store, one-way;
   Sherlock2 chose one verb with `--migrate` (the flag says what it costs).
   Declaring `x-deal-v0` never migrates, and a migrated store keeps working at v0.
2. Before `Migrate` runs, the plugin runs `coverLiveSteps` (`deal_books.go:751`), as `deal tick`
   does: `Migrate`'s genesis facts cut a checkpoint at the migration's time for any book, and a
   v0 step stored without its checkpoint after a crash must get the checkpoint stamped with its own
   time first, since a later cut at the same size cannot replace one (`deal_books.go:705-710`).
   A test crashes a v0 step before its checkpoint, migrates, and checks the step's checkpoint time.
3. Every v1 verb refuses on an unmigrated store with the explanation of item 5, never by
   surfacing the library's error.
4. Deals already open are unchanged: they stay v0 and keep working on the migrated store. The
   library serves `/1` and `/2` History cards from stamps after migration (evidencebook shelf note
   §4; a card's anchor is then a stamp's checkpoint, so the first card after the move can differ in
   timing), the plugin's `tick` stamps instead of building a tree, and the existing suite's History
   card and tick tests run once on a migrated store as the check that nothing a v0 deal does
   changed. This is the one place the setting touches v0 behaviour, and it does so only after the
   user's explicit `--migrate`. The user-facing text says "your open deals keep working" only once
   that test passes.
5. **The wording the user reads** (Sherlock2's texts, buzz ac973473, with two corrections made
   against the library in round 4 and confirmed by Sherlock2 (buzz dd4aed3e): the backup is not "written first"
   (the library commits version 2, then writes the backup, `shelf_stamp.go:111-125`), and the
   History sentence waits for a card that shows a coverage start; the full path is always printed,
   never `<db>`). Without `--migrate`, on an unmigrated store, `deal set-version x-deal-v1` prints
   and exits non-zero without changing anything: "x-deal-v1 deals need this deal store in a new
   layout. The move is one-way: afterwards, older capsulectl builds, including one an app such as
   Muse or Goose bundles, will not open the store, and going back means restoring the backup,
   written to <full path>.v1.bak during the move (if the move stops part-way, the error says why;
   fix that and run the same command again, which picks up where it stopped; the error for a file
   already at the backup name names that path, a library change in evidencebook). Your open deals keep working. Run again with --migrate to move it." A v1 verb on an
   unmigrated store prints: "This deal store is not yet in the layout x-deal-v1 deals need. See
   `deal set-version --help`; moving it is one-way." After the move: "Moved. Backup:
   <full path>.v1.bak."
   **History sentence, deferred.** Sherlock2 asked the warning to say "Your History card will count
   from the move: deals before it still show, but the card no longer claims they are complete" and
   the Moved line to say "Your History card now counts from <coverage start, year and month>."
   That is true of a card with a coverage start, which is `/3`. The plugin serves `/1` and `/2`
   (unchanged by the migration, and out of scope here): their `FirstCheckpointTime` is still the
   first tick (`cadence_standard_history.go:68-89`), so the sentence would be false today. It is
   printed once the plugin serves a `/3` card, whose build and test it depends on, not before.
   Until then this work does not close #461 acceptance 8 (the coverage start shown to users); the
   sentence has its own tracked item tied to the `/3` card, and v0 stays the default until `/3` (Q7).
   The library's two facts stay in help text: restoring the backup discards everything recorded
   after the move, and deleting or moving it ends the way back. "Your open deals keep working" is
   printed only once the migrated-store test of item 4 passes.
6. The demo (`examples/ard-commerce`) does not use the plugin's store (no `capsulectl` or
   `evidencebook-deal` reference in it), so it is not affected.

## 4. The writers

### 4.1 The deal-book writer

**Decided** (design §3.1): a deal-book record is appended through `Book.Append`/`AppendCapsule`
with `record_type` `deal_step`, `epistemic_type` `producer_claim`, the record digest as its one
payload commitment, one `cites` link to the previous record id, and the block's `seq`, `prev`,
`agreement_ref`, `seen`.

**Proposed:** one function, `sealV1(ctx, dealID, rec xdealv1.Record) (sealed, error)` beside
`sealBookStep`, that fills the block's chain members (`seq`, `prev`, `agreement_ref`, `at`, and
`seen` from `Shelf.CountedPrefix(counterparty_key, book_log_id).Last` read at sealing), runs
`xdealv1.Check` (and `CheckSealed` for a journal record) before anything is stored, and appends
through the existing `appendTo`. It reuses `appendTo`'s signing, crash hooks and `inFlight`. It
does not reuse the v0 `fixStep`, `stepRequest` or `checkSealable`.

### 4.2 The journal writer and seal-and-send

**Decided** (design §3.1, evidencebook `Shelf.SealSent`): a journal record that leaves the shelf
whole (offer, decline, cancellation) is sealed twice in one call, as a journal record and as a
standalone Capsule under `action_id` `<deal_id>/sent`. A journal record that does not leave
(intent, check, verdict, approval, admit, the journey and the sale overlays) is appended to the
journal book with `CheckSealed`.

**Proposed:** `sealJournalV1` appends to `shelf.Journal(ctx)`; `sealSentV1` stores the send as an
operation before calling `SealSent`, stores the returned pair (`xdealv1.Pair`) when the call
returns, and writes the file from it. On `SealSent`'s partial-failure result (a Record with an
error) it stores and writes the returned Capsule and does not seal again, as the library says.
Each send is one operation keyed by its fixed record, not by the deal id (an offer and its
cancellation share a deal id, `sent.go:185`): before calling `SealSent` the verb stores the
operation, `deal_sent (record_digest, payload, state, pair, written)`, with the record's JCS
payload fixed once, state `sealing`. A failure after the pair is stored is retried by re-running
the same verb, which finds the operation by its digest and writes the file again, sealing nothing.
The operation is looked up by the verb's own inputs (deal id, kind and the stored payload's
digest), so a re-run needs no digest from the user. A re-run that finds state `sealing` with no pair reads the journal for a record whose payload
commitment is that digest: none (the process died before the call, or `SealSent` returned an empty
Record, which the library says left nothing and may be retried) means it calls `SealSent` again
with the stored payload; one found means the process died inside `SealSent` after the journal
record was stored and before it returned, a window the plugin cannot close, because the library
seals a new Capsule on every call, stamped with the call's time (`shelf_sent.go:75`), so that
record's Capsule cannot be rebuilt. The verb then says the record was sealed but its file was not
made, and the way out is `deal offer cancel` for an offer (a decline or a cancellation is
repeated as a new operation), never a second `SealSent` for the same record.

### 4.3 Sending and receiving

**Proposed:** the plugin has no transport and gains none here. "Sent when sealed" means the
standalone Capsule is written, as a file, at the moment the record is sealed (`--out FILE`, or
stdout), so the host (Goose, Muse) sends it by its own means. `deal receive FILE` reads either of
the two forms a record arrives in (evidencebook design §8.3, `Read`): a sent journal record (the
standalone Capsule; offer, decline, cancellation), checked with `xdealv1.VerifySent` (with an
`OfferContext` when the deal's offer is held); or a deal-book record with its stamp proof (the
agreement and every later record), checked by the v1 `ReceiveProfile`'s `Read`. Both then go
through `Shelf.Receive`, which refuses any call without a `ReceiveProfile`, so the plugin's
`openShelf` (`deal_shelf.go:76-91`) wires one at open once evidencebook PR 4 provides it. The
verb prints, in plain words, which records are newly counted, whether a record is still missing
before a counted one, and whether two different records were received at one place, with no
statement of which is right. It seals nothing and appends nothing to a book.

## 5. The verbs

Every new verb is user-facing, so this section is the one Sherlock2 confirms. Names avoid
today's: `deal decline` already declines a held evidence request (`deal respond`'s held list), so
the v1 decline is a subcommand of `offer`.

| Verb | Writes | Where | Notes |
|---|---|---|---|
| `deal offer --input FILE --out FILE` | `propose` (offer or counter-offer, `--counter-to DIGEST`) | journal, sent | the input holds terms, rows, named records, pack digest, witnesses, adjudicator, `agree_by` |
| `deal offer decline OFFER_DIGEST [--reason not_selected\|other] --out FILE` | `agree` kind `decline` | journal, sent | Sherlock2 2a004e65: the declined buyer's copy says so and verifies |
| `deal offer cancel OFFER_DIGEST --out FILE` | `agree` kind `cancellation` | journal, sent | before the deal starts (design §3.5) |
| `deal agree OFFER --deal ID` | `agree` kind `agreement` | deal book, `seq` 1 | opens the deal book, cites the intent and admission, checks `CheckAgreement` |
| `deal receive FILE` | nothing | the store | section 4.3 |
| `deal perform` | `perform` | deal book | a delivery or any other act; `--check DIGEST` cites the passing check; names are the record names (Sherlock2 a864342c) |
| `deal settle` | `settle` | deal book | a payment leg (`--leg payer\|payee`) or a refund (`--reverses DIGEST`, `--remedy-for ROW`); `--check` on a payer leg. `deal perform` and `deal settle` are new verbs: v0 has neither (it records both through `deal note --kind act`) |
| `deal statement --row ROW --value met\|not_met\|cannot_tell [--remedy ROW]` | `statement` | deal book | `--remedy` for a statement that chooses a remedy |
| `deal dispute --row ROW [--forum F]`, `... --withdraw`, `... --challenge RULING` | `dispute` | deal book | `--forum` only picks among the forums the agreement names, and defaults to the only one; an agreement with `adjudicator: null` names none, so `deal dispute` refuses with a message |
| `deal check`, `deal note` | the journal overlay and journey kinds | journal | unchanged flags; on a v1 deal they seal the v1 records |

The rows' vocabulary and the statement words are rowstatus's, used exactly (Sherlock2 2a004e65,
capsule-viewer#322 answer): the plugin adds no label of its own. The verb names are the record names
(Sherlock2 a864342c), and `settle` also carries a refund. No message says a payment "settles" a
row: **settled** is the computed status of section 6. The help of `deal statement` says `cannot_tell` records no stance on
the row and counts as no statement (evidencebook design §9). The help of `deal decline` (a held
evidence request) and of `deal offer decline` each say which one it is and name the other.
`--reason` on a decline is optional, as the design has it, with `not_selected` and `other` the
only tokens until Sherlock2 words another (Q11); what the decline prints and what the declined buyer's copy
shows for it is Sherlock2's wording, and neither shows the winning terms or amount
(PRD_SELLER.md:306-308 as evidencebook design §3.5 cites it).

**Forums** (Sherlock2 6c12438b, Steven's #404): the offer and the agreement carry the accepted
forums next to `adjudicator` (the agreement takes them from the offer, as it takes witnesses), the
terms pack states them, and a dispute and a ruling must name one of them. The `forums` member and its checks landed in evidencebook (#493, `bc082697`): the offer and the
agreement carry `forums`, `CheckDispute` and `CheckRuling` keep a dispute or ruling inside them, and
a dispute must cite its agreement. `deal offer` takes `--forum F` (repeatable). `deal dispute`
writes into the deal book, so it lands with the deal book writers after `deal agree` (evidencebook
PR 4).

## 6. Settled and local

**Decided** (Sherlock2 2a004e65, design §4.2): where a v0 deal showed "closed", a v1 deal shows
only the computed **settled** ("every row is agreed, uncontested, or ruled with its remedy done")
or nothing; no new done or closed state. On a shelf with no witness a row status shows as
**local** (acceptance 3). Both come from `rowstatus` and `Start`, so this note's code only
passes them through once evidencebook PRs 3 to 5 land.

## 7. `deal close` and `deal terms-result` on a v1 deal

**Decided** (Sherlock2 2a004e65, evidencebook-deal#212 How ack): on a v1 deal they print a
message and exit non-zero without sealing, before the deal loads (section 3, item 4). The text is
Sherlock2's (a864342c): `deal close`: "deal <id> is an x-deal-v1 deal and has no close. Each row is
decided by statements, dates and disputes, and the deal is settled once every row is agreed,
uncontested, or ruled with its remedy done. Record a row with `deal statement`." `deal
terms-result`: the same text with "has no terms result". "See `deal report`" is added only once
the v1 report is served. `deal --help` and each command's help say which deals still
have them (v0 deals). A v0 deal keeps both verbs unchanged.

## 8. Old deals, the demo and the pin

- v0 deals, their close records and results logs, `standard_history/1` and `/2`, the temporary
  `outcome_note` verb and `examples/ard-commerce` are not changed. With the setting at its
  default, no code path of this note runs.
- The evidencebook pin moves only to a released version that contains `x-deal-v1`. Until then
  this work builds on a pseudo-version in a branch that is not merged. It also needs capsule-viewer
  to bundle cll 0.4.0 (capsule-viewer#325), because evidencebook main pulls cll v0.4.0 and the
  plugin's page tests fail without it.

## 9. Tests

- Round trips: each v1 record written by the verbs decodes, passes `Check`, and verifies under
  `capsulectl verify`.
- An offer, an agreement and a statement between two profiles (two shelves in one test), the
  offer carried as a file and received with `Receive`.
- A decline written, received and verified by the buyer's profile.
- Version isolation: with the default setting, the existing suite is unchanged; a v1 deal refuses
  `deal close`; a v0 deal refuses `deal statement`; changing the setting does not change an open
  deal.
- A crash after the pair is stored: re-running the verb writes the same file and seals nothing. A
  crash before `SealSent` or an empty-Record error: the re-run seals the stored payload once; a
  crash inside `SealSent` after the journal write: the re-run refuses with the `sealing` message
  and seals nothing. An offer, then its cancellation, then a retried cancellation each seal once.
- A mixed shelf (legacy deal, v0 book deal, v1 book deal): `deal list` and every v0 verb on the
  first two are unchanged; `deal close` on the v1 deal prints the section 7 message, not a store
  error.
- A v1 deal's two log ids differ and neither contains its deal id.
- `deal receive` of a sent offer (before PR 4: refused with a clear message), of an agreement and
  of a statement (after PR 4).

## 10. Open questions

1. **Verb names, the setting name and the input files** (sections 3 and 5) are proposals;
   Sherlock2 confirms the names, and the offer input's field names are evidencebook's (§3.3).
2. **Where `deal agree` gets the admission and intent digests** it must cite (design §3.3,
   `admit`, `intent`): from the journal's own records. The plugin writes `admit` today only as v0
   `verdict`/`approval` steps; whether the existing admission flow produces a v1 `admit` is
   section 12 territory of evidencebook's note and is not settled here.
3. **The journal overlay for the sale flow** (`sale`, `thread`, `thread_opened`, `sale_unpost`,
   `counterparty_profile`, `disclosure`): sealed in the journal on a v1 deal (design §3.2). This
   note does not move the sale verbs; a following slice does.
4. **One verb or two for the migration** (section 3a): settled. The twin preferred a separate
   store-migration verb; Sherlock2 chose `deal set-version x-deal-v1 --migrate` (one verb, the
   flag says what it costs). No Steven door (Sherlock2, ac973473).

## 11. Slice 2 as built: `deal receive`, `deal agree`, the deal-book writer, the first-act gate

**Decided** by the coordinator (2026-11-18): build `deal receive` (with the `ReceiveProfile`
wiring), `deal agree` and the deal-book writer, and the first-act gate on evidencebook's `GateAct`
(u610 and u611, evidencebook#518); leave `deal dispute` to deal-95 on this writer; no release.

- **The wiring.** A verb that takes records from the other side asks, through its context
  (`v1ReceiveContext`), for the shelf to be opened with the store of received records and
  `xdealv1.ReceiveProfile` (its `Shelf` set after `OpenSQLiteShelf`). No other verb opens it, so
  nothing changes for a v0 profile. The plugin's shelf already gives every book an empty list of
  authenticated indexes (`deal_shelf.go`), which the other side's checkpoint recomputation needs
  (eb-461).
- **The writer, `sealBookV1`.** One function seals every v1 deal-book record. The caller gives the
  body (and refs, marks); the writer sets `deal_id`, `at`, `seq`, `prev` (the record digest of the
  record before), `agreement_ref` and `seen` (`CountedPrefix(counterparty_key, counterparty_log_id).Last`,
  null when none), runs `Check` and the placement check before anything is stored, appends one
  record with one `cites` link to the one before, and returns the `BookPair`. The book's log id is
  random and stored before the book exists (`deal_versions.own_log_id`), so a run that stops after
  the append finds the book and records the agreement (`repairOpening`), never makes a second one.
  The book is read back for the gate from the book itself (`Query`, `ResolvePayload`). This is the
  API deal-95's dispute record uses: `bookRecordIn{Body: DisputeBody{...}, Refs: ...}`.
- **`deal receive FILE`.** FILE is one of: a sent record (an offer, a decline, a cancellation: the
  standalone pair `deal offer` wrote), a deal-book record (a pair), or a push `{"record": pair,
  "stamp_proof": proof}` (what the acceptor's `deal agree` writes); the proof may come alone. A
  sent record is verified with `VerifySent` (a decline or a cancellation against the offer it
  names when the holder has it), a book record with `VerifyBook`; then the store takes it
  (`Shelf.Receive`) and the verb says in words what became of the record and of the proof. It keeps
  the received offer's payload so the offer can be answered, the deal's row (side, role, the other
  side's key), the other side's book log id (learned from its first record) and its agreement's
  record id. An offer for a deal id the profile already holds is refused.
- **`deal agree OFFER_DIGEST --admit DIGEST [--intent DIGEST] [--out FILE]`.** Builds the agreement
  from the offer it answers (rows digest, witnesses, adjudicator, forums as they are), the holder's
  side and role, its checkpoint key and shelf log (the cadence log), checks it with
  `CheckAgreement`, and seals it as seq 1. `--admit` and `--intent` name records in the holder's own
  journal and are refused unless the journal holds them. The offerer agrees only after it holds the
  acceptor's agreement and names it in `seen`. The acceptor stamps right after and its file carries
  the stamp proof (design §7.2); the offerer's file carries none (it stamps with its first act).
  Running it again writes the same record again and seals nothing. How a journal `admit` record
  is sealed is open (§10, question 2): this slice does not add a verb for it.
- **The gate, `gateFirstAct`.** Wraps `ReceiveProfile.GateAct`: a first perform or payer settle is
  refused until the counterparty's stamp proof covers its agreement, on both sides (u610, "gate a");
  a counterparty with no layer is not refused and the answer says so (`Unguarded`), for the
  receipt and the card to say that no stamp proof came, counted from the holder's own receipt only
  (u611, "layer ii"); a payee leg, a statement and later acts are never gated. The counterparty's
  evidence commitment shelf log is passed whenever the holder holds it. The verbs that seal acts
  (`deal perform`, `deal settle`) are later slices; they call `gateFirstAct` before `sealBookV1`.
- **What this slice does not do:** `deal offer decline`, `deal perform`, `deal settle`,
  `deal statement`, `deal dispute`, a verb for the journal's `admit` and `intent`, the offerer's
  stamp at `agree_by` minus the margin (§7.1), and the no-layer agreement (`counterparty_layer:
  false`; `deal agree` writes `true`).
- **Release condition** (Sherlock2, evidencebook-deal#221 wording check, buzz b61e90dd): `deal agree`
  must not reach a release until a user can produce a v1 `admit` (its own verb or the admission
  flow, question 2 of section 10), and its `--admit is required` refusal must then name the verb or
  command that shows the digest. Nothing ships from this slice (no release).
