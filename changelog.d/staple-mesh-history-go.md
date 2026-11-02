### Added: `Example (Staple)` checks the Mesh node's History with the Go reader (#347)

- The Example derives the Mesh node's `inference-service-history/0` over its stream for the week closing 2026-11-02, embeds the stream's `history_card/1`, and checks it with agent-action-capsule's `inferencehistory.Evaluate` (go v0.20.1, which reads real COSE checkpoint cards). The module now requires agent-action-capsule go v0.20.1. This supersedes the note under #322 above that the Go Example still stops at the node's `history_card/1`.
- Changed by that bump: `Book.Respond` now refuses a CBOR request that is not in core deterministic encoding (`request_malformed`, digested over the octets as received), per agent-action-capsule's profile §5.2 (go v0.20.0). Before, it answered one.
