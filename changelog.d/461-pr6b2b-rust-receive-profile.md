<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: the x-deal-v1 holder on a shelf (`deal` and `shelf` features; evidencebook#461 step 5, PR 6b-2b)

- **`deal::v1::ReceiveProfile`** is x-deal-v1's `shelf::ReceiveProfile`, as Go's `xdealv1.ReceiveProfile` is:
  - **`read`** takes a book pair or a sent pair. A sent record is held in `x-deal-v1/sent/<record digest>` (`SENT_LOG_PREFIX`).
  - **`admit`** applies EBL's push rule: offers from any key; a decline from the key the offer was sent to; a cancellation from the offer's signer; a ruling from our adjudicator; and a deal-book record from the deal's counterparty, on a log whose first record is its agreement of this deal.
  - **`answered`** says whether our side answered a held offer.
  - **`proof_sender`** relates a stamp proof to its sender, refusing a fork at the genesis or two candidates.

  Give the profile its shelf with `set_shelf` once the shelf is open. The profile holds a `WeakShelf`, so the two do not keep each other alive.
- **`ReceiveProfile::can_act`** (`CanActInput`) is the first-act coverage gate: a held stamp proof must cover the counterparty's agreement. **`gate_act`** (`ActGateInput`, `ActGate`, `GATE_NO_PROOF`, `GATE_NO_LAYER`) holds a first perform or payer settle until that is so, and lets it through unguarded when the counterparty runs no layer.
- **`ShelfBook::resolve_payload`** reads one payload of a record as the record's retention state allows. A `WITHHELD` or `DELETED` record's payload is the new `ShelfError::Unavailable`.
- **`Shelf::downgrade`** and **`WeakShelf`** give a handle on a shelf that does not keep it alive.

Breaking for code that matches on every `ShelfError` variant: `ShelfError` gains `Unavailable`.
