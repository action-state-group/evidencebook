# Selling

Part of the [deal skill](../SKILL.md): its [Rules](../SKILL.md#rules-never-break-these) and [Points of no return](../SKILL.md#points-of-no-return) bind every step here, and `ID`, `OFFER` and `--profile deal` are as its [Procedures](../SKILL.md#procedures) say.

## Sale

The user sells one item. On x-deal-v1 each buyer sends an offer: keep each
with `deal receive`, run admission on the buyer you agree with, and agree to
that offer ([Purchase](purchase.md#purchase), steps 4 to 6, with the user as the seller). Decline
every other buyer's offer with `deal offer decline OFFER --out FILE` and
send each its file. A sale's listing and its buyer threads are x-deal-v0
only (see [LEGACY-X-DEAL-V0.md](../LEGACY-X-DEAL-V0.md)).

## Where the user sells: payment received, and what was delivered

- **What the user received** is a payee leg, sealed when the payment shows
  up, with the amount net of any fee taken and the rail's reference:

  ```sh
  capsulectl --profile deal deal settle --deal ID --received 6000 --receive-fee 0 --status settled \
    --payment-ref-type TYPE --payment-ref REF --out payee-leg.json
  ```

  On x402 add `--payment-ref-network NETWORK`. `--status pending` first when
  the rail says so; a later observation of the same payment names the
  earlier leg with `--supersedes`.
- **What was delivered** is an act on a commit check: `deal check` with
  `"action": "commit"`, the user's answer as in [Purchase](purchase.md#purchase) step 8, then

  ```sh
  capsulectl --profile deal deal perform --deal ID --act deliver --check CHECK_ID \
    --observed carrier_scan --out perform.json
  ```

- Send each file, and state how the rows went ([Purchase](purchase.md#purchase), step 10).
