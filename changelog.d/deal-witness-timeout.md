<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: a slow witness reads as slow, never as a consent ask

- `capsulectl/go.mod` requires capsule-cli at dcecbf5 (capsule-cli#644): a witness delivery that reached the witness and then timed out is pending with reason `witness_timeout` and the words "the witness was reached but its answer did not come in time", where it read "network consent needed, or no network".
- `deal report`'s assurance passes `witness_timeout` on in `witness_reason`, beside `network_consent_needed` and `refused`, so an agent can tell a slow witness from a missing grant.
- The deal skill says what `witness_timeout` means: the witness or the network to it is slow, it is tried again on a later run, and the user is never asked for a network grant for it. The 403 paragraph says it is tried again on a later run with a growing wait, not at every tick.
- With the same capsule-cli, `capsulectl doctor --check-witness` reports `timed_out: true` when its own probe reaches the witness and gets no answer in time, with no consent text.
