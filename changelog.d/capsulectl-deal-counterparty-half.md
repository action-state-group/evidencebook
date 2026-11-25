<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: the other party's half in your own x-deal-v1 copy (#850)

- `deal report --bundle` (your own copy, no `--share`) carries the other party's half when your store holds it: their signed agreement with the stamp proof that covers it, the same thing the History card reads for "stamped by both". A shared copy never carries it.
- `deal verify --bundle` checks it from the file alone and reports `counterparty_half` on the x-deal-v1 entry: `verified` with their key's first 16 hex digits, or `failed`, which makes the copy INVALID. A copy without it has no `counterparty_half` member, as before. It shows their signed, stamped agreement, not that they confirmed a later record.
