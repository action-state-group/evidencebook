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
writes into the deal book, so it lands with the deal book writers after `deal agree`: the dispute and
the withdrawal are built (evidencebook-deal#95); `--challenge RULING` waits for the ruling the adjudicator
sends back.

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
  body (and marks); the writer sets `deal_id`, `at`, `seq`, `prev` (the record digest of the
  record before), `agreement_ref` and `seen` (`CountedPrefix(counterparty_key, counterparty_log_id).Last`,
  null when none), runs `Check` and the placement check before anything is stored, appends one
  record with one `cites` link to the one before, and returns the `BookPair`. The book's log id is
  random and stored before the book exists (`deal_versions.own_log_id`), so a run that stops after
  the append finds the book and records the agreement (`repairOpening`), never makes a second one.
  The book is read back for the gate from the book itself (`Query`, `ResolvePayload`). This is the
  API deal-95's dispute record uses: `bookRecordIn{Body: DisputeBody{...}}` (a deal-book record carries no block refs since evidencebook v0.17.0: it cites by its body's own fields).
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

## 12. Slice 3, proposed: `deal check` and `deal note` on a v1 deal (evidencebook#626, B1b)

**Status: proposed, for review before code.** x-deal-v1 becomes the default once a v1 deal can
go from agreement to report through plugin commands (evidencebook#626). `deal admit` (#688) seals
the admission decision that the agreement cites. This slice gives a v1 deal the checks an act
needs. Section 5 says only "`deal check`, `deal note`: unchanged flags; on a v1 deal they seal the
v1 records". This section says how.

**The problem.** On main, both verbs refuse a v1 deal before anything loads (`deal.go:973-980`,
`v1Refusal`). Their v0 path reads two things:
- the folded `dealState` (`foldDeal`, `deal_rules.go:1085`);
- the raw event list itself (`checkRepresenting`, `checkAnswering`, `dealRole`,
  `checkCounterpartyClass`, `promisedPay`, `dealBoundsOpening`, `rulesInput`, `checkCard`,
  `openDeadlines`, `dealRecordSet`; `deal.go:2215-2510`).

Both start from the opening, which holds the plain values the records hold only as commitments:
the user's verbatim words, the bounds, the type, the channel and the pinned materiality predicate.
A v1 deal has no opening. Its journal holds commitments (`verbatim_commitment`,
`bounds_commitment`, `dealprofile/types.go:79-92`), not the values the checks read. So the state
cannot be rebuilt from v1 records (review round 1, both peers).

**Approach: keep the v0 events locally, seal v1 records.** Each v1 deal keeps a local, plain event
list in the plugin's store (`deal_v1_events`), in the same `dealEvent` form v0 folds. Each v1 verb
of this slice writes two things in one step: its plain event to that list, and its v1 record to the
journal, with the record's digest kept on the event. `deal check` then runs the unchanged v0 body
(fold, `evaluateDeal`, the rules checker, the card) over that list. So the card, the rules, the
materiality predicate and every check that reads the raw events are reused unchanged, and nothing is
rebuilt from commitments. A crash between the two writes is repaired as `deal admit` repairs one:
the plain event is stored first with its record's payload, and the next run seals that payload when
the journal lacks it.

   **The local list never leaves this store.** It holds the person's verbatim words and bounds in
   plain text, which v1's commitments exist to keep out of shared records. No export, bundle,
   share or disclosure carries it; only the journal's records, with their commitments, go out.

1. **The opening.** The first `deal note --kind intent` on a v1 deal writes the synthetic `open`
   event. `--input` is v0's intent note input, and `--opening FILE` gives the rest of v0's opening
   (`type`, an optional `channel`, `who`, `recourse`; as built, since the note command reads
   `--input` as an intent before the deal loads):
   - `type` and `channel` come from `--opening`;
   - `intent` (verbatim words, bounds, allowed) comes from the input, sealed as the journal `intent`
     record with its `verbatim_commitment` and `bounds_commitment` opened by the local nonces, as
     v0 keeps them (`deal_profile.go:490-508`);
   - `who` comes from `--opening`, in plain values, as v0's opening takes it. The offer's counterparty
     block and the `counterparty_profile` record hold only fingerprints (`deal_profile.go:268-279`),
     and `deal offer` writes no counterparty block (`deal_v1_offer.go:84-87`), so `who` cannot be
     recovered from records. Left out, it is empty, and a later intent that names the merchant opens
     the merchant leg, as in v0;
   - `terms` comes from the offer's `terms`, mapped field by field;
   - the recourse (`Rail`, `Refundable`) comes from `--opening`, as v0's opening takes it. An offer's
     terms (`dealprofile.Terms`) state no rail and no refundability, so nothing agreed is overridden; The agreement's adjudicator
     and forums are the dispute clause, which is not this recourse;
   - `Records` is the x-deal-v0 path (`""`), so `dealRecordSet` gives the untyped behaviour;
   - the pinned materiality predicate is read from the profile then and stored on the event, as
     v0's `deal open` seals it.

   - **Party role** comes from the deal's own v1 side: the offer's `role` for the offerer, and the
     opposite for the acceptor (`deal_v1_agree.go:190-215`). An input `party_role` that disagrees is
     refused. One left out takes the deal's role, never v0's buyer default (`deal_rules.go:128-131`).

   A `deal check` on a v1 deal with no intent yet is refused with "seal the user's intent first:
   `deal note --deal ID --kind intent`". The library refuses a `standing_intent` approval with no
   intent before it (`authorization.go:310`), and v0 cannot check without an opening.
2. **Later intents narrow.** A later intent on the same deal is written as v0's `intent` event,
   with its journey marker kept. So `foldDeal` takes it through the same path v0 does:
   - `foldIntentJourney` for a journey intent, which picks an option, opens the merchant leg and
     withdraws an item delegation (`deal_journey.go:337-367`);
   - `laterIntent` otherwise, which only narrows, as the library's `limits.narrow` does
     (`deal_rules.go:1239`, `authorization.go:423`).

   Confirmed proposals then apply as v0 folds them.
3. **Binding.** PROFILE §3 keeps `deal_id` off `intent`, `confirm_limits` approvals, `disclosure`,
   `counterparty_profile` and journey records sealed before the offer. Every such record sealed by
   this slice's verbs is bound to its deal at seal time, by the `--deal` of an existing v1 deal, in
   `deal_v1_events`. A v1 deal exists from `deal offer` or `deal receive` on, so there is no pre-offer
   case for this slice.
4. **`v1DealJournal(ctx, dealID)`** is the one function that gives a deal's journal records: those
   carrying its `deal_id`, plus those bound to it. `deal check` folds the matching local events, and
   B2 passes exactly these records to `xdealv1.CheckAuthorizations`. So the card and the
   authorization read the same subset, and another deal's intent or detail change in the same
   journal never reaches either. B2 passes the deal's earlier acts with the candidate, so the
   library's single-use check (`authorization.go:70-72`, `118-121`) sees them. `deal check` refuses
   when `v1DealJournal` holds a record of the deal, of a kind this slice's verbs write, with no
   local event. Those kinds are the journal overlay (`intent`, `check`, `verdict`, `approval`) and
   the journey overlay. A record sealed outside these verbs would reach the authorization but not
   the card. The deal's other records are not checked for a local event: `propose`, `agree` of
   every kind, `admit` and the records sent whole, which `deal offer`, `deal receive`, `deal agree`
   and `deal admit` keep in their own tables (`deal_sent`, `deal_v1`, `deal_v1_admits`).
   - **Cross-deal memory reads both.** `counterpartyMemory` reads other deals through the index's
     `deal_steps` query (`deal_counterparty.go:156-205`, `deal_index.go:130`), not through the event
     list. The shared query reads `deal_v1_events` too, in one order with duplicates removed. So a
     v1 deal's earlier disclosures count as v0's do, within one deal, across deals and across
     versions.
5. **`deal check` on a v1 deal** writes v0's `snapshot` and `check` events locally, and seals the
   journal `check` record: v0's body plus `payment_ref` on a pay and a fresh `nonce`, with
   `deal_id`. It then seals the `verdict` (one `checks` ref, a `nonce`). Writing the snapshot first
   is what makes an approval adopt the terms its card listed (`deal_rules.go:1138-1145`,
   `1189-1200`).
   - On a pass whose action the intent allows, it seals a `standing_intent` approval (with a
     `nonce`), as v0 does on its untyped path (`deal.go:2470`). The library refuses an act whose
     verdict has no approval (`authorization.go:104`).
   - `payment_ref` is the reference fixed before paying: a Lightning payment hash, an ACP order
     id, or the x402 authorization nonce. A pay check without it is refused, because the payer
     leg's check is "the passing journal check that recorded the payment reference" (design note
     461, settle's `check`). A reference the rail learns only afterwards, such as an on-chain
     transaction hash, goes on the payer leg (Sherlock2, on #699).
6. **The rules checker gets a v0-shaped record.** `rulesInput` loads the sealed v0 Capsule of the
   checked step from artifacts (`capsuleWithInput`, `rules_checker.go:548`, `867-882`). A v1 deal
   seals no v0 Capsule. So for a v1 deal, `rulesInput` builds the record the checker reads from the
   local `snapshot` event, in the v0 record's shape, and never hands it the x-deal-v1 journal
   record. The pinned rules read the v0 shape and are not forked. The test compares the members
   the rules read, with Capsule ids, digests and times normalised, since those differ between any
   two deals.
   `rulesHistory` (`rules_checker.go:897-911`) counts v0 `act` steps. B2 adds a v1 deal's `perform`
   and `settle` records to it, and that is a gate on making v1 the default (B5): spend and velocity
   rules must count v1 acts before v1 deals act by default.
7. **`deal note` on a v1 deal.**
   - These kinds write their plain event and seal their journal record, with `deal_id` placed by
     `SealContext` and the binding of item 3:
     - `intent`;
     - `approval`, whose `approves` ref names the verdict, or the intent on `confirm_limits`, with a
       `nonce`;
     - `message`, `claim`, `evidence`;
     - `change`, sealed as `detail_change`;
     - `disclosure`;
     - the agent moves `searched`, `options`, `recommended`, `chose` and `couldnt`, sealed as
       `agent_move`.
   - A `user` or `agent_card` approval that answers a verdict needs `--shown-card`, checked against
     the verdict's card. A `confirm_limits` approval answers an intent, has no card, and keeps v0's
     `--said` and limits checks (`deal.go:1542-1552`).
     v0 lets it be omitted (`deal.go:1550-1559`), but the library refuses such an approval without
     its `card_commitment` (`walkJournal`, `authorization.go:211`, `346-355`), and it scans the whole journal. So
     one approval sealed without it would break every later authorization of the deal.
   - These kinds have no v1 note form and refuse, naming the v1 verb: `act`, `payment_received`
     and `delivered` (`deal perform`, `deal settle`, B2); `dispute` (`deal dispute`); `ruling` (the
     adjudicator's case book); `acceptance` and `platform_approval` (the sale overlay, B3).
   - Sale-thread and `offer` actions on a v1 deal are refused until B3.

**Settled by Sherlock2 on #699, and what is left.** There is no door for Steven here.
- **The person's signature** (x-deal-v1 §8). It is ruled, and the ruling is in PROFILE §8. An act
  must not be relied on until the person's signatures are held, and each travels beside its
  record:
  - the approval, signed with the person's own key, a device passkey (Steven, 2026-11-17, on
    evidencebook#670, where evidencebook-deal#98 now lives);
  - the first intent a `standing_intent` approval rests on (Steven, evidencebook#500 option b).

  What is left is how the plugin carries the signature, gated on AAC defining the member
  (agent-action-capsule#627). Until then, B2 seals an act whose authorization the library accepts,
  reports the digests that still need the person's signature (`CheckAuthorizations`'s per-act
  digests), and never counts the act as authorized. A receipt or report for such an act says the
  person's own key's signature is missing.
- **`payment_ref`** keeps the record's name (PROFILE §5, check), with the form in item 5.

**Tests.**
- **One input, two versions.** The same input on a v0 deal and on an equivalent v1 deal gives the
  same card, the same verdict and equal rules-checker input on the members the rules read, with
  ids, digests and times normalised (item 6). Cases: pass, pause and deny; a
  confirmed-limits approval; a detail change; change, check, approve and check again (the second
  card lists no change); a later, wider intent that narrows rather than widens; a delegated
  `chose`; an empty `who`, then an intent that names the merchant; a seller deal and a buyer deal;
  a disclosure repeated within a deal, across deals and across versions.
- **Records.** Each sealed v1 record passes `xdealv1.CheckSealed` with its context, has a fresh
  nonce, and carries `deal_id` exactly where PROFILE §3 says. Each bound record is bound to its
  deal.
- **Crash and restart.** A run that stops between the local event and the seal is finished by the
  next run with the same digest. After a restart the check reads the same plain values.
- **Refusals.**
  - A check before any intent.
  - A pay check with no `payment_ref`.
  - A `user` approval answering a verdict with no `--shown-card` (a `confirm_limits` approval is
    sealed without one).
  - An input `party_role` that disagrees with the deal's side.
  - A check while `v1DealJournal` holds a journal-overlay or journey record of the deal with no
    local event, such as a `detail_change` sealed outside these verbs. The normal offerer flow
    (offer, receive, agree, intent, check) passes.
  - Each note kind with no v1 form, naming its verb.
  - Sale-thread actions.
- **Authorization, in this slice.** `CheckAuthorizations` over `v1DealJournal` accepts a constructed
  act after a passing check with its automatic approval, and after a pause with a user approval.
  It still accepts when another deal's intent or detail change sits in the same journal, since
  `v1DealJournal` leaves it out. It refuses an approval reused across two commands, and a
  wrong-deal check.
- **The local list stays local.** A v1 deal's export, bundle and shared copy carry no content of
  `deal_v1_events` (the verbatim words, bounds and plain counterparty values), so no later share
  path can leak it (Sherlock2, on #699).
- **Old deals.** The v0 suite is unchanged, with v1 and v0 deals in one store.

## 13. Slice 4, proposed: `deal perform`, `deal settle` and `deal statement` on a v1 deal (evidencebook#626, B2)

**Status: proposed, for review before code.** The plan and its review are on #626
(issuecomment-7079075997, answers in issuecomment-7079130863). Sherlock2 settled the x402
reference: the leg is joined to its check by digest (buzz dfc1040d). Sherlock2 settled the payer
leg's timing after review round 1 (item 5). The library holds a settled amount to
the checked one (`xdealv1.CheckActs`, evidencebook#711). This slice builds on §11's writer and gate
and on §12's local event list.

1. **One path for every act.** `deal perform` and `deal settle` build the record body and the
   settlement member. Then, inside `sealBookV1`'s `Validate`, which runs on the finished record and
   again after an `ErrBusy` rebuild, they run:
   - the first-act gate (`gateFirstAct`), on every first act whatever the flags, refusing unless
     `Allowed`, and printing `Unguarded` when the counterparty has no layer (u611). Its
     `commitmentShelfLog` is the shelf log of the counterparty's published evidence commitment (its
     card), when the holder holds one. `CanAct` refuses when that differs from the log the
     agreement names (`receive.go:747`). Empty when none is held, which skips that comparison;
   - `xdealv1.CheckActs` over `v1DealJournal` and the deal's earlier acts with their members, so
     the act's final `at` is the one checked, and a used approval and a changed amount are refused;
   - `settlement.StructureFailures` on the member, refusing any failure.

   The deal's local list is loaded through `v1Events` first, so an approval a crash left pending is
   sealed before the library reads the journal.
2. **The check follows the act.**
   - A `perform` rests on a `commit` check, a payment's payer leg on a `pay` check, and a refund's
     payer leg on a `cancel` check. Only `pay` carries `payment_ref`.
   - A payee leg cites no check, so `CheckActs` does not reach it (it skips an act with no check),
     and the writer holds it to its own rules (item 3).
   - A refund or a payment remedy has no terms leg of its own (PROFILE §7,
     `ReasonRefundCitesOffer`). It is sealed and shown, and the help says the payment row's check is
     not run for it, rather than calling it verified.
3. **The settlement member, from the deal, not from flags.**
   - **The row.** `--row` names the payment row. It may be left out when the offer has one payment
     row.
   - **The leg.**
     - Without `--reverses`, the leg is payer when the holder's party owes the row (the deal's
       `deal_v1.role` and the row's `owed_by`), and payee otherwise.
     - With `--reverses`, it is the mirror of the leg it reverses, which must be a settle record in
       the holder's **own** book, of the other leg kind, that no other refund reverses. A refund is
       one leg plus the legs that supersede it. A later observation of the same refund (a payee
       refund received, `pending` then `settled`) is a payee leg with `--supersedes` naming the
       earlier one, carrying the same `reverses`. A second, independent refund of the same leg is
       refused. The seller
       refunding a payment it received reverses its own payee leg and is the payer
       (`fixtures/appendix-a/seller-book/07-refund.json`). The buyer's refund received reverses its
       own payer leg and is the payee (`fixtures/appendix-a/buyer-book/08-refund-received.json`).
       A counterparty's leg is refused.
     - A `--leg` that disagrees with this is refused.
   - **`terms_ref`** is always the offer's sent Capsule id (`deal_v1_offers.pair` for the acceptor,
     `deal_sent` for the offerer).
   - **A payer leg** carries `amount`, from the checked `amount_minor` at
     `dealprofile.AssetScale(currency)`, and an optional `--routing-fee`. An unknown currency is
     refused.
   - **A payee leg** carries `--received` and `--receive-fee`. The writer requires the fee, `0` when
     none was taken, because AAC reads a missing fee as `fee_unstated` and leaves the payment
     `unjoined` (settlement.go:1029-1032). `CheckActs` never sees a payee leg here, so the writer
     enforces the fee.
   - **`payment_ref`** is flat: AAC's qualifiers sit at the top level, and each type's qualifiers
     are required, exactly. The type comes from `--payment-ref-type`, one of
     `settlement.PaymentRefTypes`, and its qualifiers from flags:
     - `--payment-ref-network` for `x402.transaction`;
     - `--payment-ref-method` for `mpp.reference`;
     - `--payment-ref-debtor-agent` for `iso20022.end_to_end_id`.
4. **The reference per rail (Sherlock2, option 1).**
   - **A payment's payer leg.** On `ln.payment_hash`, `bolt12.invoice_payment_hash`, `acp.order_id`
     and `ucp.order_id`, its value defaults to its `pay` check's `payment_ref` and must equal it.
     On `x402.transaction`, and every other type, `--payment-ref` is required: the leg joins its
     check by digest, and the check keeps the authorization nonce.
   - **The x402 help** says: "on x402, the link from the transaction to the checked nonce is the
     payer's claim unless a verifier reads the transaction's `AuthorizationUsed` nonce on chain".
   - **Refunds and payee legs.** A refund's legs and every payee leg take `--payment-ref` (the
     refund's own reference, or the payment as received). AAC joins a payer leg with the payee leg
     carrying the same reference. A payee leg's reference defaults to the counterparty's payer
     leg's, when the holder holds it.
   - Wrapping the `x402.payment-payload` so the nonce is checkable offline is noted, not built (the
     twin's suggestion).
5. **The payer leg: sealed once, when the rail answers (Sherlock2, buzz 037352f4 and 44899e76).**
   - **Why not at the send.** Review round 1 found two facts:
     - AAC joins one payer head with one payee head and calls a status difference a `mismatch`
       (settlement.go:1061, 1089), which `PaymentCheck` fails (payment.go:424). So a payer leg
       sealed `pending` makes a payment that went through read as failed.
     - On x402 the transaction exists only in the facilitator's settle response.
   - **The flow of a payer leg: two calls of `deal settle`.** capsulectl does not drive the rail; the
     agent does, and tells the plugin at the two moments. The flow applies to every payer leg, a
     payment's on a `pay` check and a refund's on a `cancel` check.
     1. **`deal settle --sent --check DIGEST`**, before the handoff. The spend lock (Sherlock2's
        condition 1) is the session's shelf writer lock. A write session holds it from open to close
        (`OpenSQLiteShelf` with `LockPath`, `deal_shelf.go:86`, `deal.go:176-178`), so two `--sent`
        calls, or a `--sent` and a `deal check`, run one after the other for the whole command, and
        it is never held across a person's approval. In order:
        - reconcile (item 8);
        - refuse when the check already has an open send or a payer leg;
        - run the gate and `CheckActs` on the candidate payer leg (status `settled`, `at` now), so a
          send the gate or the authorization would refuse never reaches the rail;
        - on a `pay` check, re-run the deal's spend rules over the check's snapshot with the current
          history (acts plus every open send, across v1 deals), so a second payment that a first
          one, still in flight, puts over the cap is refused;
        - store the local `sent` event with its record's payload, then append the journal `evidence`
          record. Each is its own write, as `storeV1` and `appendV1Journal` do, and `v1Events`
          recovers a crash between the two (§12). No SQLite transaction spans the append, because
          the shelf is a separate connection on the same file.

        The evidence record carries no `Who`, no counterparty identifiers and no
        `counterparty_facts`, so it is not a change of details between the check and the act
        (`changesDetails`). Its `detail_commitment` commits to the check digest, the amount, the
        currency and the reference known then (the payment hash, the nonce). The local `sent` event
        keeps them plain, as §12 keeps an intent's words.
     2. **`deal settle --check DIGEST --status settled|failed`**, when the rail answers (the Lightning
        payment result, x402's settle response, the ACP/UCP order response).
        - It requires the check's stored `sent` event and refuses without one, so no payer leg skips
          the send and its spend check.
        - The leg must match what the send bound: the amount, the currency, the reference type, and
          the reference value where the send knew it (an x402 transaction is added here).
        - It seals the one payer leg, re-running the gate and `CheckActs` at the leg's final `at`.
        - `--status pending` is refused. While the rail says pending, no payer leg is sealed, the act
          is held, and `deal status` names the payment in flight.
        - Only a re-run whose inputs match an already sealed leg rewrites its file (item 8).
   - **No detail change while a payment is in flight.** `CheckActs` refuses an act after a journal
     record that changes the details sealed after its check (`authorization.go:122-125`). So while a
     deal has an open send, every verb that seals a journal record for the deal runs the library's
     own test on the built record, and refuses when it matches, naming the payment in flight, until
     its payer leg is sealed. The library's test is `changesDetails` (`authorization.go:383-403`),
     exported as `xdealv1.ChangesDetails(Record)` with the walk calling it, so there is no second
     list. It matches:
     - a `detail_change`;
     - a `message` with a counterparty;
     - an `evidence` with a counterparty or `counterparty_facts`;
     - an `intent` with a counterparty or an item pick;
     - an `agent_move` that is `chose`.

     A payment already sent can then always be sealed.
   - **Open sends.** A send is open while its check is cited by no payer leg in the holder's own
     book. That is read from the local `sent` events and the own book, after reconciliation, across
     all v1 deals (`v1MemorySteps`). Every pay check and every `--sent` counts each open send's
     amount against the cap and names it to the user. That includes a send a crash left open, so
     money gone with no act is still counted. Once its payer leg is sealed, the act counts and the
     send does not, so the money is never counted twice.
   - **The send is irreversible; the answer is when the payer can seal.** Between them, the open
     send keeps the cap honest.
   - **No supersession.** A payer leg never `supersedes`: a second leg on the same check is a second
     act, which the library refuses. evidencebook#709 is the profile change that would let a payer
     seal `pending` and update it.
   - **No report change.** A late payee's `pending` against a `settled` payer leg is today's
     `PaymentCheck` behavior (a mismatch, read as a fail). It is not this slice's; it is
     evidencebook#713, for Steven.
6. **The record fields the rows read.**
   - `--act` and `--observed` are required on `perform`, since the schema requires both
     (`body_perform`).
   - `--statement row=value` is repeatable, checked against the offer's rows, and written as an
     empty array when none is given.
   - `--mark NAME` is repeatable, checked against the offer's `named`, so a row whose date hangs on
     a named record starts.
   - `--remedy-row ROW` makes the record a remedy for that row (`remedy_for.row`, required in a
     remedy), with an optional `--remedy-ruling DIGEST` that must be a ruling on that row. This
     applies to both `perform` and `settle`.
   - `deal statement` seals a statement record with the same `--statement` and an optional
     `--names` (a cancellation or a ruling, exactly one). It is never gated.
7. **The file to send, and the offerer's stamp.**
   - `perform`, `settle` and `statement` take `--out` and write a push through `writePush`, as
     `deal dispute` does.
   - PROFILE §8: "the offerer stamps its agreement at once when its first act waits on the
     acceptor's". So `deal agree` stamps on the offerer side too, as it does on the acceptor side,
     reusing an existing proof on a re-run (`deal_v1_agree.go:305-311`), and its file carries the
     proof.
   - There is no flag. A buyer acceptor who pays first then never waits on an offerer that forgot
     one.
8. **Crash and re-run.** The deal book is the source of truth for acts.
   - After the append, the verb writes a local event (item 9).
   - Every load (`v1Events`, and `rulesHistory` before it reads) reconciles: an own-book `perform`
     or `settle` with no local event gets one, built from the record and its member, by digest,
     once.
   - A re-run finds the record its inputs already sealed and rewrites its file, sealing nothing, as
     `deal dispute` does. A record matches only on the whole of its inputs: the body the verb would
     build (the row, `names`, marks, statements, check, `reverses` and `remedy_for`), plus the member
     without its observed time. A changed input is a new record.
   - An `--out` that already exists is refused for a new record.
9. **What each act writes locally.** The local event is v0's, so the next card's spend so far and
   the used approval read it unchanged. Every act that cites a check writes `AuthorizedBy`, the
   local event id of the check's approval (`ActAuthorization.Approval`), so that approval is never
   offered again.
   - **A payment's payer leg** on a `pay` check writes `act` with `Action: pay` and
     `Direction: out`. A `settled` leg also carries `AmountMinor` and `Currency`. A `failed` leg
     carries no amount, so it counts no spend while its approval stays used, as `CheckActs` counts
     it.
   - **The seller's refund** (a payer leg on a `cancel` check) writes `act` with `Action: cancel`,
     its amount, and no `Direction` or `Reverses`. That is what v0 seals for a cancel with no
     matching `pay` before it (`actDirection`, `deal_rules.go:624-660`), and it counts toward no
     spend cap (`actOut`).
   - **The buyer's refund received** (a payee leg with `--reverses`) returns money only once it is
     `settled`, and only once per reversed payment. When it is settled, it writes `act` with
     `Action: cancel`, `Direction: in`, its amount, and `Reverses` set to the local event id of the
     buyer's own `pay` act whose leg it reverses, unless an earlier refund already returned that
     pay. The fold then returns the money (`deal_rules.go:629-660`). A `pending` or `failed` refund
     received, and a later observation of a refund already counted, write v0's
     `payment_received` with no money returned. It cites no check, so it writes no `AuthorizedBy`.
   - **Any other payee leg** writes v0's `payment_received`.
   - **A `perform`** writes `act` with `Action: commit` and no amount.

   **`rulesHistory` gets a v1 path. v0's input is unchanged.**
   - For v1 deals it reads these events and builds each act's record from its deal's own local
     snapshot, as `v1CapsuleWithInput` does, never from the current deal's. The counterparty and
     item lookups get v1 versions.
   - It takes every v1 act that cites a check. One with no amount (a perform, a failed leg) counts
     toward velocity rules with no spend. Only amount-bearing acts count toward the
     `rulesHistoryMax` window. The buyer's refund received cites no check and is not a rules act; it
     reaches only the money fold.
   - v0 deals keep today's filter (acts with an amount), so the v0 suite is unchanged.
   - With this, the B5 gate holds: spend and velocity rules count v1 acts.
10. **The person's signature.** Each act's output lists the approval digest only where the library
    says a person signs (`NeedsPersonSignature`), plus `LimitApprovals` and `Intent` for a standing
    intent. A receipt or report says the person's own key's signature is missing until AAC#627
    defines the member (§12).

**Built in two pull requests:** `perform` and `statement` first, with items 1, 2, 6, 7, 8 and 9 as
they apply to a perform. Then `settle`, with items 3, 4 and 5 and the rest of 8 and 9.

**Tests.**
- **Acts.**
  - A perform on a commit check and a payer leg on a pay check pass `CheckSealed` and `CheckActs`.
  - A payer leg and a payee leg with `received` and `receive_fee` join in AAC's `PaymentCheck`.
  - A payee leg with no fee is refused by the writer.
- **x402.**
  - A payer leg with an `x402.transaction` reference, its network and a nonce check seals.
  - One with no network is refused.
  - A Lightning leg whose value differs from its check's is refused.
- **Pay flow.**
  - Each rail's answer point seals the leg as `settled` or `failed`. `--status pending` is refused.
  - A crash between the handoff and the answer leaves an open send that the next pay check counts.
    A crash after the leg is sealed and before its local event is written counts the act once and
    the send not at all.
  - A `--sent` the gate or the authorization refuses seals nothing.
  - A second `--sent` on the same check is refused.
  - `--status settled` with no stored send, or with an amount or reference that differs from the
    send's, is refused.
  - While a send is open, each of the five kinds `ChangesDetails` matches is refused (a `change`, a
    `chose`, an intent picking the item, a message and an evidence naming the counterparty), and the
    payment is then sealed.
  - A refund's payer leg goes through the same two calls.
  - The payer leg passes `CheckActs` with the sent evidence between its check and the act.
  - Payment A is pending, and payment B's check sees A's amount and is refused when A plus B is
    over the cap.
  - Two processes running `--sent` at once: the second waits on the writer lock, then sees the
    first's open send.
- **Refunds.** Each side is built from the two fixtures:
  - the seller's refund on a cancel check seals;
  - the buyer's refund received credits the buyer's `pay` once, only when `settled`. A pending
    one, then a settled one, credits once; a failed one credits nothing;
  - a `--reverses` naming the counterparty's leg is refused, and so is a second, independent refund
    of a leg already refunded; a superseding observation of the same refund is sealed;
  - the seller's cancel approval is not offered again;
  - a refund on a pay check is refused, and so is a payment on a cancel check;
  - the payment row's check is not shown as verified.
- **Remedies.** A remedy with a row alone, and one with its ruling, on `perform` and on `settle`. A
  ruling on another row is refused.
- **Gate.**
  - A first act with no check is still gated.
  - Both sides' first acts.
  - No witness, a wrong shelf log, the log not held, and no layer (`Unguarded` printed).
  - A buyer acceptor pays first after the offerer's `deal agree`, with no flag.
- **Crash.**
  - A stop after the append, then restart: the act is counted once by `rulesHistory`.
  - A failed local insert, then reconciliation.
  - A re-run rewrites the file and seals nothing.
  - A changed input seals a new record. Two statements on the same row values with different
    `--names` are two records.
- **Spend.**
  - `rulesHistory` counts a settled payer leg once and counts no payee leg.
  - A failed leg counts toward velocity rules with no spend, and its approval is not offered again.
  - A second pay check after a settle shows the spend so far and does not offer the used approval.
  - Consecutive v1 performs across deals count toward a velocity rule, and the v0 history input is
    unchanged.
  - Mixed v0 and v1 deals, each act read from its own deal.
- **Refusals.**
  - A `--leg` that disagrees.
  - A missing `--row` with several payment rows.
  - An unknown currency.
  - A member that fails `StructureFailures`.
  - A changed amount.
  - An `--out` that exists.
  - An unknown row or mark.
  - A `--reverses` that names no settle record of the deal.
- **The local list stays local.** The leak test extends to act events.
