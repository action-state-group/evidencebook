# Cancellation and refund

Part of the [deal skill](../SKILL.md): its [Rules](../SKILL.md#rules-never-break-these) and [Points of no return](../SKILL.md#points-of-no-return) bind every step here, and `ID`, `OFFER` and `--profile deal` are as its [Procedures](../SKILL.md#procedures) say.

- **Before the deal starts:** `deal offer cancel OFFER --out FILE` (your own
  offer) or `deal offer decline OFFER --out FILE` (one sent to you), and send
  the file.
- **After agreeing:** a cancel is checked like any act: `deal check` with
  `"action": "cancel"`, the user's answer, then cancel with the merchant.
- **A refund the user pays** (the user is the seller) rests on that cancel
  check and names the leg of the user's own book it refunds:
  `deal settle --deal ID --sent --check CANCEL_CHECK_ID --reverses LEG
  --payment-ref-type TYPE --payment-ref REF` (the refund's own reference:
  a cancel check records none), then `--status` as in [Purchase](purchase.md#purchase) step 9.
- **A refund the user receives** is a payee leg that reverses the user's own
  payment: `deal settle --deal ID --reverses LEG --received N --receive-fee N
  --status settled --payment-ref-type TYPE --payment-ref REF --out
  refund-leg.json`.
- `LEG` is the `digest` `deal settle` printed for that leg of your own
  book.
- A refund leg is shown as not checked: refunds have no terms of their own
  yet, and the payment check covers the payment's own legs.
