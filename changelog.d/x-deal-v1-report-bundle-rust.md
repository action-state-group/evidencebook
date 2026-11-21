<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- `deal::v1`: `verify_report_bundle` checks a sealed v1 report with the x-deal-v1 bundle extension
  that carries it (`ReportBundle`, `ReportBook`), as Go's `VerifyReportBundle` does, replaying
  `testdata/deal-v1/report-bundle/vectors.json`; `DealStatus::report_with_book` gives a report and
  its book pairs from one snapshot (evidencebook#598). The x-deal-v1 differential check compares
  the verifier with Go's.
- `deal::v1`: the text check refuses a number that overflows a float64, as Go's `checkText` does
  (`check`, `verify_sent`, `verify_book`, `verify_report` and the extension), at the
  canonicalization stage; it read such a number before and failed later or not at all.
