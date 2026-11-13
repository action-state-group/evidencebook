<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- Internal: the shelf commit loop iterates the records instead of indexing them, so `cargo clippy -- -D warnings` passes on Rust 1.100 (#446). No behaviour change.
