# evidencebook#797: a window on a capsulectl-deal shelf

Status: draft for the plan review (round 0). Written 2026-11-23 CST at main `375e1b8a`. It
reuses #689's window books ([689-window-books.md](689-window-books.md)) and adds no second
window mechanism.

## Problem

#689 fixes a time window on a holder's shelf and splits report-side records into window books
chained by `previous` (§1, §2, §5 there). Its windowed shelf holds window books only
(`shelf.go:68`, `attachWindow` refuses any other log id, `window_book.go:606-607`), refuses the
facts migration (`shelf_stamp.go:165-169`) and so the journal (`Shelf.Journal` needs a migrated
shelf, `shelf.go:264-267`). capsulectl-deal needs all three: one book per deal
(`deal/<id>`, `deal_books.go`), the shelf's journal for every x-deal-v1 journal record
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
// Base is "journal", each window's journal is the window book
// "journal/<label>", and the shelf also holds placed books (§3), the cadence
// and the journal facts. Without it a windowed shelf holds window books only.
Journal bool
```

- It is written with the window in `shelf_window.journal` when the file is created and compared
  on every reopen, as `Grace` is (#807). `Base` must be `JournalLogID` when it is set.
- A file created with it is shelf **version 5**, so a build that knows versions up to 4 refuses
  it rather than opening a deal book as a window book or skipping the placement check.
- It is created already on book facts: the fresh file's `shelf_migration` row is `done` in the
  creating transaction (there is no unmigrated cadence log to carry over), and `Migrate` on it
  returns nil. `Migrate` on a #689 windowed shelf without `Journal` stays refused.
- A store that exists is never windowed: as in #689, a file can gain no policy. A deal holder
  starts a windowed store with a new profile; converting an existing deal store is not this item.

## 2. The journal per window (R3)

- `Shelf.Journal(ctx)` on a journal-windowed shelf rolls (as `Shelf.Current`) and returns the
  current window's journal book. Every journal append (`appendV1Journal`, `sendV1`) therefore
  lands in the window of its commit time, and a closed window takes none (#689 §2 admission,
  #807 grace: in grace a journal window takes only what #807 admits; an x-deal-v1 journal record
  goes to the current window).
- The window books are #689's: `window_open` with `previous`, the shelf's disclosure policy
  re-stated, `window_close` and the final checkpoint; the chain (`Shelf.WindowChain`,
  `VerifyWindowChain`) is unchanged. A year of journal hands over as one chain.
- Each window journal book is a log of kind `journal` (so `Shelf.Open` refuses it, as it refuses
  the journal today) and has its own journal fact in the cadence; a closed window is `Ended`, so it
  leaves the live set once its final checkpoint is witnessed (#689 §2, Cadence).
- **Reading the whole journal.** x-deal-v1 reads the journal in order across deals and time:
  authorization reads intents and confirmed proposals in journal order and refuses an `at` that
  decreases (PROFILE.md §8), and a standing intent can be months old. `Shelf.JournalRecords(ctx)`
  returns every window's records, windows in chain order, each in log order, with the window book
  that holds it; `ResolveJournalPayload(ctx, recordID, digest)` reads a payload from the right
  book. On an unwindowed shelf both read the one journal. capsulectl's readers
  (`journalHolds`, `journalRecord`, `v1DealJournal`, the admit and check paths) move to them, so
  nothing in capsulectl reads `Records(ctx, JournalLogID)` any more. Concatenation in chain order
  keeps the journal's order and its non-decreasing `at`, since a window book admits only records
  timed inside its window.
- **x-deal-v1 text.** PROFILE.md §8 "the journal" gains one sentence: on a windowed shelf the
  journal is its window books read in chain order, and the order rules apply across them.

## 3. Deal books placed in a window (R2)

- `Shelf.Place(ctx, logID) (*Book, Window, error)` makes a new book placed in the current window
  (after a roll): it seals a `window_place` record (`window_place/v1`: `log_id`, `label`) in that
  window's journal book, then records `shelf_window_placed (log_id PRIMARY KEY, label)` in the
  same order #689 uses for its recovery: a place record without the row gets the row on the next
  open; a row is never written without the record. Placing a log id twice returns the first
  placement.
- On a journal-windowed shelf, `Shelf.Open` opens a placed book or a window journal book by its
  kind, and refuses any other log id (`ErrInvalid`, "not placed in a window"), so no deal book
  exists outside a window.
- capsulectl-deal places a deal's book when it seals the agreement (`agree`, the deal book's seq
  1, `PROFILE.md` §3 table) and its results book (`deal/<id>/results`) with it, so a deal is in
  the window its agreement was sealed in and stays there as it runs past the window's end, as the
  demo does today. Later records of the deal go to its own book whatever the date; the deal's
  journal records (checks, approvals, messages) go to the window of their own time.
- The agreement's `committed_at` must fall in the placing window. The deal clock is the shelf's
  `Now` and `Place` rolls first, so this holds unless the record carries an earlier time
  (`inFlight`, a received agreement): then `Place` refuses with `ErrWindowClosed`.
- `window_close` gains `placed`: the sorted log ids placed in the window. The close record is in
  the window's final tree, so the chain alone lists every deal book of a range; a verifier reading
  a chain checks `placed` equals the window's `window_place` records only when it holds the
  window book (the chain carries the close, not the place records). An older reader that ignores
  `placed` loses nothing: `window_close` was holder statement only.
- A deal book is not chained: its own checkpoints and the cadence's facts cover it as today. The
  window says which deals were agreed in it, not that a deal is finished.

## 4. A Terms Pack report over windows (R4)

Unchanged from #689 §4-§5, with the journal windows as the window books: the requester asks each
journal window in the range by the pack's digest, pinning its final checkpoint; the holder
answers through `Shelf.Respond`, which routes by pin and seals the `evidence_request_answered`
record in the current window's journal; the requester runs `RecordWindowChain`,
`ReportFromWindows`, and anyone runs `VerifyReportCoverage`, which names every gap.

- capsulectl-deal: `deal respond` (today `b.Respond` on the book `--book` names,
  `deal_node_request.go:161`) calls `Shelf.Respond` on a journal-windowed shelf and refuses
  `--book`; `deal window list|roll|chain|publish` are the #689 verbs over the journal windows,
  sharing capsulectl-book's code where it already exists (#828, #837) rather than copying it.
- The requester side and the coverage verb stay capsulectl-book's (`book report derive --chain`,
  `book report coverage`), since a requester's report lives in its own book.
- What a Terms Pack sees on a deal shelf is the journal: offers, admissions, intents, checks,
  approvals, verdicts and the journey overlay (messages, claims, evidence citations). Deal-book
  records (agreement, perform, settle) are not in a window's answer; the per-deal x-deal-v1 report
  (`deal report`, PROFILE.md §10) stays the report on a deal book.

## 5. capsulectl-deal

- `deal init --window <day|week|month|quarter|year|none> [--grace <duration>]` creates the
  store windowed (journal mode, `Since` the current period's start). Default: `none` in this item,
  so no existing script changes; the printed line when set is #689's ("window: month (calendar
  months, UTC), closes 7 days after it ends, fixed for this store"). Whether the default should
  become `month` for new deal profiles is Sherlock2's what (below).
- `deal set-version x-deal-v1 --migrate` on a windowed store finds it already migrated.
- A windowed store has no legacy (x-deal-v0) deal: v0 verbs that open a legacy log are refused on
  it with a hint, because their logs are not placed books.

## 6. Owner, doors, ports, PRs

- Owner: evidencebook (root package: policy member, version 5, `Place`, journal reads;
  `capsulectl/internal/deal`: the verbs; `dealprofile/x-deal-v1/PROFILE.md`: one sentence in §8
  and the placement in §3). No AAC wire change; capsule-cli unchanged.
- Doors, engineering's unless Sherlock2 says otherwise: shelf version 5 and its columns; record
  types `window_place`, `window_place/v1`; `window_close.placed`. User-visible behavior for
  Sherlock2's what: `deal init --window`, its default and printed line, and the windowed journal's
  effect on what a Terms Pack answer holds (§4 last bullet).
- Not in this item: converting an existing deal store; Rust and Python ("Frozen port: ..."
  issues, Ethan 2026-11-22); the Mesh plugin; the Goose node.
- PRs, after #828, #836 and #837 merge (they touch the window code and docs): (1) root package
  §1-§3 with tests; (2) capsulectl-deal §4-§5 and the profile text. Then an evidencebook-skills
  issue (or note on #219) for `window.py` to switch to one windowed deal profile.

## 7. Tests

- Policy: `Journal` written and compared; reopen without it, or with another `Base`, refused;
  version 5; created migrated; `Migrate` nil on it and still refused on a #689 windowed shelf.
- Journal: an append before and after a boundary lands in each window; `JournalRecords` across
  three windows in order, including an empty window; `ResolveJournalPayload` from a closed window;
  a crash between a window's open and its first journal record.
- Place: placed in the current window; `Open` of an unplaced id refused; a place record without
  its row recovered on reopen; placing twice returns the first window; an agreement timed before
  the current window refused; `window_close.placed` lists the window's deals; an empty window's
  `placed` is empty.
- Report: two journal windows answered by pack digest, one record answered in a later window,
  coverage passing; a missing window named.
- capsulectl-deal: `deal init --window month` prints the line; offer, agree, check and perform
  across a month boundary under a fixed clock: the deal stays in its window, its check lands in the
  next window's journal, authorization reads the intent from the earlier window; `deal window
  chain` over the two windows verifies; `deal respond` routes by pin.
