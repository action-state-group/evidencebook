<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: the x-deal-v1 receipt page shows local times and one witness line (capsule-viewer#423, #424)

- The page `deal report --html` writes for an x-deal-v1 deal shows its times in the reader's own zone. Due times and the window end carry UTC beside them, for example "Dec 2, 2026, 12:56 PM CST (18:56 UTC)". The page's data keeps UTC.
- With `--witness-directory`, when capsulectl verify reports your book's witness time verified, the page says it in one line: "Time-stamped by <witness> at <local time> (<UTC time>), as capsulectl verify reported on your own book. This file carries no witness receipt, so this page did not check it itself." The header line that said only "This file carries no witness receipt…" is left out on that page, so it no longer reads as "not stamped".
- capsule-viewer go is pinned by commit to main bb8169bb (capsule-viewer#427), with no release.
