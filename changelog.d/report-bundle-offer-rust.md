<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: an x-deal-v1 copy carries the offer it rests on (evidencebook#1065)

- `ReportBook` has `offer`, the offer's sent pair. `DealStatus::report_with_book` adds it to a keep or counterparty copy when the status's offer pair gives one, and `verify_report_bundle` checks it as Go's `VerifyReportBundle` does.
