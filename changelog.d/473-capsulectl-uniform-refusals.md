### Changed: capsulectl-book answers refusals uniformly (#473)

- `capsulectl-book` no longer sets `RefusalPolicySpecific`: it follows the library default (`uniform`, v0.14.0), so a refused request is answered `policy_declined` wherever `not_authorized`, `no_such_subject` or `coverage_unsatisfiable` would tell a requester whether the holder has a record for a key it does not hold. A holder's `holder-page --view asked` list shows the sealed code (`policy_declined`) as its reason.
