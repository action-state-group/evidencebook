# evidencebook#689: report-side books split by a time window, with continuity

Status: draft for the plan review (Kimi and one Claude stand-in for Codex); Sherlock2's what
answer is in §7. No code until the plan review is answered. Written 2026-11-23 CST
at main `7499da43`.

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

## Requirements this note meets

| # | Requirement (#689) | Where |
|---|---|---|
| R1 | the window is configurable per holder and fixed up front in the shelf | §1 |
| R2 | one window's book links to the next; a span checks as unbroken, no gap or fork | §2, §4 |
| R3 | a record's inclusion proof is bounded by its own window's book | §3 |
| R4 | the contract-report bundle cites the window books; a verifier checks coverage of the range | §4 |

## 1. The window policy is the shelf's

`ShelfConfig.Window` (new, `*WindowPolicy`):

```go
// WindowPolicy splits a shelf's records into one book per time window.
type WindowPolicy struct {
    Period WindowPeriod // "day", "week" (ISO, Monday), "month", "quarter", "year"
    // Base is the log id every window book's id starts with: "<base>/<label>".
    Base string
}
```

- Windows are calendar periods in UTC, half-open `[start, end)`. Labels: `2026-11-23`,
  `2026-W48`, `2026-11`, `2026-Q4`, `2026`. A label alone gives the window's bounds, so a
  reader recomputes them and never trusts a stated end.
- The policy is written once, in a new table `shelf_window (id = 1, period, base, since)`, in
  the transaction that creates the shelf; `since` is the first window's start. A shelf
  opened with a different policy, or with none when the file has one, is refused
  (`ErrInvalid`). A shelf that already holds books and no policy cannot gain one: records are
  never moved between books (R1). A windowed file carries a shelf version older builds and the
  frozen Rust and Python shelves refuse, so no writer ignores the window.
- With no policy a shelf is unchanged: the deal shelf and Goose are not windowed.

## 2. Window books and continuity

A windowed shelf has exactly one writable book, the current window's.

- `Shelf.Current(ctx) (*Book, Window, error)` opens the book of the window holding the shelf's
  `Now`, first closing every earlier window that is still open, in order (below).
  `Shelf.WindowBook(ctx, label)` opens any window's book for reading. `Shelf.Windows(ctx)`
  lists them.
- A window book refuses a record whose `committed_at` is not inside its window
  (`ErrWindowClosed`; a new `Config.Window` bound the book checks where it assigns
  `committed_at`). A caller holding a book across the boundary gets that error and asks
  `Current` again.
