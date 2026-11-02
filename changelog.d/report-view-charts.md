### Added: `report.View.Charts` fills and passes a pack's chart views to the viewer (#319)

- `report.Build` takes the view entries of a pack's `chart-views/v1` block (calendar, timeline, co-failure) in
  `View.Charts`, adds to every chart in them the `range` filter on the sealed window and the `usermeta.capsule`
  statement (population, window, runs, values, a not-run day as `not_run`) computed from the Result's dataset, checks
  each with capsule-viewer's `chartspec.Check`, and hands them to the viewer as its `charts` setting. A Result with no
  disclosed dataset, a plan without exactly one window, or a chart the filler cannot state refuses the build. A page
  built with charts gets no permalink.
- Raised the `report` module's capsule-viewer pin to go/v0.11.0 and rewrote the committed presentation pages for its
  page runtime.
