### Added: `deal offer decline` on an x-deal-v1 offer (#626, B3)

- `deal offer decline OFFER_DIGEST [--reason not_selected|other] --out FILE` declines an x-deal-v1 offer sent to your key. It seals a decline in your journal and writes the file to send back to the offer's sender, who reads the deal as declined on `deal receive`.
- **What it accepts:**
  - only an offer whose `to` is your key, since a decline is signed by the key the offer was sent to;
  - not your own offer (`deal offer cancel` withdraws that), not an open offer, and not one sent to another key;
  - once per offer, whatever the reason, and never once you agreed to it.
- **An agreement and a decline exclude each other:** `deal agree` refuses an offer whose decline is under way, and a decline re-run never overtakes an agreement.
- A stopped decline finishes on its re-run.
- **A sale to several buyers on x-deal-v1:** each buyer sends the seller an offer, and the seller agrees to one and declines the others as `not_selected`.
- It is not `deal decline`, which declines a held evidence request.
