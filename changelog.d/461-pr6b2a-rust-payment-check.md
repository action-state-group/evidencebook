<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: `deal::v1` book pairs and the payment check (evidencebook#461 step 5, PR 6b-2a)

- **`deal::v1::book_pair` and `verify_book`** (`BookRead`, `STAGE_BOOK`), as Go's `BookPair` and `VerifyBook`. A deal-book or case-book record travels as its signed Capsule, the header the Capsule commits and the x-deal-v1 record the header commits as its one payload.
- **`deal::v1::payment_check`** (`PaymentInput`, `STAGE_PAYMENT`, the pass/fail values and the not-run and failure reasons), as Go's `PaymentCheck`. It checks a payment row over the settlement legs both deal books hold, using AAC's settlement verifier, and returns the `PaymentResult` a status value carries.
- **Tests.** Both read the new `testdata/deal-v1/payment/vectors.json`, which Go's `TestPaymentVectors` writes from the Go payment and book pair tests, case for case.
- **Dependency.** The `deal` feature now turns on agent-action-capsule's `settlement` feature.
