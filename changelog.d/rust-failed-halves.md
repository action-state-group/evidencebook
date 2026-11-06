<!-- changelog: rust/evidencebook/CHANGELOG.md -->

### Added: Rust reconcile pairs a failed interaction's halves by outcome (#400)

- `Half::failed` marks the record of an interaction that did not complete; `from_store` sets it to `false`, since a header carries no outcome, so the caller marks failed halves. `reconcile_halves` now mirrors the Go `ReconcileHalves` (#205, #399): the primary-key join prefers a half of the same outcome, two failed halves are `MATCHED`, a failed half against a served one is `CONFLICTING`, and the content test runs only on two served halves. Adding a public field to `Half` is source-breaking for code that builds it with a struct literal.
