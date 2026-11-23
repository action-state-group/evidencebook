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
  `shelf_window_books (log_id, label, state, final_size, final_root)`, refuses every append and
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
- **Clearing.** Steven's clearing ruling (ops `proprietary` [muse-share-bound-history]
  DECISION, 2026-10-28 22:43 PT, Buzz 054513b2 event a01109dd) has a clear start a new stream
  under a new log id; truncating the old log stays a consistency failure. A clear is a new
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
  signs every window, so a holder could re-cut window N and re-sign N+1..M. A verifier that
  holds two different chains for one base reports equivocation.
- The chain shows which windows exist and that each was answered, never that an answer holds
  every row of its window (the limit #606 already states). An empty window reveals only that
  nothing was recorded in it.
- Why not the whole-shelf handover (`shelf_handover.go`, `461-shelf-facts.md` §2.7): it
  detects a replaced book against the witnessed shelf log, but it hands over every entry of the
  shelf, and a report's auditor is handed only the windows of its range.

**The report.** The requester asks each window book in the range, pinning its final checkpoint
(any checkpoint of the final size and root is accepted), and records each response as today.

- `Book.RecordWindowChain(ctx, chain, reported Period)` seals a `window_chain` record whose
  payload is the chain and whose statement is the reported range and the chain's digest. It
  refuses a chain that fails `VerifyWindowChain` or does not tile the range.
- `Book.ReportFromWindows(ctx, chainRecord, responses, evaluate, opts, chainOpts)`: every response answers
  the same pack, under the same holder checkpoint key (the key `RecordResponse` pins,
  `request.go:263`), at one window's final tree; every window in the range has exactly one. The
  gate `checkDerivedResult` (`derived_report.go:254-276`) is relaxed for it: each claim cites at
  least one of those response records and may cite the chain record, and nothing else. The
  bundle (with `contract-report/v1`) carries them all by closure, payloads included.
- `VerifyReportCoverage(bundle, CoverageOptions{Witnesses})` checks the chain record verifies, the windows tile the range,
  each response's anchor is its window's final tree and its proofs verify there, and lists every
  gap by name: `window 2026-03 has no answer`, `windows do not tile 2026-01..2026-12`, `chain
  breaks at <label>`. `capsulectl verify --bundle` prints its lines, and `book report build`
  puts the same lines on the page. A range with any finding never passes.

`ReportFromDerivation` over one response stays for unwindowed holders.

## 6. capsulectl-book

**The store.** `store init` on a new book profile creates `book/shelf.db`, a windowed
`OpenSQLiteShelf` with `Base` = the profile's `log_id`, `--window` defaulting to `month` and
`Since` the current period's start, and prints `window: month (calendar months, UTC), fixed for
this store`. `--window none` keeps today's one JSONL book; an existing JSONL book is not
converted by this item. The help says windows are calendar periods in UTC and cannot change
later.

**What changes inside.** `openedBook.store` becomes an interface over FileStore and the shelf's
store (`--capsule-out` exports through it); `noBookError` looks for `book/shelf.db` or
`book/log.jsonl`; the log-id check at `book.go:224-227` compares with the window's log id;
`checkpointBook` writes the window's log id and keeps witness state per log id (`booklog.go:
910-911, 1020-1024`); the monotonic clock floors at the latest record across the shelf; the
retired-entries check applies to JSONL books only.

**Verbs.**

| Verb | On a windowed store |
|---|---|
| writes (`record`, `seal`, `publish`, `policy change`, `disclose`, `staple`, `backfill`, `request --sent`) | the current window |
| core `cll`, `bundle`, `publish` (capsule-cli's `LogBackend`, `bookhost.go:14-39`) | the current window; capsule-cli is not changed |
| `bundle`, `cll list`, `report`, `doctor` in this plugin | `--window <label>`, default current |
| `respond` | `Shelf.Respond` (§4) |
| `close`, `reconcile` | a period inside the current window; a period in an earlier window is refused with a hint, since its Close would link records in another book (follow-up issue) |
| `migrate` | refused (JSONL books only) |
| new: `book window list`, `book window roll`, `book window chain --from --to --out`, `book report derive --chain <file> --response <id>...` | |

## 7. Owner, doors, ports, PRs

- Owner: evidencebook (root package: shelf, books, chain, verifier, report; `capsulectl/`: the
  CLI). No AAC wire change; capsule-cli is not changed.
- Doors, all engineering's: shelf file version 3 and its window tables; the record types
  `window_open`, `window_close`, `window_chain` and the `window_chain/v1` document. Sherlock2
  confirmed the shapes are engineering's and that none of the open points is a door for Steven
  (§8).
- Not in this item, each a follow-up issue: a Close over an earlier window's period; converting
  an existing JSONL book; the facts migration of a windowed shelf. Rust and Python: "Frozen
  port: ..." issues per Ethan's 2026-11-22 language decision; the Mesh plugin does not call
  this.
- PRs: (1) root package §1-§5 with tests; (2) capsulectl-book §6. Lane tau2-otel-1123
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
  record after the close, an unwitnessed window when witnesses are named, and two chains for one
  base, each named.
- Report: a missing window, a duplicate window, a response pinned to a tree that is not the
  final one, mixed holder keys, a range the chain does not tile, and a range crossing two
  chains, each named; the bundle verifies and the page shows the lines.
- capsulectl-book: `store init` default and print; writes across a boundary under a fixed clock;
  `book window roll` and `chain`; a two-window `report derive` and `capsulectl verify --bundle`;
  witness state per log id; core `bundle` and `cll list` on the current window; `close` on the
  previous month refused with its hint.
