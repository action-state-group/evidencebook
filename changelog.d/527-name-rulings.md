### Added: x-deal-v1 `Receive` names a counted ruling's arrival; `AfterReceiveProfile`, `Book.AppendAt`, `BookEntry` (#527)

- **`Shelf.Receive` acts on what it took.** On every call, a duplicate included, it calls an `AfterReceiveProfile`'s `AfterReceive(ctx, result, at)` before it returns, once the parts committed (on a proof part's store error too, with the record part's result), and holds only the receive lock while it does. `RecordOutcome.DealID` is the deal the profile read the record as.
- **x-deal-v1 names rulings there.** Its `ReceiveProfile` runs `xdealv1.NameRulings` with `Changes.Grew` and the received record's deal, so a ruling sent again retries a naming that failed.
  - It seals, in the holder's deal book, a `statement` naming each counted ruling of the deal's adjudicator that the book does not name yet. PROFILE §5 now requires this, so a holder's own status reads a ruling whichever way it went and no host can skip it.
  - It builds each statement from a fresh read and appends it with `Book.AppendAt`.
  - It never writes to a book that is not wholly x-deal-v1 (a withheld payload, a lifecycle record); the status value counts that book's ruling unnamed.
  - It reports the record ids it sealed even on an error.
- **The next stamp retries a failed naming.** A `BeforeStampProvider`'s `BeforeStamp(ctx, at)` runs before each stamp, from `Stamp` and from a due `Tick`, outside the shelf's lock and holding the receive lock; its error is `TickResult.BeforeStamp` and the stamp is still taken. `xdealv1.StatusProvider` implements it with `xdealv1.NameAllRulings(ctx, shelf, at)`, `NameRulings` over every v1 deal book, so a ruling that was a deal's last record is named without the host.
- **`rulings_unnamed`** in the x-deal-v1 status value counts the counted rulings of the deal's adjudicator held and named by neither book (no ids; left out at zero), so an `open` outcome never hides one silently.
- **`Book.AppendAt(ctx, seq, entry)`** appends only as the book's next record, and gives `ErrBusy` otherwise. A profile whose record names its own place uses it to build that record from the tail it read. A stored record awaiting its substrate append takes its place first. Every x-deal-v1 deal-book write, a host's own included, uses it, rebuilt on `ErrBusy`.
- **`xdealv1.BookEntry(record, prevRecordID)`** is the entry that seals an x-deal-v1 deal-book record:
  - one payload, the record's JCS bytes, whose digest is the record digest;
  - its record type, with the deal id as the subject;
  - past seq 1, a cites link to the record before it.
