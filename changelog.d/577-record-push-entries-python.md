<!-- changelog: python/CHANGELOG.md -->
### Added: the MCP and A2A entries of `Shelf.receive` (evidencebook#577)

- `evidencebook.push` (`PushMCPServer`, `PushA2AServer`, `a2a_push_extension`) carries a record push and a stamp proof over MCP (the tool `record_push`) and A2A (one Agent Card extension, one `data` part) to `Shelf.receive`, as `spec/bindings/record-push-v0.md` of agent-action-capsule specifies and Go's `Shelf.PushMCP` and `PushA2A` do, on the shared carriage vectors (`testdata/shelf/receive-carriage`). A part's refusal is an answer; a malformed push is an invalid-params error and a receiver fault an internal error, never an answer.
