<!-- changelog: python/CHANGELOG.md -->
### Added: `check_staple` reads a `standard_history/3` staple (#579)

- `DERIVATION_STANDARD_HISTORY_3` names `standard_history/3`, and `check_staple` accepts a staple of it over `full_history`, checking the card with `agent_action_capsule.standard_history.check_card(..., derivation=...)`. A book still builds no /3 card, so `staple` refuses the token. `standard_history/1` is unchanged.
