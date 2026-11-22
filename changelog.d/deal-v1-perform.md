### Added: `capsulectl deal perform` and `deal statement` on an x-deal-v1 deal (#626, B2 part 1)

- `deal perform --deal ID --check DIGEST --act ACT --observed HOW` seals a perform record in your deal book and writes the file to send to `--out`, which the other side counts with `deal receive`.
  - The act rests on your passing commit check. It is sealed only when that check's verdict has its approval, no detail changed since, and the approval has authorized no other act (`xdealv1.CheckActs`, run on the finished record).
  - Your first act waits until the other side's stamp proof covers its agreement. When the other side runs no layer, the act is sealed and the output says so.
  - The output lists the record digests the person signs (x-deal-v1 §8), with `person_signature: missing` until AAC#627 defines where those signatures travel.
- Flags:
  - `--statement row=value[:remedy]` (repeatable) says how a row went.
  - `--mark NAME` names the offer's named record this act is.
  - `--remedy-row ROW` makes the act a remedy for that row.
- `deal statement --deal ID --statement row=value ...` seals a statement record. It is never held by the first-act rule and rests on no check.
- Run again with the same flags, either command rewrites the same file and seals nothing new. An `--out` that already exists is refused for a new record.
- An act is recorded as a local step after it is sealed, so the next `deal check`'s spend so far and used approvals count it. A run that stops in between is finished by the next `deal check` or `deal perform`, once.
- `deal agree` now stamps on the offerer's side too, and its file carries the stamp proof. So a first act on either side is never left waiting on an offerer that has not acted (PROFILE §8).
