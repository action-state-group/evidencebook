<!-- changelog: python/CHANGELOG.md -->
- `standard_history_deal` prices a deal whose check states currency `BTC` as `{"assetCode": "BTC", "assetScale": 11}` (`amount_minor` counts millisatoshi) instead of raising `Unpriced`; `minor_units("BTC")` stays unpriced, and other codes with no ISO 4217 minor units are still refused (evidencebook#388).
