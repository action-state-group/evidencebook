<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- `shelf` (feature): `shelf_receipts` is keyed by checkpoint, witness URL and directory key id, as
  Go's (evidencebook#492): a witness whose directory row lists two keys keeps a receipt under each.
  Opening a shelf with the old table rebuilds it in place, keeping every row; the shelf version is
  unchanged. `Cadence::handover` orders receipts by URL and then key.
