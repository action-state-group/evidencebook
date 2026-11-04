### Fix: `StandardHistoryDeal` prices a BTC deal in msat (#388)

- A deal whose check states currency `BTC` is no longer refused as unpriced: its entry is `{value: amount_minor, assetCode: "BTC", assetScale: 11}` (10^-11 BTC = msat; the settlement-records draft's own example). The scale comes from an explicit table (`historyAssetScales`), not from `MinorUnits`, which still has no ISO 4217 minor units for BTC. The Python and Rust copies match. The card keeps BTC as its own currency row and never sums it with fiat. Other codes with no ISO 4217 minor units stay refused as unpriced.