- **Open record** (`record_type: "window_open"`, the book's first record, `producer_claim`):

  ```json
  {"record_version": "window_open/v1", "period": "month", "label": "2026-12",
   "start": "2026-12-01T00:00:00Z", "end": "2027-01-01T00:00:00Z",
   "previous": {"log_id": "acme/2026-11", "checkpoint": "<sha256 of its final checkpoint>",
                "tree_size": 412}}
  ```

  `previous` is absent only in the shelf's first window (`since`).
- **Close record** (`record_type: "window_close"`, the book's last record): the label and the
  window's counts by record type, then the book's **final checkpoint**, taken right after it
  (tree size = the close record's seq). Nothing is appended after it.
- An empty window still gets its book, with its open record, close record and final
  checkpoint, so the run has no hole.
- Continuity is one hash link per boundary: the next window's open record names the previous
  window's final checkpoint by digest. Both are signed by the shelf's one checkpoint key and
  record key (`ShelfConfig.Signer`, `CheckpointKey`), so a fork (two different final
  checkpoints, or two open records, for one label) is two signed statements by one key, which a
  verifier that sees both reports as equivocation.

- **A cleared history starts a new chain, not a gap.** Steven's clearing ruling (ops
  `proprietary` [muse-share-bound-history] DECISION, 2026-10-28 22:43 PT, Buzz 054513b2 event
  a01109dd) has a clear start a new stream under a new log id, while truncating the old log
  stays a consistency failure. A cleared shelf's new base gets a first window with no
  `previous`, and a verifier reports "a new chain began at <label>", never "window missing".
  The one key still links the two chains, as EB -03 says of clearing.

The cadence (§2.6 of `254-shelf.md`) needs no change: window books are ordinary books, and a
closed one leaves the live set once its final checkpoint is witnessed.

## 3. Proofs stay inside their window (R3)

The holder answers a request from one window book. `Shelf.Respond(ctx, request, opts)` routes a
request by its coverage:

- `expected_pin` names a checkpoint digest: the book that issued that checkpoint answers
  (`ErrNotFound` → `coverage_unsatisfiable` when no window book did).
- `min_freshness` is answered by the current window's book.

The Evidence Request and Evidence Bundle wire forms do not change: a requester asking about a
past window pins that window's final checkpoint, which it reads from the window chain (§4).

## 4. The window chain and the report's coverage (R2, R4)

**The chain document** (`window_chain/v1`), which the holder exports with
`Shelf.WindowChain(ctx, fromLabel, toLabel)` and hands to the requester or auditor:

```json
{"v": 1, "base": "acme", "period": "month", "from": "2026-01", "to": "2026-12",
 "windows": [{"label": "2026-01", "log_id": "acme/2026-01",
              "open": <the open record>, "open_proof": <inclusion proof at the final checkpoint>,
              "close": <the close record>, "close_proof": <inclusion proof>,
              "final_checkpoint": <signed checkpoint>}, ...]}
```

`VerifyWindowChain(chain, checkpointKey)` checks, for each window: the final checkpoint's
signature and log id; the open record at leaf 0 and the close record at the last leaf under it;
the labels consecutive under the period with no gap; each open record's bounds equal to its
label's; each `previous` equal to the prior window's final checkpoint digest and size. A year
handover is this document alone (Q2 below).

**The report.** The requester asks each window book in the reported range (pin its final
checkpoint), records each response as today, then:

- `Book.RecordWindowChain(ctx, chain, reported Period)` seals a `window_chain` record whose
  payload is the chain and whose statement is the reported range and the chain's digest. It
  refuses a chain that fails `VerifyWindowChain` or does not tile the range.
- `Book.ReportFromWindows(ctx, chainRecord, responses, evaluate, opts)` is
  `ReportFromDerivation` over several responses: every response must answer the same pack at
  one window's final checkpoint from the chain; every window in the range must have exactly
  one; each Result claim cites the response records it draws from and the chain record. The
  bundle (with `contract-report/v1`) carries them all by closure, payloads included.
- `VerifyReportCoverage(bundle)` (and `capsulectl verify` through it) checks: the chain record
  verifies; the windows tile the reported range; each response's anchor is its window's final
  checkpoint and its proofs verify there; no window in the range lacks a response.

What it does not show: the chain shows which windows exist and that each was answered, never
that an answer holds every row of its window (the limit #606 already states: no proof shows
that no row was left out). Listing an empty window reveals only that nothing was recorded in
it.

`ReportFromDerivation` over one response stays as it is for unwindowed holders.

## 5. capsulectl-book

- `store init` on a new book profile creates `book/shelf.db`, a windowed `OpenSQLiteShelf`
  with `Base = profile log_id` and `--window` defaulting to `month` (§7), and prints the window
  it fixed ("window: month (calendar months, UTC); fixed for this store"). `--window none` keeps
  today's one JSONL book. An existing JSONL book is not converted by this item.
- `openBook` on a windowed profile returns `Shelf.Current`; every write command lands in the
  current window. `--window <label>` on the read commands (`bundle`, `close`, `report`, `cll
  list`) selects another window's book.
- New: `book window list`, `book window chain --from --to --out`, and `book report derive
  --chain <file> --response <id>...` (several), with `capsulectl verify --bundle` checking
  coverage. `book respond` routes through `Shelf.Respond`.
- Each window book checkpoints and goes to the witness under its own log id.

## 6. Owner, doors, ports

- Owner: evidencebook (the root package for the shelf, books, chain and verifier;
  `capsulectl/` for the CLI). No AAC wire change.
- Doors, all engineering's: the shelf file's window table and version (a storage format); the
  record types `window_open`, `window_close`, `window_chain` and the `window_chain/v1` document
  (a format in a public repository that carries out the ruling and adds no product meaning).
  Sherlock2 confirmed the shapes are engineering's and that none of the open points, the
  default window and the handover included, is a door for Steven (§7).
- Rust and Python: "Frozen port: ..." issues per Ethan's 2026-11-22 language decision. The
  Mesh plugin does not call this.
- Lane tau2-otel-1123 (evidencebook-skills#219) cuts books per window by hand today; it moves to
  `Shelf.Current` and `WindowChain` when this lands.

## 7. Sherlock2's answer (the what)

Asked once, twin first (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=69b648984d3ae71697f4a4870596d770f99833aebf3817fee2c243da71c3bbe1).
Sherlock2 answered that none of the three is a door for Steven and agreed to all three, with
conditions (buzz://message?channel=0d760ea3-529d-4dec-b309-24184967f8e0&id=2c569a101cf2f0b0a251199ddd0bdd227b2ed313138800332c4e99f1fa8809a5&thread=69b648984d3ae71697f4a4870596d770f99833aebf3817fee2c243da71c3bbe1).
This note meets each:

| Condition | Where |
|---|---|
| a one-year handover verifies with nothing but the window books | §4: the chain document alone |
| a dropped or replaced window shows as a break | §2: each open cites the previous final checkpoint |
| where the agreement names a witness, the final checkpoint is the witnessed one | §4: `VerifyWindowChain` takes witness ids; with them, each final checkpoint must carry a verifying receipt, else the window reads "not witnessed" and the chain fails |
| the verifier reports a gap by name, never passes a silent hole; the page shows the same line | §4: findings `window <label> has no answer`, `windows do not tile <from>..<to>`, `chain breaks at <label>`; `report build` prints the verifier's lines |
| calendar months in UTC, and the help says so | §1; `store init --window` help |
| the default is printed when a store is created | §5 |

Q3's answer: **month** is the default for a new report-side store; existing books stay as they
are until migrated.

## 8. Tests

- Library: policy fixed (reopen with another policy, adding one to a shelf with books, both
  refused); window boundaries for each period (month ends, leap day, ISO week 53, year); a
  write after the boundary closes the window and opens the next; empty windows filled in
  order; a record outside its window refused; a crash between the close record and the next
  open record recovered on reopen; `Respond` routed by pin and by freshness; chain verify with
  a gap, a wrong `previous`, a changed bound, a record after the close and a forked label each
  refused; report coverage with a missing window, a duplicate window, a response pinned to a
  non-final checkpoint and a range the chain does not tile each refused.
- capsulectl-book: `store init --window month`, writes across a boundary under a fixed clock,
  `book window chain`, a two-window `report derive` and `capsulectl verify --bundle`.
