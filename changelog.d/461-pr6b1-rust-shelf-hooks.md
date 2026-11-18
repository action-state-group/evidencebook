<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: the shelf hooks a deal profile runs on (`shelf` feature; evidencebook#461 step 5, PR 6b-1)

The Go shelf's hooks from #484, #490, #517, #525, #526, #528 and #530, which x-deal-v1's shelf parts (PR 6b-2) need:

- **`ShelfBook::append_at(seq, entry)`** appends only as the book's next record, counting a record stored and awaiting its log append; any other position is `ShelfError::Busy`.
- **Held readers.** `Shelf::held_first`, `Shelf::held_by_deal` and `Shelf::held_proofs` (`HeldProof`) read received records and stamp proofs without the shelf lock.
- **`Shelf::seal_sent(entry, members, action_id)`** appends a journal record and seals its standalone `fyi` Capsule over the same canonical JSON bytes under the caller's `action_id`, with `members` carried in that Capsule only. A record stored whose log append failed comes back in `SealedSent` with its Capsule and the error. `seal::seal_payload` seals such a Capsule.
- **Record times by key and for received records.** `Shelf::record_time_by_key`, `received_record_time` and `received_record_time_by_key`, and the stamps that give a record its time, `Shelf::record_stamp` and `received_record_stamp`.
- **`BookStatus` is a trait** in place of the closure type (Go's four status interfaces in one): `values` is handed a `StatusInput` (the stamp's tick, the book's ids, the book and the journal if the shelf has one), and `before_stamp` runs before each stamp under the receive lock. A cadence with a book status therefore takes the receive lock at each stamp, so a `ReceiveProfile` callback must not call `Cadence::stamp` or `tick`. A closure of a book id and log id still is one. `Shelf::existing_journal` returns the journal without creating it.
- **One book's status error no longer fails the stamp.** That book keeps its last fact and is listed in `TickResult::unstated`; `TickResult::before_stamp` reports a `before_stamp` error.
- **`ReceiveProfile::after_receive`** runs on every `Shelf::receive` call once its parts committed; its error is `ReceiveError::after`. `RecordOutcome::deal_id` is the deal the profile read the record as.

Breaking for a caller that builds these structs literally: `TickResult` gains `unstated` and `before_stamp`, `ReceiveError` gains `after`, and `RecordOutcome` gains `deal_id`. `CadenceConfig::status` is `Option<Arc<dyn BookStatus>>`; a closure passed there needs its parameter types written (`|book_id: &str, log_id: &str|`).
