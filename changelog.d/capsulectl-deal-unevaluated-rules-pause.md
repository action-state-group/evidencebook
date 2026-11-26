<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: a check pauses when a rule could not be evaluated, even if the rules checker allows (#982)

- `deal check`: when the pinned rules checker answers `allow` but could not evaluate one of your measured rules (a `not_evaluable` finding), the check now adds a `rules_not_evaluable` difference ("Your rules were not fully checked: ...") and pauses for your answer. A standing intent no longer approves such a check on its own (u503; Sherlock2's ruling ops fe0e13eb). Rules the ruleset only declares (`declared, not measured`, naming no check) stay quiet as before. A finding that names a check is read as measured whatever reason the checker gives.
