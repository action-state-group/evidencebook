<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: an x-deal-v1 payment counts as an earlier payment to its merchant in the rules history (evidencebook#890)

- The history `deal check` gives a pinned rules checker now carries, for each earlier x-deal-v1 act, the payee keyed per profile (`counterparty_profile`) and the counterparty its check sealed, as it already did for x-deal-v0 acts. Before, a rule keyed on an earlier payment to this merchant (Everyday Rules r02 and r06) read every x-deal-v1 merchant as new.
- Tested end to end with capsule-engine go/v0.8.0's `guard check --limits` pinned as a buyer's rules checker on x-deal-v1 deals: within a person's per-order and weekly limits, over each, a limits file that is gone, and a payment checked on the buyer's offer before it is sent (declined and cancelled unsent, or approved, agreed and paid). capsulectl/go.mod names capsule-engine's command as a `tool` for these tests.
