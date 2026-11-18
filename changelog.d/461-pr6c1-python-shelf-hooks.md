<!-- changelog: python/CHANGELOG.md -->
### Added: the shelf hooks a deal profile runs on (evidencebook#461 step 5, PR 6c-1)

The Go shelf's hooks from #484, #490, #517, #525, #526, #528 and #530, which the Rust crate gained in #536 and #539:

- **Held readers:** `Shelf.held_first`, `held_by_deal` and `held_proofs` (`HeldProof`).
- **Record times:**
  - `Shelf.record_time_by_key`, `received_record_time` and `received_record_time_by_key`;
  - `Shelf.record_stamp` and `received_record_stamp`.
- **Book writes:**
  - `Book.append_at` appends only the next record; any other position is `BusyError`.
  - `Book.resolve_payload` reads a payload as the record's retention state allows.
- **`Shelf.seal_sent`** (`SealedSent`, `SentCapsule`): a journal record and its standalone Capsule. The Capsule is byte for byte Go's `emit.Seal` output, and a test pins the same values the Rust test pins.
- **Book status:**
  - It may be an object with `book_values(StatusInput)`, handed the stamp's tick, the book and the existing journal, and `before_stamp(at)`, run before each stamp under the receive lock.
  - A book whose status raises keeps its last fact and is listed in `TickResult.unstated`; `TickResult.before_stamp` reports a `before_stamp` error.
  - `Shelf.existing_journal` returns the journal without creating it.
- **Receive:** a receive profile may define `after_receive(result, at)`, which runs on every receive. `RecordOutcome.deal_id` names the record's deal.
- **Fixed:** a lifecycle record's statement is read as Go reads it. `retention_state` matches in any case, and a state that is not a string is corrupt.
