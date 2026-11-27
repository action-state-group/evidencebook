<!-- changelog: python/CHANGELOG.md -->
### Added: an x-deal-v1 buyer's intent names the seller by commitment (evidencebook#1031)

- `evidencebook.deal.v1` reads the optional `seller` member of an intent body beside v0's closed body, as Go does. The vendored x-deal-v1 schema and the Go types table carry it. A reader from an earlier release refuses an intent that carries it. A scoped exception to the freeze, for this member only.
