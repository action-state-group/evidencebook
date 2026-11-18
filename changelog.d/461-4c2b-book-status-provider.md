### Added: `BookStatusProvider`, handed the book and journal a stamp gives values for (#461 step 5, PR 4c-2b-1)

- A stamp holds the shelf's lock while it asks a status provider for a book's values. So a provider could not open the book or the journal it describes: `Shelf.Open` and `Shelf.Journal` take that lock.
- `BookStatusProvider.BookValues(ctx, StatusInput{N, BookID, LogID, Book, Journal})` hands over the stamp's tick and the handles the shelf already holds. `Journal` is nil before `Migrate`. A provider reads through them (`Query`, `ResolvePayload`, `Sealed`) and through the shelf's lock-free readers, never another `Shelf` method.
- A shelf prefers it to `StampStatusProvider`, which it supersedes, and to `Values`.
