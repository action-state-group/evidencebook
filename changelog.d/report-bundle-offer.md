### Added: an x-deal-v1 copy carries the offer it rests on (#1065)

- **`book.offer`.** `StatusProvider.ReportWithBook` adds the offer the agreement cites to a keep or counterparty copy's bundle extension, as its sent pair `{record, agent_input}`, byte for byte as `StatusProvider.OfferPair` gives it (the pair the holder sent, else the one it received). A receipt reads the item, quantity and price from it (Steven's u561). An adjudicator's copy does not carry it (unruled), and a copy whose holder holds no pair carries none.
- **`VerifyReportBundle`** accepts `offer` only when it passes `VerifySent`, is a propose of the report's deal whose record digest is the agreement's `offer.digest`, and is signed by the deal's offerer.
