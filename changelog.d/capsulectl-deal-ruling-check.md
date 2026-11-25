<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: an adjudicator can check whether a dispute is decided by the disputed side's own word, and rule only on that

- `deal ruling check --deal D --row R --dispute F --answer A [--answer B] [--copy C] [--ruling RF]` reads the case as `deal ruling --answer` does and prints `xdealv1.DecideCase`'s result. That is `decisive` (against, value, and the deciding record with its time) or `needs_person` with the reason. It seals nothing. With `--ruling`, a ruling already sealed on the row, each side is read only as far as that ruling read, so the check gives the answer the ruling was sealed on.
- `deal ruling --decided-by RECORD_ID` (with `--answer`) seals the ruling only if the check decides that row on that record, with that value and against that side; otherwise it refuses and says why. The ruling record does not carry the decision.
- `capsulectl/go.mod` requires evidencebook at c4ac3579, the library with `DecideCase` (#943).
