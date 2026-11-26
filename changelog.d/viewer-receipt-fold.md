<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Changed: the receipt page is drawn by capsule-viewer at 8fe749b (capsule-viewer#437, #391)

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer/go at 8fe749b, the receipt-page fold from Steven's 2026-11-25 run (capsule-viewer#424, #429-#432): term names in words ("Payment as agreed", "Delivery", "Item as described"), the terms' tally beside where the deal stands, local times in the records table, one plain line on why no payment record is shown, and the time and named-witness columns only on the holder's own keep page.
- The same range brings capsule-viewer#391: service and rental deals are worded ("Service", "Rental"), and a receipt follows the host page's colour scheme, so it is dark on a dark page.
- Each receipt manifest now pins three stylesheets: the kit's, its dark overrides and the new receipt.css (which keeps words in table cells whole), and each script hash changes.
