<!-- changelog: python/CHANGELOG.md -->
### Added: a verdict's `received_history` on x-deal-v1 (evidencebook#666)

- `evidencebook.deal.v1` reads the optional `received_history` member of a verdict body (with its `alone_deny`) beside v0's closed body, as Go does. The vendored x-deal-v1 schema and the Go types table carry it. A reader from an earlier release refuses a verdict that carries it. This is a scoped exception to the freeze, for this member only.
