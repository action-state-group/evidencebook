<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: the deal skill teaches receiving a ruling and challenging it

- The skill's dispute section says how a party takes a ruling: `deal receive ruling.json --out naming.json`, sending the naming statement before anything later; reading `stamped_at` and the "not independent (shared operator)" line as they are, never as "independent"; `deal dispute --challenge`; and keeping the other side's copy with `deal receive`. The referee's side stays out of the skill, as other audiences are for disputes a person runs; `deal ruling --help` covers it.
- `deal ruling --help` says how a new adjudicator makes its key (`capsulectl key generate --output KEY`, whose printed public key the parties name) and that its stamps go to the witness as hashes only.
