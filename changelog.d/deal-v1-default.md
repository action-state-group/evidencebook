### Changed: x-deal-v1 is the default deal version (#626, B5)

- **New deals are x-deal-v1.** With no `deal set-version`, `deal offer` starts a deal, and `deal init` makes a new store in the layout x-deal-v1 needs.
- **x-deal-v0 is the legacy version.** It is frozen and kept only for a counterparty that runs x-deal-v0 alone, and will be removed once those have moved. `deal set-version x-deal-v0` selects it.
  - Without it, `deal open` and `deal sale new`, which start an x-deal-v0 deal or sale, refuse and say so. On x-deal-v1, a sale to several buyers is one offer from each buyer, one agreement, and `deal offer decline` to the others.
- **Deals already open keep their version.** An x-deal-v0 deal finishes under x-deal-v0, and its report, export and bundle read as before.
- **An older store is never moved silently.** `deal offer` on it says to run `deal set-version x-deal-v1 --migrate` once (one-way; a backup is kept), and open deals keep working after the move.
- **The History card given on a signed ask** (`deal standard-history card`) now counts x-deal-v1 deals by default (`standard_history/3`). `standard_history/1` still counts your x-deal-v0 deals when you ask for it by name, and the card says so when the store holds any. On an older store that holds no x-deal-v1 deal, it names both ways on. The stapled card on offers is unchanged (`history_card/2`).
- **The canary keeps ticking.** `deal canary run`'s scripted deal is an x-deal-v0 purchase, so a canary profile with no version setting is set to x-deal-v0 on its next run, which says so.
- **The deal skill's demo** sets x-deal-v0 until the skill moves to x-deal-v1.
