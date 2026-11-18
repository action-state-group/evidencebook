<!-- changelog: python/CHANGELOG.md -->
- `history_card/2` (Evidence Request -03 §8.1.2, evidencebook#507), as Go's: `history_card_positions`
  (P(n, W)), the answer a `Book` builds over `checkpoints` or `full_history`
  (`ResponderPolicy.history_window`, 1 to 64, default 16, not capped by `max_answer_items`),
  `check_history_card_2` (the spec's seven ordered checks, also run on a requester's verification) and
  `staple(book, DERIVATION_HISTORY_CARD_2)`; `check_staple` accepts the card. A `checkpoints` subject
  with a range is answered with the entries and a `consistency_to_anchor` proof, without a card, and verified by a requester. It reads the shared
  vectors in `testdata/history-card-2`. Needs agent-action-capsule 0.17.0.
