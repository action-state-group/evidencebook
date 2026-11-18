### Added: x-deal-v1 `Receive` names a counted ruling's arrival; `AfterReceiveProfile`, `Book.AppendAt`, `BookEntry` (#527)

- **`Shelf.Receive` acts on what it changed.** It calls an `AfterReceiveProfile`'s `AfterReceive` before it returns, once both parts committed, and holds only the receive lock while it does.
- **x-deal-v1 names rulings there.** Its `ReceiveProfile` runs `xdealv1.NameRulings` with `Changes.Grew`.
  - It seals, in the holder's deal book, a `statement` naming each counted ruling of the deal's adjudicator that the book does not name yet. PROFILE §5 now requires this, so a holder's own status reads a ruling whichever way it went and no host can skip it.
  - It builds each statement from a fresh read and appends it with `Book.AppendAt`.
  - It leaves a book that is not readable whole for later.
  - It reports the record ids it sealed even on an error.
- **`Book.AppendAt(ctx, seq, entry)`** appends only as the book's next record, and gives `ErrBusy` otherwise. A profile whose record names its own place uses it to build that record from the tail it read.
- **`xdealv1.BookEntry(record, prevRecordID)`** is the entry that seals an x-deal-v1 deal-book record:
  - one payload, the record's JCS bytes, whose digest is the record digest;
  - its record type, with the deal id as the subject;
  - past seq 1, a cites link to the record before it.
