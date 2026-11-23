### Fixed (breaking): x-deal-v1's payment check runs on the payment's own legs beside a refund (#722)

- **The bug.** `xdealv1.PaymentCheck` stopped with reason `refund_cites_offer` as soon as a refund or remedy leg (`reverses`, `remedy_for`) cited the offer's terms leg. A payer who paid too little could seal a 1-unit refund and turn the payment row from failed into not checked.
- **The fix.** The check now runs on the payment's own legs. Refund and remedy legs citing the offer's terms leg take no part, and those not superseded are listed in the new `PaymentResult.NotChecked`. A status row counts them in `check_not_checked`. This follows Steven's ruling on #722.
- **Breaking.** `ReasonRefundCitesOffer` is removed. The payment, status and report vectors are regenerated, with a new case `short-payment-with-refund-fails` and a new status case `payment-refund-not-checked`.
