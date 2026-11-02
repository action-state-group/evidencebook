<!-- changelog: python/CHANGELOG.md -->
- `evidence.evaluate(pack, inputs)` decides admission by running capsule-engine's `admission evaluate` command
  (go/v0.3.0; `$CAPSULE_ENGINE` or `capsule-engine` on `PATH`) and returns a `Decision`; `EvaluateError` when the
  engine makes none (evidencebook#332). This replaces the earlier line saying `evaluate` raises `NotYetAvailable`;
  `find` and `serve` still do.
