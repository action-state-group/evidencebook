<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: the receipt page names its wording pack capsuleviewer.receipt.wording.en/v1 (capsule-viewer#438, #956)

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer/go at 2aa43cf. Its receipt wording pack is `capsuleviewer.receipt.wording.en/v1`, with the same words as the old example pack (Sherlock2 on #956: a person's receipt page does not run on a pack named "example"). Each receipt script hash changes with it; pages written before keep their own scripts and pack.
