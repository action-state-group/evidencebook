<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Changed (breaking): `uniform` is the default refusal policy (#225, #206 row 8)

- `RefusalPolicy::default()`, and so `ResponderPolicy::default()`, is now `Uniform`: a responder answers `policy_declined` wherever a more specific token would tell a counterparty whether the holder has a record for a key it does not hold. `RefusalPolicy::Specific` is an explicit operator opt-in; set `refusal_policy: RefusalPolicy::Specific` to keep the old answers.
