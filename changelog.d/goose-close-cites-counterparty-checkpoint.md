<!-- changelog: python/CHANGELOG.md -->
- The Goose reference's close step cites the checkpoint of the counterparty's half its node holds, and each side requests
  the other's half at the exchange's end and shares its own by default (`loop_deal_end_exchange`). `loop_deal_reconcile` now
  holds any matching half, naming its final record id and checkpoint digest; `loop_deal_close` refuses without a held half
  unless `uncited` is set; `verify_half` checks a step's `cites` links. x-deal-v0's header rule states both extra cites.
  Mesh nodes accept this form beside the current one during the overlap window (capsules-plugin#220). Step 05 maps to
  Goose's records in the example's README (evidencebook#303).
