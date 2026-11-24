<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: a buyer's deal that allows pay seals what the user confirmed (evidencebook#839)

- `deal open --records typed --authority-line` (seals nothing) prints the line to show the user before a buyer's deal that allows `pay` opens: "I'll record that you authorize: pay up to <max> for <item>. You chose <seller>; the rules check the amount, not the seller. Is that right? (yes/no)", or "You haven't picked a seller, so this doesn't limit who gets paid." when none was named. The amount is in the user's own unit.
- Such a deal now opens only with `intent.rendered`, that line as shown (refused if the open's own terms render another), and `intent.confirmed`, the user's own words answering it, and says "Recorded." once sealed. Its `task-authority/v0` carries `asked`: the item, the seller the user picked (a keyed fingerprint, or `unconstrained`), and commitments to the line shown and to the yes. Everyday Rule 26 can then cite a record of what the person said; rule 26 still reads only `allowed` and `max_total_minor`.
- Deals sealed before keep their records unchanged. The Rust crate and the Python Goose reference are not changed here.
