# Releasing capsulectl: the core and the book and deal plugins

A release is a pushed `capsulectl/vX.Y.Z` tag (Go's tag form for this nested
module). It is the one capsulectl release a person installs
(evidencebook#895 item 4). It carries the `capsulectl` core and both launchers
of the module, `capsulectl-book` and `capsulectl-deal`, at one version: one
tag, one release, one `SHA256SUMS`. The core is capsule-cli's published build
of that version, carried unchanged. The launchers stay plugins, which the core
installs; nothing is compiled into the core. (`capsulectl-deal` used to release
from evidencebook-deal, whose `v*` releases stay where they are,
evidencebook#610.) The release is built and published by
[`release.yml`](../.github/workflows/release.yml)
through [`scripts/release-build.sh`](scripts/release-build.sh), after a
release reviewer approves the run. It follows capsule-cli's release process
([docs/RELEASING.md](https://github.com/action-state-group/capsule-cli/blob/main/docs/RELEASING.md))
and its rules: agents never approve a release run, and a pushed tag or a
published release never changes. It is not a `release.py` package: those are
tagged by `tag-release.yml`, which publishes a release before its publisher
runs.

## One release, one version

The plugin embeds the `capsulectl` core it builds against, and the core's
first-party check compares the plugin's `core_version` with its own version
(capsule-cli#326, design note section 4.2). `go.mod` requires capsule-cli at
a published release tag, which the release workflow checks, and this
repository's library at a version; it refuses a `replace` of either.

**A person sees one capsulectl version** (evidencebook#895 item 4): the
release `capsulectl/vX.Y.Z` holds the core `vX.Y.Z` and the launchers built
against it. The version is set once, by the capsule-cli tag. On this side it
is written only in `capsulectl/go.mod`'s capsule-cli requirement. The release
workflow refuses a tag that is not that version, before anything is built, and
each launcher's handshake reports it as `core_version`. There is one release
per capsule-cli release users install: a fix to the plugins ships with the
next one. capsule-cli's own `vX.Y.Z` release stays as the neutral core's
release for anyone building other plugins (capsule-cli#428). Install text
points a person only to this one.

The core's binaries are not rebuilt.
[`scripts/release-core-assets.sh`](scripts/release-core-assets.sh) does the following:
- downloads capsule-cli `vX.Y.Z` into a folder of its own;
- checks it against that release's `SHA256SUMS`;
- verifies each binary's attestation with the release's bundle, bound to
  capsule-cli's release workflow, `refs/tags/vX.Y.Z` and the commit `go.mod`
  resolves;
- runs the core to check that version and commit;
- installs both new launchers with it;
- copies the three binaries and `capsulectl-vX.Y.Z.sigstore.json` into the
  release and adds them to its `SHA256SUMS`.

The release workflow runs it after the plugins' own attestation, which never
names the core. The `capsulectl` CI workflow runs the same script in its dry
run against the core that `go.mod` requires, so a defect in it shows before a
tag is pushed.

The order is: capsule-cli `vX.Y.Z` published, then the `go.mod` bump merged,
then `capsulectl/vX.Y.Z` tagged. Tag only once that capsule-cli release is
published, not only tagged. A run refused because it was not published yet
builds nothing; once it is published, the tag's pusher re-runs it. A re-run
cannot recover a tag at the wrong commit. A tag pushed at a commit whose
`go.mod` requires another capsule-cli version is refused on every run and
cannot be moved, and that version then has no capsulectl release. So tag only
the merge commit that carries the bump (step 1 below).

A release's sealed deal records name it as their producer,
`capsulectl-deal <version> (<commit>)`, and a shared copy keeps a producer only
in a fixed shape, so the workflow refuses any other version:
`v<n>.<n>.<n>` with at most four digits each, optionally followed by `-rc`,
`-alpha`, `-beta` or `-dev` and up to three digits.

## The cut

1. Merge a pull request that sets `capsulectl/go.mod`'s capsule-cli
   requirement to the published release vX.Y.Z.
2. Tag that merge commit with the same version. At that commit,
   `go -C capsulectl list -m -f '{{.Version}}' github.com/action-state-group/capsule-cli`
   prints it, a release tag and never a pseudo-version:
   `git tag -a capsulectl/vX.Y.Z <commit> -m "capsulectl vX.Y.Z" &&
   git push origin capsulectl/vX.Y.Z`.
3. A release reviewer approves the run, after checking that the tag is that
   version (the workflow refuses it otherwise, before anything is built).
   The release also carries `standard-history-vX.Y.Z.tar.gz`, the History card
   module's files (capsule-viewer's release bundle, vendored in
   `internal/book/assets/standard-history`), unpacked beside the launcher so the
   core can read the files the handshake names. The workflow checks the archive
   against the handshake before it publishes. It also carries
   `deal-view-vX.Y.Z.tar.gz`, the deal view's files, unpacked into the plugin
   directory beside `capsulectl-deal` (checked against that handshake too), and
   `deal-skill-vX.Y.Z.tar.gz`, the `skills/deal` tree. And it carries the
   core, `capsulectl-vX.Y.Z-{os-arch}` with `capsulectl-vX.Y.Z.sigstore.json`,
   added by `scripts/release-core-assets.sh` (above).
4. Verify from the downloaded assets: `sha256sum -c SHA256SUMS`, then the two
   provenance identities.
   - The core's three binaries were built by capsule-cli:
     `gh attestation verify` each with
     `--bundle capsulectl-vX.Y.Z.sigstore.json --repo action-state-group/capsule-cli
     --signer-workflow action-state-group/capsule-cli/.github/workflows/release.yml`.
   - Each launcher and each archive was built here:
     `gh attestation verify` with
     `--bundle capsulectl-book-vX.Y.Z.sigstore.json --repo action-state-group/evidencebook
     --signer-workflow action-state-group/evidencebook/.github/workflows/release.yml`.
   The bundle is published under two names, `capsulectl-book-vX.Y.Z.sigstore.json`
   and `capsulectl-deal-vX.Y.Z.sigstore.json`; they are the same file, and the
   deal one keeps the name evidencebook-deal's releases used.
5. The install pages pin the core and the plugins from this one release, the deal plugin asset, the deal skill archive
   (`deal-skill-vX.Y.Z.tar.gz`) with its `skills/deal/SKILL.md`, and the deal
   view archive (`deal-view-vX.Y.Z.tar.gz`), which is unpacked into the plugin
   directory beside `capsulectl-deal`; the pins move to this release (the
   follow-up issues on evidencebook#610 name each page).

## Repository settings

[`scripts/release-settings.sh`](scripts/release-settings.sh) applies the
`capsulectl-release` and `capsulectl-release-candidate` environments, the
`capsulectl/v*` tag rulesets and immutable releases. A repository admin runs
it (`--apply`); without an argument it prints what it would set.
