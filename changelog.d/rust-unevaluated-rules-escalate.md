<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Changed

- `deal::v1::check_authorizations`: an act under a standing intent whose verdict's rules did not allow the act on their own needs the person's signature (`ActAuthorization::rules_not_evaluable`, read by the new `deal::v1::rules_unevaluated`), as Go's `xdealv1.CheckActs` does (evidencebook#982).
