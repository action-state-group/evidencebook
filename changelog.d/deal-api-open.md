### Added: package `deal`, the Deal API's `deal.open` (#899)

- New package `github.com/action-state-group/evidencebook/deal` seals an x-deal-v1 deal's opening records on the holder's own shelf. A `Store` opened over a shelf and a handle on its SQLite file offers these calls:
  - `Offer`: an offer or a counter-offer, sent whole.
  - `CancelOffer` and `DeclineOffer`.
  - `Admit`: seals capsule-engine's admission decision, read by `ParseAdmission`, which refuses a decision an agreement cannot cite.
  - `Agree`: the agreement that opens the deal book, returned with its stamp proof.
  - `NoteReceived`: records what `Shelf.Receive` kept of the other side's records.
  - `SealInBook`: the one writer of deal-book records.
- The package also has the read calls `Deal`, `HeldOffer`, `OwnBook`, `NoLayer`, `Repair` and `OfferPair`, and `MarkWritten`. They are there for capsulectl-deal's other verbs and are not a stable API yet.
- Every call can be run again after a failure without sealing a record twice.
- The store keeps its state in the tables capsulectl-deal already uses (`deal_versions`, `deal_sent`, `deal_v1`, `deal_v1_offers`, `deal_v1_admits`), with the same schemas, so an existing deal store opens unchanged. capsulectl-deal moves onto these calls in a following change.
- `xdealv1.SealRuling` now appends through the same place-taking helper as the deal-book writer. Its behaviour is unchanged.
