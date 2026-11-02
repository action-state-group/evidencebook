### Changed: capsule-viewer `go/v0.13.0`, and the monthly pages are regenerated with it (evidencebook-skills#73)

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer `go/v0.13.0` (capsule-viewer#238), which no longer counts a calibration claim in the roll-up header. `report/testdata/presentation/{4-monthly-outcome,4b-monthly-outcome-informational,5-monthly-compliance}/page.html` are rewritten with it; the Results and snapshots are unchanged.
