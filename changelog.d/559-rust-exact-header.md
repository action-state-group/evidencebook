<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- **Fixed (`deal::v1`, feature `deal`):** `verify_book` refuses a header (or a link, correlation or reproduction in it) carrying a member its type does not have exactly, as Go now does (evidencebook#559). The crate replays Go's `testdata/deal-v1/pair-names` vectors.
