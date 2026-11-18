### Added: the x-deal-v1 sealed deal report (#461 step 5, PR 5)

- **`xdealv1.BuildReport(books, copy)`** computes one copy of the v1 report at `as_of` (design §9, PROFILE §10). The copy holds:
  - `start`, `settled`, `rows_state`;
  - each agreed row's rowstatus result, `check`, `late` statements and disputes, effective ruling and `relied_on` records;
  - `rests_on`.
- **`xdealv1.StatusProvider.Report(ctx, logID, copy)`** computes the same from a shelf, at the book's checkpoint. `xdealv1.ReportPayload` is its JCS payload, which a host seals with `dealprofile.ReportCapsule`.
- **The card members a page must not misread:**
  - `counterparty_kind` is null ("not stated").
  - `counterparty_proof` is `held`, `not_yet` or `no_layer` (u611).
  - `disputes` has one entry per dispute record.
  - `rulings_unnamed` is shown beside an `open` outcome.
  - `status_fact` says whether the book's last status fact is current.
- **The adjudicator's copy** withholds the rows, the counterparty's prefix, the witnesses, the disputes and the unnamed count.
- **Vectors:** `testdata/deal-v1/report/` and `testdata/deal-v1/settled/`.
- **A payment remedy is not counted yet,** because it needs its own terms leg. A ruled row whose ruling orders a payment reads remedy `not_counted`, never `due` or `not_honoured`, and the deal stays unsettled. `remedy_made` says whether a remedy for the row is held.
- **Snapshot rules:** a report refuses books holding a record timed after `as_of`. From a shelf, it rests on the book's latest checkpoint covering the whole book, and writes nothing.
- **`settled`:** an `agreed` row whose value is `not_met` counts toward it only with a remedy made (Steven's SH-16 ruling).
- **`Shelf.ExistingJournal(ctx)`** is the shelf's journal, or nil when the shelf has none. Unlike `Shelf.Journal`, it never creates one.
