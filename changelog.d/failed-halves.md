### Added: reconcile failed exchanges by outcome (#205)

- `Half.Failed` marks the record of an exchange that did not complete. `ReconcileHalves` pairs a failed half with its counterpart on the exchange id as before, then decides the pair by outcome: two failed halves are `MATCHED`, a failed half against a served one is `CONFLICTING`, and the content check runs only on two served halves. A served pair is classified exactly as before (evidencebook#205).
