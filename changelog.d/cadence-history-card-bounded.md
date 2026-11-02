### Changed: `Cadence.HistoryCard` is bounded like a Book's card (#354)

- `Cadence.HistoryCard` returns the new `ErrOverLimit` when the card would list more than `DefaultMaxAnswerItems` (10,000) ticks, about 35 days at the default 5-minute interval, the same bound a Book's `history_card/1` has. Before, the card grew without bound at about 288 entries a day. A longer-lived cadence staples its `standard_history/1` card (`Cadence.StandardHistoryAnchor`), which counts and does not list ticks. The card's form and `CheckStaple` are unchanged.
