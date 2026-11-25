### Changed: `report` requires the library at 9ae62286, which has the front door's calls (#895)

- `report/go.mod` requires the `evidencebook` library at 9ae62286 (was 3550482e), which carries `Book.RecordAct` and `Book.EvaluatePack` (#908). A new Example, `Example_frontDoor`, runs the three calls end to end: it records three acts, evaluates them under capsule-engine's Everyday Rules 0.3.8, renders the bundle with `Build` as the contract report (VALID, 21 claims), and seals and checks the page with `SealPage` and `CheckPage`.
