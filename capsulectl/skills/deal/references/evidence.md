# Evidence that arrives later

Part of the [deal skill](../SKILL.md): its [Rules](../SKILL.md#rules-never-break-these) and [Points of no return](../SKILL.md#points-of-no-return) bind every step here, and `ID`, `OFFER` and `--profile deal` are as its [Procedures](../SKILL.md#procedures) say.

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
