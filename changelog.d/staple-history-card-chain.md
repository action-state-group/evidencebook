### Fixed: `CheckStaple` verifies that a `history_card/1`'s checkpoints form one chain (#344)

- `CheckStaple` now refuses a `history_card/1` staple whose checkpoints are not one chain: each checkpoint signed under
  the first's key, one log, each entry's signed `prev_size` and `prev_root` the one before's, and each consistency proof
  reaching the next commitment. Before, a card of unrelated checkpoints with the right form passed. The shared staple
  vectors pin two refusals (a skipped checkpoint, a consistency proof that does not verify).
