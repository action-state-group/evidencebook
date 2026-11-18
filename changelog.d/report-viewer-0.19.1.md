### Changed: `report` and capsulectl-book render with capsule-viewer v0.19.1 (evidencebook-deal#220)

- The `report` and `capsulectl` modules require capsule-viewer `go/v0.19.1` (was `v0.18.2`), whose pages verify with agent-action-capsule 0.14.0 and cll 0.4.0. The report-build pages in `report/testdata/presentation/` (`4-monthly-outcome`, `4b-monthly-outcome-informational`, `5-monthly-compliance`) are regenerated with `capsulectl book report build`. Their new text is the viewer's own:
  - the totals note ("These totals count every rule in force…");
  - the month in the outcome report's title;
  - the "of 8" counts and the "Judged" chips;
  - "requirement" in place of "test" on the compliance page, and its numbered notes.
  - Each page still passes 10 of 10 checks.
