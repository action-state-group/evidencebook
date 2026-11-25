<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: `deal compose` seals only what a refusal's responder signed (#872)

- A composed refusal member now carries only `request_digest`, `reason`, `issued_at`, `key_id` and `sig`, and its digest is over those. Before, compose copied the whole recorded refusal, so a `detail` or a `policy_digest` the responder's signature does not cover was sealed under your composition as if it were the responder's evidence. The refusal still verifies, and recording a response (`deal request --response`) is unchanged.
