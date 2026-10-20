<!-- SPDX-License-Identifier: Apache-2.0 -->
# Releasing

This repository releases two packages: the Go module at the repository root
and the Rust crate in `rust/evidencebook/`. They are versioned and tagged
independently.

| Package | Manifest | Changelog | Tag | Released by |
|---|---|---|---|---|
| Go module `github.com/action-state-group/evidencebook` | `CHANGELOG.md` heading | `CHANGELOG.md` | `v<version>` | an admin pushing the tag |
| crate `evidencebook` | `rust/evidencebook/Cargo.toml` and `rust/Cargo.lock` | `rust/evidencebook/CHANGELOG.md` | `crates/evidencebook-v<version>` | `publish-rust.yml`, environment `crates-io` (tag rule `crates/*-v*`) |

The crate releases the same way as the packages of capsule-emit,
agent-action-capsule and checkpointed-local-log: a release is a merged version
bump. It is listed in [`.github/releases.json`](.github/releases.json), which
[`.github/release.py`](.github/release.py) reads. The `crates/` prefix is only
on the Git tag; the crates.io version stays plain (`0.0.6`).

## Cutting a crate release

1. **Prepare.** Run [Release · Prepare](https://github.com/action-state-group/evidencebook/actions/workflows/prepare-release.yml)
   on `main`, choosing `crates/evidencebook` and `patch`, `minor` or `major`.
   It raises the version in `Cargo.toml` and `Cargo.lock`, commits the bump
   with a sign-off, and pushes `release/crates-evidencebook-v<version>`.
2. **Review.** Open the pull request from the link in the run summary. Open it
   yourself: a pull request opened by the workflow would not start CI. Move
   the crate CHANGELOG's Unreleased entries under the new version on the same
   branch.
3. **Merge.** [Release · Tag](https://github.com/action-state-group/evidencebook/actions/workflows/tag-release.yml)
   runs on the merge. If the crate's version changed, it creates the tag and
   GitHub Release on the merge commit and dispatches `publish-rust.yml` on
   that tag.
4. **Publish.** `publish-rust.yml` checks that the tag names this crate at its
   `Cargo.toml` version, that the commit is on `main` and that crates.io does
   not have the version yet, then uploads through OIDC trusted publishing from
   the `crates-io` environment. Confirm the run and the new version on
   crates.io.

Bumping the crate version by hand in any pull request releases it the same way
when it merges.

If a publish fails after the tag exists:

- **A transient or configuration failure** (crates.io outage, trusted
  publisher or environment settings): fix it and re-run the failed publisher
  run. If crates.io already accepted the version, the re-run stops there: the
  version is released.
- **A defect in the code or the workflow:** a re-run uses the tagged commit
  again, so a fix merged to `main` does not reach it. Release the fix as a new
  version.
- **Release · Tag failed before dispatching** (no publisher run exists): re-run
  the Release · Tag run. It accepts a tag and GitHub Release it already
  created on the same commit and dispatches the publisher again.

## Go

The module is `github.com/action-state-group/evidencebook`, at the repository
root, so its tags are bare `v<version>` (not `go/v<version>`). Go has no
manifest version and no publisher workflow; a Go release is the pushed tag
itself.

1. Merge a `chore(release): v<version>` pull request that moves the
   CHANGELOG's Unreleased entries under `## v<version> (<date>)`.
2. An admin pushes `v<version>` on that merge commit. Nobody else creates `v*`
   tags, and no workflow does.

The version is fetchable through the Go module proxy as soon as the tag
exists, so a bad release can only be withdrawn with a `retract` directive in a
later version. CI on `main` covers the module's tests.

## Safeguards

- **Guard.** `release.py check` refuses a tag from another package, a version
  that differs from the manifest, and a commit that is not on `main`.
  `publish-rust.yml` additionally checks the crate list, the `Cargo.toml`
  version, crates.io availability and crate metadata.
- **Environment.** `crates-io` accepts deployments only from `crates/*-v*`
  tags, so a job started from a branch or another tag cannot upload.
- **Tags.** The `release tags` ruleset lets only admins move or delete tags.

## Registry configuration

crates.io's trusted publisher for `evidencebook` names this repository,
`publish-rust.yml` and the `crates-io` environment. Renaming the workflow
breaks publishing until crates.io is updated first. No registry token is
stored in this repository. Tags `crates/evidencebook-v0.0.4` to `-v0.0.6` were
pushed by hand before this flow existed.
