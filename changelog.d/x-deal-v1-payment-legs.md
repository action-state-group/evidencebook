- `xdealv1`: a payment row's `relied_on` in the v1 report lists each settle leg `PaymentCheck`
  read, so a page shows the payment each leg records (evidencebook#588). `BuildReport` refuses a
  payment result that does not begin with the offer's digest or names a record the books do not
  hold. New report vectors in `testdata/deal-v1/report/`.
- `xdealv1`: the `x-deal-v1` bundle extension (`ReportBundle`, `ReportBook`): the sealed report's
  id, the holder's seq 1 agreement and the records the report's rows rely on, as book pairs.
  `StatusProvider.ReportWithBook` returns a report and its book from one snapshot, and
  `VerifyReportBundle` verifies a report with its extension. Vectors in
  `testdata/deal-v1/report-bundle/`.
