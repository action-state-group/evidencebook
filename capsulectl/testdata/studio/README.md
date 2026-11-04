# Terms Pack Studio replay fixture

`sticker-trades.json` (`studio-sticker-trades/v0`) is ten past fox-sticker trades for the Terms
Pack Studio first demo (evidencebook-deal#136). `scripts/studio-replay.sh FIXTURE SELLER_HOME
BUYER_HOME` replays them through `capsulectl` and the deal plugin into a seller's and a buyer's
deal store; `scripts/check-studio-replay.sh` replays into fresh stores and checks the result.

Nine trades are clean. Trade 7 is the seller raising the price (150 to 175 minor USD) after the
buyer accepted the offer; the buyer pays 175.

## What a store holds

Each trade is a sale (`sale-…`, one item, closed with its completed thread) with one buyer thread
(`deal-…`). The buyer's store holds the other half of the same deal under the **same `deal-…` id**.
Ten closed `deal` entries per store (`deal list --json`, `kind: "deal"`); the seller's also holds ten
closed `sale` entries.

Read a deal's sealed records with `capsulectl --profile deal deal export --deal ID --output FILE`
(a JSON array in order, no raw values). Two shapes, both with the replay's own sealed time (the
CLI cannot backdate, so a whole replay spans a minute or two):

- the `*/v0` records (`task-authority/v0`, `proposed-action/v0`, `action-evaluation/v0`,
  `action-record/v0`, `action-approval/v0`, `action-outcome/v0`): `{"type": …, "at": …, "body": …}`;
- the deal's own records (`baseline`, `evidence`, `close`): `{"body": …, "x-deal-v0": {"record_type":
  …, "at": …, "seq": …}}`, with **no** top-level `type` or `at`.

## Seller's records for a clean trade (in order)

| record (`type`, or `x-deal-v0.record_type`) | `body` carries |
|---|---|
| `baseline` | the deal's terms |
| `task-authority/v0` | the sale's authority |
| `proposed-action/v0` | `action: "offer"`, `amount_minor`, `terms.price_minor` |
| `action-evaluation/v0` | the check's disposition and findings |
| `action-record/v0` | `action: "offer"`, `amount_minor` (the offer as made) |
| `action-approval/v0` | the buyer's acceptance (observed) |
| `proposed-action/v0` | `action: "commit"`, `amount_minor`, `terms.price_minor` |
| `action-evaluation/v0` | |
| `action-record/v0` | `action: "commit"`, `amount_minor` |
| `evidence` | `source: "x402_settle_response"` (payment received; `x-deal-v0.at` is its time) |
| `evidence` | `source: "seller_handoff"` (delivered; `x-deal-v0.at` is its time) |
| `action-outcome/v0` | `status: "received"` |
| `close` | |

The paid amount is in the buyer's terms leg and settlement legs, not in these exports; the
buyer's own store has `action-record/v0` with `action: "pay"` and `amount_minor`.

## Trade 7

The seller raises the price after the buyer accepted and the seller committed. (The deal itself
refuses a commit at a changed price, `changed_after_acceptance`, so the raise comes after the
commit.) The miss is in the sealed amounts: the offer `action-record/v0` and the commit
`action-record/v0` are both 150, then a `detail_change` record follows the commit:

```json
{"body": {"changed": ["price"], "source": "seller_message", "terms": {"price_minor": 175}},
 "x-deal-v0": {"record_type": "detail_change", "at": "…", "seq": …}}
```

and the buyer's half pays 175 (`action-record/v0` `action: "pay"`, `amount_minor: 175`). Trade 7
has a commit record like the other nine, so it counts as a provided deal on the History card.

So a "price as offered" fold is: the last offer's `amount_minor` equals the sealed commit's
`amount_minor`, and no `detail_change` record has `"price"` in `body.changed`.

## History card

`deal standard-history declare --holder-kind person`, two `deal tick`s, then `deal standard-history
card --out FILE` on a replayed store gives (checked on the replay): the seller's card counts 10
provided and 0 bought, the buyer's 10 bought and 0 provided; every deal reads `undecided` (no
deal reads `held` or `did_not_hold`: that needs a reconcile Result, which these x-deal-v0 deals
do not carry) and `happened` is `receipt_only` for all ten.

## Times

Sealed times are the replay's clock, so "paid before delivered" and "delivered within N days" are
met by every trade on the sealed `x-deal-v0.at` of the `x402_settle_response` and `seller_handoff` evidence.
The fixture carries no times.
