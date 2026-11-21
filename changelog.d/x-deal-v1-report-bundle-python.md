<!-- changelog: python/CHANGELOG.md -->
- `evidencebook.deal.v1`: `verify_report_bundle` checks a sealed v1 report with the x-deal-v1
  bundle extension that carries it (`ReportBundle`, `ReportBook`), as Go's `VerifyReportBundle`
  does, replaying `testdata/deal-v1/report-bundle/vectors.json`; `StatusProvider.report_with_book`
  gives a report and its book pairs from one snapshot (evidencebook#598). The x-deal-v1
  differential check compares the verifier with Go's.
