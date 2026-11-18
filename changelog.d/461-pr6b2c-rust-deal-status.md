<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: a v1 deal book's status, ruling naming and report on a shelf (`deal` and `shelf` features; evidencebook#461 step 5, PR 6b-2c)

- **`deal::v1::book_entry`** gives the entry that seals a record in its deal book: one payload, header record_type `deal_step`, the deal id as the subject, and a cites link to the record before it.
- **`deal::v1::name_rulings` and `name_all_rulings`** seal, in the holder's deal book, a statement naming each counted ruling of the deal's adjudicator that the book does not name yet (PROFILE §5; evidencebook#527).
  - The x-deal-v1 `ReceiveProfile` now names rulings on every receive (`after_receive`).
  - A book that is not readable whole is never written to.
  - A failure returns, beside its error, the record ids sealed before it (`NamingError`).
- **`deal::v1::DealStatus`** is Go's `xdealv1.StatusProvider` as a `shelf::BookStatus`. It gives a v1 deal book's profile, link value and status value at each stamp, and names rulings before each stamp.
  - `DealStatus::report` is the report of a deal book from the shelf, resting on its latest checkpoint.
  - An optional `OfferPair` lets a payment row's check run.
