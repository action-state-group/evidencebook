<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: an x-deal-v0 act with a broken link to its check marks the rules history incomplete (evidencebook#955)

- In the history `deal check` gives a pinned rules checker, an earlier x-deal-v0 act that rests on an approval or a check that cannot be found, or whose check cannot be read, now marks the history incomplete, as an x-deal-v1 act already does. Before, the act was kept without who it was with and the history was called complete, so a rule keyed on an earlier payment to this merchant (Everyday Rules r02 and r06) read that merchant as new and did not say the history was incomplete.
- An act recorded without a check, which rests on nothing, is unchanged: it leaves the history complete.
