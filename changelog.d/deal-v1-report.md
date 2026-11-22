### Added: `capsulectl deal report` on an x-deal-v1 deal (#626, B4b part 1)

- `deal report --deal ID` on an x-deal-v1 deal prints your own copy of the deal's report: each row's status as computed now from what you hold, whether the deal is settled, and what it rests on (PROFILE §10). Printing seals and writes nothing.
- `--out FILE` also seals the report, standalone and appended to no book, and writes its pair, which verifies against the deal's agreement.
- `--read-only` prints.
- A book with records after its last checkpoint has no report until the next `deal tick` covers it. A deal with no book yet names `deal agree`.
- `--html`, `--email` and `--from-bundle` do not apply. A shared copy (`--share`) and the bundle (`--bundle`) are not served yet.
- `deal close` and `deal terms-result` on an x-deal-v1 deal now add "See `deal report`".
