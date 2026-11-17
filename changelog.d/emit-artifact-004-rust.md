<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Changed: `shelf` feature on capsule-emit-artifact 0.0.4 (#479)

- The `evidencebook` crate (`shelf` feature) and the interop crate depend on `capsule-emit-artifact` 0.0.4, which is on agent-action-capsule 0.11, so agent-action-capsule 0.11.0 is the only copy in the graph and one `SqliteStore` can be handed to both this crate and `capsule-emit` 0.0.13. A consumer that names `capsule-emit-artifact` 0.0.3 next to this crate adopts 0.0.4.
