# Releasing capsulectl-book

A release is a pushed `capsulectl/vX.Y.Z` tag (Go's tag form for this nested
module), built and published by
[`publish-capsulectl.yml`](../.github/workflows/publish-capsulectl.yml)
through [`scripts/release-build.sh`](scripts/release-build.sh), after a
release reviewer approves the run. It follows capsule-cli's release process
([docs/RELEASING.md](https://github.com/action-state-group/capsule-cli/blob/main/docs/RELEASING.md))
and its rules: agents never approve a release run, and a pushed tag or a
published release never changes. It is not a `release.py` package: those are
tagged by `tag-release.yml`, which publishes a release before its publisher
runs.

## The core and the library it pairs with

The plugin embeds the `capsulectl` core it builds against, and the core's
first-party check compares the plugin's `core_version` with its own version
(capsule-cli#326, design note section 4.2). `go.mod` requires capsule-cli at
a published release tag, which the publish workflow checks, and this
repository's library at a version; it refuses a `replace` of either. Every capsule-cli
release that users install needs a capsulectl-book release built against it.

## The cut

1. Tag the merge commit: `git tag -a capsulectl/vX.Y.Z <commit> -m
   "capsulectl-book vX.Y.Z" && git push origin capsulectl/vX.Y.Z`.
2. A release reviewer approves the run.
3. Verify from the downloaded assets: `sha256sum -c SHA256SUMS`, and
   `gh attestation verify` on each binary with
   `--bundle capsulectl-book-vX.Y.Z.sigstore.json`.

## Repository settings

[`scripts/release-settings.sh`](scripts/release-settings.sh) applies the
`capsulectl-release` and `capsulectl-release-candidate` environments, the
`capsulectl/v*` tag rulesets and immutable releases. A repository admin runs
it (`--apply`); without an argument it prints what it would set.
