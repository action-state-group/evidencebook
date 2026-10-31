# Changelog

## Unreleased

- Requires capsule-viewer `v0.7.0` (the frames merged since 0.6.0:
  capsule-viewer#186, #187, #188, #189, #190, #192, #193). The presentation
  goldens are rebuilt with `build-fixtures.sh` against it (capsulectl-book from
  evidencebook#244), their snapshots regenerated with Chrome.
- The receipt with the other party's half (the 9b frames 6 and 7;
  evidencebook-deal#78, #79, #81; design note `docs/design/two-sided-receipt.md`).
  The deal view gains a second module, `capsulectl.deal-view-composed/v0`, for a
  deal composition: it checks the composition and each of the other party's
  answers itself, and says "Stamped by both sides", "Your receipt only" or "No
  record". The other party's side shows only their key's first digits, never a
  name. A record that is in the file but did not check gets its own box,
  never one that reads as missing. The single-book deal page no longer reads a
  composition.
- `deal compose --html --keep` builds that page and carries your keep copy in it
  whole, so the page checks your side too. The page says on its face that it is
  your private copy and that the composed bundle is the file to share. The
  composed bundle is unchanged.
- `deal compose` takes a single answer with `--keep`, and refuses an answer
  recorded as not holding up.
- A Mesh payer's receipt is read the same way: both halves must name the same
  exchange by the client's nonce and request. Tested on a synthetic composition
  until the plugin builds the real one (Mesh-LLM/capsules-plugin#196).
- A bundle with two rounds reads its latest round (#81).

## v0.1.0-rc26

Built against capsulectl v0.1.0-rc26 and capsule-viewer v0.6.0 (RELEASING.md).

- The deal view draws the receipt as Steven's 9b frames do (evidencebook-deal#82,
  #80): a verdict banner, the receipt card in the user's own words, "Your side
  · in your book" with a time on every line, the other side marked as not in
  this file, a closed "The stamps" list in the five words (passed, failed,
  missing, not checked, unclear), "What this receipt proves" and "What it
  doesn't", and "Tell your agent". The steps, authority, what was told and the
  anomalies sit under "More on this deal". A shared copy speaks of the holder
  in the third person. The other side's half, and a Mesh node's copy
  (evidencebook-deal#78, #79), are not drawn yet.

- The presentation fixtures are rebuilt with capsule-viewer 0.6.0's page runtime and
  capsulectl-book rc26 (`BOOK_REF=v0.1.0-rc26`; the outcome report's new headline and tile wording); the
  fixture builder no longer fails on a second run (it removes the `10-composed-private-half` directory it
  writes `--bundle` into).

## v0.1.0-rc25

Built against capsulectl v0.1.0-rc25 and capsule-viewer v0.5.0 (RELEASING.md).

- Pages carry their bundle in a JSON data element (capsule-viewer#109, #167):
  the share gate reads that element as the bundle, and still reads a page
  built before it by the `window.__BUNDLE__ =` prefix (#41, #48).

- The user's own receipt line for a flagged check gives what caused the flag
  first, in the rules' own words (a rule the checker could not evaluate is
  said so), and then, after what the user and the agent chose, the rules the
  ruleset declares but the checker does not measure, as "not yet checked", so
  the unmeasured rules no longer read as the reason for the flag. With no flag
  the not-yet-checked note stands on its own. A shared copy names none of the
  user's rules, and an adjudicator's copy keeps the not-yet-checked note and
  who chose what, without the reasons (#55).

- The rules checker is held to the acting role's pin: `deal check` checks the
  checker's answer against that role's definition digests
  (`rules_checker.roles`, capsule-cli#492), so swapping one role's pin by
  `profile update` changes only that role's check; a role with no entry, or
  one that names a pack alone, is held to the flat digests, and a profile
  without roles checks as before. Sending the checker the role's pack is a
  separate change that needs the `external-check-input/v0` schema to carry it
  (#66). The two witness-delivery wording assertions follow
  capsule-cli#489 (#68).

- `deal terms-leg` writes the buyer's signed terms leg, the file
  `deal note --kind payment_received|delivered --terms` answers, so scripts
  and outside developers share one public command. It signs with the profile's key, or
  with `--ephemeral` a key made for the run, and touches no deal store (#70).

- The spending-limit gate masks a random id by the copy's JSON structure: only
  the value of a `cose` member found by walking the copy's real JSON regions,
  not any quoted id-shaped string found by a pattern. Prose that imitates
  JSON (`My "limit": "8USD-Per-Month-Plan-2026".`) and a field value shaped
  like an id (a seller's agent claim) no longer hide a bound from the gate
  (#62; design note `docs/design/limit-gate-structural-id-mask.md`).

- The x-deal-v0 profile is EvidenceBook's `dealprofile` (evidencebook v0.10.0,
  evidencebook#93). The plugin seals records, steps and reports through it, and
  `skills/deal/profile/` and the plugin's copies of the schemas, sentence marks
  and baseline vectors are gone: the skill points to EvidenceBook's
  `dealprofile/x-deal-v0/`. No sealed byte changes; the baseline vectors from
  #65 still pass. `scripts/run-demo.sh` reads the profile from
  `DEAL_PROFILE_DIR`, or from the evidencebook module when run from source.

- The deal skill's "Points of no return" table lists a seller's `offer` and
  `represent`, and a test fails when the table and the plugin's action list
  differ (#23).

- The presentation goldens' tests run on capsule-viewer/go v0.3.0
  (`pageruntime`, `presentation`) in place of the names capsule-cli removed in
  capsule-cli#486, and `sameViewer` accepts that one runtime only. `page.html`
  and the snapshots under `testdata/presentation` are refreshed in Chrome; their
  summary line reads "checkpoint signature not checked here". The Node check
  that ran a deal page with no `crypto` global (`TestADealPageWithoutWebCrypto`,
  a script capsule-cli deleted with its vendored runtime) is gone, and no
  test now runs a built deal page without `crypto` (#54) (#51).

- `deal compose --html FILE --keep KEEP_BUNDLE` writes the composer's
  both-halves page: the composed bundle with the composer's own keep copy
  (`deal report --bundle`) shown as "Your half (private)", joined at the page
  (evidencebook-deal#31, option b). composed/v1 is unchanged: the keep copy
  rides in the page's bootstrap, never in the composed bundle, so share and
  export carry served copies only. The page is refused, and no bundle is
  written, unless the keep bundle verifies VALID, is audience keep, and names
  the composition's deal id and its composer. Join states still come from the
  served members alone. Adds the wording key `report.private_half`.

- The `deal report --html` page runs capsule-viewer's own page runtime
  (`go/pageruntime` in capsule-viewer/go v0.3.0), pinned by its digest, in
  place of capsule-cli's vendored build. Its bootstrap renders through the
  viewer's `renderEvidenceGraph`. The summary line now names the one signature
  check the page didn't run ("checkpoint signature not checked here", from
  capsule-viewer#118). Prerequisite for capsule-cli#472 (#49).

- The deal words move out of capsulectl's built-in wording pack into this
  plugin's own pack, `capsulectl.deal-wording/v0`
  (`internal/deal/assets/wording/capsulectl-deal-en.json`, 268 keys, names
  unchanged; capsule-cli#428 step 3). The plugin registers that pack with
  `cli.RegisterWording`, and its handshake carries it, so `capsulectl profile
  update --wording` and `doctor` check a pack pinned on a deal profile against
  the deal keys as well as the core's. A pack whose `card.terms.*` entries could
  hide a term is still refused. A pack made from the core's pack alone is now
  refused with a line naming this plugin. A pack pinned earlier holds every key
  and is still used. Builds against capsule-cli `5745be57` (unreleased;
  capsule-cli#480 merged).
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
