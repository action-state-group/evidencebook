<!-- changelog: python/CHANGELOG.md -->
- `Book` answers `standard_history/1` over `full_history` when given a `StandardHistorySource(holder_kind, inputs)`
  (`Book(..., standard_history=...)`), byte for byte with Go's `StandardHistorySource` (evidencebook#328): to any
  authenticated requester and never a stranger, without a source it is `derivation_unsupported`. `staple` builds a
  `standard_history/1` staple from such a book. `derive_standard_history` is Go's `DeriveStandardHistory`.
