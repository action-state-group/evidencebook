<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: `deal report --html` writes an x-deal-v1 deal's receipt page (capsule-viewer#405)

- `deal report --deal D --html FILE` on an x-deal-v1 deal writes your own copy's receipt page. Before, it refused with "has no page yet".
- capsule-viewer's x-deal-v1 receipt module draws it from the same bundle `--bundle` writes.
- The page carries this binary's own `deal verify --bundle` report of that bundle. It says "capsulectl verify reported its signature check as: pass. This page did not check the signature itself."; a report that does not pass refuses the copy on the page.
- It takes no `--out`, `--bundle` or `--withheld`, and no `--share` but keep.
- capsule-viewer go is pinned by commit to main 130877b1, with no release. That commit carries #417, #420 (the other party's half), #421 (the witness time and the file-scoped line) and #422 (the runtime).
- `deal report --deal D --html FILE --witness-directory W` also checks your own book's witness time. It finds the stamp proof and receipts as `deal checkpoint proof` does and passes them to its own `verify`, beside the copy. The page carries only what verify reported, never the proof (no copy carries witness evidence, since a stamp proof shows your shelf's checkpoint and position).
- `deal verify --bundle F --stamp-proof P --witness-directory W` reports, in the copy's x-deal-v1 entry, `witnessed: {status, witness, at}`:
  - `verified`, with the directory's name for the witness and the earliest verified receipt's own time;
  - `withheld`, with no directory, no receipt held, or no receipt it could check under the directory;
  - `failed`, when the proof is not of this copy's own signed checkpoint or does not verify, or a receipt fails and none verifies.
  It never changes the verdict.
