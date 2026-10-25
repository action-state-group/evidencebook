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
repository's library at a version; it refuses a `replace` of either.

**A release takes the version of the capsulectl release it pairs with**
(Ethan, 2026-10-24): the plugin built against capsulectl `v0.1.0-rc24` is
tagged `capsulectl/v0.1.0-rc24`. Every capsule-cli release that users install
needs a capsulectl-book release built against it, and there is one per
capsulectl release: a fix to the plugin ships with the next one. The release
workflow refuses a tag that is not that version, before anything is built.

Tag only once that capsule-cli release is published, not only tagged. A run
refused because it was not published yet builds nothing; once it is
published, the tag's pusher re-runs it. A re-run cannot recover a tag at the
wrong commit: a tag pushed at a commit whose `go.mod` requires another
capsule-cli version is refused on every run and cannot be moved, and that
core then gets no capsulectl-book release. So tag only the merge commit that carries the
bump (step 1 below).

## The cut

1. Merge a pull request that sets `capsulectl/go.mod`'s capsule-cli
   requirement to the published release vX.Y.Z the plugin pairs with.
2. Tag that merge commit with the same version. At that commit,
   `go -C capsulectl list -m -f '{{.Version}}' github.com/action-state-group/capsule-cli`
   prints it, a release tag and never a pseudo-version:
   `git tag -a capsulectl/vX.Y.Z <commit> -m "capsulectl-book vX.Y.Z" &&
   git push origin capsulectl/vX.Y.Z`.
3. A release reviewer approves the run, after checking that the tag is that
   version (the workflow refuses it otherwise, before anything is built).
4. Verify from the downloaded assets: `sha256sum -c SHA256SUMS`, and
   `gh attestation verify` on each binary with
   `--bundle capsulectl-book-vX.Y.Z.sigstore.json --repo action-state-group/evidencebook
   --signer-workflow action-state-group/evidencebook/.github/workflows/release.yml`.

## Repository settings

[`scripts/release-settings.sh`](scripts/release-settings.sh) applies the
`capsulectl-release` and `capsulectl-release-candidate` environments, the
`capsulectl/v*` tag rulesets and immutable releases. A repository admin runs
it (`--apply`); without an argument it prints what it would set.
