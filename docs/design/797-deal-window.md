# evidencebook#797: a window on a capsulectl-deal shelf

Status: revised after the plan review's round 0 (Kimi, and one Claude stand-in for Codex, who is
out until 2026-11-28); §8 holds the findings and answers. Sherlock2 answered the what (§4, §5) and
confirmed the names carry no product meaning. Written 2026-11-23 CST at main `375e1b8a`. It reuses #689's window books
([689-window-books.md](689-window-books.md)) and adds no second window mechanism.

## Problem

#689 fixes a time window on a holder's shelf and splits report-side records into window books
chained by `previous` (§1, §2, §5 there). Its windowed shelf holds window books only
(`attachWindow` refuses any other log id, `window_book.go:606-607`), refuses the facts migration
(`shelf_stamp.go:165-169`) and so the journal (`Shelf.Journal` needs a migrated shelf,
`shelf.go:264-267`). capsulectl-deal needs all three: one book per deal (`deal/<32 hex>` for an
x-deal-v1 deal, `deal_v1_book.go:105-113`), the shelf's journal for every x-deal-v1 journal record
(`appendV1Journal`, `deal_v1_events.go:365`), and the facts cadence (`deal set-version x-deal-v1
--migrate`, `deal_v1.go:239`, `:499`). So a deal holder judged by a Terms Pack has no window today,
and the tau2 demo opens one deal profile per window (evidencebook-skills
`demo/tau2-live/window.py`).

