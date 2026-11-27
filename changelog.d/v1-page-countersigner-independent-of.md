<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: an x-deal-v1 receipt page's countersigner rows carry `independent_of` (#1019)

- `capsulectl deal report --html` now writes each countersigner row of the page's countersigned copy with the `independent_of` list of the directory row that named the signer, as the directory states it (capsule-cli reports it as `signer_independent_of`). The page (capsule-viewer#457) leaves out "Its directory entry does not list it as independent of the parties." only when that list names both parties, and keeps it otherwise. `deal report`'s printed JSON is unchanged.
