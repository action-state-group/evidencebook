### Added: `deal list` shows x-deal-v1 deals (#626, B4a)

- **The plain line** keeps v0's columns: kind `deal`; the opened time is the offer's; the last record time is your deal book's last record's (the offer's before the book opens); the log id is empty until your book opens.
- **The state:**
  - before your book opens, it is the deal's stored state: `offered`, `offer_received`, `agreeing`, `declined` or `cancelled`;
  - once it opens, it is computed now, and never sealed: `settled` or `open`, as a report says, or `not_checkpointed` while your book has records after its last checkpoint;
  - any other failure is an error, not a state.
- **Notes under the table** (on stderr, so the tab-separated lines stay a table), and in `--json` as `settled_note` and `checkpoint_note`:
  - beside `settled`: "settled: computed from the agent's records; no act carries the person's own signature yet (agent-action-capsule#627)";
  - beside `not_checkpointed`: "the next cadence tick covers it (about every <interval>), then list again".
- **Listing writes nothing.**
- **`--json`** adds `version`, `side`, `role`, `counterparty_key`, `outcome_as_of` (when the state was computed), and the offer's item.
  - The amount is given only when the offer has exactly one payment row; otherwise it is null, with `payment_rows`.
  - The summary line says "counterparty: no name recorded (key only)".
  - An x-deal-v1 deal has no `say` line yet (evidencebook#732).
- **`--since` / `--until`** list an x-deal-v1 deal when any of its records falls in the range, with its state now, and say so in `x_deal_v1_note`. Such deals are not yet in the range's totals or its disclosures (evidencebook#732).
