<!-- changelog: python/CHANGELOG.md -->
- `evidence.evaluate(pack, card)` decides admission by running capsule-engine's `admission evaluate` command
  (go/v0.3.0; `$CAPSULE_ENGINE` or `capsule-engine` on `PATH`) and returns a `Decision`; `EvaluateError` when the
  engine makes none (evidencebook#332). `find` and `serve` still raise `NotYetAvailable`.
