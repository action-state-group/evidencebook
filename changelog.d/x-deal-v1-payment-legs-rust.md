<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- `deal::v1`: a payment row's `relied_on` in the v1 report lists each settle leg the payment
  check read, as Go does (evidencebook#588); `build_report` refuses a payment result that does
  not begin with the offer's digest or names a record the books do not hold.
