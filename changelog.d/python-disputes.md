<!-- changelog: python/CHANGELOG.md -->
- `standard_history_deal` counts disputes as the deal plugin does: a buyer's dispute step with no ruling is `raised`/`open`, and a ruling step ends it `for` or `against` the holder (`raised` for a buyer's store, `received` for a seller's), so a seller with a ruling against it no longer shows a clean card (#457).
