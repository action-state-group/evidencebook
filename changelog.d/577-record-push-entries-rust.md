<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Added: the MCP and A2A entries of `Shelf::receive` (evidencebook#577)

- `Shelf::push_mcp` and `Shelf::push_a2a` (and `push_a2a_extension`), behind the `shelf` feature, carry a record push and a stamp proof over MCP (the tool `record_push`) and A2A (one Agent Card extension, one `data` part) to `Shelf::receive`, as `spec/bindings/record-push-v0.md` of agent-action-capsule specifies and Go's `Shelf.PushMCP` and `PushA2A` do, byte for byte on the shared carriage vectors (`testdata/shelf/receive-carriage`). A part's refusal is an answer; a malformed push is an invalid-params error and a receiver fault an internal error, never an answer.
