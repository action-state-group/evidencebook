# Purchase, booking and signature

Part of the [deal skill](../SKILL.md): its [Rules](../SKILL.md#rules-never-break-these) and [Points of no return](../SKILL.md#points-of-no-return) bind every step here, and `ID`, `OFFER` and `--profile deal` are as its [Procedures](../SKILL.md#procedures) say.

## Purchase

Any checkout or agreed buy: a retail order, a marketplace buy, a
subscription, an in-app purchase. The user is the buyer here; when the
seller makes the offer, the two sides swap in steps 2 to 6.

**Two shapes.** When the other side runs this tool too (another agent, a
marketplace seller who keeps records), the deal is between two books and the
files go back and forth (steps 2 to 6 as written). When it does not (a shop's
website, a merchant that keeps no signed records of its own), the deal has your book
only: your offer names no key, you agree to it yourself, and nothing goes to
the other side (the "No layer on the other side" notes in steps 2, 3 and 6).
A retail checkout is almost always the second shape.

1. **Find what they asked for.** Search and show options. Nothing is sealed
   yet: the deal starts with the offer.
2. **The offer.** Write the offer's members to a file (the role, the other
   side's key in `to`, the terms, the admission pack's digest, `agree_by`,
   the rows each side owes, who referees a dispute and where) and run:

   ```sh
   capsulectl --profile deal deal offer --input offer.json --out offer.sent.json
   ```

   **No layer on the other side:** put `"to": null` in the offer. It stays
   with you; there is nothing to send, and `deal offer` says so.

   The user sends `offer.sent.json` to the seller. A counter-offer is `deal
   offer --counter-to OFFER --input ...`; withdrawing your own offer before
   the deal starts is `deal offer cancel OFFER --out FILE`, and declining one
   sent to you is `deal offer decline OFFER --out FILE`. Each writes the file
   to send.
