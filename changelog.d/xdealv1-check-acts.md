### Added: `xdealv1.CheckActs`, which holds a settled amount to the checked one (#626)

- x-deal-v1 §8 keeps x-deal-v0's authorization rules 5 and 6 unchanged. Rule 5 requires an act's amount and currency to equal the checked ones where both carry them.
- `CheckAuthorizations` reads records, but a settle's leg travels in its Capsule's `settlement` member, which the record does not carry, so it could not compare them.
- `CheckActs(journal, []ActIn{{Record, Settlement}})` runs the same check and also refuses:
  - a leg whose asset code differs from the check's `currency`;
  - a leg whose settled amount differs from the check's `amount_minor`. A payer leg settles its `amount`, and a payee leg settles `received` plus `receive_fee`.
- Amounts compare as integers at the larger scale, with `amount_minor` at the currency's minor units. A checked amount in a currency with no known minor units is refused.
- `CheckAuthorizations` is unchanged and reads an act with no settlement member.
