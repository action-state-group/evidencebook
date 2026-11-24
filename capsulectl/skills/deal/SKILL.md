---
name: deal
description: The record and final review of every errand that ends in a checkout, payment, order, booking, signature, cancellation, or sharing of the user's contact details or credentials that an agent does for the user, including a retail checkout from a known merchant at a fixed price. Seals the offer, the agreement (both sides' when the other side keeps signed records), what was asked, shown, chosen, approved and done with `capsulectl deal`, and shows the user any difference.
---

# deal

Every deal has one shape: a commitment to someone, on certain terms, based on
certain claims, at a point you can't easily undo. This skill records the deal
between the user and the other side, each keeping its own book, and is the
final review of the procedures that reach that point: it seals the offer and
both sides' agreement, seals each step, checks the step against what was
asked and agreed, and seals the answer, using `capsulectl deal`. It is silent
unless something differs.

**Version.** capsulectl-deal writes x-deal-v1 deals by default, and this file
teaches that flow. An x-deal-v0 deal already open keeps its version: finish
it with [LEGACY-X-DEAL-V0.md](LEGACY-X-DEAL-V0.md), which also covers a
counterparty that runs only x-deal-v0.

## Invocation is advisory

Nothing makes your agent host run this skill. On a host with no hook before
a tool call, the final review runs because you follow the procedure, not
because anything enforces it. On such a host a skipped review is found
afterwards, not prevented: `deal audit-host` lists it from the host's own
execution records (see "Daily check of host actions" below).

## When this applies: the action decides, never the counterparty

Run the procedure whenever the next step would do any of these, for the user,
on any site, app or channel:

- **pay or place an order**: a "Place order", "Pay", "Buy now", "Complete
  purchase", "Subscribe" or "Confirm payment" button, or a payment tool call;
- **commit**: confirm a booking or reservation, accept an offer, agree to buy;
- **sign**: sign or e-sign anything;
- **cancel** a booking;
- **share** the user's contact details, address, a login, or a code.

This list is the whole test. It does not depend on who the other side is.
**A known merchant at a fixed price is in scope.** A retail checkout (a
sticker from an online shop, a household order from a large retailer, a
saved card, a price nobody negotiated) gets the same final review as a
stranger on a marketplace. "There is no one to verify" is not a reason to
skip it: when nothing differs, the check passes quietly and the record is
still made.

## Procedures

