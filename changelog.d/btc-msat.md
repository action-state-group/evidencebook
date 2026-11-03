
### Docs: for currency BTC, `amount_minor` counts millisatoshi (#385)

- x-deal-v0 (PROFILE.md sections 3 and 11 and the `currency` descriptions in the schemas, Go, Python and Rust copies): for currency `BTC`, `amount_minor` counts millisatoshi (10^-11 BTC), shown as "N msat". `dealprofile.MinorUnits("BTC")` stays unpriced, and a BTC deal is refused as unpriced by `StandardHistoryDeal` (a test pins it). Description-only; validation is unchanged.
