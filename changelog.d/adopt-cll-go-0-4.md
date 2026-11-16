### Changed: checkpointed-local-log go/v0.4.0 (CLL 0.9.0, -02 checkpoint `sub`) (#452)

- The root, `capsulectl` and `report` modules require checkpointed-local-log `go/v0.4.0` (was `v0.3.0`). A checkpoint's CWT `sub` is now the bare log id, so every checkpoint pin changes: `testdata/derivation`, `testdata/standard-history-respond` and `testdata/shelf` are regenerated, and the third-party guide's pins, `sub` values and the invariance request's digest follow. `capsulectl`'s book witness store implements `PendingWitnessesFor`, which the CLL 0.9.0 witness-state contract adds.
