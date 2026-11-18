<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- `history_card/2` (Evidence Request -03 §8.1.2, evidencebook#507), as Go's: positions from evidence-request's `history_card::positions`
  (P(n, W)), the answer `respond` builds over `checkpoints` or `full_history`
  (`ResponderPolicy::history_window`, 1 to 64, default 16, not capped by `max_answer_items`),
  `staple::check_history_card_2` (the spec's seven ordered checks) and
  `staple::staple_history_card_2`; `check_staple` accepts the card. A `checkpoints` subject with a
  range is answered with the entries alone (`Subject::CheckpointRange`). It reads the shared vectors in
  `testdata/history-card-2`. Needs evidence-request 0.7.0.
