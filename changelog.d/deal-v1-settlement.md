### Added: `deal settlement` on an x-deal-v1 deal (#626, B4d)

- `deal settlement --deal ID` on an x-deal-v1 deal prints, for each row whose owed is an amount:
  - the report row's check word (`passed`, `failed`, `not_recomputed`, …);
  - the payment check's result as the library reads your records now: `check`, `reason`, `checked_over`, `excluded`, and the derived settlement;
  - or `not_run` with why: `not_recomputed`, `no_offer_pair`, `no_acceptor_key`.
- Nothing is computed in the plugin, and nothing is written.
- The output carries `settled_note`: no act carries the person's own signature yet.
- v0's `--leg`, `--object`, `--payer-key` and `--payee-key` are refused, since a v1 deal reads the other side's legs from the records you have received.
- A deal whose book has not opened names `deal agree`.
