<!-- changelog: python/CHANGELOG.md -->
- `Cadence.history_card` raises the new `OverLimitError` when the card would list more than `DEFAULT_MAX_ANSWER_ITEMS` (10,000) ticks, as Go's `Cadence.HistoryCard` does (evidencebook#354).
