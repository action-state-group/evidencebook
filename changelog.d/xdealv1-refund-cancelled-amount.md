### Fixed: `xdealv1.CheckActs` holds a refund to its cancel check's `cancelled_amount_minor` (#719)

- A seller's outgoing cancel check records the amount as `cancelled_amount_minor` when it states no `amount_minor`, as x-deal-v0's builder does. A refund leg answering such a check is now held to that amount, by the same rule as any other checked amount: a payee leg settles `received` plus `receive_fee`, and must state the fee.
- `amount_minor`, when stated, is still the amount held to. Only a cancel check is read this way.
- With a `fee_minor` (other than 0) beside it, who bears the fee is not recorded, so the refund owed is not known. No amount is held, and the new `ActAuthorization.AmountNotHeld` says so, never as a match (evidencebook#743 tracks recording who bears the fee).
- An act that cites a check of the wrong kind is now told so before any amount is compared.
- PROFILE.md §8 says the same.
- `deal settle` no longer sends a new refund on a cancel check that states a cancellation fee. It used to build the full `cancelled_amount_minor`, which picked who bears the fee. It now refuses and says why (evidencebook#743). A refund already sent completes, or is written again, with the amount its send recorded, and a note says that amount is not checked.
