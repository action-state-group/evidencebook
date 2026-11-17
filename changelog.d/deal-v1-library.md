- **The library calls the x-deal-v1 profile needs** (evidencebook#461, `docs/design/461-deal-profile.md`
  §11.1, PR 1). Each is additive and profile-neutral:
  - `rowstatus.ComputeLate` lists, by index into the input, the statements and disputes the rule
    sets aside as late as of T, beside `Compute`'s result, which is unchanged
    (`testdata/row-status/late.json`, hand-written from the rule).
  - `Shelf.SealSent` appends a sent journal record and returns its standalone Capsule, signed by the
    journal's key, so a record that leaves the shelf whole never shows its place in the journal.
  - `Shelf.HeldFirst` returns every received record at position 1 of a log, across senders and both
    records of a fork, and `Shelf.HeldProofs` every stamp proof held from a sender, re-verified, with
    the records it covers against the counted prefix now (none after a fork at the genesis). Both
    take no lock.
  - `Shelf.ReceivedRecordTime` is a received record's time from the holder's own stamps: the first
    fact whose link value reaches the record's position, and the earliest named receipt covering it.
