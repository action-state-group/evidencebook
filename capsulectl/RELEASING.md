# Releasing capsulectl-book

A release is a pushed `capsulectl/vX.Y.Z` tag (Go's tag form for this nested
module), built and published by
[`release.yml`](../.github/workflows/release.yml)
through [`scripts/release-build.sh`](scripts/release-build.sh), after a
release reviewer approves the run. It follows capsule-cli's release process
([docs/RELEASING.md](https://github.com/action-state-group/capsule-cli/blob/main/docs/RELEASING.md))
and its rules: agents never approve a release run, and a pushed tag or a
published release never changes. It is not a `release.py` package: those are
tagged by `tag-release.yml`, which publishes a release before its publisher
runs.

## The core and the library it pairs with, and the version

The plugin embeds the `capsulectl` core it builds against, and the core's
first-party check compares the plugin's `core_version` with its own version
(capsule-cli#326, design note section 4.2). `go.mod` requires capsule-cli at
a published release tag, which the release workflow checks, and this
repository's library at a tagged version; it refuses a `replace` of either.

**A release takes the version of the capsulectl release it pairs with**
(Ethan, 2026-10-24): the plugin built against capsulectl `v0.1.0-rc24` is
tagged `capsulectl/v0.1.0-rc24`. Every capsule-cli release that users install
needs a capsulectl-book release built against it, and there is one per
capsulectl release: a fix to the plugin ships with the next one. Tag only
once that capsule-cli release is published, not only tagged: the workflow
refuses an unpublished one, and a refused tag is spent.

## The cut

1. Tag the merge commit with the capsule-cli version `go.mod` requires
   (`go list -m -f '{{.Version}}' github.com/action-state-group/capsule-cli`):
   `git tag -a capsulectl/vX.Y.Z <commit> -m "capsulectl-book vX.Y.Z" &&
   git push origin capsulectl/vX.Y.Z`.
2. A release reviewer approves the run.
3. Verify from the downloaded assets: `sha256sum -c SHA256SUMS`, and
   `gh attestation verify` on each binary with
   `--bundle capsulectl-book-vX.Y.Z.sigstore.json --repo action-state-group/evidencebook
   --signer-workflow action-state-group/evidencebook/.github/workflows/release.yml`.

## Repository settings

[`scripts/release-settings.sh`](scripts/release-settings.sh) applies the
`capsulectl-release` and `capsulectl-release-candidate` environments, the
`capsulectl/v*` tag rulesets and immutable releases. A repository admin runs
it (`--apply`); without an argument it prints what it would set.
