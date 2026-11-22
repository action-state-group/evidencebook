### Added: `capsulectl deal check` and `deal note --kind approval` on an x-deal-v1 deal (#626, B1b part 2)

- `deal check` on an x-deal-v1 deal runs the same check as on an x-deal-v0 deal, with the same card, rules and materiality, over the deal's local steps. It seals the x-deal-v1 journal `check` (with a fresh nonce) and its `verdict`, and on a pass the automatic `standing_intent` approval, each with the deal's id.
- A pay check on an x-deal-v1 deal records `payment_ref`, the reference fixed before paying that the payment will be made under (a Lightning payment hash, an ACP order id, the x402 authorization nonce), and is refused without one: the payer leg cites the check that recorded it. An x-deal-v0 deal refuses `payment_ref`.
- The pinned rules checker reads an x-deal-v0-shaped record of the check, rebuilt in memory from the local step and never stored, so the same input gives the same rules input on both versions.
- `deal note --kind approval` on an x-deal-v1 deal needs `--shown-card` for the user's answer to a check, so every approval carries the card commitment the authorization reads.
- A check refuses when the deal's journal holds a check, verdict, approval, intent or journey record that has no local step, since such a record would reach the authorization but not the card.
- An x-deal-v1 deal's disclosures count in the cross-deal counterparty memory as an x-deal-v0 deal's do.
