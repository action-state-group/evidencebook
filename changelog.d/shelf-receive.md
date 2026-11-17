- The shelf's store of what the other side pushes (evidencebook#461 §3): `Shelf.Receive` takes a record, a
  `StampProof`, or both, each part on its own, and returns the deal ids whose counted prefix grew, shrank or
  forked and those whose records an accepted proof newly covers. A `ReceiveProfile` (in `ShelfConfig.Receive`)
  reads each record and holds the push rule, so the library knows no record format. The store acknowledges a
  duplicate, stores a gap without counting it until it is filled, keeps both records of a fork as evidence and ends
  the counted prefix before it for good, and bounds record size, records per book and unanswered unsolicited
  offers (dropping the oldest first). A proof is refused, and its evidence kept, when it contradicts a held proof
  at the same size or the records held; every accepted proof is kept. `Cadence.StampProof` builds a proof for an
  audience and `VerifyStampProof` checks one. `Shelf.Forget` deletes a settled deal's records under the holder's
  `Retention` (by default the life of the shelf, so never), leaving tombstones that read as `ErrDeleted`.
  `CountedPrefix`, `ReceivedRecordOf` and `Evidence` read the store. Works on version 1 and 2 shelves.
