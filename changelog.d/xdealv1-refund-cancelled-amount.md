### Fixed: `xdealv1.CheckActs` holds a refund to its cancel check's `cancelled_amount_minor` (#719)

- A seller's cancel check records the refund as `cancelled_amount_minor` when it states no `amount_minor`, as x-deal-v0's builder does. The settled amount of a refund leg answering such a check is now held to that amount, by the same rule as any other checked amount: a payee leg settles `received` plus `receive_fee`, and must state the fee.
- `amount_minor`, when stated, is still the amount held to.
- Only a cancel check is read this way.
