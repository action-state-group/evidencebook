<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: `deal tick` names the tick number of each checkpoint it delivers or leaves pending (#1083)

- `deal tick`'s output gains `delivered_ticks`, one `{"checkpoint": <size>, "tick": <N>}` per delivered checkpoint in `delivered`'s order, and each `pending` row gains `tick` when a cadence tick holds its checkpoint (absent for one no tick holds). `tick` is the stamp's number, the same number as the output's top-level `tick`, so a stamp cut by `deal agree` or `deal ruling` and delivered by a later tick is numbered by capsulectl, not inferred from its size. `delivered`, `pending`'s other members and the rest of the output are unchanged. The tick numbers are found in one walk back over the cadence per run, not one per row.
