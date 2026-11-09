<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Changed: depend on agent-action-capsule 0.10, checkpointed-local-log 0.5 and capsule-emit-artifact 0.0.3 (#437)

- `agent-action-capsule` 0.10.0 (was 0.8.0), `checkpointed-local-log` 0.5.0 (was 0.4.0) and, with the `shelf` feature, `capsule-emit-artifact` 0.0.3 (was 0.0.2). `evidence-request` 0.4.1 still depends on `checkpointed-local-log` 0.4, so a build carries both lines.
