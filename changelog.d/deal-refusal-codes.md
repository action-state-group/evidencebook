### Changed: package `deal`'s refusals name its own calls (#899)

- A refusal that tells the caller what to call next names this package's call (`Store.Admit`, `Store.NoteReceived`, `Store.CancelOffer` and the rest), where it named a capsulectl-deal command before; a caller that matched the old text should match `Code` instead.
- `InputError` and `ConflictError` carry `Code`, of the new type `Code`, and `Args` on those refusals: 16 constants (`CodeAgreeNoAdmit`, `CodeDeclineAgain` and the rest), each with the values its message names, so a caller can tell the refusals apart without matching text, or word them in its own terms. The constants are stable; their values are for the caller's own process.
