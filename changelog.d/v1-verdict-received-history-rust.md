<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: a verdict's `received_history` on x-deal-v1 (evidencebook#666)

- `deal::v1::VerdictBody` reads and writes the optional `received_history` member (`ReceivedHistoryDigests`: `history_sha256`, `input_sha256`), beside v0's closed body, as Go does. The vendored x-deal-v1 schema carries it. A reader from an earlier release refuses a verdict that carries it. This is a scoped exception to the port freeze, for this member only.
