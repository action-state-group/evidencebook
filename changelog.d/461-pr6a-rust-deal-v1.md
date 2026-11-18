<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: `deal::v1`, the x-deal-v1 deal record profile (evidencebook#461 step 5, PR 6a)

- **What it is.** The Rust counterpart of Go's `dealprofile/xdealv1`, behind the `deal` feature. It gives the same bytes and refusals as Go.
- **What it covers:**
  - names and record types;
  - `decode`, `encode`, `digest` and `check`, plus `check_sealed`, `check_agreement`, `check_dispute` and `check_ruling`;
  - `start` and `start_from_books`;
  - `row_inputs`, `build_status` and `resolve_row`;
  - `build_report`, `report_payload` and `settled`;
  - `verify_sent`, `pair` and `terms_leg`;
  - `check_authorizations`, including the check that journal records do not share a nonce, and `new_nonce`.
- **Tests.** Each of these reads the shared vectors: every fixture under `dealprofile/x-deal-v1/fixtures`, and the records, start, status, report, settled, decline and authorization vectors under `testdata/deal-v1`.
- **Schema.** The x-deal-v1 schema is vendored byte for byte, and a test holds the vendored copy equal to the source.
- **`row_status::compute_late`** returns the late statements and late disputes, as Go's `ComputeLate` does, and passes `testdata/row-status/late.json`. `row_status::Date` and `Row` now serialize as Go writes them.
- **Not yet ported:** the shelf-coupled parts. These are the receive profile, the status provider, ruling naming and the payment check, and they come in PR 6b.
