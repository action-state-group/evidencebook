<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: `deal report --html` writes an x-deal-v1 deal's receipt page (capsule-viewer#405)

- `deal report --deal D --html FILE` on an x-deal-v1 deal writes your own copy's receipt page. Before, it refused with "has no page yet".
- capsule-viewer's x-deal-v1 receipt module draws it from the same bundle `--bundle` writes.
- The page carries this binary's own `deal verify --bundle` report of that bundle. It says "capsulectl verify reported its signature check as: pass. This page did not check the signature itself."; a report that does not pass refuses the copy on the page.
- It takes no `--out`, `--bundle`, `--withheld`, or `--share` other than keep.
- capsule-viewer go is pinned by commit to capsule-viewer#417, with no release.
