<!-- changelog: python/CHANGELOG.md -->
- The Goose reference's commitment staples a `history_card/1` built as `Book.Staple` builds it, over `checkpoints` with a pinned anchor and a nonce, not over `full_history` (evidencebook#345). `evidence-commitment/1` has no member for the request, so the commitment carries the reply only.
