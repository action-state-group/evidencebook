<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: a `standard_history/3` or `/4` card is refused when a deal's `agreed_at` is later than its first witnessed time (agent-action-capsule#696)

- `deal standard-history card --derivation standard_history/3` (or `/4`) refuses the card, naming the deal, when a deal's `agreed_at` is later than the record time of the holder's own agreement from a witness the agreement names by more than 300 seconds (`xdealv1.CheckAgreedAt`; agent-action-capsule `spec/standard-history-v0.md` §11.2 "Agreed no later than witnessed"). A deal no named witness timed, or whose agreement names none, is not checked.