3. **The other side keeps it.** On the seller's side, `deal receive
   offer.sent.json` (no layer on the other side: skip this step). Every file the other side sends later (its agreement,
   statements, payment legs, a dispute) is kept the same way, with `deal
   receive FILE`: what you have not received does not count for you.
4. **Admission, on each side.** Each side runs its admission pack on the
   other and seals the decision:

   ```sh
   capsule-engine admission evaluate --pack PACK --input INPUT --admit ADMIT > admission.json
   capsulectl --profile deal deal admit --deal ID --input admission.json
   ```

   Only output with `"admits_deal": true` goes on: a GO, or a LIMIT whose
   tier holds the deal. An ASK waits for its answers in capsule-engine; a NO
   stops the deal. Pass the engine's output for this counterparty and this
   amount: `deal admit` cannot tell which run it came from. `deal agree`
   refuses a side with no admission decision of its own.
5. **What the user asked, sealed before agreeing.** The deal's first note is
   the user's intent with the opening (the deal's type, who and the way
   back). An intent that gives `party_role` must give your side:

   ```sh
   capsulectl --profile deal deal note --deal ID --kind intent --input intent.json --opening opening.json
   # intent.json:  {"verbatim":"buy the desk lamp, at most 70 dollars","max_total_minor":7000}
   # opening.json: {"type":"purchase","who":{"name":"Lamp Shop","domain":"lamp.example"},
   #                "recourse":{"rail":"card","refundable":true}}
   ```

   `verbatim` is the user's exact words, never your paraphrase. `allowed`
   says what they asked you to do: `"allowed": []` when they only asked for
   options, the actions (`["pay"]`) when they asked you to act, and left out
   only when they handed you the whole deal; left out, nothing is
   restricted. `max_total_minor` is their limit. Then the journey, as notes
   (see "The user picked from options"), and the intent for the user's pick.
   The first intent's output prints `intent`, its journal digest: `deal agree`
   cites it by default, and on the buyer's side it names the seller.
6. **The agreement exchange.** The side the offer was sent to agrees first:
   1. the seller runs `deal agree OFFER --out seller-agreement.json`, and the
      user sends the file;
   2. the buyer runs `deal receive seller-agreement.json`, then `deal agree
      OFFER --out buyer-agreement.json`, and the user sends that back;
   3. the seller runs `deal receive buyer-agreement.json`.

   Each side checkpoints right after its agreement, and the file carries
   that checkpoint's proof. Neither side's first act is sealed until it holds
   the other's: an act run too early is refused and says to run it again
   once the file arrives. Running `deal agree` again writes the same
   agreement again, with the newest checkpoint proof that covers it; it
   never seals a second agreement.

   **No layer on the other side:** run `deal agree OFFER --out
   agreement.json` on your own offer right after steps 4 and 5. It seals at
   once, with no file from the other side; the file is your own copy. Your
   first act then goes ahead without the other side's checkpoint proof, and
   its output marks it (`first_act_gate`: `counterparty_no_layer`). The
   payment check cannot match legs the other side never writes, so `deal
   report` says the record is one-sided.
7. **Final review:** Before you type any of the user's own details into the
   checkout (name, email, delivery address, phone), follow [Disclosure](disclosure.md)
   for them, on this deal. Then fill the checkout up to the last screen
   before the order is placed; do not press the final button. Then `deal
   check` with `"action": "pay"`, the cart's item and price as `terms`, the
   exact total about to be charged, tax and shipping included, as
   `amount_minor`, `who` and `recourse` as the checkout shows them now (the
   payee and the rail, so a change since the opening shows), and
   `payment_ref`, the reference fixed before paying that the payment will be
   made under (a Lightning payment hash, an ACP order id, the x402
   authorization nonce, or on a card checkout the merchant's order number
   when the checkout shows it; when it shows none before you pay,
   `"payment_ref_after_payment": true` instead, step 9). On a card or a wallet, `authorized_max_minor` is
   the largest amount the payment may be authorized for, as the checkout or
   wallet shows it (a hold larger than the total included), never just the
   total:

   ```sh
   capsulectl --profile deal deal check --deal ID --input pay.json
   # pay.json: {"action":"pay","amount_minor":6000,"authorized_max_minor":6000,
   #            "terms":{"item":"desk lamp","price_minor":6000,"currency":"USD"},"payment_ref":"..."}
   ```

8. **The user's answer.** Any request to the user to go ahead uses the
   check's `approval_text`, as it is. On `"proceed": true`, go on. On
   `pause`, show the card exactly as the [Rules](../SKILL.md#rules-never-break-these) say, then seal what the user
   answered, with the card they saw:

   ```sh
   capsulectl --profile deal deal note --deal ID --kind approval --check CHECK_ID \
     --choice proceed --said "yes" --shown-card card.txt
   ```

   `CHECK_ID` is the `check_id` the check printed; `--choice` is one of the
   check's `options` (`proceed` or `hold`); `card.txt` holds the check's
   `card` field unchanged. Go on only if that returns `"proceed":
   true`. When the user picks "Call the number I found" on the card, follow
   [Evidence that arrives later](evidence.md) for that choice.
9. **The payment, in two calls.** Before handing it to the rail:

   ```sh
   capsulectl --profile deal deal settle --deal ID --sent --check CHECK_ID --payment-ref-type TYPE
   ```

   Only once that succeeds, make the payment (press the button, or tell the
   sub-task to). When the rail answers:

   ```sh
   capsulectl --profile deal deal settle --deal ID --check CHECK_ID --status settled --out payer-leg.json
   ```

   (`--status failed` when it failed; a failed payment uses up its check, so
   check again before retrying.) `TYPE` is the rail's registered reference
   type: `ln.payment_hash`, `bolt12.invoice_payment_hash`, `acp.order_id`,
   `ucp.order_id`, `x402.transaction`, and the others `deal settle --help`
   names. On every type but `ln.payment_hash`, `bolt12.invoice_payment_hash`,
   `acp.order_id` and `ucp.order_id`, the second call also takes
   `--payment-ref`, the rail's own reference as it comes back; on x402 give
   `--payment-ref-network NETWORK` on both calls.

   **A card checkout on a website** has no rail reference: the payment is
   under the merchant's order number, `x-deal-v1.merchant_order`, with
   `--payment-ref-origin`, the checkout's origin (`https://shop.example`;
   scheme and host only). When the checkout shows the order number before
   you pay, that number is the pay check's `payment_ref` (step 7), and the
   two calls are:

   ```sh
   capsulectl --profile deal deal settle --deal ID --sent --check CHECK_ID \
     --payment-ref-type x-deal-v1.merchant_order --payment-ref-origin https://shop.example
   capsulectl --profile deal deal settle --deal ID --check CHECK_ID --status settled --out payer-leg.json
   ```

   When it shows none before you pay, the pay check says
   `"payment_ref_after_payment": true` instead of `payment_ref`; `--sent` is
   the same, and `--status settled` adds `--payment-ref ORDER_NUMBER` once
   the confirmation shows it (a failed checkout may leave it out). The
   report then says the reference was recorded after payment. Never put a
   card number, or any part of one, in a reference: it is refused. When the
   merchant's order number itself looks like a card number, `--status
   settled` takes `--no-payment-ref` instead, and the report says no
   reference was recorded. While
   the rail still says pending, run nothing more for this payment. The user
   sends `payer-leg.json` to the other side (with no layer there, it is the
   user's own copy, and nothing is sent).
10. **How it went.** Each side states how its rows went:

    ```sh
    capsulectl --profile deal deal statement --deal ID --statement paid_as_agreed=met --out statement.json
    ```

    `met`, `not_met` or `cannot_tell`. The user sends the file (with no
    layer on the other side, there is no one to send it to).
11. **Reading the deal.** `deal report --deal ID` reads your book only up
    to its last checkpoint, and refuses a book with records after it: then
    wait for that checkpoint as
    [references/expert.md](expert.md#reading-at-the-last-checkpoint)
    says, and run it again. Then:
    - `deal report --deal ID`: each row's status; `--bundle FILE` writes your
      own copy as an Evidence Bundle;
    - when the user wants to send it to the other side, make a shared copy,
      never the user's own: `deal report --deal ID --share counterparty --to
      NAME --bundle FILE`. It is put on record before the file is written. It
      is the only copy to make for someone else ([SKILL.md](../SKILL.md#what-you-say-voice-and-say),
      "A report goes out only when asked").
    - `deal report --bundle` (with `--share` too) refuses a copy whose
      standalone report does not verify. To check a deal copy, run `capsulectl deal
      verify --bundle FILE`: the core's generic verbs (`capsulectl
      presentation list`, `presentation build`, and `capsulectl bundle`
      writing a page) read no deal section, so they can build a page for a
      copy whose deal fails.

### The user picked from options

Most purchases go: ask broadly, then pick. "A desk lamp under $70." You show
five; the user picks one. The deal's first intent holds the broad ask.
Unless you seal the pick, the check compares the specific item with the broad
words and reports the user's own choice as "Not what you asked". That is a
false pause.

So after the first intent (with `--opening`), seal the journey as notes,
then the pick as an `intent`, **before** `deal check`:

```sh
capsulectl --profile deal deal note --deal ID --kind searched    --input s.json  # {"where":"ebay, etsy","results":37}
capsulectl --profile deal deal note --deal ID --kind options     --input o.json  # {"options":[{"item":"...","who":{...},"price_minor":175}, ...],"found":37}
capsulectl --profile deal deal note --deal ID --kind recommended --input r.json  # {"option":1,"reason":"cheapest delivered"}
capsulectl --profile deal deal note --deal ID --kind chose       --input c.json  # your choice, only when the user handed it to you
capsulectl --profile deal deal note --deal ID --kind couldnt     --input x.json  # {"reason":"sign_in_required","who":{...}}
capsulectl --profile deal deal note --deal ID --kind intent      --input p.json  # the user's pick
# p.json: {"verbatim":"the brass one","asked":{"item":"Brass desk lamp"}}
```

- `verbatim` is the user's own words for the pick. `asked` holds only what
  the user chose, written exactly as the check's `terms` write it. It adds to
  the ask in force, field by field.
- capsulectl labels the note itself (`move` in its output): `picked`,
  `switched`, `narrowed` or `raised`. You never choose the label.
- Leave `allowed` and `max_total_minor` out to keep the user's. An intent may
  propose a change; it never applies one. A raised limit needs the user's own
  words, sealed as `deal note --kind approval --check <the intent's
  capsule_id> --choice confirm_limits --said "<their words>"`. Seal it only
  when the user has said it. An intent may lower the limit or drop an action
  without a confirmation.
- What you picked yourself (a size, a colour or a delivery option the user never mentioned) stays out of `asked` and in the check's `terms`. The check
  lists it as yours ("I picked …, not you"). A choice you made is never shown
  as the user's.
- When the user hands you the choice ("any of them, the cheapest"), say so in
  the first intent: `"delegated": ["item"]` with their words in
  `"delegated_said"`, and seal your choice as `chose`, never as their intent.
- `couldnt` reasons: `sign_in_required`, `out_of_stock`, `cannot_ship_here`,
  `blocked`, `payment_declined`, `site_error`, or `other` with a few words in
  `"detail"`.

The check still reports what the user did not choose, and an unverified
claim stays unverified. An item different from the user's pick, or a price
over their limit, still pauses after the pick is sealed.

## One errand, one deal

One deal per errand, kept for the whole errand. On x-deal-v1 the deal starts
with its offer, so:

1. **Before the offer, nothing is sealed.** Search and show options; the
   first note is the intent sealed right after the offer is made or
   received (Purchase, step 5).
2. **A switch to another merchant is a new offer.** The deal's counterparty
   is the key its offer names. When the user moves to another merchant, that
   is a new deal with that merchant's offer; withdraw your own open offer
   with `deal offer cancel OFFER --out FILE` first, and send the file.
3. **The merchant comes before the check.** `deal check` needs the opening's
   `who`, or the user's pick or your `chose`.
4. **Tax and total go in the amount.** The check's `amount_minor` is
   everything to be charged; `price_minor` is the item's price. `conditions`
   hold only choices (a size, a colour, a delivery option).
5. **Called off.** When the user drops the errand before agreeing, withdraw
   your offer (`deal offer cancel OFFER --out FILE`) or decline theirs
   (`deal offer decline OFFER --out FILE`), and send the file.
6. **Find deals with `deal list`**, never from memory.

## Booking

A hotel, flight, table, appointment, rental or ticket. It is a Purchase: the
offer, admission, intent and agreement as in steps 1 to 6 (a booking site is
almost always the no-layer shape).

1. Fill the booking form up to the last screen before it is confirmed. Do
   not press the final button.
2. **Final review:** when the confirmation charges a card, it is a payment:
   Purchase steps 7 to 9 with `"action": "pay"`. When it charges nothing now
   (pay at the hotel, a free table), `deal check` with `"action": "commit"`,
   the booking as `terms` and its price as `amount_minor`.
3. Any request to the user to go ahead uses the check's `approval_text`, as
   it is. On `"proceed": true`, confirm. On `pause`, the user's answer as in
   Purchase step 8; confirm only if that returns `"proceed": true`.
4. Right after confirming, on the commit branch, record the booking on that
   check (on the payment branch the payer leg of Purchase step 9 is the
   record; `deal perform` rests only on a commit check):

   ```sh
   capsulectl --profile deal deal perform --deal ID --act book --check CHECK_ID \
     --observed confirmation_page --out perform.json
   ```

5. How it went and reading the deal, Purchase steps 10 and 11.

## Signature

A contract, a lease, a rental agreement, a service order, any e-signature.
It is a Purchase up to the agreement (steps 1 to 6).

1. Read the document, and fill it up to the signature without signing.
2. **Final review:** `deal check` with `"action": "commit"`, the document's
   terms as `terms` and what it commits the user to pay as `amount_minor`.
3. Any request to the user to go ahead uses the check's `approval_text`, as
   it is. On `"proceed": true`, sign. On `pause`, the user's answer as in
   Purchase step 8; sign only if that returns `"proceed": true`.
4. Right after signing:

   ```sh
   capsulectl --profile deal deal perform --deal ID --act sign --check CHECK_ID \
     --observed esign_receipt --out perform.json
   ```

   The record is the agent's: it says the user approved and the document was
   signed, never that the user's own signature is in it (see What "settled"
   means).
5. How it went and reading the deal, Purchase steps 10 and 11.

## What "settled" means, and the person's signature

An act's output lists `person_signature: missing`, and `deal report` carries
a `settled_note`: no act on an x-deal-v1 deal carries the person's own
signature yet, because where those signatures travel is still being defined
(agent-action-capsule#627). The user's sealed approval is the user's answer
to a check, recorded by the agent; it is not their signature. Say "settled,
as the agent's records show"; never say the user signed.
