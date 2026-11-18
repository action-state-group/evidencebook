<!-- changelog: python/CHANGELOG.md -->
- **Added (`evidencebook.deal.v1`):** byte for byte with Go (evidencebook#461):
  - sent records and book pairs: `pair` and `verify_sent` for an offer, decline or cancellation sent whole, and `book_pair` and `verify_book` for a deal-book record with its header and payload;
  - `terms_leg` and `payment_rows`;
  - `payment_check`, the recomputed check of a payment row over AAC's settlement legs.

  `verify_book` reads the header as Go's `json.Unmarshal` reads `evidencebook.Header`, from the types table, which now holds it. The package passes `testdata/deal-v1/decline` and `testdata/deal-v1/payment`, including Go's `BookPair` bytes.
