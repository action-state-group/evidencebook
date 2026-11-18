- **`Book.Sealed(ctx, recordID)`** returns a record as it was sealed: its canonical header, its
  Capsule (without signature) and its Producer Envelope, as the store keeps them, in the caller's own
  copies. They are what another party verifies the record by on its own. The x-deal-v1 profile sends
  a deal-book record to the counterparty in this form (evidencebook#461 step 5, design note §8.3).
