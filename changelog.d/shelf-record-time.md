- Record time (evidencebook#461 §2.6): `Shelf.RecordTime(bookID, position, witnesses)` returns the earliest
  registration time, among the receipts held from the named witnesses (their receipt URLs), of a shelf
  checkpoint covering the first fact of the book whose size covers the position, or no time. `Witnessed` keeps
  every verified receipt in the new `shelf_receipts` table, one per witness with its registration time, the
  earliest kept; a witnessed tick missing there is filled in at the cadence's next open. `Handover` carries every
  receipt held for its anchor, and `VerifyHandover` reports `Witnessed` only for a receipt that gives a
  registration time.
