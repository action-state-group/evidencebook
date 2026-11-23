<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: a receipt says when the checkpoints stopped (capsule-cli#600)

- `capsulectl deal report` adds "Checkpoints stopped at <time>: ..." to the receipt's assurance text (and `assurance.checkpoints_stopped` in its JSON) and to the receipt email when the newest checkpoint tick is more than two cadence intervals old, so a stalled schedule does not read as a clean history. It says nothing while the schedule is on time or before the first tick, and it is not on the sealed page, which checks offline. The line points at the capsule-cli README's "Keeping the poll running"; capsulectl still has no scheduler or daemon.
