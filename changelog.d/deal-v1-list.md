### Added: `deal list` shows x-deal-v1 deals (#626, B4a)

- **The plain line** keeps v0's columns: kind `deal`; the opened time is the offer's; the last record time is your deal book's last record's (the offer's before the book opens); the log id is empty until your book opens.
- **The state** is never a guess:
  - before your book opens, it is the deal's stored state: `offered`, `offer_received`, `agreeing`, `declined` or `cancelled`;
  - once your book opens, it is `settled` or `open` as a report computed now says, or `not_checkpointed` while your book has records after its last checkpoint;
  - any other failure is an error.
  - Listing writes nothing; the computed state is sealed nowhere.
- **`--json`** adds `version`, `side`, `role`, `counterparty_key`, `outcome_as_of` (when the state was computed), and the offer's item.
  - The amount is given only when the offer has exactly one payment row; otherwise it is null, with `payment_rows`.
- **`--since` / `--until`** list an x-deal-v1 deal when any of its records falls in the range, with its state now, and say so in `x_deal_v1_note`.
  - Such deals are not yet in the range's totals, its disclosures or the `say` lines (evidencebook#732).
