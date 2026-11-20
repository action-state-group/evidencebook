### Added: a `standard_history/3` staple is read (#579)

- `DerivationStandardHistory3` names `standard_history/3` (SH §11), and `CheckStaple` accepts a staple of it over `full_history`, checking the card with the `standard_history/3` form (`standardhistory.CheckAs`). A book still builds no /3 card, since it holds no shelf facts: the host counts it from a shelf, and `Staple` refuses the token. `standard_history/1` and `/2` are unchanged.
