### Added: `deal settlement` on an x-deal-v1 deal (#626, B4d)

- `deal settlement --deal ID` on an x-deal-v1 deal prints, for each row whose owed is an amount, in the offer's order:
  - `report_check`: the report row's verdict (`passed`, `failed`, `not_run`, `not_recomputed`);
  - `payment.check`: the payment check on the legs, as the library reads your records now (`pass` or `fail`, or `""` with a `reason` when the check ran without a verdict, such as legs not yet agreed or several payments; with `checked_over`, and, when present, `excluded` and the derived settlement);
  - or `not_run`, when the check did not run, with a plain `not_run_note`:
    - `no_offer_pair`: this store holds the offer but not its signed copy, so the payment check cannot run here; receive the offer file again (`deal receive`) to keep it;
    - `no_acceptor_key`: your agreement names no key for the accepting side (the other side runs no layer), so the payment check cannot run on this deal;
    - `not_recomputed`: the row is not recomputed, as its verdict says.
- `rows_state` says whether the rows are listed. Before the deal starts, `rows_note` says so instead of an empty list.
- Nothing is computed in the plugin, and nothing is written.
- The output carries `settled_note`: no act carries the person's own signature yet.
- v0's `--leg`, `--object`, `--payer-key` and `--payee-key` are refused: a v1 deal reads the other side's legs from the records you have received.
- A deal whose book has not opened names `deal agree`.
- The checkpoint message of `deal report`, `deal list` and `deal settlement` now ends "then run the command again".
