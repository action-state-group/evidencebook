### Fixed: `xdealv1.BookEntry` seals header `record_type` `deal_step`

- Since v0.18.0, `BookEntry` (and so `Receive`'s ruling naming) sealed a deal-book record's header with the payload's own x-deal-v1 type (`statement`, `settle` and so on). Design §3.1 and v0 say `deal_step`, with `epistemic_type` `producer_claim`; the payload's `record_type` says which x-deal-v1 type it is. No reader checks the header's record type, so books already sealed stay readable.
