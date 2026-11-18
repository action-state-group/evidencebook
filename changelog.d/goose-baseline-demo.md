<!-- changelog: python/CHANGELOG.md -->
- The Goose reference's `Terms` takes `demo` (default `False`): a deal opened with `demo=True` seals `demo: true` in its
  baseline, the x-deal-v0 baseline's optional member, which a receipt viewer shows as a demonstration deal. A deal
  opened without it is sealed as before, with no `demo` member.
