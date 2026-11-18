### Added: a status provider is told its stamp, and a record's first covering stamp (#461 step 5, PR 4c-2a)

- `StampStatusProvider` is a `StatusProvider` that also has `StampValues(ctx, n, bookID, logID)`: n is the tick of the stamp it gives values for, the migration's stamp included. A shelf calls it in place of `Values` when its provider implements it, so a status value can name a record its own stamp is the first to cover (`{"stamp": n}`, design §8.1).
- `Shelf.RecordStamp(ctx, bookID, position)` and `Shelf.ReceivedRecordStamp(ctx, bookID, receivedLogID, position)` are the tick of the first fact that covers a record, or that first reaches a received one. They find that fact the same way `RecordTime` and `ReceivedRecordTime` do, and they take no lock, as those do.
