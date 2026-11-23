### Added: `capsulectl deal report` on an x-deal-v1 deal (#626, B4b part 1)

- `deal report --deal ID` on an x-deal-v1 deal prints your own copy of the deal's report: each row's status as computed now from what you hold, whether the deal is settled, and what it rests on (PROFILE §10). It counts the other side's records you have received; the other side may hold different records and see a different status. Printing seals and writes nothing.
- Beside the report, `settled_note` says that no act on the deal carries the person's own signature yet (the format is still being defined: agent-action-capsule#627), so "settled" is computed from the agent's records, which the person has not signed. evidencebook#729 replaces this with a check per act.
- `--out FILE` also seals the report, standalone and appended to no book, and writes it with its signature to that new file. The sealed report verifies against the deal's agreement, and it must be made with the profile you agreed with.
- A book with records after its last checkpoint has no report yet: the next cadence tick covers it (about every cadence interval, 5 minutes by default; `deal tick` runs one once it is due), then run `deal report` again. A deal with no agreement of yours recorded yet names `deal agree`.
- `--html`, `--email`, `--from-bundle`, `--directory` and `--include-agent-claims` do not apply.
- `deal close` and `deal terms-result` on an x-deal-v1 deal now add "See `deal report`".
