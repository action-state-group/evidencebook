<!-- changelog: python/CHANGELOG.md -->
### Changed: depend on agent-action-capsule 0.16 and capsule-emit 0.15 (#436)

- `agent-action-capsule>=0.16.0,<0.17` and `capsule-emit>=0.15.0,<0.16`. The library's `PROFILES` now names `evidence-interop/2`, which the book does not implement; a request naming it is refused like any unknown profile, with the detail `profiles supported: evidence-interop/0, evidence-interop/1`.
