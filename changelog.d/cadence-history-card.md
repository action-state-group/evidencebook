### Added: `Cadence.HistoryCard` builds the profile's `history_card/1` (#341)

- `Cadence.HistoryCard(ctx, n)` returns the profile's `history_card/1` over the cadence log as a `Staple`: the cadence
  checkpoint of every finished tick through tick `n` (the newest finished tick when `n` is 0), each with its consistency
  proof from the one before, as of tick `n`'s checkpoint and signed by the shelf's `Signer`. `CheckStaple` accepts it for
  `KeyID` of that signer. A host that holds a shelf and no per-profile book (capsulectl-deal) staples this card.
