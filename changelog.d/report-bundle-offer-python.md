<!-- changelog: python/CHANGELOG.md -->
### Added: an x-deal-v1 copy carries the offer it rests on (evidencebook#1065)

- `ReportBook` has `offer`, the offer's sent pair. `StatusProvider.report_with_book` adds it to a keep or counterparty copy when `offer_pair` gives one, and `verify_report_bundle` checks it as Go's `VerifyReportBundle` does.
- **Fixed.** `verify_sent` refuses at the sent stage a record its digest cannot be taken of (a float JCS refuses), as Go's `VerifySent` does, instead of raising `FloatInDigestError`.
