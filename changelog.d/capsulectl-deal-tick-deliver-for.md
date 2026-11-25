<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: `deal tick` bounds the time it spends delivering pending checkpoints (evidencebook#937)

- A `deal tick` run (and `deal tick --now`) no longer takes one witness round trip per pending checkpoint. Deliveries go oldest first and stop at the first checkpoint the witness does not take, since the witness holds every later one until it has a receipt. A run without `--wait-up-to` also stops once it has spent `--deliver-for` (default 5s, `0` for no limit) on deliveries; the check is between deliveries, so one under way still finishes. A `--wait-up-to` run has no limit unless `--deliver-for` is given, and the budget is per poll.
- Each checkpoint a run left is listed in `pending` with its stored reason and `not_tried_this_run: true`; the next tick resumes from the oldest.
