<!-- changelog: python/CHANGELOG.md -->
- `Handover.witnessed` and `verify_handover_times`: a handover carries the earlier witnessed shelf
  checkpoints and their receipts, `verify_handover` checks them, and a reader resolves record times from
  them as Go does (evidencebook#461, invariant C; `testdata/shelf/handover/earliest-receipt-not-anchor.json`).
