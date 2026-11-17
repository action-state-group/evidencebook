<!-- changelog: python/CHANGELOG.md -->
- `python/examples/ard-commerce`: the Goose ARD two-seller demo. Maya's agent finds two sellers through an ARD
  catalogue, asks each for its evidence over the sellers' mailboxes, assesses each under a demo admission pack with
  `capsule-engine admission evaluate`, and a presenter's pick (a popup) opens the deal and pays it over Lightning in
  msat. Kestrel's card is a hand-written sample until the sentinel record ships. The Goose node takes
  `CADENCE_OPTIONS` (the cadence's interval) as a class attribute.
