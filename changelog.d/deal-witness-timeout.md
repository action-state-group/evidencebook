<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: a slow witness reads as slow, never as a consent ask

- `capsulectl/go.mod` requires capsule-cli at dcecbf5 (capsule-cli#644): a witness delivery that reached the witness and then timed out is pending with reason `witness_timeout` and the words "the witness was reached but its answer did not come in time", where it read "network consent needed, or no network".
- `deal report`'s assurance passes `witness_timeout` on in `witness_reason`, beside `network_consent_needed` and `refused`, so an agent can tell a slow witness from a missing grant.
- The deal skill says what `witness_timeout` means: the witness or the network to it is slow, it retries on its own, and the user is never asked for a network grant for it.
- With the same capsule-cli, `capsulectl doctor --check-witness` reports a witness that was reached but did not answer the probe in time as `timed_out: true`, with no consent text.
