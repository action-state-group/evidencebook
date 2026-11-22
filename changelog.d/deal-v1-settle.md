### Added: `capsulectl deal settle` on an x-deal-v1 deal (#626, B2 part 2)

- `deal settle` seals a payment leg in your deal book and writes the file to send to `--out`, which the other side counts with `deal receive`. The leg (payer or payee), its terms (the offer as sent) and its amount come from the deal and your check. The settlement member is checked with AAC's structure check before anything is sealed.
- A payment you make takes two calls.
  - `deal settle --sent --check DIGEST`, before you hand the payment to the rail. It runs the first-act rule, the check's approval and rule 5's amount on the leg as it will be sealed, so a payment they would refuse never leaves. It then counts the amount toward your limits and seals a "payment sent" evidence record in your journal.
  - `deal settle --check DIGEST --status settled|failed --out FILE`, when the rail answers. It seals the one payer leg, bound to what was sent.
  - While the rail still says pending, nothing more is sealed. While a payment is in flight, no record that changes the deal's details can be sealed, so the payment already sent stays authorized.
  - A failed payment keeps its approval used and counts no spend.
- References:
  - `--payment-ref-type` and `--payment-ref` name the payment's reference, with the type's qualifiers (`--payment-ref-network` for x402, and so on).
  - On `ln.payment_hash`, `bolt12.invoice_payment_hash`, `acp.order_id` and `ucp.order_id`, the payer leg's reference is the check's `payment_ref`.
  - On x402, the leg carries the transaction, and the link from it to the checked nonce is the payer's claim unless a verifier reads it on chain.
- What you received is a payee leg: `--received` and `--receive-fee` (0 when none was taken), `--status pending|settled|failed`, and `--supersedes` for a later observation.
- A refund:
  - It is a leg with `--reverses` naming a leg of your own book: the seller's payer leg on its cancel check, and the buyer's refund received.
  - The buyer's refund received returns the money once, when settled.
  - A second, independent refund of the same leg is refused.
- The rules checker's history now counts x-deal-v1 acts: a payment from its send, and a perform. Each is read as the x-deal-v0-shaped record of its own deal.
