<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: an x-deal-v1 copy carries the offer it rests on, and its receipt shows the item (#1065)

- `deal report --bundle` and `--html` on an x-deal-v1 deal: the holder's own copy and the counterparty's copy carry the offer the agreement cites (`book.offer`, its signed sent pair, as the store holds it). The receipt page draws the item from the copy itself. An adjudicator's copy does not carry it: whether it should is unruled.
- A shared bundle's disclosure record names the offer it carries by its record digest (`offer`).
- capsule-viewer moves to 462da574, which includes the receipt view-model note's PRs 3 and 4 and the sent-pair reading of `book.offer` (capsule-viewer#493, #499, #501). The page's core now draws the holder's book-witness line under the banner and one check list with capsulectl's answers. The module's ladder no longer has a witness rung, and the ladder is drawn once per page.
