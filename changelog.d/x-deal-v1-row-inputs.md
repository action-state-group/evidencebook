- **x-deal-v1 row inputs and the statements a fact carries** (evidencebook#461 step 5, PR 4c-1).
  - `xdealv1.RowInputs` projects the holder's deal book, the counterparty's counted prefix and the
    adjudicator's case book onto one `rowstatus.Input` per agreed row: the row, the named records by
    `marks`, each side's statements, disputes with `seen_in_time`, withdrawals, rulings and non-payment
    remedies. The payment check and payment remedies follow with `PaymentCheck`.
  - The statements a status fact carries are those `rowstatus` can still read at or after the fact.
    A randomized differential test against `rowstatus.Compute` holds that.
  - Design note §8.1 defines the status value's times: a known time, `null`, or `{"stamp": n}`.
