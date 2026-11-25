<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: `deal list --json` lists an x-deal-v1 deal's own payment legs (evidencebook#920)

- Each x-deal-v1 deal with a book carries `own_legs`: the latest own leg of each payment (`leg`, `status`, `payment_ref_type`), read from the deal book without a checkpoint. A leg a later own leg supersedes, and a refund or remedy leg, are left out; no reference value, amount or counterparty is listed. The Goose loop node counts its settlements on a rail from it (`loop_settlement_disclosure`, for evidence-network/loop step 06), so legs sealed before that tool existed are counted too. The plain table is unchanged.
