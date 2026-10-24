# Changelog

## Unreleased

### Added

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
  capsule-cli's. No command, flag, output or record changes.
