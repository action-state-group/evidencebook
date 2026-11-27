<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: an x-deal-v1 buyer's intent names the seller by commitment (evidencebook#1031)

- `deal::v1::IntentBody` (v0's body plus an optional `seller`, `deal::v1::Seller`) reads and writes an x-deal-v1 intent, as Go does, and the vendored x-deal-v1 schema carries the member. A reader from an earlier release refuses an intent that carries it. A scoped exception to the port freeze, for this member only.
