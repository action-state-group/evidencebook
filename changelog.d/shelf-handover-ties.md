- The whole-shelf handover ties each genesis fact to the old witnessed tree when the holder can
  (evidencebook#461 §2.7): `Handover.Ties` carries, per book, the old witnessed tick's leaf and path, its
  root's position in the shelf log, the cadence checkpoint and its receipts, the old book checkpoint and a
  consistency proof to the genesis checkpoint. `VerifyHandover` checks each tie and fails on one that does not
  verify; a genesis fact without one is still reported in `Untied`.
