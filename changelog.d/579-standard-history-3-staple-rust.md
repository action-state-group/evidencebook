<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: `check_staple` reads a `standard_history/3` staple (#579)

- `DERIVATION_STANDARD_HISTORY_3` names `standard_history/3`, and `check_staple` accepts a staple of it over `full_history` whose card has exactly the /3 card's members and names its derivation. As for `standard_history/1`, the card's remaining form (SH §6 `card_form`) has no Rust checker yet. No book builds a /3 staple. `standard_history/1` is unchanged.
