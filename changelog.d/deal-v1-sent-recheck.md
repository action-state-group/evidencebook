### Changed: `capsulectl deal settle --sent` asks the check again before money leaves (#718)

- Two pay checks that each passed before either payment left could, together, go over a limit only one of them fits.
- `deal settle --sent` now runs the check's evaluation and your rules checker again before the payment leaves. The rules checker's history holds every payment counted from the moment it was sent, on this deal and every other x-deal-v1 deal.
- The send is refused, and nothing is sealed, when:
  - the evaluation answers worse;
  - the rules cannot answer now;
  - the rules answer worse;
  - the rules fail a rule the check did not.

  Run `deal check` again.
- A payment that failed no longer counts, so a refused send may fit again once it fails.
