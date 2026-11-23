<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: `deal checkpoint proof` (x-deal-v1, #788)

- `deal checkpoint proof --deal ID --copy FILE --out FILE` writes the stamp proof of your deal book and the witness receipts your shelf holds, for the checkpoint your copy rests on (`deal report --share ... --bundle`). Send it to the adjudicator with your copy: `deal ruling entries --proof` reads it to say "entered" rather than "entered, not witnessed", when a witness your agreement names has a receipt for that checkpoint. With no such receipt yet, the file changes nothing: the entry stays "entered, not witnessed", and the output says so. A later run replaces the file.
- It carries no record content, only checkpoints, proofs and witness receipts, so it is not a disclose act.
- It is read-only: it never seals, stamps or contacts the witness. When no stamp covers that checkpoint yet, run `deal tick` once one is due.
