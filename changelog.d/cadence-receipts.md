### Added: `Cadence.Receipts` (#788)

- `Cadence.Receipts` gives the witness receipts a shelf holds for one of its signed cadence checkpoints. A party sends them beside its stamp proof; x-deal-v1's entry check reads them to see that a witness registered the party's checkpoint. A receipt carries no record content: only the witness's URL, its signed receipt, and the entry hash with its position in the witness's log (leaf index and tree size).
