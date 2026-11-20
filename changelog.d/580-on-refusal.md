### Added: `RespondOptions.OnRefusal`, the decided reason as a local diagnostic (#580)

- `RespondOptions.OnRefusal` is called once for each refusal `Respond` returns, with the request digest, the reason the gate decided and the reason the refusal carries. They differ only under the uniform refusal policy, which folds `not_authorized`, `no_such_subject` and `coverage_unsatisfiable` into `policy_declined`. It is for the holder's operator: never sealed, replied or served, and no byte of a reply, record or cut changes.

### Fixed: README named the wrong default refusal (#581)

- The README said an unauthenticated request for a protected subject gets `not_authorized`; under the default uniform policy it gets `policy_declined`.
