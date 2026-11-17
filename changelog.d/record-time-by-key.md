- **Record times by witness key id** (evidencebook#461; Steven, #482 Q1, u604: an x-deal-v1 agreement names each
  witness by its verified key id, with its URL only a hint). `Shelf.RecordTimeByKey`,
  `Shelf.ReceivedRecordTimeByKey` and `HandoverTimes.RecordTimeByKey` give the times `RecordTime`,
  `ReceivedRecordTime` and `HandoverTimes.RecordTime` give, with witnesses matched on the key each receipt verified
  under rather than its URL, so a witness that moved stays the same witness. The URL readers are unchanged.
