### Added: `capsulectl deal perform` and `deal statement` on an x-deal-v1 deal (#626, B2 part 1)

- `deal perform --deal ID --check DIGEST --act ACT --observed HOW --out FILE` seals a perform record in your deal book and writes the file to send to `--out` (required, so the output can list the person's signatures), which the other side counts with `deal receive`.
  - The act rests on your passing commit check. It is sealed only when that check's verdict has its approval, no detail changed since, and the approval has authorized no other act (`xdealv1.CheckActs`, run on the finished record).
  - Your first act is refused until you hold the other side's stamp proof on its agreement (`deal receive` brings it); run `deal perform` again then. If the other side runs no stamping, the act is sealed and the output says no stamp proof came.
  - The output lists the record digests the person must sign with their own key (`person_signature: missing`). Until those signatures are held, no one should rely on this act. The person cannot add them yet: where they travel is still being defined (agent-action-capsule#627).
- Flags:
  - `--statement row=value[:remedy]` (repeatable) says how a row went.
  - `--mark NAME` names the offer's named record this act is.
  - `--remedy-row ROW` makes the act a remedy for that row.
- `deal statement --deal ID --statement row=value ...` seals a statement record. A statement can be sent at any time: it does not wait for the other side's stamp proof, and it needs no check.
- Run again with the same flags, either command rewrites the same file and seals nothing new. A statement that repeats an older one after a different one is sealed again, since a row reads the side's latest. An `--out` that already exists is refused for a new record.
- An act is recorded as a local step after it is sealed, so the next `deal check`'s spend so far and used approvals count it. A run that stops in between is finished by the next `deal check` or `deal perform`, once.
- `deal agree` now stamps on the offerer's side too, and its file carries the stamp proof. So a first act on either side is never left waiting on an offerer that has not acted (PROFILE §8).
