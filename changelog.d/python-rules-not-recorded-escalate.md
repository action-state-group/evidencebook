<!-- changelog: python/CHANGELOG.md -->
### Changed

- `evidencebook.deal.v1.check_authorizations`: an act under a standing intent whose verdict carries no rules block needs the person's signature (`ActAuthorization.rules_not_recorded`), as Go's `xdealv1.CheckActs` does (Sherlock2's ruling ops bb59237c; evidencebook#983).
