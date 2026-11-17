- A whole-shelf handover now carries the receipt history a reader needs for record times
  (evidencebook#461, `docs/design/461-deal-profile.md` §8.1, invariant C). `Handover.Witnessed` lists every
  shelf checkpoint before the anchor that the holder holds receipts for, with those receipts.
  `VerifyHandover` checks that each is the holder's checkpoint of the shelf log, within the anchor, and
  recomputed by the handed-over entries. The new `VerifyHandoverTimes` gives a book position the record time
  the holder's `RecordTime` gives, even when the earliest receipt is not the anchor's
  (`testdata/shelf/handover/earliest-receipt-not-anchor.json`).
