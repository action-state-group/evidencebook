### Added: the x-deal-v1 entry check (#773)

- `xdealv1.EntryOf` reads one party's copy, the bundle `deal report --share ... --bundle` writes, and says whether that party entered a ruling. The states:
  - `entered`: the party's statement naming the ruling is in its book under the copy's checkpoint, which is signed by the checkpoint key its agreement names, and a receipt from a witness the agreement names verifies over the cadence checkpoint that carries that book checkpoint;
  - `entered_unwitnessed`: the same, without such a receipt;
  - `not_entered`: the copy's checkpoint is after the ruling and holds no such statement;
  - `not_shown`: the copy predates the ruling.

  None of these is a refusal. The proof is the party's own records; the copy, a disclose act, is how they reach the adjudicator.
- `evidencebook.VerifyWitnessReceipt` verifies a witness receipt for another party's cadence checkpoint, with the check `Cadence.Witnessed` applies to the holder's own.
- Rust and Python: #774, #775.
