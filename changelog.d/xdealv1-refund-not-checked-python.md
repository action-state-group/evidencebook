<!-- changelog: python/CHANGELOG.md -->
### Fixed (breaking): `evidencebook.deal.v1.payment_check` runs on the payment's own legs beside a refund (evidencebook#722)

- A refund or remedy leg citing the offer's terms leg no longer stops the check with `refund_cites_offer`. That let a 1-unit refund turn a failing payment into "not checked".
- The check now runs on the payment's own legs. Those refund legs, when not superseded, are listed in the new `PaymentResult.not_checked`, and a status row counts them in `check_not_checked`. This matches Go byte for byte, on the shared vectors.
- `REASON_REFUND_CITES_OFFER` is removed.
- This is a correctness fix to a function the package already has, not a new port.
