<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: an x-deal-v0 deal copy's page says it carries no witness receipt (capsule-viewer#527)

- `deal report --html` without `--witness-directory` on an x-deal-v0 copy now draws "This file carries no witness receipt, so this page could not check a time-stamp itself." instead of "No witness this verifier trusts has vouched for this log ... it could be a private copy of the log.": a v0 copy carries no parent-log witness receipt by design. capsule-viewer/go is pinned at 6bc5a4db (capsule-viewer#528) in capsulectl and report, which also carries capsule-viewer#526's subject_bundle receipt check and agent-action-capsule 0.17.0 in the page runtime.
