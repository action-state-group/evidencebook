### Added: `Cadence.Receipts` (#788)

- `Cadence.Receipts` gives the witness receipts a shelf holds for one of its signed cadence checkpoints. A party sends them beside its stamp proof, so a reader can see a witness timed its checkpoint, as x-deal-v1's entry check reads them. A receipt carries no record content: only the witness's URL, its signed receipt, and the entry hash and its position.
