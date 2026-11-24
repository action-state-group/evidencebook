# evidencebook#689: report-side books split by a time window, with continuity

Status: revised after the plan review's round 0 (Kimi, and one Claude stand-in for Codex, who is
out until 2026-11-28); Sherlock2's what answer is in §8. Written 2026-11-23 CST at main
`7499da43`.

## Problem

A contract report (#606) proves the holder's rows with inclusion proofs into the holder's log
at one signed checkpoint (`DerivedInputs`, `derived_report.go:37-56`; the holder builds the
answer in `Book.Respond`, `interop_respond.go:593-623`). capsulectl-book keeps one log per
profile (`capsulectl/internal/book/book.go:183`, `OpenCLL(..., p.LogID, ...)`), so a one-month
report out of a five-year book carries proofs and a checkpoint over all five years. `book close`
seals a period inside that one book (`close.go:104-140`); it does not bound the log.

The decision (#689, Ethan 2026-11-22, company-internal-all#125; Steven on the 2026-11-21 call):
split report-side records into books by a time window that is configurable (month, year, other
periods), fixed up front as a property of the holder's shelf, with continuity across windows so
a whole year can be handed over and checked as one unbroken run. The consumer side (one book
per deal) does not change.

| # | Requirement (#689) | Where |
|---|---|---|
| R1 | the window is configurable per holder and fixed up front in the shelf | §1 |
| R2 | one window's book links to the next; a span checks as unbroken, no gap or fork | §2, §5 |
| R3 | a record's inclusion proof is bounded by its own window's book | §4 |
| R4 | the contract-report bundle cites the window books; a verifier checks coverage of the range | §5 |

## 1. The window policy is the shelf's

```go
// WindowPolicy splits a shelf's records into one book per time window.
type WindowPolicy struct {
    Period WindowPeriod // "day", "week" (ISO, Monday), "month", "quarter", "year"
    Base   string       // every window book's log id is "<base>/<label>"
    Since  time.Time    // the first window's start; must be a period boundary
}
```

- Windows are calendar periods in **UTC**, half-open `[start, end)`. Labels: `2026-11-23`,
  `2026-W48`, `2026-11`, `2026-Q4`, `2026`. A label alone gives its bounds, so a reader
  recomputes them. `Since` must be the start of a period (refused otherwise), so the first
  window is an ordinary one.
- `ShelfConfig.Window` is written once, into a new table `shelf_window (id = 1, period, base,
  since)`, only in the transaction that creates the shelf in a fresh file. That same
  transaction writes shelf file **version 3** (windowed). A reopen with a different policy, or
  with none, is refused (`ErrInvalid`); a file that already exists can never gain a policy.
  Records are never moved between books.
- Versions 1 and 2 stay unwindowed; older builds and the frozen Rust and Python shelves refuse
  version 3, so no writer ignores the window. `Migrate` (the shelf-facts migration, which sets
  version 2, `shelf_stamp.go:184`) refuses a windowed shelf: a report-side store does not run
  the facts migration in this item (§7).
- With no policy a shelf is unchanged: the deal shelf and Goose are not windowed.

## 2. Window books

A windowed shelf has exactly one writable book: the newest open window's.

- `Shelf.Current(ctx) (*Book, Window, error)` first rolls (below), then returns the book of the
  window holding the shelf's `Now`. It never goes backwards: if `Now` is before the newest
  window's start (the clock stepped back over a boundary), it refuses (`ErrClockBehind`).
  `Shelf.WindowBook(ctx, label)` opens any window's book; `Shelf.Windows(ctx)` lists them.
- `Shelf.Roll(ctx)` closes every window that has ended, oldest first, and opens the books of
  any empty windows between the last one and `Now`, so the run has no hole. Closing is
  therefore lazy until something calls `Roll`: `Current`, `Shelf.Respond` and `WindowChain`
  call it, and a host may call it on a timer (capsulectl: `book window roll`), so last month's
  final checkpoint exists without waiting for a write.
- A window book refuses a record whose `committed_at` is outside its window
  (`ErrWindowClosed`), except its own `window_open` and `window_close`, the index roots its
  checkpoints commit, and the policy the shelf carries in (below), which carry their real commit
  time: an open can be committed after its start (a window filled in late), a close and the
  index roots before it at or after its end. After the close a book is **closed**: book state, kept in
  `shelf_window_books (label, log_id, start, state, final_entries, final_root)`, refuses every append and
  every checkpoint other than returning its final one.
- **Open record** (`record_type: "window_open"`, leaf 0, `producer_claim`):

  ```json
  {"record_version": "window_open/v1", "period": "month", "label": "2026-12",
   "start": "2026-12-01T00:00:00Z", "end": "2027-01-01T00:00:00Z",
   "previous": {"log_id": "acme/2026-11", "entries": 412, "root": "<hex root hash>"}}
  ```

  `previous` names the previous window's final tree by leaf count and root, not by a checkpoint
  digest: two signed checkpoints of one tree differ in their issue time, and the one a witness
  signed must match too. `previous` is absent only in the `Since` window, and a verifier
  refuses an open record without it anywhere else.
- **Close record** (`record_type: "window_close"`): the label and the window's counts by record
  type. The close sequence is: (1) a checkpoint, which commits any pending authenticated index
  roots (`book.go:787-803`); (2) append the close record; (3) the **final checkpoint**, whose
  tree size is the close record's seq + 1. `window_open` and `window_close` are exempt from the
  authenticated indexes, as `index_root` is (`book.go:396-399`), so step 3 appends nothing. An
  empty window's book holds its open record, the index roots of its first checkpoint and its
  close.
- `window_close` is not the existing `Close`: close/v1 names a counterparty and needs a
  record before it to be its head (`close.go:110-132`), which an empty window does not have,
  and its schema is AAC's.
- **Recovery** on open: a close record without the final checkpoint gets it (step 3 is
  idempotent); a closed window whose next window has no book gets it; a next book with no open
  record gets one. Each step is checked against `shelf_window_books`.
- **Clearing.** Steven's ruling on clearing a history (2026-10-28) has a clear start a new
  stream under a new log id; truncating the old log stays a consistency failure. A clear is a new
  shelf file with a new `Base` and `Since`. A verifier never joins two chains: a range that
  crosses from one to the other fails coverage with "chain breaks at <label>: a new chain
  began", never passes. The one key still links the two, as EB -03 says of clearing.
- **Cadence.** A host that runs one passes `Ended` true for a closed window book
  (`cadence.go:848-884`), so it leaves the live set once its final checkpoint is witnessed; a
  closed book's checkpoint is the final one, so an unwitnessed one costs an unchanged leaf per
  tick. capsulectl-book runs no cadence.

## 3. What the shelf shares across window books

A book reads its disclosure policy from its own log (`policyInForce`,
`disclosure_policy.go:439`), so the shelf re-states the policy in force at the end of the
previous window as the new window's second record (a `disclosure_policy` record, kind
`adopted`, with the same policy and digest); a crash between the open record and it is finished
on the next roll.

On a windowed shelf the derivation registrations and the unsigned-ask limiter belong to the
shelf, not to one book (`interop_derivation.go:121-131`, `disclosure_policy.go:210-213`): a new
window book starts with the shelf's, so a pack stays answerable across a boundary and an
unsigned asker's weekly cap does not restart with each window. `Shelf.RegisterDerivation` and
the shelf's limiter feed every window book.

## 4. Proofs stay inside their window (R3)

`Shelf.Respond(ctx, request, opts)` answers from one window book and records in another:

- `expected_pin` naming a checkpoint: the window book whose checkpoints hold that digest builds
  the answer. A pin naming an exchange half: the book holding that record. No match:
  `coverage_unsatisfiable`.
- `min_freshness`: the current window's book. A request with no coverage member is already
  refused (`request.go:386-390`).
- The answer (artifact, proofs, anchor) is built by the answering book; the
  `evidence_request_answered` record is sealed in the **current** window's book, when it
  happened. A closed book never issues a new anchor: a freshness floor it cannot meet with its
  final checkpoint is `coverage_unsatisfiable`. This splits `Book.respond`'s answer and its
  record (`interop_respond.go:297-337`) into two steps; `Book.Respond` keeps doing both in one
  book for an unwindowed shelf.

The Evidence Request and Evidence Bundle wire forms do not change.

## 5. The window chain and the report's coverage (R2, R4)

**The chain** (`window_chain/v1`), exported by `Shelf.WindowChain(ctx, fromLabel, toLabel, opts)` (`opts.Receipts` supplies
the host's witness receipts of each final checkpoint):

```json
{"v": 1, "base": "acme", "period": "month", "since": "2026-01-01T00:00:00Z",
 "from": "2026-01", "to": "2026-12",
 "windows": [{"label": "2026-01", "log_id": "acme/2026-01",
              "open": <record>, "open_proof": <inclusion proof>,
              "close": <record>, "close_proof": <inclusion proof>,
              "final_checkpoint": <signed checkpoint>, "receipts": [<witness receipt>]}, ...]}
```

`VerifyWindowChain(chain, VerifyChainOptions{CheckpointKey, Witnesses})` checks each window: the final checkpoint's
signature and log id; the open record at leaf 0 and the close record at the last leaf; the
labels consecutive under the period from `since`; each open record's bounds equal to its
label's; each `previous` equal to the prior window's final size and root. With witness ids,
each final checkpoint must carry a verifying receipt from them. Every failure is named: `chain
breaks at <label>`, `window <label> not witnessed`, `window <label> bounds differ`.

What it shows and what it does not:

- A year handover is this document alone; the shelf log is not needed.
- With witness receipts, a window replaced after the fact shows as a break against what the
  witness signed. Without them the chain proves continuity only as the holder states it: one key
  signs every window, so a holder could re-cut window N and re-sign N+1..M. Two different chains
  for one base, signed by one key, are equivocation; a verifier that holds both can see it by
  comparing them. This item does not detect it.
- The chain shows which windows exist and that they link. The report's coverage check shows each
  was answered, never that an answer holds every row of its window (the limit #606 already
  states). An empty window shows only that the holder recorded nothing in it; it does not show
  that nothing happened.
- Why not the whole-shelf handover (`shelf_handover.go`, `461-shelf-facts.md` §2.7): it
  detects a replaced book against the witnessed shelf log, but it hands over every entry of the
  shelf, and a report's auditor is handed only the windows of its range.

**The report.** The requester asks each window book in the range, pinning its final checkpoint
(any checkpoint of the final size and root is accepted), and records each response as today.

- `Book.RecordWindowChain(ctx, chain, VerifyChainOptions)` seals a `window_chain` record whose
  payload is the chain and whose statement is the reported range (the chain's From..To) and the
  chain's digest. It
  refuses a chain that fails `VerifyWindowChain` or does not tile the range.
- `Book.ReportFromWindows(ctx, chainRecord, responses, evaluate, opts, chainOpts)`: every response answers
  the same pack, under the same holder checkpoint key (the key `RecordResponse` pins,
  `request.go:263`), at one window's final tree; every window in the range has exactly one. The
  gate `checkDerivedResult` (`derived_report.go:254-276`) is relaxed for it: each claim cites at
  least one of those response records and may cite the chain record, and nothing else. The
  bundle (with `contract-report/v1`) carries them all by closure, payloads included.
- `VerifyReportCoverage(bundle, CoverageOptions{CheckpointKey, Witnesses})`, under the holder
  checkpoint key the verifier obtained itself (never one read from the bundle), checks the
  Result as `ReportFromWindows` does, the chain record verifies, the windows tile the range,
  each response's anchor is its window's final tree and its proofs verify there, and lists every
  gap by name: `window 2026-03 has no answer`, `windows do not tile 2026-01..2026-12`, `chain
  breaks at <label>`. A range with any finding never passes. The core's `capsulectl verify`
  builds without this package and cannot run it; PR 2 adds the book plugin verb that prints
  these lines, and `book report build` puts the same lines on the page.

`ReportFromDerivation` over one response stays for unwindowed holders.

## 6. capsulectl-book

**The store.** `store init` on a new book profile creates `book/shelf.db`, a windowed
`OpenSQLiteShelf` with month windows (`Base` = the profile's `log_id`, `Since` the current
month's start), and prints `window: month (calendar months, UTC), fixed for this store`
(evidencebook#805). `capsulectl book window init --period <p>` creates the store with another
window instead, or `--period none` with one JSONL book for the whole history; the help says
windows are calendar periods in UTC and cannot change later. A reproduced scratch book and a
profile whose pre-book cll.jsonl still holds entries or a checkpoint stay JSONL, and `store init`
prints why. The backend declares its window
logs to the core (`OwnsLogID`, capsule-cli#622), so `cll checkpoint status|publish` work on the
current window's checkpoints and `cll proof verify` on any window log's; closed windows' final
checkpoints go to the witness with `book window publish`. An existing JSONL book is not converted (#808).

**What changes inside.** `openedBook` carries the shelf and the current window; its record
store is an interface (a window book's records are read as sealed); the clock floors at the
newest window's last record before the shelf rolls; witness state is kept per window log
(`book/witness/<label>.json`), and `book window roll` and `publish` queue each closed window's
final checkpoint for the profile's witness; a reproduced scratch book stays JSONL.

**Verbs.**

| Verb | On a windowed store |
|---|---|
| writes (`publish`, `seal`, `policy change`, `disclose`, `staple`, `backfill`, `request`) and the core's `cll checkpoint create`, `bundle` (capsule-cli's `LogBackend`) | the current window |
| core `cll list` | the newest window |
| `respond` | `Shelf.Respond` (§4) with the profile's packs registered on the shelf |
| `close`, `reconcile` | a period inside the current window; a period in an earlier window is refused with a hint (evidencebook#807) |
| `migrate` | refused (JSONL books only) |
| `book window list`, `roll`, `chain --from --to --out`, `publish` | new |
| `book report derive --chain <file> --response <id>...` | seals the chain and one Result over every window's answer; writes the bundle only when its coverage has no finding |
| `book report coverage --bundle --holder-checkpoint-key` | prints `VerifyReportCoverage`'s range, windows and findings; exits non-zero on any finding |
| `book report build --holder-checkpoint-key [--witness-directory]` | for a report over windows: `report.Build` checks the coverage itself (`report.View.Coverage`, evidencebook#806) and states the range in a banner, or writes no page and names every gap; no permalink |

## 7. Owner, doors, ports, PRs

- Owner: evidencebook (root package: shelf, books, chain, verifier, report; `capsulectl/`: the
  CLI). No AAC wire change; capsule-cli is not changed.
- Doors, all engineering's: shelf file version 3 and its window tables; the record types
  `window_open`, `window_close`, `window_chain` and the `window_chain/v1` document. Sherlock2
  confirmed the shapes are engineering's and that none of the open points is a door for Steven
  (§8).
- Not in this item, each a follow-up issue: a Close over an earlier window's period (#807);
  converting an existing JSONL book (#808); the month default for `store init` after
  capsule-cli#622 (#805); windows on a capsulectl-deal shelf (#797); the facts migration of a
  windowed shelf. Rust and Python: "Frozen
  port: ..." issues per Ethan's 2026-11-22 language decision; the Mesh plugin does not call
  this.
- PRs: (1) root package §1-§5 (#800); (2) the `report` module's coverage banner (#806); (3)
  capsulectl-book §6. Lane tau2-otel-1123
  (evidencebook-skills#219) moves to `Shelf.Current`, `Roll` and `WindowChain` after (1).

## 8. Sherlock2's answer (the what)

Asked once, twin first (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=69b648984d3ae71697f4a4870596d770f99833aebf3817fee2c243da71c3bbe1).
Sherlock2 answered that none of the three is a door for Steven and agreed to all three, with
conditions (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=2c569a101cf2f0b0a251199ddd0bdd227b2ed313138800332c4e99f1fa8809a5&thread=69b648984d3ae71697f4a4870596d770f99833aebf3817fee2c243da71c3bbe1).

| Condition | Where |
|---|---|
| a one-year handover verifies with nothing but the window books | §5: the chain alone |
| a dropped or replaced window shows as a break | §2 `previous`; §5 says what holds without a witness |
| where the agreement names a witness, the final checkpoint is the witnessed one | §5: witness ids make each final checkpoint need a verifying receipt |
| the verifier reports a gap by name and never passes a silent hole; the page shows the same line | §5 |
| calendar months in UTC, and the help says so | §1, §6 |
| the default is printed when a store is created | §6 |

The default window is **month** for a new report-side store; existing books stay as they are.

## 9. Tests

- Policy: reopen with another policy or none, a policy on an existing file, an unaligned
  `Since`, and `Migrate` on a windowed shelf, each refused; versions 1 and 2 unchanged.
- Boundaries for each period (month ends, leap day, ISO week 53, quarter, year).
- Roll: a write after the boundary closes the window and opens the next; empty windows filled
  in order; the close is the last leaf and the final checkpoint appends nothing; a crash after
  step 1, after step 2, after step 3, after the next book's open, and partway through filling
  several empty windows, each recovered on reopen; a closed book refuses appends and new
  checkpoints; the clock stepping back over a boundary refused.
- Shared state: a pack registered before a boundary answers after it; the unsigned-ask limit
  holds across a boundary.
- Respond: routed by checkpoint pin, by half pin and by freshness; a past window answers and the
  answered record lands in the current window; a closed book never issues an anchor.
- Chain: a gap, a wrong `previous`, an open without `previous` after `Since`, a changed bound, a
  record after the close, and an unwitnessed window when witnesses are named, each named; a
  witnessed chain passing.
- Report: a missing window, a duplicate window, a response pinned to a tree that is not the
  final one, mixed holder keys, a range the chain does not tile, and a range crossing two
  chains, each named; the bundle verifies and the page shows the lines.
- capsulectl-book: `store init` default and print; writes across a boundary under a fixed clock;
  `book window roll` and `chain`; a two-window `report derive` and the plugin's coverage verb;
  witness state per log id; core `bundle` and `cll list` on the current window; `close` on the
  previous month refused with its hint.

## 10. Grace: a Close over an earlier window (#807)

Sherlock2 (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=ee4e11316e8261b53e5abda85dcb7c0ffae445d837819e9ae9ba25cd48b430df&thread=cdfa9aef3d12815598c3ea6602a65a5e6cfafb54e3270ca6a50c7bce506bba85):
engineering's call, option (a): a grace period after a window ends during which its book still
takes a close/v1 Close for a period inside it; default 7 days; fixed and printed at creation;
the verifier, not only the writer, names any other late record; a window still in grace is named;
acknowledgements and rebuttals are never cut off (they go in the current window). Rejected: a
Close in the current window linking the earlier book (an Evidence Bundle over two logs, a spec
door); no monthly Close at all (fails the what); late Closes named only in the `window_close`
statement (not bound to the final tree).

**Policy.** `WindowPolicy.Grace` (zero: no grace, as before), non-negative and shorter than the
period's shortest length (day 24 h, week 7 d, month 28 d, quarter 89 d, year 365 d). It is
written with the window in `shelf_window.grace` (seconds) when the store is created, compared on
every reopen, and read as 0 from a store created before the column (an idempotent `ALTER TABLE`
on open, the `migrateShelfReceipts` pattern). A store with a grace above 0 is shelf version 4,
so a build that knows only version 3 refuses it rather than closing its windows early; a store
with grace 0 stays version 3. capsulectl's default: 7 days for month, quarter and year windows,
1 day for week, none for day.

**Roll.** A window's row is `open` or `closed`; an open row with a stored boundary is in its
grace. At the first roll after a window's end, the shelf takes a **boundary checkpoint** of it
(stored as `boundary_entries`, `boundary_root`), so the window is in grace, and opens the next
window, whose open record's `previous` names that boundary tree; if that roll comes at or after
`End + Grace`, it closes the window directly, as with grace 0. At the first roll at or after
`End + Grace` the shelf closes a window in grace (close record, final checkpoint) as before; the
close record names the boundary, so a chain can neither swap nor drop it, even for the last
window of a chain. Every roll closes every window in grace whose grace has ended, oldest first,
then opens windows up to the clock; several windows can be in grace only if the grace exceeds a
period, which the policy refuses. Recovery: a window is in grace only after its boundary
checkpoint is stored; a next window's open record is written from the stored boundary, never from
`final_*`. With grace 0 the boundary checkpoint is the final one and the chain is as in §5.

**Admission.** In grace a window book takes only a close/v1 Close (read with `ReadClose`, its
period inside the window, a superseding Close included; a legacy body is refused) and the index
roots its own checkpoints commit; at or after `End + Grace` it takes nothing, rolled or not. An
acknowledgement or rebuttal goes in the current window at any time, as do a Close sealed after
the grace and `reconcile` (which adds no record).

**Chain v2.** Each window entry also carries the boundary checkpoint (with receipts when the host
has them), a CLL consistency proof from the boundary tree to the final tree, and the **tail**:
every leaf after the boundary up to the close record, each with its inclusion proof under the
final checkpoint. `VerifyWindowChain` checks the next window's `previous` against the boundary
tree, the consistency proof, that the tail is contiguous from the boundary's last leaf + 1 to
the close record, and that every tail record is a close/v1 for a period inside the window or a
strictly decoded `index_root`; any other record is a named finding (`window 2026-10 holds a
late <type> record at <seq>`), as is a missing leaf. The boundary is positional, so a backdated
`committed_at` cannot hide a record; once a window took its boundary its book refuses every
record but a Close for its period, whatever the clock reads. What a chain does not show: that a
holder did not delay a window's boundary past its end to place ordinary records before it. With witnesses named, the boundary and final checkpoints
must each carry a verifying receipt. `WindowChainVersion` becomes 2; a version 1 chain (grace 0)
still verifies.

**Naming a window in grace.** `Shelf.WindowChain` over a range that includes a window in grace
returns `ErrInGrace` naming it and when it closes ("window 2026-10 is in grace until
2026-11-08T00:00:00Z; no final checkpoint yet"); `book window chain`, `report derive --chain`
and `report coverage` print it. A pin on a window-in-grace checkpoint is answered from that book
(its answer is not at the final tree, so a report over windows cannot use it until the grace
ends).

**capsulectl.** `store init` and `book window init --grace <duration>` fix the grace with the
window; the notice and help state it ("window: month (calendar months, UTC), closes 7 days after
it ends, fixed for this store"); `book window list` shows each window's close time; `book close`
over a period inside a window in grace seals the Close in that window; `book window roll` and
`publish` queue the boundary and final checkpoints for the witness (`witness/<label>.json`);
`close` names the window it sealed in.

**Tests.** Policy: grace written, compared, refused when not shorter than the period, a version 3
file without the column reads 0, version 4 for grace above 0. Roll: boundary then grace then
close; a clock jump across several windows; crashes after the next row before the boundary
checkpoint, between the boundary and the next open record, and at each close step. Admission:
an in-window Close admitted in grace; refused: an ordinary record, a Close outside the window, a
legacy Close, any record at `End + Grace` without a roll. Chain tamper: an ordinary late record,
a backdated ordinary record after the boundary, an omitted tail leaf, a rewritten prefix (the
consistency proof fails), a boundary without receipts under witnesses, a malformed `index_root`;
an honest chain with late Closes and index roots verifies; a v1 chain verifies. Respond and
report: a pin on a window in grace; `ErrInGrace` from chain and derive. capsulectl: the notice and
help, `--grace` and its refusals (no store is left behind), `book close` last month in grace
and after it, an ordinary record after a window ends landing in the current window, `book window
list`, a chain refused over a window in grace, the boundary checkpoint queued for the witness,
`close --bundle-out` in grace (anchored at that window's checkpoint, its disclosure record in the
current window, since a window in grace takes only Closes); an old store opening with grace 0
is the root package's test.
capsulectl-book has no verb that writes an acknowledgement or rebuttal; the root package's tests
show a window in grace refusing every record but a Close inside it, so such records go to the
current window.

## 11. Converting a JSONL book: the predecessor (#808)

**The decision.** The old JSONL book closes as a whole and verifies as the window chain's
predecessor, because a conversion that left the old book
unlinked would read as a clear (a new stream) and a separately checked one would leave the join
unchecked. Continuity across the conversion is as strong as between windows.

**The close.** `Book.CloseForWindows(base, period, grace)` checkpoints the book (committing any
pending index roots), appends a `book_close` record (`book_close/v1`: `counts_by_kind`, and the
`successor`: base, period, grace in seconds), and takes the final checkpoint, whose tree ends at
that record. The record is found by position and is not indexed, so the final checkpoint commits
no new index root. A book whose last record is its `book_close` refuses every append
(`ErrBookClosed`), recomputed on open, so neither a crash window nor a later verb writes after the
close. Closing again with the same successor returns the same policy (a resumed conversion);
another successor is refused.

**The store.** The policy carries `Predecessor` (the book's final tree) and `PredecessorPolicy` (the
disclosure policy in force at the close, which the first window opens with, since the shelf cannot
open the JSONL book). `Since` is the first whole second after the close record's commit time
(`SuccessorSince`), so no window holds a moment the book also does. The first window runs from
`Since` to the next period start: a partial window with its usual label, `partial` in
`WindowState` and on the chain's verified window. Every reader of a window's bounds goes through one helper that clamps the first
window's start to `Since`, so a record dated before the conversion is refused there. The policy,
predecessor included, is fixed in `shelf_window` and compared on every reopen; a shelf with a
predecessor is version 5, which builds without predecessors refuse. The unsigned-ask limiter
starts afresh with the store.

**The chain.** The chain of such a store is `v: 3`, so a verifier that knows only 1 and 2 refuses it
by version. A chain from the store's first window carries `predecessor`: the `book_close` record
with its inclusion proof under the book's final checkpoint, that checkpoint and its receipts
(`ChainPredecessorFromBook`; the host builds it, since the shelf does not hold the book).
`VerifyWindowChain` checks the checkpoint under the holder's key and of its log; the close record
at its last leaf, signed by the windows' key, naming this store's base and period, with `Since` the
first whole second after it; the final tree equal to the first window's `previous`; and, under
witnesses, its receipts ("predecessor not witnessed"). A missing predecessor, an altered tree, and
a record after the close (the close is then not the last leaf of the tree the window names) are
named findings. A chain from a later window carries a note on its first window: "continues from a
predecessor book, not included", which rests on the chain's version and since, both unsigned.

**Coverage.** A report over the conversion's period names the part before `Since` ("window 2026-11
starts at <Since>; [<period start>, <Since>) is in the predecessor book, not covered"): no window
answers for it.

**capsulectl.** `book window convert` (a second PR) runs the close and creates the store, refusing a
store already windowed, no JSONL book, a reproduced book, an unmigrated pre-book `cll.jsonl`, a
clock behind, or no `--yes`; it states that the conversion is one-way, the old book stays at its
path and its records never move, the same key continues, and it is not a clear. Closes over a
period before the conversion are sealed in the old book first: after conversion such a Close is
refused, with a hint naming the predecessor book, and a period that spans the conversion is closed
in two parts. Where the profile names a witness, the old book's final checkpoint is queued in
`book/witness.json` and delivered like a window's final; until a receipt arrives the chain reports
"predecessor not witnessed". A profile with no witness converts, labelled unwitnessed: the verifier
checks receipts only when witnesses are passed, so `convert` and the chain say so.
