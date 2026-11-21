<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- Requires `evidence-request` 0.8.0: a ranged checkpoints request is `{"checkpoint_range": [a, b]}` (`Subject::CheckpointRange`), as Evidence Request -03 names it, and `{"checkpoints": [a, b]}` is `request_malformed` (evidencebook#595).