The bar: Ethan, 2026-11-23 (coordinator chat, quoted on #797): the window is a property of the
shelf, so the consumer side and Terms Pack judging both use the shelf; the consumer side keeps
one book per deal. Steven, 2026-11-21 call: the split rule is chosen up front.

| # | Requirement (#797) | Where |
|---|---|---|
| R1 | a deal shelf carries #689's policy, fixed when the shelf is created | §1 |
| R2 | each deal's book is placed in the window its agreement falls in, one book per deal | §3 |
| R3 | the journal is bounded by the window, with continuity across windows | §2 |
| R4 | a Terms Pack report over a range of windows verifies coverage as #689's does | §4 |

## 1. The policy: the journal is the windowed log

`WindowPolicy` gains one member:

```go
// Journal makes the window books the shelf's journal (evidencebook#797):
// each window's journal is the window book "journal/<label>", and the shelf
// also holds its other books (deal, node and case books), the cadence and
// book facts. Without it a windowed shelf holds window books only.
Journal bool
```

- `Base` must be `JournalLogID` and `Grace` must be 0 when it is set (refused otherwise). No
  grace: #807's grace admits only a close/v1 Close, which x-deal-v1 has no form of
  (`deal_v1.go:124-133`), so a grace would only hold back each window's final checkpoint.
- It is written with the window in `shelf_window.journal` when the file is created and compared
  on every reopen, as `Grace` is; `ReadShelfWindow` (`window_book.go:761-805`) reads it, so a host
  reopens with the stored policy. The version rule at `window_book.go:241` becomes: `Journal`
  gives shelf **version 6**, else grace above 0 gives 4, else 3 (version 5 is #836's shelf with a
  predecessor, `689-window-books.md` §11 on its branch). A build that knows only up to 5 refuses a
  version 6 file rather than refusing its deal books one by one. A version 6 file never has a
  predecessor (converting a deal store is not this item), and a test checks that a journal policy
  on a version 5 file is refused. Whichever of #836 and this item merges second takes the higher
  number.
- It is created already on book facts: the creating transaction writes the `shelf_migration` row
  as `done` (there is no unmigrated cadence log to carry over), so `Cadence.Migrated` is true and
  `deal set-version x-deal-v1 --migrate` skips `Migrate`. `Migrate` on a #689 windowed shelf
  without `Journal` stays refused.
- A store that exists is never windowed (#689: a file can gain no policy). Converting an
  existing deal store is not this item.

## 2. The journal per window (R3)

- `Shelf.Journal(ctx)` on a journal-windowed shelf rolls (as `Shelf.Current`) and returns the
  current window's journal book, so every journal append lands in the window of its commit time.
  Window journal books are of kind `journal` and open only through the journal API
  (`Journal`, `JournalView`, `WindowBook`); `Shelf.Open` refuses them, as it refuses the journal.
- The window books are #689's: `window_open` with `previous`, the disclosure policy re-stated,
  `window_close` and the final checkpoint. The chain (`Shelf.WindowChain`, `VerifyWindowChain`)
  is unchanged but for `window_close/v2` (§3). A year of journal hands over as one chain.
- **One ordered journal read.** Journal readers today take one `*Book` and one seq space:
  capsulectl's `journalHolds`, `journalRecord`, `v1DealJournal` and the admit path; and in
  `dealprofile/xdealv1`, `ReceiveProfile.ours` (a `journalSeen` watermark, `receive.go:394-435`),
  `StatusProvider`'s offer lookup and `ownBook` (`status_provider.go:95`, `:185`, `:228-243`) fed
  by `StatusInput.Journal` (`shelf_stamp.go:72`, filled by `existingJournalLocked`,
  `:861-869`), and `ReportSnapshot` (`report.go:634-640`). The root package adds
  `Shelf.JournalView(ctx)`, a read-only view over every window's journal book in chain order with
  one seq space (window N's records follow window N-1's close record), offering what those readers
  call on a `*Book`: `Size`, `Query` by seq, `ResolvePayload`, and the records list. It is
  append-only: a window closes before the next opens and a closed window takes no record
  (grace 0), so a seq never moves and `journalSeen` stays valid. The view opens each window's book as
  `Shelf.Journal` opens the journal today, so a record a crash left stored but not yet in its log
  is recovered and visible to `Query` and `ResolvePayload` (`journalHolds` relies on it,
  `deal_v1.go:392-398`; a second admit sealed for a missed one would fail authorization for good,
  `deal_v1_admit.go:196-199`). On an unwindowed shelf the view is the one journal. `StatusInput.Journal`, `ExistingJournal` and the readers above move to a
  small interface both satisfy; `existingJournalLocked` returns the view on a windowed shelf
  without creating or rolling anything.
- **Journal facts.** `factFor` decides a journal fact by `shelf_logs.kind = 'journal'`, not by the
  log id `journal` (`shelf_stamp.go:451`), so each window's journal gets a journal fact, whose
  size stays hidden as #461 §2.2 requires.
- **Leaving the live set.** The root package exports `Shelf.WindowEnded(ctx, logID)`: true for a
  closed window book. capsulectl's cadence `Ended` predicate (`deal_shelf.go:152-179`) asks it
  for a `journal/<label>` id, so a closed window leaves the live set once its final checkpoint is
  witnessed (#689 §2, Cadence).
- **Order across windows.** Appends are serialized and each rolls first, so the view keeps the
  order records were committed in. Window admission bounds the Capsule's `committed_at`, not the
  x-deal-v1 block's `at`, which is read earlier (`deal_v1.go:266`): a record with `at` 23:59:59
  can land in the next window, and a crash-recovered record (`deal_v1_events.go:84-104`) carries
  its earlier `at`. PROFILE.md §8's rule (the journal read in order, `at` never decreasing) is
  checked on the view as it is on one journal today; windows add no new case and no new
  guarantee. **x-deal-v1 text.** PROFILE.md §8 gains one sentence: on a windowed shelf the journal
  is its window journals read in chain order, and the order rules apply across them.

## 3. Deal books placed in a window (R2)

- `Shelf.Place(ctx, logID, at, first) (*Book, Window, error)` rolls to `at` and places a new,
  empty book in the window holding `at`, refusing (`ErrWindowClosed`) when that is not the current
  window. It writes `shelf_window_placed (log_id, label, state)` as `pending`, seals a
  `window_place` record (`window_place/v1`: `log_id`) in that window's journal, marks the row
  `placed`, and then runs `first`, which appends the book's first record, before it releases the
  shelf's lock: no roll can close the window between the placement and the agreement and leave
  the deal out of the window's close (review of #846). A crash between the steps is finished by
  the book's next `Place`: a pending row whose record is in the journal is marked placed, and one
  without it gets the record. Placing a book that is placed and holds records returns its window.
  Placing one that is still empty in an earlier window (a crash before its agreement, then a retry
  after the boundary) places it again in the current window; the latest place record counts. A
  book that holds records and was never placed is refused.
- capsulectl-deal reads the agreement's time once, calls `Place` with it and with the
  agreement's append as `first`, which appends the agreement (the deal book's seq 1, PROFILE.md §3) with that same time as the in-flight record
  time (`deal_v1_book.go:217` reads `v1Now()` today; it takes the time read before `Place`), so
  a clock that crosses a boundary between the two cannot put the agreement in the next window. A received agreement is not in an own book
  (`shelf_received`), so it is not placed. A deal stays in the window its agreement was sealed in
  as it runs past the window's end. Its later deal-book records go to its own book; its journal
  records (checks, approvals, messages) go to the window of their own time.
- No book is refused for being unplaced: node books (`deal node`, the Goose node,
  `deal_node.go:30-40`) and adjudicator case books (`case/<hash>`, `deal_v1_entries.go:170-178`)
  open as today and are not windowed. Placement is what a deal agreement does, not a gate.
- `window_close/v2` adds `placed`: for each book whose latest place record is in the window and
  that holds its seq 1 at the close, its log id and its tree then (`{log_id, entries, root}`, from
  a checkpoint the close takes), sorted by log id. A deal's answer in a report must be at that tree
  or a later one (§4). The close is in the window's final tree, so the
  chain alone lists the deals of a range. `VerifyWindowChain` reads `window_close/v1` and `/v2`;
  a build that knows only v1 refuses a v2 close (strict decode, `window_chain.go:388-393`), so a
  verifier of a deal shelf's chain needs this build.
- A deal book is not chained: its own checkpoints and the cadence's facts cover it as today.

## 4. A Terms Pack report over windows (R4)

The #689 path, with the journal windows as the window books: the requester asks each journal
window in the range by the pack's digest, pinning its final checkpoint; the holder answers with
`Shelf.Respond` (routes by pin; the `evidence_request_answered` record goes in the current
window's journal); the requester runs `RecordWindowChain` and `ReportFromWindows`; anyone runs
`VerifyReportCoverage`, which names every gap.

**The what, decided: (b).** Sherlock2 (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=1dec2d22e3d3ea0f518f464c400e513478db0ebb12aeac18be4b714f468401a1&thread=d90677151450165ee08197e67e240adfba3e7710092e6598167404fea5a143ba):
(b), because a report that passes coverage with every agreement, perform and settle left out is a
false green; a deal's line says it runs past the window: "deal <log id> answered over its whole
book, through <checkpoint>". In the discussion round on the twin's points
(buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=721fff7f33a117bd9fc69692dbe8e19f1b291c74b2bb77c7b58ea4c6a7a8d6b8&thread=d90677151450165ee08197e67e240adfba3e7710092e6598167404fea5a143ba):
the #606 limit (rows proven in the holder's log; field values the holder's word; no proof that no
row was left out) is stated once per report beside the coverage section, and again on anything
shown on its own, such as one deal's answer or a single-deal view. The question as asked: A window's journal holds offers, admissions,
intents, checks, approvals, verdicts and the journey overlay (messages, claims, evidence
citations). The agreement, perform, settle and statement records are in each deal's own book. So
what must a Terms Pack report over windows on a deal shelf cover?

- (a) **The journal windows only.** #689's verifier unchanged. Coverage says nothing about the
  deals' own records; every printed coverage line states that limit.
- (b) **The journal windows and each deal placed in them.** The requester also asks each deal
  book in a window's `placed` once, at its latest checkpoint; `VerifyReportCoverage` names a
  placed deal with no answer (`<log id> of window 2026-11 has no answer`), or with an answer
  at any tree but the one the chain proves for it ("<log id> of window W is answered at a tree of n
  records that is not the tree its window's chain proves"). The chain carries, per placed deal
  (`ChainWindow.Deals`), the deal's signed checkpoint at the close (the tree the close lists), its
  latest signed checkpoint when the chain is cut, and a CLL consistency proof between them, as a
  window's grace boundary carries one (`ChainBoundary.Consistency`); `VerifyWindowChain` checks
  them. So a holder can neither answer from an early checkpoint and leave out what followed nor
  answer from a fork (Sherlock2's review of #846; the coordinator chose the recorded tree over a
  time floor, which would rest on the holder's clock, and the proof over an answer pinned at the
  close tree, which would drop the deal's acts after the window). The member is optional and only
  on journal chains, whose `window_close/v2` older verifiers already refuse, so the chain's
  version does not change. A deal book is not
  bounded by the window, so its answer covers the whole deal, including records after the window.
  It needs three root-package changes beyond (a): `Shelf.Respond` routes a pin on a placed deal
  book's checkpoint to that book (today `answeringBookLocked` looks only through window rows,
  `window_respond.go:62-93`); `ReportFromWindows` accepts, beside one answer per window at its
  final tree, one answer per placed deal at or after its tree at the close, and lets claims cite them
  (`window_report.go:137-139`); and `VerifyReportCoverage` applies the same rule
  (`window_report.go:386-388`).

The build is (b).

capsulectl-deal: `deal window list|roll|chain` (the #689 verbs over the journal windows; the
deal plugin links no capsulectl-book package, so they are thin calls on the root package's
`Windows`, `Roll` and `WindowChain`) and `deal window respond --request FILE --pack FILE...`,
which registers the given compiled packs on the shelf (`Shelf.RegisterDerivation`) and answers
through `Shelf.Respond`. There is no `deal window publish`: a deal store's witness sees its
cadence log, whose book facts already cover every journal window and placed deal book, and the
witnessed per-window final checkpoints a #689 chain can require (`VerifyChainOptions.Witnesses`)
stay capsulectl-book's. `deal node respond` and `deal respond` are unchanged. The requester side
and the coverage verb stay capsulectl-book's (`book report derive --chain`, `book report
coverage`, which prints each deal's coverage line and the report's limit).

## 5. capsulectl-deal

- `deal init --window <day|week|month|quarter|year|none>` creates the shelf with the policy
  before anything else writes the file (`openOwnedTarget` and `MarkOwned` write it first today,
  `deal.go:1203-1212`, and #689 fixes a policy only on a fresh file), then the rest as today.
  Default `none` (Sherlock2, above: revisit `month` once windowed deal shelves have a real user).
  With a period it prints "window: month (calendar months, UTC), fixed for this store"; without
  one it prints on stderr, in capsulectl-book's wording (`booklog.go:895`), "no window: one journal
  for this store's whole history, fixed for this store" (Sherlock2, discussion round above).
- `openShelf` (`deal_shelf.go:85-100`) reads the stored policy (`ReadShelfWindow`) and passes it,
  so every verb opens a windowed store.
- A windowed store has no x-deal-v0 deal: v0 verbs that open a legacy log are refused on it with a
  hint. The clock stepping back over a boundary refuses journal writes with #689's
  `ErrClockBehind` message.

## 6. Owner, doors, ports, PRs

- Owner: evidencebook: the root package (policy member, version 6, `Place`, `JournalView`,
  `WindowEnded`, journal facts by kind, `window_close/v2`, and under §4 (b) deal-book routing in
  `Shelf.Respond` and the deal answers in `ReportFromWindows` and `VerifyReportCoverage`), `dealprofile/xdealv1` (readers on the
  view), `capsulectl/internal/deal` (verbs) and `dealprofile/x-deal-v1/PROFILE.md` (§3 placement,
  §8 one sentence). No AAC wire change; capsule-cli unchanged.
- Doors, engineering's unless Sherlock2 says otherwise: shelf version 6 and its column; record type
  `window_place` (`window_place/v1`); `window_close/v2`. User-visible what for Sherlock2: what a
  report over windows covers (§4), `deal init --window` and its default.
- Not in this item: converting an existing deal store; Rust and Python ("Frozen port: ..." issues,
  Ethan 2026-11-22); the Mesh plugin; the Goose node.
- PRs, after #828, #836 and #837 merge: (1) root package and `dealprofile/xdealv1` §1-§4 with
  tests (#846); (2) capsulectl-deal §4-§5, capsulectl-book's coverage lines and the profile text;
  (3) the `report` module's page: each deal's coverage line and the report's limit, once its
  root pin is bumped. Then the tau2 demo's switch, filed in evidencebook-skills.

## 7. Tests

- Policy: `Journal` written, compared and read back by `ReadShelfWindow`; reopen without it
  refused; `Base` other than `journal` or a grace refused; version 6; created migrated; `Migrate`
  refused on a #689 windowed shelf.
- Journal: appends before and after a boundary land in each window; the view over three windows
  (one empty) in order with one seq space; `ResolvePayload` from a closed window; a receive cache
  built before a roll reads the new window's records; a crash between a window's open and its
  first journal record; a recovered record with an earlier `at` behaves as on one journal. A crash after an admit is stored and
  before its append, then `deal admit` again: the view shows it held and no second admit is sealed.
- Facts: a stamp across a boundary gives each window's journal a journal fact (size hidden) and
  each v1 deal book its profile and status, with the offer in an earlier window; a closed,
  witnessed window leaves the live set.
- Place: the current window; an agreement time read before a boundary and `Place` called after
  it refused, under a fixed clock stepping between the two; a place record without its row recovered; a placed, written book
  returns its window; an empty book placed in an earlier window placed again; node and case books
  open unplaced; `window_close/v2.placed` lists the window's deals, empty for an empty window; a
  v1 chain still verifies.
- Report (per the what): two windows answered by pack digest, coverage passing; a missing window
  named; under (b), a placed deal with no answer named.
- capsulectl-deal: `deal init --window month` creates the window and prints the line; a reopen
  uses it; offer, agree, check and perform across a month boundary under a fixed clock (the deal
  stays in its window, its check lands in the next window's journal, authorization reads the
  intent from the earlier window); `deal report` on a windowed store; `deal window chain` over the
  two windows verifies; `deal window respond` with an unregistered pack refused and a registered
  one answered; node verbs work; a v0 verb refused with its hint.

## 8. Plan review, round 0

Kimi (session `session_ac6cc308-a190-4bb8-bf42-d8a5c7920afa`) and one Claude stand-in for Codex
(`claude-opus-5-5`, session `e1ddd070-6dcc-46a4-bc57-79435229a34c`), on `f0ab6617`. Every finding
was checked in code and accepted; none declined.

| Finding (raised by) | Sev | Answer |
|---|---|---|
| xdealv1's journal readers (receive cache, status provider, report) and `StatusInput.Journal` miss a windowed journal: facts say profile "none", `deal report` fails (both) | P1 | §2 one ordered journal read; §6 owner; §7 tests |
| window journals would get book facts that show their size (stand-in) | P1 | §2 journal facts by kind |
| the `Open` gate breaks node and case books; `deal node respond` rewired by mistake (both) | P1 | §3 no gate; §4 a new `deal window respond` |
| a journal-only report passes coverage while leaving every deal act out (stand-in) | P1 | §4 open what, asked before PR 1 |
| `deal init` writes the file before the shelf opens, so the policy would be refused (Kimi) | P2 | §5 |
| `Place` crash and retry across a boundary; wrong error named (stand-in) | P2 | §3 |
| closed journal windows never leave the live set (both) | P2 | §2 `WindowEnded` |
| §2 and §3 disagree on `Open` (both) | P2 | §2 |
| results book placement is dead (v1 has no terms result) (stand-in) | P2 | dropped |
| grace adds only delay on a deal shelf (both) | P2 | §1 grace 0 |
| `placed` breaks older strict verifiers (Kimi) | P2 | §3 `window_close/v2` |
| the `at` ordering argument conflates header and block time (both) | P2 | §2 reworded; §7 test |
| the policy's source on reopen (Kimi) | P3 | §1, §5 `ReadShelfWindow` |
| no pack registration for respond (Kimi) | P3 | §4 |
| the new version vs the grace version check; clock stepping back (stand-in) | P3 | §1, §5 |
| verification (stand-in): option (b) omits the respond, report and verifier changes it needs | P2 | §4 (b), §6; targeted check |
| verification (Kimi): the view must recover a stored, unlogged record as the journal's open does | P2 | §2; §7 test; targeted check |
| verification (stand-in): the agreement time is read after `Place`, so a boundary can split them | P3 | §3 `Place(ctx, logID, at)`; §7 test |

Verification (one call each, pre-approved for P1 fixes, on `64a457c1`): both confirmed every
round-0 finding fixed; their three new findings (P2, P2, P3) are fixed in the next commit and
checked by the driver against the cited code (no model round, as the severity rule says). The
version number moved from 5 to 6 after the Sherlock2 twin found #836 already uses 5
(`689-window-books.md` §11 on `claude/eb-808`).

review: rounds 2, accepted 18, declined 0, pre-existing 0; classes: missing reader migration, missing recovery invariant, missing dependency, privacy leak in facts, scope gate breaks callers, coverage gap, write order, crash recovery, live-set growth, doc contradiction, dead machinery, compatibility, wrong invariant; record: this section
