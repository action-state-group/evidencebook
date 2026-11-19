<!-- changelog: python/CHANGELOG.md -->
- **Fixed (`evidencebook.deal.v1`):** `verify_sent`, `verify_book` and `verify_report` refuse a pair whose members are not exactly its own, and `payment_check` reads a settlement member's `leg`, `sealer_role` and `terms_ref` by their exact names, as Go now does (evidencebook#559).
