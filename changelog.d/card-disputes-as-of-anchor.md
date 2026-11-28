<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: a `/3` or `/4` card reads a deal's disputes as of its anchor's time (#1087)

- `capsulectl-deal standard-history` now forms each deal's `disputes` as of `T`, the anchor's time, from the same records as the deal's row results (agent-action-capsule `spec/standard-history-v0.md` §11.1 "One card, one time"). A dispute whose record time is after `T` is no entry; a withdrawal after `T` has not withdrawn its dispute; a ruling after `T` resolves nothing, so the dispute is `open` until the anchor reaches it. A record with no time counts as existing, as the row rule reads it. A card from an earlier capsulectl-deal whose tally counted a later record now fails `counts_recompute` against the handover. A status value whose `disputes` are not its rows' dispute records, one for one in order, is refused as corrupt.

### Fixed: a shared copy is no longer refused for a ruling's own digests (#1087)

- The share gate read a ruling's sealed detail as text, so the digit runs in its digests (the copies checked, the dispute it answers) counted as private values, and a copy whose ruling id happened to hold one ("ruling ba0ce7155ca0") was refused. The gate now reads a ruling's detail as its referee alone.
