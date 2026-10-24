# Releasing capsulectl-deal

A release is a pushed `v*` tag, built and published by
[`release.yml`](.github/workflows/release.yml) through
[`scripts/release-build.sh`](scripts/release-build.sh), after a release
reviewer approves the run. CI runs the same build as a dry run on every
change.

A release's sealed records name it as their producer,
`capsulectl-deal <tag> (<commit>)`, and a shared copy keeps a producer only
in a fixed shape, so the workflow refuses any other tag: `v<n>.<n>.<n>`, with
at most four digits each, optionally followed by `-rc`, `-alpha`, `-beta`
or `-dev` and up to three digits.

The process follows capsule-cli's release process
([docs/RELEASING.md](https://github.com/action-state-group/capsule-cli/blob/main/docs/RELEASING.md))
and its rules: agents never approve a release run, and a pushed tag or a
published release never changes.

## The core it pairs with

The plugin embeds the `capsulectl` core it builds against, and the core's
first-party check compares the plugin's `core_version` with its own version
(capsule-cli#326, design note section 4.2). So:

1. `go.mod` requires capsule-cli at a published release tag, with no
   `replace`. The release workflow refuses anything else.
2. Every capsule-cli release that users install needs a capsulectl-deal
   release built against it: bump the require, merge, then tag.

## The cut

1. On a fresh `main`, tag the merge commit:
   `git tag -a vX.Y.Z <commit> -m "capsulectl-deal vX.Y.Z" && git push origin vX.Y.Z`.
2. A release reviewer approves the run (Actions, Release, Review deployments).
3. Verify from the downloaded assets: `sha256sum -c SHA256SUMS`, and
   `gh attestation verify` on each asset with
   `--bundle capsulectl-deal-vX.Y.Z.sigstore.json --repo action-state-group/evidencebook-deal
   --signer-workflow action-state-group/evidencebook-deal/.github/workflows/release.yml`.
4. The install page pins the plugin asset, the skill archive
   (`deal-skill-vX.Y.Z.tar.gz`) and its `skills/deal/SKILL.md`.

## Repository settings

[`scripts/release-settings.sh`](scripts/release-settings.sh) applies the
release environments (`release`, `release-candidate`) with their reviewers,
the release tag rulesets and immutable releases. A repository admin runs it:

```sh
scripts/release-settings.sh            # print what it would set
scripts/release-settings.sh --apply    # set it, then read it back
```
