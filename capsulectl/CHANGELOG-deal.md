# Changelog

## Unreleased

The first release of capsulectl-deal and the deal skill from this repository
will carry these entries (capsule-cli#326); its version is not set yet. It
requires capsulectl v0.1.0-rc23: any other capsulectl reports a version
mismatch. capsulectl no longer installs it by name (`plugin install deal`);
download `capsulectl-deal-<version>-<os>-<arch>` from the release, check it
against `SHA256SUMS` and its attestation, then run
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
  it embeds, so two releases on one core no longer seal the same stamp
  (capsule-cli#326, design note section 4.2). A build without a release still
  seals the core's `capsulectl <version> (<commit>)`. Shared copies keep the
  new stamp. Records already sealed are unchanged (#3).

### Added

- The deal report's page data carries `page_producer` (the build that made the
  page: name, version, commit) and, on a release, `release_notes` (this
  repository's release). With capsule-cli#448 the page compares builds only by
  name, says a step capsulectl sealed apart as an earlier build, and links
  this plugin's release notes (#3).
- The release workflow, `.github/workflows/release.yml`: a pushed `v*` tag is
  built, attested and published once a release reviewer approves the run. It
  publishes only a tag a shared copy keeps as a producer version, and only
  against a published capsule-cli release (#3).
- The user's own deal report carries `card_openings`: for each answer given on a
  card, the answer's step and the card's opening `{nonce, text}`, which
  recomputes to the approval's sealed `card_commitment` (`rendering_commitment`
  in a typed record). A page can then show the card the user answered on,
  checked when the page is written (actionstate-muse#204). No shared copy
  carries it. It rides in the user's own sealed report; no approval or other
  step record changes. The page check refuses to write a page whose card
  opening does not recompute, names no approval, repeats a step or rides on a
  shared copy (evidencebook-deal#1).

### Moved here from capsule-cli (capsule-cli#326)

- `capsulectl-deal` and the `deal` skill now live in this repository, imported
  from capsule-cli at commit e0511c4 (capsule-cli#446). Their history before that is
  capsule-cli's. The import itself changed no command, flag, output or
  record; the entries above came after it.
