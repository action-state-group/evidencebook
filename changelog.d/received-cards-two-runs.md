### Changed: the deal check seals the run given no cards; `received_cards` go to a second run read for `notice` alone (evidencebook-deal#166)

- `dealprofile/x-deal-v0/PROFILE.md` and `external-check-input-v0.schema.json` state how the deal check treats `received_cards`: the sealed result comes from a run of the checker given no cards, and a second run given them is read only for its `notice`, so nothing a card could change reaches a sealed result. No schema member changes.
