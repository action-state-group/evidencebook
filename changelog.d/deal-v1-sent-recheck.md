### Changed: `capsulectl deal settle --sent` asks the check again before money leaves (#718)

- Two pay checks that each passed before either payment left could, together, go over a limit only one of them fits.
- `deal settle --sent` now re-runs the check's evaluation and your rules over what has been paid since, every payment in flight included, on this deal and every other x-deal-v1 deal. It refuses a send the check would now answer worse, and seals nothing; run `deal check` again.
- A payment that failed no longer counts, so a refused send may fit again once it fails.
