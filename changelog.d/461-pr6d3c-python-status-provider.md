<!-- changelog: python/CHANGELOG.md -->
- **Added (`evidencebook.deal.v1`):** `StatusProvider`, Go's x-deal-v1 `StatusProvider` (evidencebook#461).
  - `book_values` gives a v1 deal book's profile, link value and status value at each stamp.
  - `before_stamp` names any ruling still unnamed.
  - `report` computes a copy of the v1 report from what the shelf holds.
  - Go's `TestHolderTranscripts` now also records the stamp and report scenarios, and Python replays them, matching each fact, opening and report byte for byte.
  - `testdata/deal-v1/forked-finals` holds a forked case book's conflicting rows.
- **Added (`evidencebook`):** `Entry.members`, Go's `Entry.Members`: further top-level members of a record's Capsule (a settlement leg's, for one), which its id and signature cover.
