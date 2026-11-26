<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: an x-deal-v0 act with a broken link to its check marks the rules history incomplete (evidencebook#955)

- In the history `deal check` gives a pinned rules checker, an earlier x-deal-v0 act that rests on an approval or a check that cannot be found, or whose check cannot be read, now marks the history incomplete, as an x-deal-v1 act already does. Before, the act was kept without who it was with and the history was called complete.
- What the person sees: a limit that needs every earlier payment, such as Everyday Rules' 7-day limit (r05), now says it could not be checked because the history is incomplete, and the check asks the person instead of allowing the payment. Before, that limit was checked as if the history were complete.
- Not changed: on that incomplete history, Everyday Rules r02 and r06 still tell the person there was no earlier payment to that merchant, now beside r05's "not fully checked" (capsule-engine#327).
- An act recorded with no check at all is unchanged and leaves the history complete: it cites no check, so there is no link to break.
