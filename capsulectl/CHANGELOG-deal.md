# Changelog

## Unreleased

- Builds against capsule-cli `ed9c4db4` (unreleased; capsule-cli#479 merged), whose core
  has no deal view of its own: `capsulectl presentation list` and `build` use the
  deal view only from this plugin, with its files unpacked beside the launcher,
  and otherwise resolve a deal receipt to the fallback page.
- The deal view (`capsulectl.deal-view/v0`, the deal section of a receipt's
  page) is this plugin's own presentation module now (capsule-cli#428 step 4):
  its files are in `internal/deal/assets/`, the handshake offers it, and a
  release ships `deal-view-VERSION.tar.gz` to unpack beside the launcher
  (README). `deal report --html` pages are unchanged; with the files unpacked,
  `capsulectl presentation build` also carries the deal view. Builds against
  capsule-cli `d5f42c06` (unreleased; capsule-cli#478 merged), whose handshake can
  state a plugin's presentations.
- Builds against capsule-cli `8a856b75` (unreleased; capsule-cli#428 PR B), whose core names no company
  plugin. The deal plugin now says the words the core no longer does: `bundle`,
  `disclose` and `permalink` on a deal profile without `--deal` name the deal to
  pass (`name the deal with --deal DEAL_ID`, `--log-id` does not apply), and the
  canary's alarm still reads `canary alarm: ...` and exits 6. `doctor
  --install-check` reports the deal skill under `skills` (it was `deal_skills`),
  and the installed plugin must declare `core_version` to be checked against the
  core. `cll list` on a deal profile with no `log_id` now gets the core's generic
  message (the deal plugin keeps several logs in its store; name one with `--log-id`).
- `canary` is a deal verb now: `capsulectl deal canary run|watch` (it was
  `capsulectl canary run|watch` in capsulectl, which drops its own with
  capsule-cli#428). `--expect-version` takes this binary's release tag, and
  the sealed note and `binary_version`/`binary_commit` name that tag and
  commit, with the capsulectl core's version beside them (`core_version`). Behavior, flags, the exit code 6 on an alarm and the
  alarm's words are unchanged. Its guide (`docs/CANARY.md`) and the two GitHub
  Actions templates (`examples/canary-watch.yml`,
  `examples/deal-harness-watch.yml`) moved here from capsule-cli; the
  templates install this plugin's binary and run `capsulectl-deal canary
  watch`, which needs no capsulectl beside it.
- Builds against an unreleased capsulectl (capsule-cli `005e7e97`, capsule-viewer `389c1bac` (its Go module pinned by pseudo-version) and agent-action-capsule `1d1752bf`, all unreleased; capsule-cli#428) and
  names no deal-specific export of its host package where a generic one
  exists: the plugin owns its store through `cli.OpenOwnedTarget` and marks it
  (`plugin_owned`, written under the writer lock by every write session and by
  `deal init`, never on a read), `bundle|disclose|permalink --deal` and `cll checkpoint cadence`
  come from `cli.ExtendBundle` and `cli.GraftCommand`, and `deal init` now
  writes `log: deal` into the profile. Deals behave as before; a deal profile
  saved without `log:` is still read as the deal plugin's until the core drops
  that default.
- A deal page opened by the host's viewer (`presentation build`, a shared copy) now
  reads "None of the bundle's checks failed, but some were not run" where it read
  "Contents checked", and its status is `incomplete`, not `verified`. The
  browser has no checkpoint verifier, so under agent-action-capsule 0.8.0's strict
  mode the page does not check the checkpoint signature
  (`checkpoint_verifier_unavailable`); it does check every record's producer
  signature, and a forged signer now reads `failed`. The page scripts assert this
  is the only check not run. No capsulectl-deal release can be cut from this
  until capsule-cli, capsule-viewer and agent-action-capsule are tagged.
- Builds against evidencebook v0.8.0. The deal cadence verifies witness receipts under a directory built from the profile's pinned witness (endpoint and key), so an unpinned profile trusts no witness and no receipt reads as witnessed; a receipt that cannot be checked leaves its tick unwitnessed and pending (evidencebook-deal#32).

## v0.1.0-rc23

The first release of capsulectl-deal and the deal skill from this repository
(capsule-cli#326), built against capsulectl v0.1.0-rc23. A capsulectl-deal
release takes the version of the capsulectl release it pairs with
(RELEASING.md), and requires that capsulectl: any other capsulectl reports a
version mismatch.
capsulectl no longer installs it by name (`plugin install deal`); download
`capsulectl-deal-<version>-<os>-<arch>` from the release, check it against
`SHA256SUMS` and its attestation, then run
`capsulectl plugin install ./capsulectl-deal-<version>-<os>-<arch>`.

### Known issues

Still open as of capsule-cli v0.1.0-rc23, where they are tracked (first
listed in its rc22 release notes):

- The deal journey shows only an OK or a no as your row (capsule-cli#415).
  Another answer to a pause, such as asking to verify the contact first, has
  no row of its own.
- A taken-down listing still counts (capsule-cli#384). There is no way yet to
  record that a listing came down, so its classes still count in
  `listing_classes`.
- The deal profile's checker needs `jsonschema` (capsule-cli#422). Install it
  before you run `skills/deal/profile/check_profile.py`.
- The deal page's close date is in UTC (capsule-cli#426) while the page's
  other times are local.

### Changed

- A release of `capsulectl-deal` seals its own build as each record's
  producer, `capsulectl-deal <tag> (<commit>)`, instead of the capsulectl core
  it embeds, so a record names the deal build and commit that sealed it
  (capsule-cli#326, design note section 4.2). A build without a release still
  seals the core's `capsulectl <version> (<commit>)`. Shared copies keep the
  new stamp. Records already sealed are unchanged (#3).

### Added

- The deal skill has a Sale procedure (`skills/deal/SKILL.md`, "Sale"): what
  the agent does, step by step, when the user sells, starting from
  `deal sale new` (#34).
- A nightly workflow, `.github/workflows/core-main.yml`, builds capsulectl
  from capsule-cli main and runs the deal page and portability checks against
  it, so a core change that breaks deal pages shows before a core release; a
  failed run comments on one open issue (#4).
- The deal report's page data carries `page_producer` (the build that made the
  page: name, version, commit) and, on a release, `release_notes` (this
  repository's release). With capsule-cli#448 the page compares builds only by
  name, marks a step capsulectl sealed as an earlier build, and links this
  plugin's release notes (#3).
- The release workflow, `.github/workflows/release.yml`: a pushed `v*` tag is
  built, attested and published once a release reviewer approves the run. It
  publishes only a tag a shared copy keeps as a producer version, and only
  against a published capsule-cli release (#3).
- The user's own deal report carries `card_openings`: for each answer given on a
  card, the answer's step and the card's opening `{nonce, text}`, which
  recomputes to the approval's sealed `card_commitment` (`rendering_commitment`
  in a typed record). A page can then show the card the user answered on,
  checked when the page is written. No shared copy
  carries it. It rides in the user's own sealed report; no approval or other
  step record changes. The page check refuses to write a page whose card
  opening does not recompute, names no approval, repeats a step or rides on a
  shared copy (evidencebook-deal#1).

### Moved here from capsule-cli (capsule-cli#326)

- `capsulectl-deal` and the `deal` skill now live in this repository, imported
  from capsule-cli at commit e0511c4 (capsule-cli#446). Their history before that is
  capsule-cli's. The import itself changed no command, flag, output or
  record; the entries above came after it.
