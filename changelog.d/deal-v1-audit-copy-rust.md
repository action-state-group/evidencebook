<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- `deal::v1` reads x-deal-v1 audit copies (evidencebook#1058): `verify_report` refuses an
  `audit` report that carries a witness, a referee or a witnessed time, and
  `verify_report_bundle` accepts audit copies and refuses one carrying an offer that names an
  `ask`; `build_report` makes one.
