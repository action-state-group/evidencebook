<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- `check_staple` refuses a `history_card/1` staple whose checkpoints are not one chain (signature under the first's key,
  one log, signed `prev_size`/`prev_root`, consistency proofs), as Go's `CheckStaple` does (evidencebook#344).
