# Releasing capsulectl-deal

A release is a pushed `v*` tag, built and published by the release workflow
through [`scripts/release-build.sh`](scripts/release-build.sh), after a
release reviewer approves the run.

**Not yet releasable.** The release workflow lands in its own pull request,
after three things, in this order:

1. a repository admin applies the release settings below, so a pushed tag
   waits for a reviewer;
2. sealed records name the plugin's own build as their producer,
   `capsulectl-deal <tag> (<commit>)`, and capsule-cli's deal page compares
   producers by name (capsule-cli#326, design note section 4.2);
3. the workflow refuses a tag outside the producer shape a shared copy keeps
   (`v<n>.<n>.<n>`, optionally `-rc<n>`, `-alpha<n>`, `-beta<n>` or `-dev<n>`).

Until then `scripts/release-build.sh` runs only as CI's dry run. It follows capsule-cli's release process
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
   `--bundle capsulectl-deal-vX.Y.Z.sigstore.json`.
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
