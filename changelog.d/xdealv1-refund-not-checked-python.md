<!-- changelog: python/CHANGELOG.md -->
### Fixed (breaking): `evidencebook.deal.v1.payment_check` runs on the payment's own legs beside a refund (evidencebook#722)

- A refund or remedy leg citing the offer's terms leg no longer stops the check with `refund_cites_offer`. That let a 1-unit refund turn a failing payment into "not checked".
- The check now runs on the payment's own legs. Those refund legs, when not superseded, are listed in the new `PaymentResult.not_checked`, and a status row counts them in `check_not_checked`. This matches Go byte for byte, on the shared vectors.
- A payment leg is now superseded only by a later payment leg answering the same terms leg that AAC's verifier reads beside the offer. Before, a refund, a leg of another terms leg, or a malformed leg carrying `supersedes` could take the payment leg out of the check, which read "not agreed" instead of failed. A refund is still superseded by any later leg of its side. `not_checked` is the dataclass's last field, so positional construction is unchanged.
- `REASON_REFUND_CITES_OFFER` is removed.
- This is a correctness fix to a function the package already has, not a new port.
