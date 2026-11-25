### Changed: `capsulectl` requires the evidencebook library at cc061f0e (#895)

- `capsulectl/go.mod` requires the `evidencebook` library at cc061f0e (was 7ad15a2d), the main of 2026-11-25. Both launchers are then built against the x-deal-v1 receive role check (#887) and the front door's calls (#908). The old pin already carried the `deal` package (#911). The capsule-cli requirement is unchanged: it stays at main 0043a944, which carries v0.1.0-rc54 and `cli.StapleVerdict` (capsule-cli#638).
