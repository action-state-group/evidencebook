<!-- changelog: python/CHANGELOG.md -->
### Changed (breaking): `uniform` is the default refusal policy (#225, #206 row 8)

- `ResponderPolicy().refusal_policy` is now `REFUSAL_POLICY_UNIFORM` (an empty string reads as uniform too): a responder answers `policy_declined` wherever a more specific token would tell a counterparty whether the holder has a record for a key it does not hold. `REFUSAL_POLICY_SPECIFIC` is an explicit operator opt-in; pass `ResponderPolicy(refusal_policy=REFUSAL_POLICY_SPECIFIC)` to keep the old answers.
