- **A witness's receipts are kept per key** (evidencebook#492). `shelf_receipts` is keyed by
  checkpoint, witness URL and directory key id, so a witness whose directory row lists two keys keeps
  a receipt under each for the same checkpoint, and `RecordTimeByKey` reads each key's own time.
  Before, one receipt per URL was kept and the other key's was lost. Opening a shelf whose
  table has the old key rebuilds it in place and keeps every row; the shelf version is unchanged.
  The handover carries every receipt held, ordered by URL and then key.
