<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Breaking: depend on agent-action-capsule 0.10, checkpointed-local-log 0.5 and capsule-emit-artifact 0.0.3 (#437)

- `agent-action-capsule` 0.10.0 (was 0.8.0), `checkpointed-local-log` 0.5.0 (was 0.4.0) and, with the `shelf` feature, `capsule-emit-artifact` 0.0.3 (was 0.0.2).
- With the `shelf` feature, `HostLogStores::log` is now `checkpointed-local-log` 0.5's `SqliteStore`, so a host that names it moves its own `checkpointed-local-log` pin to 0.5 with this crate.
- `evidence-request` 0.4.1 still depends on `checkpointed-local-log` 0.4, so a build carries both lines.
