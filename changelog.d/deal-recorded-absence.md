<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: a recorded absence on a deal's requests log is typed `recorded_absence` (evidencebook#116)

- `deal request --absent-until` now records the absence as `recorded_absence`, the name the evidencebook library and the Mesh plugin use, instead of `evidence_absence`. The command's output already said `recorded_absence` and is unchanged.
- Absences recorded under the old name are kept as they are and still read as absences: they compose the same, and they still refuse a second outcome for their request.
