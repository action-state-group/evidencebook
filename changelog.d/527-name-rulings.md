### Added: x-deal-v1 `NameRulings` names a counted ruling's arrival; `BookEntry` (#527)

- `xdealv1.NameRulings(ctx, shelf, dealIDs, at)` seals, in the holder's deal book, a `statement` naming each counted ruling of the deal's adjudicator that the book does not name yet. PROFILE §5 now requires a receiving library to do this. A host calls it after each `Receive` with `ReceiveResult.Changes.Grew`, so a holder's own status reads a ruling whichever way it went.
- `xdealv1.BookEntry(record, prevRecordID)` is the entry that seals an x-deal-v1 deal-book record. Its one payload is the record's JCS bytes, whose digest is the record digest; its record type and deal id are the subject; and past seq 1 it carries a cites link to the record before it.
