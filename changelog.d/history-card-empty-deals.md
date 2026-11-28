<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: a History card that counts no deal carries `deals: []` (evidencebook#1074)

- `deal standard-history card` wrote the holder's own copy with `"deals": null` in its `x-standard-history/v0` block when no deal had started, under every derivation (`standard_history/1`, `/3` and `/4`), and the History card module capsulectl-book ships refuses a `deals` member that is not an array. It now writes `[]`. An own copy written before this fix with `deals: null` is not read by the module: write it again with `deal standard-history card`. The shared copy carries no `deals` and is unchanged.