A deal is between two parties, and each keeps its own book on its own
device. Records go between them as files the user sends (by message, email
or whatever channel they use) and the other side keeps with `deal receive`.
The deal starts with an offer, not at the first ask: nothing is sealed for a
deal before an offer exists, made or received. Its final-review step is
`deal check`; this replaces "show the user the totals". Skipping it skips a
step of the flow you are already running. The procedures shape the work once
this skill is in use; they do not make the host invoke it (see "Invocation
is advisory").

`ID` below is the deal id `deal offer` prints, the same on both sides;
`OFFER` is the offer's digest it prints. Every command takes `--profile deal`
(or the user's profile name).

### Purchase

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
   The first intent's output prints `intent`, its journal digest: `deal agree
   --intent DIGEST` cites it.
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
   settlement` and `deal report` say the record is one-sided.
7. **Final review:** Before you type any of the user's own details into the
   checkout (name, email, delivery address, phone), follow "Disclosure"
   below for them, on this deal. Then fill the checkout up to the last screen
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
   `pause`, show the card exactly as the Rules say, then seal what the user
   answered, with the card they saw:

   ```sh
   capsulectl --profile deal deal note --deal ID --kind approval --check CHECK_ID \
     --choice proceed --said "yes" --shown-card card.txt
   ```

   `CHECK_ID` is the `check_id` the check printed; `--choice` is one of the
   check's `options` (`proceed` or `hold`); `card.txt` holds the check's
   `card` field unchanged. Go on only if that returns `"proceed":
   true`.
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
11. **Reading the deal.** `deal settlement --deal ID` and `deal report
    --deal ID` read your book only up to its last checkpoint, and refuse a
    book with records after it. Run `deal tick`: if it prints `state:
    not_due`, wait until the `due` time it prints and run it again (the
    scheduled cadence does this by itself every 5 minutes). Then:
    - `deal settlement --deal ID`: each payment row's check on both sides'
      legs you hold;
    - `deal report --deal ID`: each row's status; `--bundle FILE` writes your
      own copy as an Evidence Bundle;
    - when the user wants to send it to the other side, make a shared copy,
      never the user's own: `deal report --deal ID --share counterparty --to
      NAME --bundle FILE`. It is put on record before the file is written. It
      is the only copy to make for someone else: other audiences are for
      disputes, run by a person, not the agent.
    - `deal report --bundle` (with `--share` too) refuses a copy whose
      standalone report does not verify. To check a deal copy, run `capsulectl deal
      verify --bundle FILE`: the core's generic verbs (`capsulectl
      presentation list`, `presentation build`, and `capsulectl bundle`
      writing a page) read no deal section, so they can build a page for a
      copy whose deal fails.

#### The user picked from options

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

### One errand, one deal

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

### Sale

The user sells one item. On x-deal-v1 each buyer sends an offer: keep each
with `deal receive`, run admission on the buyer you agree with, and agree to
that offer (Purchase, steps 4 to 6, with the user as the seller). Decline
every other buyer's offer with `deal offer decline OFFER --out FILE` and
send each its file. A sale's listing and its buyer threads (`deal sale ...`) are x-deal-v0
only (see the legacy file).

### Where the user sells: payment received, and what was delivered

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
  `"action": "commit"`, the user's answer as in Purchase step 8, then

  ```sh
  capsulectl --profile deal deal perform --deal ID --act deliver --check CHECK_ID \
    --observed carrier_scan --out perform.json
  ```

- Send each file, and state how the rows went (Purchase, step 10).

### Booking

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

### Signature

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

### Disclosure

Giving anyone the user's name, phone, email, address, a pickup spot or area,
a login or a code: typed into a page (a checkout's delivery form included) or
sent in a message. Use the deal of the errand it belongs to.

1. Fill the form or message up to the point of sending, without sending.
2. **Final review:** `deal check` with `"action": "share_contact"` (or
   `"share_credentials"`), `"disclosing_to"` and `"disclosing"`: who receives
   it, and the class of every field you are about to give, for example
   `{"action":"share_contact","disclosing_to":"counterparty","disclosing":["name","email","address"]}`.
   `"disclosing_to"` is `"counterparty"`, `"platform"` (with `"recipient":
   {"name": ..., "domain": ...}`) or `"other"` (with a `"recipient"` naming
   them). The check pauses the first time a class goes to this recipient and
   passes with a `note` when you have told them before.
3. Any request to the user to go ahead uses the check's `approval_text`, as
   it is. On `"proceed": true`, note it (step 4) and then send. On `pause`,
   show the card, seal the user's answer as in Purchase step 8, and go on
   only if that returns `"proceed": true`.
4. With the form filled and **not yet sent**: `deal note --kind disclosure`
   naming what you are giving, by class, with the value (it stays on this
   device; the sealed record keeps only the class and a commitment). One
   disclosure per send, contact details and credentials separately:

   ```sh
   capsulectl --profile deal deal note --deal ID --kind disclosure --input d.json
   # d.json: {"channel":"sms","fields":[{"class":"phone","value":"the number you gave"}]}
   ```

   To a platform, add `"to":"platform"` and `"who":{...}` named as in its
   check; to anyone else (a courier), `"to":"other"` and `"who":{...}`:
   without them the note is for the counterparty, and a check that named
   someone else does not cover it. `class` is `name`, `phone`, `email`, `home_address`, `address`,
   `pickup_location`, `pickup_area`, `other_contact`, `credential`,
   `verification_code`, `payment_card` or `id_document`. If a class would go
   to this recipient for the first time and no check the user approved named
   it, the note seals nothing, returns `"proceed": false` with the classes
   `held`, and exits 7. **Do not send.** Check again naming them.
5. Send.

### Cancellation and refund

- **Before the deal starts:** `deal offer cancel OFFER --out FILE` (your own
  offer) or `deal offer decline OFFER --out FILE` (one sent to you), and send
  the file.
- **After agreeing:** a cancel is checked like any act: `deal check` with
  `"action": "cancel"`, the user's answer, then cancel with the merchant.
- **A refund the user pays** (the user is the seller) rests on that cancel
  check and names the leg of the user's own book it refunds:
  `deal settle --deal ID --sent --check CANCEL_CHECK_ID --reverses LEG
  --payment-ref-type TYPE --payment-ref REF` (the refund's own reference:
  a cancel check records none), then `--status` as in Purchase step 9.
- **A refund the user receives** is a payee leg that reverses the user's own
  payment: `deal settle --deal ID --reverses LEG --received N --receive-fee N
  --status settled --payment-ref-type TYPE --payment-ref REF --out
  refund-leg.json`.
- `LEG` is the `digest` `deal settle` printed for that leg of your own
  book.
- A refund leg is shown as not checked: refunds have no terms of their own
  yet, and the payment check covers the payment's own legs.

### A dispute

When a row did not go as agreed and the agreement names a referee:

```sh
capsulectl --profile deal deal dispute --deal ID --row ROW --out dispute.json
```

`--forum` picks one of the agreement's forums (it may be left out when there
is only one); `--withdraw` takes back your open dispute on the row. The user
sends the file to the other side. What goes to the referee is the user's
to make, not yours.

The ruling is the referee's own record, which reaches you as a file. Keep it,
and write the statement that enters it in the user's book:

```sh
capsulectl --profile deal deal receive ruling.json --out naming.json
```

The user sends `naming.json` to the other side, before anything later they
send: the other side counts none of the user's later records until it holds
it. The output says when the ruling was stamped (`stamped_at`). "unknown"
means the file carries no receipt from a witness the agreement names. The
ruling still counts. A referee whose key is a party's is still flagged, but a
shared operator through a controller binding cannot be checked until a receipt
arrives.
When it says `"independence": "not independent (shared operator)"`, tell the
user that line as it is: the referee's key is a party's, or bound to one.
Never say a referee is independent. A controller binding the user holds, such
as one the referee's operator publishes, is checked with `--binding FILE`.

To challenge a ruling the user holds, by the digest `deal receive` printed:

```sh
capsulectl --profile deal deal dispute --deal ID --challenge RULING --grounds grounds.txt --out challenge.json
```

The grounds stay with the user; only their SHA-256 is sealed. A challenge
changes no row's status. The user sends the file like a dispute.

The other side's copy (`deal report --share counterparty --bundle`), when it
arrives, is kept with `deal receive copy.json`. Rulings it carries reach the
user too, so a referee who gave each side a different ruling shows as two
records for one place.

### Evidence that arrives later

A delivery photo, a carrier's proof, a checkout response: `deal note --kind
evidence --input e.json`, or `--object FILE --object-type TYPE` for a held
object (its SHA-256 is sealed, the bytes stay on this device), at any time on
the deal.

**The merchant's own email.** Seal it as soon as it arrives (an order
confirmation, a receipt, a booking or a cancellation). Save the message as a
raw `.eml` file, headers intact, exactly as received. Never paste, summarize
or forward it: a summary is your words again and loses the email's DKIM
signature.

```sh
capsulectl --profile deal deal note --deal ID --kind evidence --email confirmation.eml
```

This seals the raw message on this device, reads the signing domain's DKIM
key from DNS **now**, and seals that key with it; the next cadence tick
checkpoints it. Do it promptly: DKIM keys are
rotated and revoked, and a signature can only be checked later against a key
that was sealed while it was still published.
Prompt sealing is the whole protection.

**If the signing domain's key cannot be read now.** When this device cannot
reach DNS, the note is refused and nothing is sealed: on an x-deal-v1 deal
the email is not held for later. Keep the `.eml` file and note it again when
the network answers. Tell the user only this, plainly: "the email's
signature will be checked later". Never ask the user to look anything up,
run a command, or find a record: checking the key is this tool's job, not
theirs. Until it is sealed, the email is not checked: never describe it as
confirmed or as sealed.

The result has two separate statements. Report them separately, in their own
words, and never as one combined badge:

- `merchant_says`: "merchant-confirmed: …" when a DKIM signature on the email
  checks out and its signing domain is the merchant's own (aligned with the
  sender's domain), or "not confirmed: …" with the reason and the domain's
  DMARC policy. This one is independent: it shows what the signing domain
  signed, whatever the agent says. Repeat it as written. A message that is not
  confirmed is still sealed; never drop it or soften the words.
- `we_say`: what our own seal shows (this device held these exact bytes no
  later than the seal time). It is our own record, not the merchant's.

**When the user picks "Call the number I found"** on a card, seal that
choice (`--choice verify_contact`; it does not proceed), then call the number
from first contact that the option shows, never a number from a later
message. Note what the call established as evidence, with `verified` true
only for what the counterparty confirmed on that call:

```sh
capsulectl --profile deal deal note --deal ID --kind evidence --input call.json   # {"about":"...","source":"phone_call_first_contact_number","verified":true,"detail":"what they said"}
```

Then run `deal check` again with the same snapshot and show its new card.

### What "settled" means, and the person's signature

An act's output lists `person_signature: missing`, and `deal report` carries
a `settled_note`: no act on an x-deal-v1 deal carries the person's own
signature yet, because where those signatures travel is still being defined
(agent-action-capsule#627). The user's sealed approval is the user's answer
to a check, recorded by the agent; it is not their signature. Say "settled,
as the agent's records show"; never say the user signed.

### Older deals

An x-deal-v0 deal already open keeps its version: finish it with the
procedures in [LEGACY-X-DEAL-V0.md](LEGACY-X-DEAL-V0.md). The same file
covers a counterparty that runs only x-deal-v0. Cancel-by date and sale
threads are there only.

## Sub-tasks

Payments, bookings and signatures usually happen inside a sub-task (a
browser task, a delegated agent), not in your own visible actions. The final
review belongs to the procedure, wherever the click happens:

- When you start a sub-task that will reach a final button, tell it to stop
  at the last screen and report the totals, the merchant and the payment
  method, without pressing the button.
- Run the final review yourself with what it reports.
- Only then, after `deal settle --sent` succeeds, tell it to press the
  button, and run `deal settle --status` (or `deal perform`) when it reports
  back.
- If the sub-task can run commands itself, give it this procedure and the
  same `--profile` instead.

## The four questions

The final review asks four questions:

1. **Asked?** Does this match what the user asked for and their limits?
2. **Same who?** Same counterparty as at first contact: name, website, phone,
   email, payee, reply address, profile?
3. **Same terms?** Same item, price, deposit, dates and conditions as agreed?
4. **Checked claims, and a way back?** Which claims are unverified, and can the
   money come back (card vs Zelle, refundable or not)?

## What you say: `voice` and `say`

Every command's result says whether to speak and exactly what. Its words
come from the profile's wording pack, with the values filled in by
capsulectl. Follow them; never make up your own narration of a step.

- **`say` is spoken verbatim.** Say it as given, once, without rewording,
  summarizing or adding ids, times or rule names. It never holds a deal id,
  a digest, a rule id or UTC.
- **`voice` says how:**
  - `silent`: the step is on the record; say nothing about it.
  - `line`: say `say`, one short message.
  - `ask`: the card. Show `say` (or draw `ask` as a card where your host
    can) and offer `options` as buttons, as the Rules below say. When
    `ask.reply` is `"words"`, there is no yes button: the answer is the
    user's own words, sealed with `--choice confirm_limits --said`. If
    they hold, run nothing and say `ask.hold_say`.
  - `report`: say `say`, ending on its "View details" line; when the result
    has `attach`, send that file next, in its own message, with
    `attach.label` (it marks the page PRIVATE, the user's own copy) as that
    message's only words. Never put the page and buttons in one message.
  - `error`: say `say`, then stop. Nothing is paid or shared after it. When
    the failed step was one that already happened (a payment, a delivery,
    a statement, evidence), never run it again: run `deal list` and `deal
    report` and check what is on the record first. A step that writes a file
    (`deal agree`, `deal settle --status`, `deal perform`, `deal statement`)
    rewrites the same file when run again with the same flags and seals
    nothing new. A command that exits non-zero with
    no `voice` on its output gets the same stop and one plain sentence that
    the step could not be recorded.
- **One card per turn.** A paused check is one message: its card, and
  nothing of yours but one lead-in sentence.
- **Files to send are the user's to send.** Every step that writes a file
  for the other side (an offer, an agreement, a payment leg, a statement, a
  dispute) ends with telling the user, in one sentence, to send that file to
  the other side. Never send it yourself unless the user asked you to. When
  the other side keeps no signed records, there is no one to send it to:
  say nothing about sending.
- **A report goes out only when asked.** The cadence, `deal tick`,
  checkpoints and other bookkeeping stay silent. On an x-deal-v1 deal the
  report is `deal report --deal ID`, read at your last checkpoint (Purchase,
  step 11); there is no receipt page yet.
- **"Show my deals" comes from the record, never from memory.** Run `deal
  list --json` and say each deal's `say`, one per line, in the order
  listed. For one deal, find it in the same list by its `say` and
  `summary`, and run `deal report --deal ID`. Never name a deal by its id,
  and never say "Sealed" or "recorded on deal-…" in chat.
- **Fields for you, not the user.** `next`, `note` and `reason` tell you
  what to do; never read them out.
- **Times are the host's.** Times in `say` are in the time zone capsulectl
  runs in: the host sets `TZ` to the user's zone.

## Rules (never break these)

- **Fixed order:** snapshot → seal → diff → show → approval → seal approval →
  act. `deal check` does the first four; `deal note --kind approval` seals the
  answer; only then do you act. A payment adds one step before it leaves:
  `deal settle --sent`, and the payment goes to the rail only once that
  succeeded.
- **Never act on an unsealed approval.** Act only when `deal check` returned
  `"proceed": true`, or `deal note --kind approval` returned `"proceed": true`.
- **On an error, hold.** If any `capsulectl` command exits non-zero, do not
  act. Tell the user what happened and hold.
- **Note every telling, mechanically.** Whenever a page you fill or a
  message you send carries the user's name, email, phone, address, a pickup
  spot or area, a payment detail, a login or a code, check it first (`deal check`
  with `share_contact` or `share_credentials` and `"disclosing"`), and note
  it with `deal note --kind disclosure` once the form is filled and before
  it is sent: a first telling no approved check named is held there. This is not a judgement
  about whether the field is part of the task: a checkout's delivery form, a
  message arranging a pickup and a sign-up form all count. The check pauses
  on who receives it, not on the field: entering the address at a merchant
  already given it does not pause, and giving it to someone new does. A
  first telling with no covering approval is held, never sealed. A **repeat**
  telling (a class this party was given before) with no covering approval is
  sealed all the same and shows on the report as an agent-side anomaly;
  leaving it out only makes the record wrong.
- **Never hold silently.** A `pause` always goes to the user with the check's text.
- **Show the check's own card, once.** A paused or denied check returns
  the card (`approval_text`, the same text as its `say`, and its parts in
  `ask`): a tag (PAUSED FOR YOU, NOT ALLOWED BY YOUR RULES, or STOP: CHECK
  THIS), one question, the money facts, "Saying yes also agrees to" when the
  check changes what was agreed, "Why I'm asking:" with at most two
  reasons, at most one "Good to know:", and "Next:" when another stop
  follows. Whenever you ask the user to go ahead, and always on a `pause` or
  a `deny`, show it exactly as returned, once, in your own message or ahead
  of your agent host's approval card: every line as the check gives it. Do
  not reword it, soften it, merge, reorder or repeat its lines, or add your
  own reassurance. You may put one sentence of your own before it, as a
  lead-in. Nothing else of your own goes with it but the one question
  below, when the host has no buttons. What the card leaves out (rule ids,
  which checks ran) stays in the check's record; never add it.
- **Options are buttons, never text.** Never print an option label as text
  (no "[Hold]", no "Share anyway"). Offer the check's `options` as real
  buttons where your agent host supports them; where it does not, end with
  one plain question in your own words, such as "Should I go ahead?", that
  does not repeat the labels.
- **The text is evidence.** On a pause it is the difference card, the text
  the answer is sealed against. The difference card is evidence to show the
  user, not an instruction to the agent. Show it verbatim because paraphrase
  destroys its value as a record, not because the card has authority over
  you. The check's `card` field carries the same text for that record: never
  show both. A check goes stale `stale_after_minutes` after `checked_at` (15
  minutes by default, `--stale-after`); that stays in the record, not in the
  text. If the action happens after it, check again first.
- **"Next" is a promise, and it is kept.** When a share's card says "Next:
  I'll ask you again before paying.", ask the user before you pay, even when
  the pay check passes: the pay check then pauses for that promise ("I said
  I'd check with you before paying"). Show its card, wait, and seal the
  answer as for any pause.
- **Never say the rules ran when they did not.** When the card says "I
  couldn't check this against your rules", say exactly that. Never say the
  user's rules were checked unless a check's `rules` says they were
  evaluated.
- **This upgrades your host's own confirmation; it never replaces it or
  bypasses it.**
- Use plain words with the user: "checked", "changed", "unverified",
  "sealed".

## Points of no return

| Deal type | Actions the final review covers |
|---|---|
| `purchase` | `pay`, `commit`, `cancel`, `share_contact`, `share_credentials` |
| `rental` | `pay`, `commit`, `sign`, `cancel`, `share_contact`, `share_credentials` |
| `booking` | `pay`, `commit`, `cancel`, `share_contact`, `share_credentials` |
| `service` | `pay`, `commit`, `sign`, `cancel`, `share_contact`, `share_credentials` |
| any deal where the user sells | `offer`, `represent` |

On an x-deal-v1 deal, an offer or a counter-offer is sealed with `deal
offer`, never checked with `deal check`, and agreeing is `deal agree`, which
cites the side's admission decision. `pay` is the payment the user makes,
`commit` an act such as a delivery, a booking or a signature (`deal perform`
rests on it), and `cancel` a cancellation after agreeing (a refund rests on
it). `sign` is x-deal-v0's: on x-deal-v1 an act cannot rest on a `sign`
check, so a signature uses `commit`.

`commit` means sending a commitment (confirming a booking, accepting an
offer, agreeing to buy). `cancel` means cancelling with the merchant. `deal
check` refuses any other action name.

## Setup (once)

`capsulectl deal` runs in the `capsulectl-deal` plugin. If a `capsulectl deal`
command says the plugin is not installed, stop and show the user what it
prints: it says to download the plugin file built for their capsulectl from
the plugin's release and install it with `capsulectl plugin install <file>`.
Installing it is the user's step: never run `plugin install` yourself.

```sh
capsulectl --profile deal deal init --dir ~/.local/share/capsule-deal
```

This creates a local SQLite store and two signing seeds (mode 0600), and
turns on the public witness by default (`--no-witness` turns it off; `ASSURANCE.md` compares it with a countersignature). It
prints `public_key`: the other side names it in an offer's `to`, so give it
to them when they make the offer. Raw names, numbers, addresses and message
text stay in this local store: each sealed step is an x-deal-v1 record (see
EvidenceBook's
[`dealprofile/x-deal-v1/PROFILE.md`](https://github.com/action-state-group/evidencebook/tree/main/dealprofile/x-deal-v1/PROFILE.md)).
Your journal stays on this device, and only what the deal needs (the offer,
your agreement, your acts and statements) goes to the other side, as files
the user sends.

A profile made before x-deal-v1 was the default refuses x-deal-v1 commands
until its store is moved, once: `capsulectl --profile deal deal set-version
x-deal-v1 --migrate` (a backup is kept beside the store). That is the user's
step: show them the message and never run it yourself.

**Admission.** Before agreeing, each side seals capsule-engine's admission
decision on the other (Purchase, step 4). capsule-engine is its own program:
if `capsule-engine` is not installed, stop at that step and tell the user.

Which of your own picks (a size, a delivery option the user never named)
pause a check is set by a materiality predicate the profile pins:
`deal init --materiality builtin:neutral` (the predicate built into
capsulectl: a quantity of 1 does not pause, a size, variant, delivery or other
quantity does) or a FILE of the user's own, or later `profile update
--materiality` (see EvidenceBook's
[`dealprofile/x-deal-v0/materiality-predicate/`](https://github.com/action-state-group/evidencebook/tree/v0.10.0/dealprofile/x-deal-v0/materiality-predicate)). With none configured, every attribute you
picked pauses the check and the user is asked about it. **Setting or changing
the predicate is the user's policy: never do it yourself.** If a check refuses
because the predicate changed since it was pinned, tell the user; re-pinning is
theirs to run.

Then set up the **checkpoint cadence**: schedule
`capsulectl --profile deal cll checkpoint cadence --wait-up-to 5m` **every 5
minutes** on this machine (a cron line `*/5 * * * *`, or the host's scheduler
at a five-minute interval). Each run is a poll: it waits for a tick due within
its 5 minutes and publishes it on time, on the profile's own clock (every 5m,
give or take 1m, by default), and a run with no tick due publishes nothing. A
run stays alive up to 5 minutes; if the host caps how long a scheduled job may
run, schedule more often with a matching smaller `--wait-up-to`. Never drop
`--wait-up-to`: without it a scheduled run publishes on the scheduler's grid,
which hides the cadence's jitter. Deal steps never publish anything
themselves. If your agent host asks before a program reaches a website, the
first tick raises that question for the witness's site: tell the user to
choose **"Always allow this site"**, not "allow once", because later ticks
run on the schedule, when nobody is there to answer, and an allow-once grant
leaves every later tick pending. Say plainly that the grant covers the
witness's whole site: for the default witness, witness.agentactioncapsule.org,
that is agentactioncapsule.org and all its subdomains.
A tick that could not reach the witness says so: "pending: network consent
needed, or no network". A tick refused with HTTP 403 says "pending: the
request was refused (HTTP 403), by the witness or by a network gate on this
host": on an agent host that asks before network access, that too is most
likely a missing standing grant, and the tick names the site. It is retried
at every tick.

Tell the user, in one message, exactly what this does: their deal steps are
sealed on this device; about every 5 minutes, whether or not anything happened, one
checkpoint of hashes goes to the witness; the witness never sees content, how
many deals there are, or when they happen. A remote checker (minimal deal
fields, never message text) stays off unless they turn it on. Ask once, up
front, before turning the remote checker on, or if they would rather have no
witness.

## Command reference

All commands take `--profile deal` and print one JSON object (`deal list`
without `--json` prints lines). `--help` on each gives every flag.

| Command | What it does |
|---|---|
| `deal init --dir DIR [--no-witness] [--materiality FILE\|builtin:neutral] [--window day\|week\|month\|quarter\|year\|none]` | Creates the profile: its store, signing and checkpoint seeds (mode 0600) and its cadence log. Prints `public_key`, the key the other side names in an offer's `to`, and the store's `window` (its period, or `none`) with a `notice`. `--window` (default `none`) splits the store's journal into calendar windows in UTC; each deal's book stays whole, placed in the window its agreement is sealed in. The window, or its absence, is fixed when the store is created. |
| `deal window list` / `deal window roll` / `deal window chain --from LABEL --to LABEL --out FILE` | On a store created with `--window`: lists the windows and the deals placed in each; closes ended windows; writes the chain a requester's report over windows is checked against, with each placed deal's proof. |
| `deal window respond --request FILE --requester ID --output FILE [--pack FILE]...` | On a store created with `--window`: answers a Terms Pack request from the window or placed deal book its pin names, and records the answer in the current window. Each `--pack` is a pack file (capsulectl-book's format) this answer may serve; without `--requester`, the requester you authenticated, a Terms Pack is not answered. |
| `deal offer --input FILE --out FILE [--counter-to OFFER]` | Seals your offer and writes the file to send. Prints `deal_id` and `offer` (its digest). An offer with `"to": null` is to a party that keeps no signed records: nothing to send. |
| `deal offer cancel OFFER --out FILE` / `deal offer decline OFFER --out FILE` | Withdraws your offer, or declines one sent to you, before the deal starts; writes the file to send. |
| `deal receive FILE` | Keeps a file the other side sent (an offer, a decline or cancellation, an agreement, a statement, a payment leg, a dispute, a stamp proof, a ruling, or the other side's copy) and says whether it counts yet. A ruling is entered in the user's book: `--out FILE` writes the statement to send, and the output gives `stamped_at` and, when it applies, `"independence": "not independent (shared operator)"` (`--binding`, `--revocation` add the user's own). Otherwise it seals nothing. |
| `deal admit --deal ID --input FILE` | Seals capsule-engine's admission decision (`capsule-engine admission evaluate --admit FILE`) for the deal; only `"admits_deal": true` is accepted. |
| `deal note --deal ID --kind intent --input FILE [--opening FILE]` | Seals what the user asked; the deal's first intent carries `--opening`. Other kinds on x-deal-v1: `searched`, `options`, `recommended`, `chose`, `couldnt`, `message`, `claim`, `evidence`, `change`, `disclosure`, and `approval` (`--check CHECK_ID --choice OPTION --said WORDS --shown-card FILE`). |
| `deal agree OFFER --out FILE [--intent DIGEST]` | Seals your agreement as the first record of your deal book and writes the file to send. The side the offer was sent to agrees first; the offerer agrees once it holds that agreement. On your own offer to no key, you agree alone, at once. |
| `deal check --deal ID --input FILE` | The final review: seals the snapshot, compares it with what was asked and agreed, and returns `proceed`, or a `pause` or `deny` with its card. A pay check carries `payment_ref`. |
| `deal settle --deal ID --sent --check CHECK_ID --payment-ref-type TYPE` | Before the handoff to the rail: checks the payment and counts it as in flight. Seals no leg. A card checkout is `--payment-ref-type x-deal-v1.merchant_order --payment-ref-origin ORIGIN`. |
| `deal settle --deal ID --check CHECK_ID --status settled\|failed --out FILE` | When the rail answers: seals your payer leg and writes the file to send. |
| `deal settle --deal ID --received N --receive-fee N --status S --payment-ref-type T --payment-ref R [--payment-ref-network N] --out FILE` | What you received: your payee leg (`--payment-ref-network` on x402). `--reverses LEG` makes a leg a refund of a leg of your own book; `--supersedes LEG` replaces an earlier observation. |
| `deal perform --deal ID --act ACT --check CHECK_ID --observed HOW --out FILE` | An act other than a payment (a delivery), on a passing commit check. |
| `deal statement --deal ID --statement ROW=VALUE --out FILE` | How a row went: `met`, `not_met` or `cannot_tell`. |
| `deal dispute --deal ID --row ROW --out FILE [--forum F] [--withdraw]` | Disputes a row before the agreement's referee, or takes it back. `--challenge RULING --grounds FILE` challenges a ruling the user holds; only the grounds' SHA-256 is sealed. |
| `deal tick` | The checkpoint cadence's poll. Prints `state` (`ticked` or `not_due`) and `due`. Schedule `cll checkpoint cadence --wait-up-to 5m` every 5 minutes (Setup). |
| `deal settlement --deal ID` | Each payment row's check over both sides' legs you hold, as your records read it now. |
| `deal report --deal ID [--bundle FILE] [--out FILE] [--share counterparty --to NAME]` | Each row's status, read at your last checkpoint. `--bundle` writes an Evidence Bundle; `--share counterparty` makes it the copy for the other side, put on record first; `--out` seals the report. |
| `deal list [--json] [--since D] [--until D]` | Every deal in the store with its state; read-only. |
| `deal audit-host --executions FILE` | The daily check of host actions (below). |
| `deal set-version x-deal-v1 --migrate` | Moves a store made before x-deal-v1 was the default to the new layout, once, keeping a backup. The user's step: never run it yourself. |

## Daily check of host actions

Once a day (a scheduled task on the host is enough), run:

```sh
capsulectl --profile deal deal audit-host --executions executions.jsonl
```

`executions.jsonl` is the host's own record of the tool calls that ran in
the period, those of the agent and of every sub-task it started, written in
the format in `AUDIT-HOST.md`. Read the execution records, not the
conversation: the payment is usually a sub-task's action, not one you can see
in the chat. The pass lists each consequential action with no deal record
(`unrecorded`), and prints on its face what it cannot see. It exits 3 when
anything is unrecorded. Show the user the `summary` and the `unrecorded` list
as they are. It can list what is missing from the records it read; it cannot
prove that nothing else happened.

## Try it: the demo (2 minutes, no real money)

`demo/lamp/` is a scripted DEMO desk lamp purchase, played on both sides:
the buyer offers, the seller receives, each side seals its admission
decision, the buyer seals its intent, the two agree, and the buyer's check
before paying pauses because the agent picked the item. Always say it is a
demo. `scripts/run-demo.sh` runs it unattended in its own temporary
directory, compares the card with `expected-card.txt` (ignoring trailing
whitespace), holds, and prints the report. The admission decisions in it
are capsule-engine's real output, kept so the demo needs no engine install;
a real deal seals the engine's output for its own counterparty and amount.

## What this is, honestly

- The trail is **tamper-evident against ourselves and the agent, not
  non-repudiation**. The seed that signs it is on this machine, so a later
  edit or deletion of a sealed step is detectable, and once a tick has
  reached the witness, not even this device can rewrite what it had sealed
  by then. The trail does not prove it was the user, rather than the
  machine, who said something. It covers this skill's own records only,
  never the agent host's own store: a change there is not detected.
- **Invocation is advisory, not enforced.** The check seals what was asked,
  proposed, approved and done; the host elects to call it, and a `pause` is
  advice to the host. It holds an action only where the host runs `deal check`
  from a pre-action hook. Without one, the backstop is detection: a skipped
  check shows in the trail, and an action with no deal at all shows in
  `deal audit-host`.
- It checks the agreement and the payment legs both sides recorded, not the
  rail: it cannot see the payment rail unless you record it.
- No act carries the person's own signature yet (agent-action-capsule#627):
  "settled" is computed from the agent's records.
- Say "flags the warning signs before you pay" and "a record you can check
  yourself". Never say it guarantees safety, prevents fraud, or is a legal
  proof.
